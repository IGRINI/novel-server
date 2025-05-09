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
			Transport: &http.Transport{ // Базовая конфигурация транспорта
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
				ForceAttemptHTTP2:   true, // Попробуем использовать HTTP/2
			},
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
func (c *authClient) GetUserCount(ctx context.Context, adminAccessToken string) (int, error) {
	countURL := c.baseURL + "/internal/auth/users/count"
	log := c.logger.With(zap.String("url", countURL))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, countURL, nil)
	if err != nil {
		log.Error("Failed to create user count HTTP request", zap.Error(err))
		return 0, fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	// Используем метод doAdminRequest для передачи админ-токена
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
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
func (c *authClient) ListUsers(ctx context.Context, limit int, afterCursor string, adminAccessToken string) ([]models.User, string, error) {
	listURL := c.baseURL + "/internal/auth/users"
	log := c.logger.With(zap.String("url", listURL), zap.Int("limit", limit), zap.String("cursor", afterCursor))

	u, err := url.Parse(listURL)
	if err != nil {
		log.Error("Failed to parse base URL for user list", zap.Error(err))
		return nil, "", fmt.Errorf("internal error parsing URL: %w", err)
	}
	q := u.Query()
	q.Set("limit", strconv.Itoa(limit))
	if afterCursor != "" {
		q.Set("cursor", afterCursor)
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

	// Используем метод doAdminRequest для передачи админ-токена
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
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

	// Структура для десериализации ответа с пагинацией
	type paginatedUsersResponse struct {
		Data       []models.User `json:"data"`
		NextCursor string        `json:"next_cursor"`
	}

	var response paginatedUsersResponse
	if err := json.Unmarshal(respBodyBytes, &response); err != nil {
		log.Error("Failed to unmarshal user list response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
		return nil, "", fmt.Errorf("invalid user list response format from auth service: %w", err)
	}

	log.Info("User list retrieved successfully", zap.Int("userCount", len(response.Data)))
	return response.Data, response.NextCursor, nil
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

// SetInterServiceToken устанавливает JWT токен для последующих запросов.
func (c *authClient) SetInterServiceToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.interServiceToken = token
	c.logger.Debug("Inter-service token updated in client")
}

// UpdateUser отправляет запрос на обновление пользователя в auth-service.
func (c *authClient) UpdateUser(ctx context.Context, userID uuid.UUID, payload UserUpdatePayload, adminAccessToken string) error {
	updateURL := fmt.Sprintf("%s/internal/auth/users/%s", c.baseURL, userID.String())
	log := c.logger.With(zap.String("url", updateURL), zap.String("userID", userID.String()))

	reqBodyBytes, err := json.Marshal(payload)
	if err != nil {
		log.Error("Failed to marshal update user request payload", zap.Error(err))
		return fmt.Errorf("internal error marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, updateURL, bytes.NewBuffer(reqBodyBytes))
	if err != nil {
		log.Error("Failed to create update user HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Use the admin request helper
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("request to auth service timed out: %w", err)
		}
		return fmt.Errorf("failed to execute update user request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode == http.StatusNoContent {
		log.Info("User updated successfully via auth-service")
		return nil // Success
	}

	// Handle errors
	respBodyBytes, _ := io.ReadAll(httpResp.Body)
	log.Warn("Received non-OK status for update user", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))

	var errResp models.ErrorResponse
	if err := json.Unmarshal(respBodyBytes, &errResp); err == nil {
		switch errResp.Code {
		case models.ErrCodeNotFound:
			return models.ErrUserNotFound
		case models.ErrCodeValidation:
			return fmt.Errorf("%w: %s", models.ErrInvalidInput, errResp.Message)
		default:
			return fmt.Errorf("auth service error (%s): %s", errResp.Code, errResp.Message)
		}
	}

	return fmt.Errorf("received unexpected status %d from auth service for update user", httpResp.StatusCode)
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

// ResetPassword отправляет запрос на сброс пароля пользователя и генерацию нового.
func (c *authClient) ResetPassword(ctx context.Context, userID uuid.UUID, adminAccessToken string) (string, error) {
	resetURL := fmt.Sprintf("%s/internal/auth/users/%s/password", c.baseURL, userID.String())
	log := c.logger.With(zap.String("url", resetURL), zap.String("userID", userID.String()))

	// Генерируем новый пароль на стороне клиента (admin-service)
	// В реальном приложении пароль должен генерироваться и возвращаться auth-service,
	// но для примера сделаем так.
	newPassword, err := generateRandomPassword(12) // 12 characters long
	if err != nil {
		log.Error("Failed to generate random password locally", zap.Error(err))
		return "", fmt.Errorf("internal error generating password: %w", err)
	}

	reqPayload := updatePasswordRequestClient{NewPassword: newPassword}
	reqBodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		log.Error("Failed to marshal reset password request payload", zap.Error(err))
		return "", fmt.Errorf("internal error marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, resetURL, bytes.NewBuffer(reqBodyBytes))
	if err != nil {
		log.Error("Failed to create reset password HTTP request", zap.Error(err))
		return "", fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Use the admin request helper
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("request to auth service timed out: %w", err)
		}
		return "", fmt.Errorf("failed to execute reset password request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode == http.StatusNoContent {
		log.Info("Password reset successfully via auth-service")
		// Возвращаем сгенерированный пароль ТОЛЬКО при успехе
		// В реальном приложении, auth-service мог бы вернуть что-то другое или ничего.
		return newPassword, nil
	}

	// Handle errors
	respBodyBytes, _ := io.ReadAll(httpResp.Body)
	log.Warn("Received non-OK status for reset password", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))

	var errResp models.ErrorResponse
	if err := json.Unmarshal(respBodyBytes, &errResp); err == nil {
		if errResp.Code == models.ErrCodeNotFound {
			return "", models.ErrUserNotFound
		}
		return "", fmt.Errorf("auth service error (%d): %s", errResp.Code, errResp.Message)
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

// GetUserInfo - вызывает эндпоинт /internal/auth/users/{user_id} для получения данных одного пользователя
func (c *authClient) GetUserInfo(ctx context.Context, userID uuid.UUID, adminAccessToken string) (*models.User, error) {
	userInfoURL := fmt.Sprintf("%s/internal/auth/users/%s", c.baseURL, userID.String())
	log := c.logger.With(zap.String("url", userInfoURL), zap.String("userID", userID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoURL, nil)
	if err != nil {
		log.Error("Failed to create user info HTTP request", zap.Error(err))
		return nil, fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	// Используем метод doAdminRequest для передачи админ-токена
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
	if err != nil {
		log.Error("HTTP request for user info failed", zap.Error(err))
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("request to auth service timed out: %w", err)
		}
		return nil, fmt.Errorf("failed to communicate with auth service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read user info response body", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return nil, fmt.Errorf("failed to read auth service response: %w", err)
	}

	if httpResp.StatusCode == http.StatusNotFound {
		log.Warn("User not found in auth service", zap.Int("status", httpResp.StatusCode))
		return nil, fmt.Errorf("%w: user %s", models.ErrUserNotFound, userID)
	}
	if httpResp.StatusCode != http.StatusOK {
		log.Warn("Received non-OK status for user info", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		return nil, fmt.Errorf("received unexpected status %d from auth service for user info", httpResp.StatusCode)
	}

	var user models.User
	if err := json.Unmarshal(respBodyBytes, &user); err != nil {
		log.Error("Failed to unmarshal user info response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
		return nil, fmt.Errorf("invalid user info response format from auth service: %w", err)
	}

	log.Info("User info retrieved successfully")
	return &user, nil
}

// <<< ДОБАВЛЕНО: Методы для удовлетворения интерфейса TokenVerifier >>>

// VerifyInterServiceToken implements interfaces.TokenVerifier by calling the auth service.
func (c *authClient) VerifyInterServiceToken(ctx context.Context, tokenString string) (*models.Claims, error) {
	log := c.logger.With(zap.String("operation", "VerifyInterServiceToken"))
	log.Debug("Verifying inter-service token via auth service")

	if tokenString == "" {
		return nil, fmt.Errorf("token string cannot be empty")
	}

	// Endpoint в auth-service для верификации межсервисных токенов
	endpointURL := c.baseURL + "/internal/auth/token/verify" // Предполагаем этот эндпоинт

	requestBody := struct {
		Token string `json:"token"`
	}{
		Token: tokenString,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		log.Error("Failed to marshal token verification request body", zap.Error(err))
		return nil, fmt.Errorf("failed to marshal token verification request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Error("Failed to create token verification request (POST)", zap.Error(err))
		return nil, fmt.Errorf("failed to create POST request for token verification: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Используем базовый HTTP клиент, НЕ doRequestWithTokenRefresh, т.к. для верификации токен не нужен
	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Error("Failed to execute POST request for token verification", zap.Error(err))
		return nil, fmt.Errorf("failed to execute POST request for token verification: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Error("Auth service returned non-OK status for token verification", zap.Int("status_code", resp.StatusCode))
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Warn("Auth service token verification error response body", zap.ByteString("body", bodyBytes))
		return nil, fmt.Errorf("token verification failed: auth service returned status %d", resp.StatusCode)
	}

	var claims models.Claims
	if err := json.NewDecoder(resp.Body).Decode(&claims); err != nil {
		log.Error("Failed to decode token claims from auth service response", zap.Error(err))
		return nil, fmt.Errorf("failed to decode token claims from auth service: %w", err)
	}

	log.Debug("Successfully verified inter-service token via auth service")
	return &claims, nil
}

// VerifyToken implements interfaces.TokenVerifier by calling the auth service.
func (c *authClient) VerifyToken(ctx context.Context, tokenString string) (*models.Claims, error) {
	log := c.logger.With(zap.String("operation", "VerifyToken"))
	log.Debug("Verifying user token via auth service")

	if tokenString == "" {
		return nil, fmt.Errorf("token string cannot be empty")
	}

	// Предполагаемый Endpoint в auth-service для верификации ПОЛЬЗОВАТЕЛЬСКИХ токенов
	endpointURL := c.baseURL + "/internal/auth/token/verify-user" // Предполагаем этот эндпоинт

	requestBody := struct {
		Token string `json:"token"`
	}{
		Token: tokenString,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		log.Error("Failed to marshal user token verification request body", zap.Error(err))
		return nil, fmt.Errorf("failed to marshal user token verification request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Error("Failed to create user token verification request (POST)", zap.Error(err))
		return nil, fmt.Errorf("failed to create POST request for user token verification: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Используем базовый HTTP клиент
	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Error("Failed to execute POST request for user token verification", zap.Error(err))
		return nil, fmt.Errorf("failed to execute POST request for user token verification: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Error("Auth service returned non-OK status for user token verification", zap.Int("status_code", resp.StatusCode))
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Warn("Auth service user token verification error response body", zap.ByteString("body", bodyBytes))
		return nil, fmt.Errorf("user token verification failed: auth service returned status %d", resp.StatusCode)
	}

	var claims models.Claims
	if err := json.NewDecoder(resp.Body).Decode(&claims); err != nil {
		log.Error("Failed to decode user token claims from auth service response", zap.Error(err))
		return nil, fmt.Errorf("failed to decode user token claims from auth service: %w", err)
	}

	log.Debug("Successfully verified user token via auth service")
	return &claims, nil
}

// <<< КОНЕЦ ДОБАВЛЕНИЯ >>>

// <<< NEW: Helper function for requests requiring admin privilege verification >>>
func (c *authClient) doAdminRequest(ctx context.Context, req *http.Request, adminAccessToken string) (*http.Response, error) {
	// Add admin token header
	if adminAccessToken == "" {
		c.logger.Error("Admin access token is empty for admin-required request", zap.String("url", req.URL.String()))
		return nil, errors.New("admin access token is required but missing")
	}
	req.Header.Set("X-Admin-Authorization", "Bearer "+adminAccessToken)

	// Use the existing logic for inter-service token and potential refresh
	return c.doRequestWithTokenRefresh(ctx, req)
}

// BanUser - вызывает эндпоинт POST /internal/auth/users/{userID}/ban
func (c *authClient) BanUser(ctx context.Context, userID uuid.UUID, adminAccessToken string) error {
	banURL := fmt.Sprintf("%s/internal/auth/users/%s/ban", c.baseURL, userID.String())
	log := c.logger.With(zap.String("url", banURL), zap.String("userID", userID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, banURL, nil)
	if err != nil {
		log.Error("Failed to create ban user HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}
	// NO BODY for ban request

	// Use the new admin request helper
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
	if err != nil {
		// Error already logged by doAdminRequest or doRequestWithTokenRefresh
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("request to auth service timed out: %w", err)
		}
		// Check for specific auth errors if needed, otherwise return generic
		return fmt.Errorf("failed to execute ban request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode == http.StatusNoContent {
		log.Info("User banned successfully via auth-service")
		return nil // Success
	}

	// Handle errors
	respBodyBytes, _ := io.ReadAll(httpResp.Body)
	log.Warn("Received non-OK status for ban user", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))

	// Try to parse error response
	var errResp models.ErrorResponse
	if err := json.Unmarshal(respBodyBytes, &errResp); err == nil {
		switch errResp.Code {
		case models.ErrCodeNotFound:
			return models.ErrUserNotFound
		case models.ErrCodeForbidden:
			return models.ErrForbidden // Maybe auth-service denies banning certain users?
		default:
			return fmt.Errorf("auth service error (%d): %s", errResp.Code, errResp.Message)
		}
	}

	return fmt.Errorf("received unexpected status %d from auth service for ban user", httpResp.StatusCode)
}

// UnbanUser - вызывает эндпоинт DELETE /internal/auth/users/{userID}/ban
func (c *authClient) UnbanUser(ctx context.Context, userID uuid.UUID, adminAccessToken string) error {
	unbanURL := fmt.Sprintf("%s/internal/auth/users/%s/ban", c.baseURL, userID.String())
	log := c.logger.With(zap.String("url", unbanURL), zap.String("userID", userID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, unbanURL, nil)
	if err != nil {
		log.Error("Failed to create unban user HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}

	// Use the admin request helper
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("request to auth service timed out: %w", err)
		}
		return fmt.Errorf("failed to execute unban request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode == http.StatusNoContent {
		log.Info("User unbanned successfully via auth-service")
		return nil // Success
	}

	// Handle errors
	respBodyBytes, _ := io.ReadAll(httpResp.Body)
	log.Warn("Received non-OK status for unban user", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))

	var errResp models.ErrorResponse
	if err := json.Unmarshal(respBodyBytes, &errResp); err == nil {
		if errResp.Code == models.ErrCodeNotFound {
			return models.ErrUserNotFound
		}
		return fmt.Errorf("auth service error (%d): %s", errResp.Code, errResp.Message)
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
