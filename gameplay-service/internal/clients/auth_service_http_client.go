package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	interfaces "novel-server/shared/interfaces"
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
	// TODO: Add any necessary headers, e.g., for inter-service authentication
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
	endpointURL := c.baseURL + "/internal/users/batch-info"

	// Формируем тело запроса
	requestBody := struct {
		UserIDs []uuid.UUID `json:"user_ids"`
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
		// TODO: Read response body for more error details if available
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

/*
// Старая реализация GetUsersInfo через GET с query параметрами (удалена)
func (c *HTTPAuthServiceClient) GetUsersInfo_Get(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]interfaces.UserInfo, error) {
    // ... (код старой реализации) ...
}
*/
