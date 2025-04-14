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
	"time"

	"novel-server/shared/models" // Для структуры TokenDetails и кодов ошибок
	"go.uber.org/zap"
)

// authClient реализует AuthServiceHttpClient (интерфейс определен в auth.go).
type authClient struct {
	baseURL    string
	httpClient *http.Client
	logger     *zap.Logger
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
		logger: logger.Named("AuthServiceClient"),
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
	loginURL := c.baseURL + "/auth/login" // Собираем полный URL
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

	log.Debug("Sending login request to auth-service")
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Error("HTTP request to auth-service failed", zap.Error(err))
		// Проверяем ошибки контекста (например, таймаут)
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

	// Обрабатываем ответ
	if httpResp.StatusCode == http.StatusOK {
		var tokenDetails models.TokenDetails
		if err := json.Unmarshal(respBodyBytes, &tokenDetails); err != nil {
			log.Error("Failed to unmarshal successful login response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
			return nil, fmt.Errorf("invalid success response format from auth service: %w", err)
		}
		log.Info("Login successful via auth-service")
		return &tokenDetails, nil
	}

	// Обрабатываем ошибки от auth-service
	log.Warn("Received error response from auth-service", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))

	// Пытаемся распарсить тело ошибки (предполагаем формат ErrorResponse из auth/handler)
	type authErrorResponse struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	var errResp authErrorResponse
	if err := json.Unmarshal(respBodyBytes, &errResp); err == nil {
		// Сопоставляем код ошибки auth-service с нашими ошибками (если нужно)
		// Например, коды 401xx должны мапиться в models.ErrInvalidCredentials
		if httpResp.StatusCode == http.StatusUnauthorized || httpResp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %s (code: %d)", models.ErrInvalidCredentials, errResp.Message, errResp.Code)
		}
		// Для других ошибок можно вернуть общее сообщение
		return nil, fmt.Errorf("auth service error: %s (status: %d, code: %d)", errResp.Message, httpResp.StatusCode, errResp.Code)
	}

	// Если не удалось распарсить ошибку, возвращаем общую ошибку на основе статуса
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
	// Используем JWT токен
	if c.interServiceToken != "" { 
		httpReq.Header.Set("X-Internal-Service-Token", c.interServiceToken)
	} else {
		log.Warn("Inter-service token is not set, internal API call might fail")
	}

	log.Debug("Sending user count request to auth-service")
	httpResp, err := c.httpClient.Do(httpReq)
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
		// Пытаемся разобрать ошибку, как в Login
		// ... (можно добавить парсинг authErrorResponse)
		return 0, fmt.Errorf("received unexpected status %d from auth service for user count", httpResp.StatusCode)
	}

	// Ожидаем ответ типа: {"count": 123}
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

