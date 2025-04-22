package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"novel-server/shared/models" // Для структуры TokenDetails и кодов ошибок

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// authClient реализует AuthServiceHttpClient (интерфейс определен в auth.go).
type authClient struct {
	baseURL           string
	httpClient        *http.Client
	logger            *zap.Logger
	mu                sync.RWMutex
	interServiceToken string // Поле для JWT токена
	staticSecret      string // Поле для статичного секрета
}

// NewAuthServiceClient создает новый клиент для auth-service.
func NewAuthServiceClient(baseURL string, timeout time.Duration, logger *zap.Logger, staticSecret string) (AuthServiceHttpClient, error) {
	// Проверяем baseURL
	_, err := url.ParseRequestURI(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL for auth service: %w", err)
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	if staticSecret == "" {
		logger.Warn("Static Inter-service secret is not set for AuthServiceClient, token generation will likely fail")
	}

	return &authClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout, // Таймаут на весь запрос
			// TODO: Настроить Transport для keep-alive, max idle conns и т.д.
		},
		logger:       logger.Named("AuthServiceClient"),
		staticSecret: staticSecret, // Сохраняем статичный секрет
	}, nil
}

// loginRequest - внутренняя структура для тела запроса /auth/login
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login отправляет запрос на вход в auth-service.
func (c *authClient) Login(ctx context.Context, username, password string) (*models.TokenDetails, error) {
	loginURL := c.baseURL + "/auth/login"
	log := c.logger.With(zap.String("url", loginURL), zap.String("username", username))

	reqPayload := loginRequest{
		Username: username,
		Password: password,
	}
	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		log.Error("Failed to marshal login request payload", zap.Error(err))
		return nil, fmt.Errorf("internal error marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, bytes.NewBuffer(reqBody))
	if err != nil {
		log.Error("Failed to create login HTTP request", zap.Error(err))
		return nil, fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	// Используем универсальный метод для выполнения запроса
	httpResp, err := c.doRequestWithTokenRefresh(ctx, httpReq)
	if err != nil {
		log.Error("HTTP request to auth-service failed", zap.Error(err))
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("request to auth service timed out: %w", err)
		}
		return nil, fmt.Errorf("failed to communicate with auth service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read response body from auth-service", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return nil, fmt.Errorf("failed to read auth service response: %w", err)
	}

	if httpResp.StatusCode == http.StatusOK {
		var tokenDetails models.TokenDetails
		if err := json.Unmarshal(respBodyBytes, &tokenDetails); err != nil {
			log.Error("Failed to unmarshal successful login response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
			return nil, fmt.Errorf("invalid success response format from auth service: %w", err)
		}
		log.Info("Login successful via auth-service")
		return &tokenDetails, nil
	}

	log.Warn("Received error response from auth-service", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))

	type authErrorResponse struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	var errResp authErrorResponse
	if err := json.Unmarshal(respBodyBytes, &errResp); err == nil {
		if httpResp.StatusCode == http.StatusUnauthorized || httpResp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %s (code: %d)", models.ErrInvalidCredentials, errResp.Message, errResp.Code)
		}
		return nil, fmt.Errorf("auth service error: %s (status: %d, code: %d)", errResp.Message, httpResp.StatusCode, errResp.Code)
	}

	if httpResp.StatusCode == http.StatusUnauthorized || httpResp.StatusCode == http.StatusNotFound {
		return nil, models.ErrInvalidCredentials
	}

	return nil, fmt.Errorf("received unexpected status %d from auth service", httpResp.StatusCode)
}

// --- Новые методы ---

// GetUserCount - вызывает эндпоинт /internal/auth/users/count в auth-service
func (c *authClient) GetUserCount(ctx context.Context) (int, error) {
	countURL := c.baseURL + "/internal/auth/users/count"
	log := c.logger.With(zap.String("url", countURL))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, countURL, nil)
	if err != nil {
		log.Error("Failed to create user count HTTP request", zap.Error(err))
		return 0, fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	// Используем универсальный метод для выполнения запроса
	httpResp, err := c.doRequestWithTokenRefresh(ctx, httpReq)
	if err != nil {
		log.Error("HTTP request for user count failed", zap.Error(err))
		if errors.Is(err, context.DeadlineExceeded) {
			return 0, fmt.Errorf("request to auth service timed out: %w", err)
		}
		return 0, fmt.Errorf("failed to communicate with auth service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read user count response body", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return 0, fmt.Errorf("failed to read auth service response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		log.Warn("Received non-OK status for user count", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		return 0, fmt.Errorf("received unexpected status %d from auth service for user count", httpResp.StatusCode)
	}

	type countResponse struct {
		Count int `json:"count"`
	}
	var resp countResponse
	if err := json.Unmarshal(respBodyBytes, &resp); err != nil {
		log.Error("Failed to unmarshal user count response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
		return 0, fmt.Errorf("invalid count response format from auth service: %w", err)
	}

	log.Info("User count retrieved successfully", zap.Int("count", resp.Count))
	return resp.Count, nil
}

// ListUsers - вызывает эндпоинт /internal/auth/users в auth-service с параметрами пагинации.
func (c *authClient) ListUsers(ctx context.Context, limit int, afterCursor string) ([]models.User, string, error) {
	listURL := c.baseURL + "/internal/auth/users"
	log := c.logger.With(zap.String("url", listURL), zap.Int("limit", limit), zap.String("after", afterCursor))

	u, err := url.Parse(listURL)
	if err != nil {
		log.Error("Failed to parse base URL for user list", zap.Error(err))
		return nil, "", fmt.Errorf("internal error parsing URL: %w", err)
	}
	q := u.Query()
	q.Set("limit", strconv.Itoa(limit))
	if afterCursor != "" {
		q.Set("after", afterCursor)
	}
	u.RawQuery = q.Encode()
	finalURL := u.String()
	log = log.With(zap.String("finalURL", finalURL))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, finalURL, nil)
	if err != nil {
		log.Error("Failed to create user list HTTP request", zap.Error(err))
		return nil, "", fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	// Используем универсальный метод для выполнения запроса
	httpResp, err := c.doRequestWithTokenRefresh(ctx, httpReq)
	if err != nil {
		log.Error("HTTP request for user list failed", zap.Error(err))
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, "", fmt.Errorf("request to auth service timed out: %w", err)
		}
		return nil, "", fmt.Errorf("failed to communicate with auth service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read user list response body", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return nil, "", fmt.Errorf("failed to read auth service response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		log.Warn("Received non-OK status for user list", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		return nil, "", fmt.Errorf("received unexpected status %d from auth service for user list", httpResp.StatusCode)
	}

	var users []models.User
	if err := json.Unmarshal(respBodyBytes, &users); err != nil {
		log.Error("Failed to unmarshal user list response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
		return nil, "", fmt.Errorf("invalid user list response format from auth service: %w", err)
	}

	log.Info("User list retrieved successfully", zap.Int("userCount", len(users)))
	return users, "", nil
}

// doRequestWithTokenRefresh выполняет HTTP запрос с автоматическим обновлением токена при получении 401
func (c *authClient) doRequestWithTokenRefresh(ctx context.Context, req *http.Request) (*http.Response, error) {
	// Добавляем текущий токен в запрос
	if c.interServiceToken != "" {
		req.Header.Set("X-Internal-Service-Token", c.interServiceToken)
	} else {
		c.logger.Warn("Inter-service token is not set, request might fail")
	}

	// Выполняем запрос
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}

	// Если получили 401, пробуем обновить токен и повторить запрос
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close() // Закрываем тело первого ответа

		// Пробуем получить новый токен
		newToken, err := c.GenerateInterServiceToken(ctx, "admin-service")
		if err != nil {
			return nil, fmt.Errorf("failed to refresh inter-service token: %w", err)
		}

		// Устанавливаем новый токен
		c.SetInterServiceToken(newToken)

		// Создаем новый запрос с тем же контекстом и телом
		newReq := req.Clone(ctx)
		newReq.Header.Set("X-Internal-Service-Token", newToken)

		// Повторяем запрос с новым токеном
		return c.httpClient.Do(newReq)
	}

	return resp, nil
}

// --- Новый метод ---

// generateInterServiceTokenRequest - структура для запроса /internal/auth/token/generate
type generateInterServiceTokenRequest struct {
	ServiceName string `json:"service_name"`
}

// generateInterServiceTokenResponse - структура для ответа
type generateInterServiceTokenResponse struct {
	InterServiceToken string `json:"inter_service_token"`
}

// GenerateInterServiceToken - вызывает эндпоинт для генерации межсервисного токена.
func (c *authClient) GenerateInterServiceToken(ctx context.Context, serviceName string) (string, error) {
	genURL := c.baseURL + "/internal/auth/token/generate"
	log := c.logger.With(zap.String("url", genURL), zap.String("requestingService", serviceName))

	reqPayload := generateInterServiceTokenRequest{
		ServiceName: serviceName,
	}
	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		log.Error("Failed to marshal inter-service token request payload", zap.Error(err))
		return "", fmt.Errorf("internal error marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, genURL, bytes.NewBuffer(reqBody))
	if err != nil {
		log.Error("Failed to create inter-service token HTTP request", zap.Error(err))
		return "", fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if c.staticSecret != "" {
		httpReq.Header.Set("X-Internal-Service-Token", c.staticSecret)
	} else {
		log.Warn("Static Inter-service secret is not set for token generation request")
	}

	// Используем универсальный метод для выполнения запроса
	httpResp, err := c.doRequestWithTokenRefresh(ctx, httpReq)
	if err != nil {
		log.Error("HTTP request for inter-service token generation failed", zap.Error(err))
		if errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("request to auth service timed out: %w", err)
		}
		return "", fmt.Errorf("failed to communicate with auth service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read inter-service token generation response body", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return "", fmt.Errorf("failed to read auth service response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		log.Warn("Received non-OK status for inter-service token generation", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		return "", fmt.Errorf("received unexpected status %d from auth service for token generation", httpResp.StatusCode)
	}

	var resp generateInterServiceTokenResponse
	if err := json.Unmarshal(respBodyBytes, &resp); err != nil {
		log.Error("Failed to unmarshal inter-service token generation response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
		return "", fmt.Errorf("invalid token generation response format from auth service: %w", err)
	}
	if resp.InterServiceToken == "" {
		log.Error("Received empty inter-service token from auth-service", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		return "", fmt.Errorf("received empty inter-service token")
	}

	log.Info("Inter-service token generated successfully")
	return resp.InterServiceToken, nil
}

// BanUser sends a request to ban a user.
func (c *authClient) BanUser(ctx context.Context, userID uuid.UUID) error {
	banURL := fmt.Sprintf("%s/internal/auth/users/%s/ban", c.baseURL, userID.String())
	log := c.logger.With(zap.String("url", banURL), zap.String("userID", userID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, banURL, nil)
	if err != nil {
		log.Error("Failed to create ban user HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	// Используем универсальный метод для выполнения запроса
	httpResp, err := c.doRequestWithTokenRefresh(ctx, httpReq)
	if err != nil {
		log.Error("HTTP request for ban user failed", zap.Error(err))
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("request to auth service timed out: %w", err)
		}
		return fmt.Errorf("failed to communicate with auth service: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode == http.StatusNoContent {
		log.Info("User banned successfully")
		return nil
	}

	respBodyBytes, _ := io.ReadAll(httpResp.Body)
	log.Warn("Received non-204 status for ban user", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
	if httpResp.StatusCode == http.StatusNotFound {
		return models.ErrUserNotFound
	}
	return fmt.Errorf("received unexpected status %d from auth service for ban user", httpResp.StatusCode)
}

// UnbanUser sends a request to unban a user.
func (c *authClient) UnbanUser(ctx context.Context, userID uuid.UUID) error {
	unbanURL := fmt.Sprintf("%s/internal/auth/users/%s/ban", c.baseURL, userID.String())
	log := c.logger.With(zap.String("url", unbanURL), zap.String("userID", userID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, unbanURL, nil)
	if err != nil {
		log.Error("Failed to create unban user HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	// Используем универсальный метод для выполнения запроса
	httpResp, err := c.doRequestWithTokenRefresh(ctx, httpReq)
	if err != nil {
		log.Error("HTTP request for unban user failed", zap.Error(err))
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("request to auth service timed out: %w", err)
		}
		return fmt.Errorf("failed to communicate with auth service: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode == http.StatusNoContent {
		log.Info("User unbanned successfully")
		return nil
	}

	respBodyBytes, _ := io.ReadAll(httpResp.Body)
	log.Warn("Received non-204 status for unban user", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
	if httpResp.StatusCode == http.StatusNotFound {
		return models.ErrUserNotFound
	}
	return fmt.Errorf("received unexpected status %d from auth service for unban user", httpResp.StatusCode)
}

// ValidateAdminToken sends a token to auth-service for full validation.
type validateTokenRequest struct {
	Token string `json:"token"`
}

func (c *authClient) ValidateAdminToken(ctx context.Context, token string) (*models.Claims, error) {
	validateURL := c.baseURL + "/internal/auth/token/validate"
	log := c.logger.With(zap.String("url", validateURL))

	reqPayload := validateTokenRequest{Token: token}
	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		log.Error("Failed to marshal validate token request payload", zap.Error(err))
		return nil, fmt.Errorf("internal error marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, validateURL, bytes.NewBuffer(reqBody))
	if err != nil {
		log.Error("Failed to create validate token HTTP request", zap.Error(err))
		return nil, fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	// Используем универсальный метод для выполнения запроса
	httpResp, err := c.doRequestWithTokenRefresh(ctx, httpReq)
	if err != nil {
		log.Error("HTTP request for token validation failed", zap.Error(err))
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("request to auth service timed out: %w", err)
		}
		return nil, fmt.Errorf("failed to communicate with auth service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read token validation response body", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return nil, fmt.Errorf("failed to read auth service response: %w", err)
	}

	if httpResp.StatusCode == http.StatusOK {
		var claims models.Claims
		if err := json.Unmarshal(respBodyBytes, &claims); err != nil {
			log.Error("Failed to unmarshal successful token validation response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
			return nil, fmt.Errorf("invalid success response format from auth service: %w", err)
		}
		log.Debug("Token validation successful via auth-service")
		return &claims, nil
	}

	log.Warn("Received error response from auth-service for token validation", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))

	if httpResp.StatusCode == http.StatusUnauthorized {
		type authErrorResponse struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		var errResp authErrorResponse
		if err := json.Unmarshal(respBodyBytes, &errResp); err == nil && errResp.Code == 40103 {
			return nil, models.ErrTokenExpired
		}
		return nil, models.ErrTokenInvalid
	}

	return nil, fmt.Errorf("auth service validation returned status %d", httpResp.StatusCode)
}

// SetInterServiceToken устанавливает JWT токен для последующих запросов.
func (c *authClient) SetInterServiceToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.logger.Info("Inter-service token has been set")
	c.interServiceToken = token
}

// UpdateUser отправляет запрос на обновление пользователя в auth-service.
func (c *authClient) UpdateUser(ctx context.Context, userID uuid.UUID, payload UserUpdatePayload) error {
	updateURL := fmt.Sprintf("%s/internal/auth/users/%s", c.baseURL, userID.String())
	log := c.logger.With(zap.String("url", updateURL), zap.String("userID", userID.String()))

	reqBody, err := json.Marshal(payload)
	if err != nil {
		log.Error("Failed to marshal user update payload", zap.Error(err))
		return fmt.Errorf("internal error marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, updateURL, bytes.NewBuffer(reqBody))
	if err != nil {
		log.Error("Failed to create user update HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	// Используем универсальный метод для выполнения запроса
	httpResp, err := c.doRequestWithTokenRefresh(ctx, httpReq)
	if err != nil {
		log.Error("HTTP request for user update failed", zap.Error(err))
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("request to auth service timed out: %w", err)
		}
		return fmt.Errorf("failed to communicate with auth service: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode == http.StatusNoContent {
		log.Info("User updated successfully")
		return nil
	}

	respBodyBytes, readErr := io.ReadAll(httpResp.Body)
	if readErr != nil {
		log.Error("Failed to read error response body for user update", zap.Int("status", httpResp.StatusCode), zap.Error(readErr))
		return fmt.Errorf("received unexpected status %d and failed to read body from auth service for user update", httpResp.StatusCode)
	}

	log.Warn("Received non-204 status for user update", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))

	type authErrorResponse struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	var errResp authErrorResponse
	if err := json.Unmarshal(respBodyBytes, &errResp); err == nil {
		if httpResp.StatusCode == http.StatusUnauthorized || httpResp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("%w: %s (code: %d)", models.ErrInvalidCredentials, errResp.Message, errResp.Code)
		}
		return fmt.Errorf("auth service error: %s (status: %d, code: %d)", errResp.Message, httpResp.StatusCode, errResp.Code)
	}

	if httpResp.StatusCode == http.StatusUnauthorized || httpResp.StatusCode == http.StatusNotFound {
		return models.ErrInvalidCredentials
	}

	return fmt.Errorf("received unexpected status %d from auth service for user update", httpResp.StatusCode)
}

// --- Генерация случайного пароля ---
const (
	passwordLength = 12
	passwordChars  = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

// generateRandomPassword создает случайную строку заданной длины.
func generateRandomPassword(length int) (string, error) {
	b := make([]byte, length)
	_, err := rand.Read(b) // Используем криптографически стойкий генератор
	if err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	for i := 0; i < length; i++ {
		b[i] = passwordChars[int(b[i])%len(passwordChars)]
	}
	return string(b), nil
}

// --- Структура для запроса сброса пароля в auth-service ---
type updatePasswordRequestClient struct {
	NewPassword string `json:"new_password"`
}

// ResetPassword генерирует новый пароль и отправляет запрос на его установку в auth-service.
func (c *authClient) ResetPassword(ctx context.Context, userID uuid.UUID) (string, error) {
	log := c.logger.With(zap.String("userID", userID.String()))

	// 1. Генерируем новый случайный пароль
	newPassword, err := generateRandomPassword(passwordLength)
	if err != nil {
		log.Error("Failed to generate random password", zap.Error(err))
		return "", fmt.Errorf("internal error generating password: %w", err)
	}
	log.Debug("Generated new random password for reset")

	// 2. Отправляем запрос на обновление пароля в auth-service
	resetURL := fmt.Sprintf("%s/internal/auth/users/%s/password", c.baseURL, userID.String())
	log = log.With(zap.String("url", resetURL))

	reqPayload := updatePasswordRequestClient{NewPassword: newPassword}
	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		log.Error("Failed to marshal reset password request payload", zap.Error(err))
		return "", fmt.Errorf("internal error marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, resetURL, bytes.NewBuffer(reqBody))
	if err != nil {
		log.Error("Failed to create reset password HTTP request", zap.Error(err))
		return "", fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	// Используем универсальный метод для выполнения запроса
	httpResp, err := c.doRequestWithTokenRefresh(ctx, httpReq)
	if err != nil {
		log.Error("HTTP request for reset password failed", zap.Error(err))
		if errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("request to auth service timed out: %w", err)
		}
		return "", fmt.Errorf("failed to communicate with auth service: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode == http.StatusNoContent {
		log.Info("Password reset successfully via auth-service")
		return newPassword, nil
	}

	respBodyBytes, _ := io.ReadAll(httpResp.Body)
	log.Warn("Received non-204 status for reset password", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
	if httpResp.StatusCode == http.StatusNotFound {
		return "", models.ErrUserNotFound
	}
	return "", fmt.Errorf("received unexpected status %d from auth service for reset password", httpResp.StatusCode)
}

// --- Новый метод для обновления токена ---

// refreshTokenRequest - структура для запроса /internal/auth/token/refresh
type refreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// refreshTokenResponse - структура для ответа от /internal/auth/token/refresh
// Предполагаем, что сервис возвращает и токены, и клеймы, чтобы избежать повторного запроса валидации.
type refreshTokenResponse struct {
	Tokens models.TokenDetails `json:"tokens"`
	Claims models.Claims       `json:"claims"`
}

// RefreshAdminToken отправляет запрос на обновление токенов в auth-service.
func (c *authClient) RefreshAdminToken(ctx context.Context, refreshToken string) (*models.TokenDetails, *models.Claims, error) {
	refreshURL := c.baseURL + "/internal/auth/token/refresh"
	log := c.logger.With(zap.String("url", refreshURL))

	reqPayload := refreshTokenRequest{RefreshToken: refreshToken}
	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		log.Error("Failed to marshal refresh token request payload", zap.Error(err))
		return nil, nil, fmt.Errorf("internal error marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshURL, bytes.NewBuffer(reqBody))
	if err != nil {
		log.Error("Failed to create refresh token HTTP request", zap.Error(err))
		return nil, nil, fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	// Для обновления токена используем прямой вызов, чтобы избежать рекурсии
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Error("HTTP request for refresh token failed", zap.Error(err))
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, nil, fmt.Errorf("request to auth service timed out: %w", err)
		}
		return nil, nil, fmt.Errorf("failed to communicate with auth service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read refresh token response body", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to read auth service response: %w", err)
	}

	if httpResp.StatusCode == http.StatusOK {
		var resp refreshTokenResponse
		if err := json.Unmarshal(respBodyBytes, &resp); err != nil {
			log.Error("Failed to unmarshal successful refresh token response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
			return nil, nil, fmt.Errorf("invalid success response format from auth service: %w", err)
		}
		if resp.Tokens.AccessToken == "" || resp.Tokens.RefreshToken == "" || resp.Claims.UserID == uuid.Nil {
			log.Error("Received incomplete data in successful refresh token response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
			return nil, nil, fmt.Errorf("incomplete data received from auth service")
		}
		log.Info("Token refresh successful via auth-service")
		return &resp.Tokens, &resp.Claims, nil
	}

	log.Warn("Received error response from auth-service for token refresh", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))

	if httpResp.StatusCode == http.StatusUnauthorized {
		return nil, nil, models.ErrInvalidCredentials
	}

	return nil, nil, fmt.Errorf("auth service token refresh returned status %d", httpResp.StatusCode)
}

// <<< Конец нового метода >>>
