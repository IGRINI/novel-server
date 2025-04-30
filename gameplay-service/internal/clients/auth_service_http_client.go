package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	interfaces "novel-server/shared/interfaces"
	"novel-server/shared/models"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Compile-time check to ensure implementation satisfies the interface.
var _ interfaces.AuthServiceClient = (*HTTPAuthServiceClient)(nil)

type HTTPAuthServiceClient struct {
	baseURL    string // Base URL of the auth-service (e.g., "http://auth-service:8080")
	httpClient *http.Client
	logger     *zap.Logger
	// Заголовок для межсервисной аутентификации уже добавляется
	interServiceToken string // Example: Token for secure inter-service calls
}

// NewHTTPAuthServiceClient creates a new HTTP client for the auth service.
func NewHTTPAuthServiceClient(baseURL string, interServiceToken string, logger *zap.Logger) *HTTPAuthServiceClient {
	// Ensure base URL doesn't have a trailing slash
	baseURL = strings.TrimSuffix(baseURL, "/")
	return &HTTPAuthServiceClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second, // Example timeout
		},
		interServiceToken: interServiceToken, // Store the token
		logger:            logger.Named("HTTPAuthServiceClient"),
	}
}

// GetUsersInfo implements interfaces.AuthServiceClient using a POST request
// with a list of IDs in the body.
func (c *HTTPAuthServiceClient) GetUsersInfo(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]interfaces.UserInfo, error) {
	log := c.logger.With(zap.Int("user_id_count", len(userIDs)))
	log.Debug("Requesting user info from auth service (POST)")

	if len(userIDs) == 0 {
		return make(map[uuid.UUID]interfaces.UserInfo), nil
	}

	// Используем предполагаемый внутренний эндпоинт для пакетного получения
	endpointURL := c.baseURL + "/internal/auth/users/batch-info"

	// Формируем тело запроса
	requestBody := struct {
		UserIDs []uuid.UUID `json:"userIds"`
	}{
		UserIDs: userIDs,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		log.Error("Failed to marshal auth service request body", zap.Error(err))
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Создаем HTTP POST запрос
	req, err := http.NewRequestWithContext(ctx, "POST", endpointURL, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Error("Failed to create auth service request (POST)", zap.Error(err))
		return nil, fmt.Errorf("failed to create POST request for auth service: %w", err)
	}

	// Устанавливаем заголовки
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.interServiceToken != "" {
		req.Header.Set("X-Internal-Service-Token", c.interServiceToken)
	} else {
		c.logger.Warn("Inter-service token is not set for auth service client (POST), API call might fail")
	}

	// Выполняем запрос
	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Error("Failed to execute POST request to auth service", zap.Error(err))
		return nil, fmt.Errorf("failed to execute POST request to auth service: %w", err)
	}
	defer resp.Body.Close()

	// Проверяем статус ответа
	if resp.StatusCode != http.StatusOK {
		log.Error("Auth service returned non-OK status (POST)", zap.Int("status_code", resp.StatusCode))
		// Читаем тело ответа для деталей ошибки
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			log.Warn("Failed to read error response body from auth service", zap.Error(readErr))
		} else {
			log.Warn("Auth service error response body", zap.ByteString("body", bodyBytes))
			// Можно попытаться распарсить как APIError, если структура известна
			// var apiErr sharedModels.APIError // Предполагая, что есть такая структура
			// if err := json.Unmarshal(bodyBytes, &apiErr); err == nil {
			//   log.Warn("Auth service API error details", zap.Any("api_error", apiErr))
			//   return nil, fmt.Errorf("auth service returned status %d: %s", resp.StatusCode, apiErr.Message)
			// }
		}
		return nil, fmt.Errorf("auth service returned status %d (POST)", resp.StatusCode)
	}

	// Декодируем тело ответа (ожидаем { "data": [...] })
	var responsePayload struct {
		Data []interfaces.UserInfo `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&responsePayload); err != nil {
		log.Error("Failed to decode auth service response (POST)", zap.Error(err))
		return nil, fmt.Errorf("failed to decode auth service response (POST): %w", err)
	}

	// Преобразуем список в карту
	userInfoMap := make(map[uuid.UUID]interfaces.UserInfo, len(responsePayload.Data))
	for _, info := range responsePayload.Data {
		userInfoMap[info.ID] = info
	}

	log.Debug("Successfully received and processed user info from auth service (POST)", zap.Int("user_count_received", len(userInfoMap)))
	return userInfoMap, nil
}

// VerifyInterServiceToken implements interfaces.TokenVerifier by calling the auth service.
func (c *HTTPAuthServiceClient) VerifyInterServiceToken(ctx context.Context, tokenString string) (*models.Claims, error) {
	log := c.logger.With(zap.String("operation", "VerifyInterServiceToken"))
	log.Debug("Verifying inter-service token via auth service")

	if tokenString == "" {
		return nil, fmt.Errorf("token string cannot be empty")
	}

	// Endpoint в auth-service для верификации межсервисных токенов
	endpointURL := c.baseURL + "/internal/auth/token/verify"

	// Тело запроса с токеном
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

	// Создаем HTTP POST запрос
	req, err := http.NewRequestWithContext(ctx, "POST", endpointURL, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Error("Failed to create token verification request (POST)", zap.Error(err))
		return nil, fmt.Errorf("failed to create POST request for token verification: %w", err)
	}

	// Устанавливаем заголовки
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// Для запроса верификации токена НЕ нужен другой межсервисный токен в заголовке,
	// так как auth-service сам является доверенным источником для этой операции.

	// Выполняем запрос
	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Error("Failed to execute POST request for token verification", zap.Error(err))
		return nil, fmt.Errorf("failed to execute POST request for token verification: %w", err)
	}
	defer resp.Body.Close()

	// Проверяем статус ответа
	if resp.StatusCode != http.StatusOK {
		log.Error("Auth service returned non-OK status for token verification", zap.Int("status_code", resp.StatusCode))
		bodyBytes, _ := io.ReadAll(resp.Body) // Читаем тело ошибки (ошибку чтения игнорируем)
		log.Warn("Auth service token verification error response body", zap.ByteString("body", bodyBytes))
		// Возвращаем ошибку, указывающую на невалидный токен (или другую проблему на стороне auth-service)
		return nil, fmt.Errorf("token verification failed: auth service returned status %d", resp.StatusCode)
	}

	// Декодируем тело ответа (ожидаем models.Claims)
	var claims models.Claims
	if err := json.NewDecoder(resp.Body).Decode(&claims); err != nil {
		log.Error("Failed to decode token claims from auth service response", zap.Error(err))
		return nil, fmt.Errorf("failed to decode token claims from auth service: %w", err)
	}

	log.Debug("Successfully verified inter-service token via auth service")
	return &claims, nil // Возвращаем полученные claims
}

// VerifyToken implements interfaces.TokenVerifier by calling the auth service.
// ПРИМЕЧАНИЕ: Этот метод нужен для удовлетворения интерфейса TokenVerifier.
// Реальная проверка ПОЛЬЗОВАТЕЛЬСКИХ токенов в admin-service обычно происходит
// через middleware, которая может использовать этот метод или напрямую auth-service.
// Здесь мы предполагаем, что есть эндпоинт `/internal/auth/token/verify-user`.
func (c *HTTPAuthServiceClient) VerifyToken(ctx context.Context, tokenString string) (*models.Claims, error) {
	log := c.logger.With(zap.String("operation", "VerifyToken"))
	log.Debug("Verifying user token via auth service")

	if tokenString == "" {
		return nil, fmt.Errorf("token string cannot be empty")
	}

	// Предполагаемый Endpoint в auth-service для верификации ПОЛЬЗОВАТЕЛЬСКИХ токенов
	endpointURL := c.baseURL + "/internal/auth/token/verify-user"

	// Тело запроса с токеном
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

	// Создаем HTTP POST запрос
	req, err := http.NewRequestWithContext(ctx, "POST", endpointURL, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Error("Failed to create user token verification request (POST)", zap.Error(err))
		return nil, fmt.Errorf("failed to create POST request for user token verification: %w", err)
	}

	// Устанавливаем заголовки
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Выполняем запрос
	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Error("Failed to execute POST request for user token verification", zap.Error(err))
		return nil, fmt.Errorf("failed to execute POST request for user token verification: %w", err)
	}
	defer resp.Body.Close()

	// Проверяем статус ответа
	if resp.StatusCode != http.StatusOK {
		log.Error("Auth service returned non-OK status for user token verification", zap.Int("status_code", resp.StatusCode))
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Warn("Auth service user token verification error response body", zap.ByteString("body", bodyBytes))
		return nil, fmt.Errorf("user token verification failed: auth service returned status %d", resp.StatusCode)
	}

	// Декодируем тело ответа (ожидаем models.Claims)
	var claims models.Claims
	if err := json.NewDecoder(resp.Body).Decode(&claims); err != nil {
		log.Error("Failed to decode user token claims from auth service response", zap.Error(err))
		return nil, fmt.Errorf("failed to decode user token claims from auth service: %w", err)
	}

	log.Debug("Successfully verified user token via auth service")
	return &claims, nil // Возвращаем полученные claims
}

/*
// Старая реализация GetUsersInfo через GET с query параметрами (удалена)
func (c *HTTPAuthServiceClient) GetUsersInfo_Get(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]interfaces.UserInfo, error) {
    // ... (код старой реализации) ...
}
*/