// ListUsers - вызывает эндпоинт /internal/auth/users в auth-service
func (c *authClient) ListUsers(ctx context.Context) ([]models.User, error) {
	listURL := c.baseURL + "/internal/auth/users"
	log := c.logger.With(zap.String("url", listURL))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		log.Error("Failed to create user list HTTP request", zap.Error(err))
		return nil, fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	// Используем JWT токен
	if c.interServiceToken != "" { 
		httpReq.Header.Set("X-Internal-Service-Token", c.interServiceToken)
	} else {
		log.Warn("Inter-service token is not set, internal API call might fail")
	}

	log.Debug("Sending user list request to auth-service")
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Error("HTTP request for user list failed", zap.Error(err))
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("request to auth service timed out: %w", err)
		}
		return nil, fmt.Errorf("failed to communicate with auth service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read user list response body", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return nil, fmt.Errorf("failed to read auth service response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		log.Warn("Received non-OK status for user list", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		// ... (можно добавить парсинг authErrorResponse)
		return nil, fmt.Errorf("received unexpected status %d from auth service for user list", httpResp.StatusCode)
	}

	// Ожидаем ответ типа: [{"id": 1, "username": "...", ...}, ...]
	var users []models.User
	if err := json.Unmarshal(respBodyBytes, &users); err != nil {
		log.Error("Failed to unmarshal user list response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
		return nil, fmt.Errorf("invalid user list response format from auth service: %w", err)
	}

	log.Info("User list retrieved successfully", zap.Int("userCount", len(users)))
	return users, nil
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
	// --- Используем статичный секрет для запроса ГЕНЕРАЦИИ токена --- 
	if c.staticSecret != "" { // Используем поле staticSecret
		httpReq.Header.Set("X-Internal-Service-Token", c.staticSecret) 
	} else {
		log.Warn("Static Inter-service secret is not set for token generation request")
	}

	log.Debug("Sending inter-service token generation request to auth-service")
	httpResp, err := c.httpClient.Do(httpReq)
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
func (c *authClient) BanUser(ctx context.Context, userID uint64) error {
	banURL := fmt.Sprintf("%s/internal/auth/users/%d/ban", c.baseURL, userID)
	log := c.logger.With(zap.String("url", banURL), zap.Uint64("userID", userID))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, banURL, nil) // Используем POST
	if err != nil {
		log.Error("Failed to create ban user HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}
	// Используем JWT токен для межсервисного взаимодействия
	if c.interServiceToken == "" {
		log.Warn("Inter-service token is not set, ban user API call might fail")
		// Возможно, стоит вернуть ошибку, т.к. без токена запрос обречен?
		return fmt.Errorf("inter-service token not available")
	}
	httpReq.Header.Set("X-Internal-Service-Token", c.interServiceToken)

	log.Debug("Sending ban user request to auth-service")
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Error("HTTP request to ban user failed", zap.Error(err))
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("request to auth service timed out: %w", err)
		}
		return fmt.Errorf("failed to communicate with auth service: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode == http.StatusNoContent {
		log.Info("User banned successfully")
		return nil // Успех
	}

	// Обрабатываем ошибки
	respBodyBytes, _ := io.ReadAll(httpResp.Body) // Читаем тело для логирования
	log.Warn("Received non-204 status for ban user", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
	if httpResp.StatusCode == http.StatusNotFound {
		return models.ErrUserNotFound
	}
	// TODO: Разобрать другие возможные ошибки из auth-service (например, 401 - невалидный токен)
	return fmt.Errorf("received unexpected status %d from auth service for ban user", httpResp.StatusCode)
}

// UnbanUser sends a request to unban a user.
func (c *authClient) UnbanUser(ctx context.Context, userID uint64) error {
	unbanURL := fmt.Sprintf("%s/internal/auth/users/%d/ban", c.baseURL, userID)
	log := c.logger.With(zap.String("url", unbanURL), zap.Uint64("userID", userID))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, unbanURL, nil) // Используем DELETE
	if err != nil {
		log.Error("Failed to create unban user HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}
	// Используем JWT токен для межсервисного взаимодействия
	if c.interServiceToken == "" {
		log.Warn("Inter-service token is not set, unban user API call might fail")
		return fmt.Errorf("inter-service token not available")
	}
	httpReq.Header.Set("X-Internal-Service-Token", c.interServiceToken)

	log.Debug("Sending unban user request to auth-service")
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Error("HTTP request to unban user failed", zap.Error(err))
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("request to auth service timed out: %w", err)
		}
		return fmt.Errorf("failed to communicate with auth service: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode == http.StatusNoContent {
		log.Info("User unbanned successfully")
		return nil // Успех
	}

	// Обрабатываем ошибки
	respBodyBytes, _ := io.ReadAll(httpResp.Body) // Читаем тело для логирования
	log.Warn("Received non-204 status for unban user", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
	if httpResp.StatusCode == http.StatusNotFound {
		return models.ErrUserNotFound
	}
	// TODO: Разобрать другие возможные ошибки из auth-service
	return fmt.Errorf("received unexpected status %d from auth service for unban user", httpResp.StatusCode)
}

// ValidateAdminToken sends a token to auth-service for full validation.
type validateTokenRequest struct { // Внутренняя структура для запроса
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
	// Используем JWT токен для межсервисного взаимодействия
	if c.interServiceToken == "" {
		log.Warn("Inter-service token is not set, token validation API call might fail")
		return nil, fmt.Errorf("inter-service token not available")
	}
	httpReq.Header.Set("X-Internal-Service-Token", c.interServiceToken)

	log.Debug("Sending token validation request to auth-service")
	httpResp, err := c.httpClient.Do(httpReq)
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

	// Обрабатываем ошибки валидации от auth-service
	log.Warn("Received error response from auth-service for token validation", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))

	// --- Исправленная обработка ошибок --- 
	if httpResp.StatusCode == http.StatusUnauthorized {
		// Пытаемся понять, протух ли токен или просто невалиден/отозван/юзер забанен
		type authErrorResponse struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		var errResp authErrorResponse
		// Проверяем код ошибки из тела ответа auth-service, если он есть
		if err := json.Unmarshal(respBodyBytes, &errResp); err == nil && errResp.Code == 40103 { // Используем литерал 40103, т.к. константа недоступна
			return nil, models.ErrTokenExpired // Если код = ExpiredToken, возвращаем нужную ошибку
		}
		// Во всех остальных случаях 401 (невалидный, отозван, пользователь забанен) возвращаем общую ошибку
		return nil, models.ErrTokenInvalid
	}
	// --- Конец исправленной обработки --- 

	// Другие ошибки (500 и т.д.)
	return nil, fmt.Errorf("auth service validation returned status %d", httpResp.StatusCode)
}

// SetInterServiceToken устанавливает JWT токен для последующих запросов.
func (c *authClient) SetInterServiceToken(token string) {
	c.logger.Info("Inter-service token has been set")
	c.interServiceToken = token
}

// UpdateUser отправляет запрос на обновление пользователя в auth-service.
func (c *authClient) UpdateUser(ctx context.Context, userID uint64, payload UserUpdatePayload) error {
	updateURL := fmt.Sprintf("%s/internal/auth/users/%d", c.baseURL, userID)
	log := c.logger.With(zap.String("url", updateURL), zap.Uint64("userID", userID))

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
	// Используем JWT токен
	if c.interServiceToken != "" {
		httpReq.Header.Set("X-Internal-Service-Token", c.interServiceToken)
	} else {
		log.Warn("Inter-service token is not set, internal API call might fail")
	}

	log.Debug("Sending user update request to auth-service")
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Error("HTTP request for user update failed", zap.Error(err))
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("request to auth service timed out: %w", err)
		}
		return fmt.Errorf("failed to communicate with auth service: %w", err)
	}
	defer httpResp.Body.Close()

	// Проверяем статус ответа
	if httpResp.StatusCode == http.StatusNoContent {
		log.Info("User updated successfully")
		return nil // Успех, ожидаем 204 No Content
	}

	// Если статус не 204, пытаемся прочитать и разобрать тело ошибки
	respBodyBytes, readErr := io.ReadAll(httpResp.Body)
	if readErr != nil {
		log.Error("Failed to read error response body for user update", zap.Int("status", httpResp.StatusCode), zap.Error(readErr))
		// Возвращаем ошибку на основе статуса, т.к. тело не смогли прочитать
		return fmt.Errorf("received unexpected status %d and failed to read body from auth service for user update", httpResp.StatusCode)
	}

	log.Warn("Received non-204 status for user update", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))

	// Пытаемся разобрать ошибку auth-service (authErrorResponse)
	type authErrorResponse struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	var errResp authErrorResponse
	if err := json.Unmarshal(respBodyBytes, &errResp); err == nil {
		// Сопоставляем код ошибки auth-service с нашими ошибками (если нужно)
		// Например, коды 401xx должны мапиться в models.ErrInvalidCredentials
		if httpResp.StatusCode == http.StatusUnauthorized || httpResp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("%w: %s (code: %d)", models.ErrInvalidCredentials, errResp.Message, errResp.Code)
		}
		// Для других ошибок можно вернуть общее сообщение
		return fmt.Errorf("auth service error: %s (status: %d, code: %d)", errResp.Message, httpResp.StatusCode, errResp.Code)
	}

	// Если не удалось распарсить ошибку, возвращаем общую ошибку на основе статуса
	if httpResp.StatusCode == http.StatusUnauthorized || httpResp.StatusCode == http.StatusNotFound {
		return models.ErrInvalidCredentials
	}

	return fmt.Errorf("received unexpected status %d from auth service for user update", httpResp.StatusCode)
}

// --- Генерация случайного пароля ---
const ( 
	passwordLength = 12
	passwordChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
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
func (c *authClient) ResetPassword(ctx context.Context, userID uint64) (string, error) {
	log := c.logger.With(zap.Uint64("userID", userID))
	
	// 1. Генерируем новый случайный пароль
	newPassword, err := generateRandomPassword(passwordLength)
	if err != nil {
		log.Error("Failed to generate random password", zap.Error(err))
		return "", fmt.Errorf("internal error generating password: %w", err)
	}
	log.Debug("Generated new random password for reset") // Не логируем сам пароль!

	// 2. Отправляем запрос на обновление пароля в auth-service
	resetURL := fmt.Sprintf("%s/internal/auth/users/%d/password", c.baseURL, userID)
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
	// Используем JWT токен
	if c.interServiceToken == "" {
		log.Warn("Inter-service token is not set, reset password call might fail")
		return "", fmt.Errorf("inter-service token not available")
	}
	httpReq.Header.Set("X-Internal-Service-Token", c.interServiceToken)

	log.Debug("Sending reset password request to auth-service")
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Error("HTTP request for reset password failed", zap.Error(err))
		if errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("request to auth service timed out: %w", err)
		}
		return "", fmt.Errorf("failed to communicate with auth service: %w", err)
	}
	defer httpResp.Body.Close()

	// Проверяем статус ответа
	if httpResp.StatusCode == http.StatusNoContent {
		log.Info("Password reset successfully via auth-service")
		return newPassword, nil // Возвращаем сгенерированный пароль
	}

	// Обрабатываем ошибки
	respBodyBytes, _ := io.ReadAll(httpResp.Body)
	log.Warn("Received non-204 status for reset password", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
	if httpResp.StatusCode == http.StatusNotFound {
		return "", models.ErrUserNotFound
	}
	// TODO: Разобрать другие возможные ошибки из auth-service (400, 401, 500)
	return "", fmt.Errorf("received unexpected status %d from auth service for reset password", httpResp.StatusCode)
}