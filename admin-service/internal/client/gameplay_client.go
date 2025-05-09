package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	io "io"
	"net/http"
	"net/url"
	"novel-server/shared/models"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// gameplayClient реализует GameplayServiceClient.
type gameplayClient struct {
	baseURL           string
	httpClient        *http.Client
	logger            *zap.Logger
	mu                sync.RWMutex
	interServiceToken string
	authClient        AuthServiceHttpClient
}

// NewGameplayServiceClient создает новый клиент для gameplay-service.
func NewGameplayServiceClient(baseURL string, timeout time.Duration, logger *zap.Logger, authClient AuthServiceHttpClient) (GameplayServiceClient, error) {
	_, err := url.ParseRequestURI(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL for gameplay service: %w", err)
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	if authClient == nil {
		return nil, fmt.Errorf("auth client cannot be nil")
	}

	return &gameplayClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		logger:     logger.Named("GameplayServiceClient"),
		authClient: authClient,
	}, nil
}

// SetInterServiceToken устанавливает JWT токен для последующих запросов.
func (c *gameplayClient) SetInterServiceToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logger.Debug("Inter-service token set for GameplayServiceClient")
	c.interServiceToken = token
}

// doRequestWithTokenRefresh выполняет HTTP запрос с автоматическим обновлением токена при получении 401
func (c *gameplayClient) doRequestWithTokenRefresh(ctx context.Context, req *http.Request) (*http.Response, error) {
	// Добавляем текущий токен в запрос
	c.mu.RLock()
	token := c.interServiceToken
	c.mu.RUnlock()

	if token != "" {
		req.Header.Set("X-Internal-Service-Token", token)
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

		// Пробуем получить новый токен через authClient
		newToken, err := c.authClient.GenerateInterServiceToken(ctx, "admin-service")
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

// doAdminRequest выполняет HTTP запрос с добавлением заголовка X-Admin-Authorization
func (c *gameplayClient) doAdminRequest(ctx context.Context, req *http.Request, adminAccessToken string) (*http.Response, error) {
	// Добавляем админский токен в заголовок
	if adminAccessToken == "" {
		c.logger.Error("Admin access token is empty for admin-required request", zap.String("url", req.URL.String()))
		return nil, errors.New("admin access token is required but missing")
	}
	req.Header.Set("X-Admin-Authorization", "Bearer "+adminAccessToken)

	// Добавляем межсервисный токен в заголовок
	c.mu.RLock()
	token := c.interServiceToken
	c.mu.RUnlock()

	if token != "" {
		req.Header.Set("X-Internal-Service-Token", token)
	} else {
		c.logger.Warn("Inter-service token is not set for admin request, request might fail", zap.String("url", req.URL.String()))
	}

	// Выполняем запрос
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	// Если получили 401 или 403, пробуем обновить токен и повторить запрос
	if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		resp.Body.Close() // Закрываем тело первого ответа

		// Пробуем получить новый токен через authClient
		c.logger.Warn("Received 401/403 status, trying to refresh inter-service token",
			zap.Int("statusCode", resp.StatusCode),
			zap.String("url", req.URL.String()))

		newToken, err := c.authClient.GenerateInterServiceToken(ctx, "admin-service")
		if err != nil {
			return nil, fmt.Errorf("failed to refresh inter-service token: %w", err)
		}

		// Устанавливаем новый токен
		c.SetInterServiceToken(newToken)

		// Создаем новый запрос с тем же контекстом и телом
		newReq := req.Clone(ctx)
		newReq.Header.Set("X-Admin-Authorization", "Bearer "+adminAccessToken)
		newReq.Header.Set("X-Internal-Service-Token", newToken)

		// Повторяем запрос с новым токеном
		return c.httpClient.Do(newReq)
	}

	return resp, err
}

// --- Реализация методов интерфейса --- //

// DTO для ответа /internal/users/{user_id}/drafts
type listDraftsResponse struct {
	Data       []models.StoryConfig `json:"data"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

// ListUserDrafts получает список черновиков пользователя.
func (c *gameplayClient) ListUserDrafts(ctx context.Context, userID uuid.UUID, limit int, cursor string, adminAccessToken string) ([]models.StoryConfig, string, error) {
	listURL := fmt.Sprintf("%s/internal/users/%s/drafts", c.baseURL, userID.String())
	log := c.logger.With(zap.String("url", listURL), zap.String("userID", userID.String()), zap.Int("limit", limit), zap.String("cursor", cursor))

	u, err := url.Parse(listURL)
	if err != nil {
		log.Error("Failed to parse base URL for list drafts", zap.Error(err))
		return nil, "", fmt.Errorf("internal error parsing URL: %w", err)
	}
	q := u.Query()
	q.Set("limit", strconv.Itoa(limit))
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	u.RawQuery = q.Encode()
	finalURL := u.String()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, finalURL, nil)
	if err != nil {
		log.Error("Failed to create list drafts HTTP request", zap.Error(err))
		return nil, "", fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	log.Debug("Sending list drafts request to gameplay-service")
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
	if err != nil {
		log.Error("HTTP request for list drafts failed", zap.Error(err))
		return nil, "", fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read list drafts response body", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return nil, "", fmt.Errorf("failed to read gameplay service response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		log.Warn("Received non-OK status for list drafts", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		return nil, "", fmt.Errorf("received unexpected status %d from gameplay service for list drafts", httpResp.StatusCode)
	}

	var resp listDraftsResponse
	if err := json.Unmarshal(respBodyBytes, &resp); err != nil {
		log.Error("Failed to unmarshal list drafts response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
		return nil, "", fmt.Errorf("invalid list drafts response format from gameplay service: %w", err)
	}

	log.Info("User drafts retrieved successfully", zap.Int("count", len(resp.Data)), zap.String("nextCursor", resp.NextCursor))
	return resp.Data, resp.NextCursor, nil
}

// DTO для ответа /internal/users/{user_id}/stories
type listStoriesResponse struct {
	Data    []*models.PublishedStory `json:"data"`
	HasMore bool                     `json:"has_more"`
}

// ListUserPublishedStories получает список опубликованных историй пользователя.
func (c *gameplayClient) ListUserPublishedStories(ctx context.Context, userID uuid.UUID, limit, offset int, adminAccessToken string) ([]*models.PublishedStory, bool, error) {
	listURL := fmt.Sprintf("%s/internal/users/%s/stories", c.baseURL, userID.String())
	log := c.logger.With(zap.String("url", listURL), zap.String("userID", userID.String()), zap.Int("limit", limit), zap.Int("offset", offset))

	u, err := url.Parse(listURL)
	if err != nil {
		log.Error("Failed to parse base URL for list stories", zap.Error(err))
		return nil, false, fmt.Errorf("internal error parsing URL: %w", err)
	}
	q := u.Query()
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	u.RawQuery = q.Encode()
	finalURL := u.String()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, finalURL, nil)
	if err != nil {
		log.Error("Failed to create list stories HTTP request", zap.Error(err))
		return nil, false, fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	log.Debug("Sending list stories request to gameplay-service")
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
	if err != nil {
		log.Error("HTTP request for list stories failed", zap.Error(err))
		return nil, false, fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read list stories response body", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return nil, false, fmt.Errorf("failed to read gameplay service response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		log.Warn("Received non-OK status for list stories", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		return nil, false, fmt.Errorf("received unexpected status %d from gameplay service for list stories", httpResp.StatusCode)
	}

	var resp listStoriesResponse
	if err := json.Unmarshal(respBodyBytes, &resp); err != nil {
		log.Error("Failed to unmarshal list stories response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
		return nil, false, fmt.Errorf("invalid list stories response format from gameplay service: %w", err)
	}

	log.Info("User stories retrieved successfully", zap.Int("count", len(resp.Data)), zap.Bool("hasMore", resp.HasMore))
	return resp.Data, resp.HasMore, nil
}

// GetDraftDetailsInternal получает детали черновика.
func (c *gameplayClient) GetDraftDetailsInternal(ctx context.Context, draftID uuid.UUID, adminAccessToken string) (*models.StoryConfig, error) {
	detailURL := fmt.Sprintf("%s/internal/drafts/%s", c.baseURL, draftID.String())
	log := c.logger.With(zap.String("url", detailURL), zap.String("draftID", draftID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, detailURL, nil)
	if err != nil {
		log.Error("Failed to create get draft details internal HTTP request", zap.Error(err))
		return nil, fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	log.Debug("Sending get draft details internal request to gameplay-service")
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
	if err != nil {
		log.Error("HTTP request for get draft details internal failed", zap.Error(err))
		return nil, fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read get draft details internal response body", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return nil, fmt.Errorf("failed to read gameplay service response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		log.Warn("Received non-OK status for get draft details internal", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		if httpResp.StatusCode == http.StatusNotFound {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("received unexpected status %d from gameplay service for get draft details internal", httpResp.StatusCode)
	}

	var resp models.StoryConfig
	if err := json.Unmarshal(respBodyBytes, &resp); err != nil {
		log.Error("Failed to unmarshal get draft details internal response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
		return nil, fmt.Errorf("invalid draft details internal response format from gameplay service: %w", err)
	}

	log.Info("Draft details internal retrieved successfully")
	return &resp, nil
}

// GetPublishedStoryDetailsInternal получает детали опубликованной истории.
func (c *gameplayClient) GetPublishedStoryDetailsInternal(ctx context.Context, storyID uuid.UUID, adminAccessToken string) (*models.PublishedStory, error) {
	detailURL := fmt.Sprintf("%s/internal/stories/%s", c.baseURL, storyID.String())
	log := c.logger.With(zap.String("url", detailURL), zap.String("storyID", storyID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, detailURL, nil)
	if err != nil {
		log.Error("Failed to create get published story details internal HTTP request", zap.Error(err))
		return nil, fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	log.Debug("Sending get published story details internal request to gameplay-service")
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
	if err != nil {
		log.Error("HTTP request for get published story details internal failed", zap.Error(err))
		return nil, fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read get published story details internal response body", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return nil, fmt.Errorf("failed to read gameplay service response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		log.Warn("Received non-OK status for get published story details internal", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		if httpResp.StatusCode == http.StatusNotFound {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("received unexpected status %d from gameplay service for get published story details internal", httpResp.StatusCode)
	}

	var resp models.PublishedStory
	if err := json.Unmarshal(respBodyBytes, &resp); err != nil {
		log.Error("Failed to unmarshal get published story details internal response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
		return nil, fmt.Errorf("invalid published story details internal response format from gameplay service: %w", err)
	}

	log.Info("Published story details internal retrieved successfully")
	return &resp, nil
}

// ListStoryScenesInternal получает список сцен истории.
func (c *gameplayClient) ListStoryScenesInternal(ctx context.Context, storyID uuid.UUID, adminAccessToken string) ([]models.StoryScene, error) {
	scenesURL := fmt.Sprintf("%s/internal/stories/%s/scenes", c.baseURL, storyID.String())
	log := c.logger.With(zap.String("url", scenesURL), zap.String("storyID", storyID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, scenesURL, nil)
	if err != nil {
		log.Error("Failed to create list story scenes internal HTTP request", zap.Error(err))
		return nil, fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	log.Debug("Sending list story scenes internal request to gameplay-service")
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
	if err != nil {
		log.Error("HTTP request for list story scenes internal failed", zap.Error(err))
		return nil, fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read list story scenes internal response body", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return nil, fmt.Errorf("failed to read gameplay service response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		log.Warn("Received non-OK status for list story scenes internal", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		return nil, fmt.Errorf("received unexpected status %d from gameplay service for list story scenes internal", httpResp.StatusCode)
	}

	var resp []models.StoryScene
	if err := json.Unmarshal(respBodyBytes, &resp); err != nil {
		log.Error("Failed to unmarshal list story scenes internal response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
		return nil, fmt.Errorf("invalid story scenes internal response format from gameplay service: %w", err)
	}

	if resp == nil {
		resp = make([]models.StoryScene, 0)
	}

	log.Info("Story scenes internal retrieved successfully", zap.Int("count", len(resp)))
	return resp, nil
}

// UpdateDraftInternal обновляет черновик.
func (c *gameplayClient) UpdateDraftInternal(ctx context.Context, draftID uuid.UUID, configJSON, userInputJSON string, status models.StoryStatus, adminAccessToken string) error {
	updateURL := fmt.Sprintf("%s/internal/drafts/%s", c.baseURL, draftID.String())
	log := c.logger.With(zap.String("url", updateURL), zap.String("draftID", draftID.String()), zap.String("status", string(status)))

	// Подготовка тела запроса
	body := map[string]interface{}{
		"configJson":    configJSON,
		"userInputJson": userInputJSON,
		"status":        status,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		log.Error("Failed to marshal update draft internal request body", zap.Error(err))
		return fmt.Errorf("internal error creating request body: %w", err)
	}
	bodyReader := bytes.NewReader(bodyBytes)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, updateURL, bodyReader)
	if err != nil {
		log.Error("Failed to create update draft internal HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	log.Debug("Sending update draft internal request to gameplay-service")
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
	if err != nil {
		log.Error("HTTP request for update draft internal failed", zap.Error(err))
		return fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusNoContent && httpResp.StatusCode != http.StatusOK {
		respBodyBytes, _ := io.ReadAll(httpResp.Body)
		log.Warn("Received non-OK/NoContent status for update draft internal", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		return fmt.Errorf("received unexpected status %d from gameplay service for update draft internal", httpResp.StatusCode)
	}

	log.Info("Draft update internal request sent successfully")
	return nil
}

// UpdateStoryInternal обновляет историю.
func (c *gameplayClient) UpdateStoryInternal(ctx context.Context, storyID uuid.UUID, configJSON, setupJSON string, status models.StoryStatus, adminAccessToken string) error {
	updateURL := fmt.Sprintf("%s/internal/stories/%s", c.baseURL, storyID.String())
	log := c.logger.With(zap.String("url", updateURL), zap.String("storyID", storyID.String()), zap.String("status", string(status)))

	// Преобразуем строки JSON в json.RawMessage для правильного маршалинга
	var configRaw json.RawMessage
	if configJSON != "" {
		if !json.Valid([]byte(configJSON)) {
			log.Error("Invalid configJSON string provided to client", zap.String("configJSON", configJSON))
			return fmt.Errorf("%w: invalid config JSON format", models.ErrBadRequest)
		}
		configRaw = json.RawMessage(configJSON)
	}

	var setupRaw json.RawMessage
	if setupJSON != "" {
		if !json.Valid([]byte(setupJSON)) {
			log.Error("Invalid setupJSON string provided to client", zap.String("setupJSON", setupJSON))
			return fmt.Errorf("%w: invalid setup JSON format", models.ErrBadRequest)
		}
		setupRaw = json.RawMessage(setupJSON)
	}

	// Подготовка тела запроса с json.RawMessage
	body := map[string]interface{}{
		"configJson": configRaw, // Теперь используется RawMessage
		"setupJson":  setupRaw,  // Теперь используется RawMessage
		"status":     status,
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		log.Error("Failed to marshal update story internal request body", zap.Error(err))
		return fmt.Errorf("internal error creating request body: %w", err)
	}
	bodyReader := bytes.NewReader(bodyBytes)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, updateURL, bodyReader)
	if err != nil {
		log.Error("Failed to create update story internal HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	log.Debug("Sending update story internal request to gameplay-service")
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
	if err != nil {
		log.Error("HTTP request for update story internal failed", zap.Error(err))
		return fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusNoContent && httpResp.StatusCode != http.StatusOK {
		respBodyBytes, _ := io.ReadAll(httpResp.Body)
		log.Warn("Received non-OK/NoContent status for update story internal", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		return fmt.Errorf("received unexpected status %d from gameplay service for update story internal", httpResp.StatusCode)
	}

	log.Info("Story update internal request sent successfully")
	return nil
}

// UpdateSceneInternal обновляет сцену.
func (c *gameplayClient) UpdateSceneInternal(ctx context.Context, sceneID uuid.UUID, contentJSON string, adminAccessToken string) error {
	updateURL := fmt.Sprintf("%s/internal/scenes/%s", c.baseURL, sceneID.String())
	log := c.logger.With(zap.String("url", updateURL), zap.String("sceneID", sceneID.String()))

	// Подготовка тела запроса
	body := map[string]string{
		"contentJson": contentJSON,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		log.Error("Failed to marshal update scene internal request body", zap.Error(err))
		return fmt.Errorf("internal error creating request body: %w", err)
	}
	bodyReader := bytes.NewReader(bodyBytes)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, updateURL, bodyReader)
	if err != nil {
		log.Error("Failed to create update scene internal HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	log.Debug("Sending update scene internal request to gameplay-service")
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
	if err != nil {
		log.Error("HTTP request for update scene internal failed", zap.Error(err))
		return fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusNoContent && httpResp.StatusCode != http.StatusOK {
		respBodyBytes, _ := io.ReadAll(httpResp.Body)
		log.Warn("Received non-OK/NoContent status for update scene internal", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		return fmt.Errorf("received unexpected status %d from gameplay service for update scene internal", httpResp.StatusCode)
	}

	log.Info("Scene update internal request sent successfully")
	return nil
}

// DeleteSceneInternal удаляет сцену по ее ID.
func (c *gameplayClient) DeleteSceneInternal(ctx context.Context, sceneID uuid.UUID, adminAccessToken string) error {
	deleteURL := fmt.Sprintf("%s/internal/scenes/%s", c.baseURL, sceneID.String())
	log := c.logger.With(zap.String("url", deleteURL), zap.String("sceneID", sceneID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, nil)
	if err != nil {
		log.Error("Failed to create delete scene HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}
	// Указываем, что мы ожидаем JSON ответ (хотя в DELETE он может быть пустым)
	httpReq.Header.Set("Accept", "application/json")

	log.Debug("Sending delete scene request to gameplay-service")
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
	if err != nil {
		log.Error("HTTP request for delete scene failed", zap.Error(err))
		return fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	// Проверяем статус ответа
	if httpResp.StatusCode == http.StatusNotFound {
		log.Warn("Scene not found for deletion", zap.Int("status", httpResp.StatusCode))
		return models.ErrNotFound // Возвращаем стандартную ошибку
	}
	if httpResp.StatusCode != http.StatusOK && httpResp.StatusCode != http.StatusNoContent {
		respBodyBytes, _ := io.ReadAll(httpResp.Body)
		log.Warn("Received non-OK status for delete scene", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		return fmt.Errorf("received unexpected status %d from gameplay service for delete scene", httpResp.StatusCode)
	}

	log.Info("Scene deleted successfully")
	return nil
}

// ListStoryPlayersInternal получает список состояний игроков для данной истории.
func (c *gameplayClient) ListStoryPlayersInternal(ctx context.Context, storyID uuid.UUID, adminAccessToken string) ([]models.PlayerGameState, error) {
	listURL := fmt.Sprintf("%s/internal/stories/%s/players", c.baseURL, storyID.String())
	log := c.logger.With(zap.String("url", listURL), zap.String("storyID", storyID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		log.Error("Failed to create list story players internal HTTP request", zap.Error(err))
		return nil, fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	log.Debug("Sending list story players internal request to gameplay-service")
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
	if err != nil {
		log.Error("HTTP request for list story players internal failed", zap.Error(err))
		return nil, fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read list story players internal response body", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return nil, fmt.Errorf("failed to read gameplay service response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		log.Warn("Received non-OK status for list story players internal", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		// Можно добавить обработку 404, если пустой список не считается ошибкой
		return nil, fmt.Errorf("received unexpected status %d from gameplay service for list story players internal", httpResp.StatusCode)
	}

	var resp []models.PlayerGameState
	if err := json.Unmarshal(respBodyBytes, &resp); err != nil {
		log.Error("Failed to unmarshal list story players internal response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
		return nil, fmt.Errorf("invalid player game state list response format from gameplay service: %w", err)
	}

	// Если ответ null, возвращаем пустой срез, а не nil
	if resp == nil {
		resp = make([]models.PlayerGameState, 0)
	}

	log.Info("Story players internal retrieved successfully", zap.Int("count", len(resp)))
	return resp, nil
}

// GetPlayerProgressInternal получает детали прогресса игрока.
func (c *gameplayClient) GetPlayerProgressInternal(ctx context.Context, progressID uuid.UUID, adminAccessToken string) (*models.PlayerProgress, error) {
	detailURL := fmt.Sprintf("%s/internal/player-progress/%s", c.baseURL, progressID.String())
	log := c.logger.With(zap.String("url", detailURL), zap.String("progressID", progressID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, detailURL, nil)
	if err != nil {
		log.Error("Failed to create get player progress internal HTTP request", zap.Error(err))
		return nil, fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	log.Debug("Sending get player progress internal request to gameplay-service")
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
	if err != nil {
		log.Error("HTTP request for get player progress internal failed", zap.Error(err))
		return nil, fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read get player progress internal response body", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return nil, fmt.Errorf("failed to read gameplay service response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		log.Warn("Received non-OK status for get player progress internal", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		if httpResp.StatusCode == http.StatusNotFound {
			return nil, models.ErrPlayerProgressNotFound // Используем специальную ошибку, если она есть, или models.ErrNotFound
		}
		return nil, fmt.Errorf("received unexpected status %d from gameplay service for get player progress internal", httpResp.StatusCode)
	}

	var resp models.PlayerProgress
	if err := json.Unmarshal(respBodyBytes, &resp); err != nil {
		log.Error("Failed to unmarshal get player progress internal response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
		return nil, fmt.Errorf("invalid player progress internal response format from gameplay service: %w", err)
	}

	log.Info("Player progress internal retrieved successfully")
	return &resp, nil
}

// UpdatePlayerProgressInternal обновляет детали прогресса игрока.
func (c *gameplayClient) UpdatePlayerProgressInternal(ctx context.Context, progressID uuid.UUID, progressData map[string]interface{}, adminAccessToken string) error {
	updateURL := fmt.Sprintf("%s/internal/player-progress/%s", c.baseURL, progressID.String())
	log := c.logger.With(zap.String("url", updateURL), zap.String("progressID", progressID.String()))

	// Подготовка тела запроса
	bodyBytes, err := json.Marshal(progressData)
	if err != nil {
		log.Error("Failed to marshal update player progress internal request body", zap.Error(err))
		return fmt.Errorf("internal error creating request body: %w", err)
	}
	bodyReader := bytes.NewReader(bodyBytes)

	// Создаем PUT запрос
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, updateURL, bodyReader)
	if err != nil {
		log.Error("Failed to create update player progress internal HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	log.Debug("Sending update player progress internal request to gameplay-service")
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
	if err != nil {
		log.Error("HTTP request for update player progress internal failed", zap.Error(err))
		return fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	// Проверяем статус ответа
	if httpResp.StatusCode == http.StatusNotFound {
		log.Warn("Player progress not found for update", zap.Int("status", httpResp.StatusCode))
		return models.ErrPlayerProgressNotFound // Используем специальную ошибку, если она есть, или models.ErrNotFound
	}
	if httpResp.StatusCode == http.StatusBadRequest {
		respBodyBytes, _ := io.ReadAll(httpResp.Body)
		log.Warn("Received Bad Request status for update player progress internal", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		// Попытаемся распарить тело ошибки для логирования деталей
		var errResp models.ErrorResponse
		if err := json.Unmarshal(respBodyBytes, &errResp); err == nil {
			log.Warn("Gameplay service returned Bad Request details", zap.String("code", errResp.Code), zap.String("message", errResp.Message))
		}
		return models.ErrBadRequest // Возвращаем стандартную ошибку
	}
	if httpResp.StatusCode != http.StatusOK && httpResp.StatusCode != http.StatusNoContent {
		respBodyBytes, _ := io.ReadAll(httpResp.Body)
		log.Warn("Received non-OK/NoContent status for update player progress internal", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		return fmt.Errorf("received unexpected status %d from gameplay service for update player progress internal", httpResp.StatusCode)
	}

	log.Info("Player progress updated successfully")
	return nil
}

// DeletePlayerProgressInternal удаляет прогресс игрока через внутренний API.
func (c *gameplayClient) DeletePlayerProgressInternal(ctx context.Context, progressID uuid.UUID, adminAccessToken string) error {
	deleteURL := fmt.Sprintf("%s/internal/player-progress/%s", c.baseURL, progressID.String())
	log := c.logger.With(zap.String("url", deleteURL), zap.String("progressID", progressID.String()))

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, nil)
	if err != nil {
		log.Error("Failed to create delete player progress HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.doAdminRequest(ctx, req, adminAccessToken)
	if err != nil {
		log.Error("HTTP request for delete player progress failed", zap.Error(err))
		return fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		log.Warn("Player progress not found for deletion", zap.Int("status", resp.StatusCode))
		return models.ErrPlayerProgressNotFound
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Warn("Received non-OK status for delete player progress", zap.Int("status", resp.StatusCode), zap.ByteString("body", bodyBytes))
		return fmt.Errorf("received unexpected status %d from gameplay service for delete player progress", resp.StatusCode)
	}

	log.Info("Player progress deleted successfully")
	return nil
}

// DeleteStoryPlayerInternal удаляет состояние игрока (GameState) через внутренний API.
func (c *gameplayClient) DeleteStoryPlayerInternal(ctx context.Context, storyID, playerID uuid.UUID, adminAccessToken string) error {
	deleteURL := fmt.Sprintf("%s/internal/stories/%s/players/%s", c.baseURL, storyID.String(), playerID.String())
	log := c.logger.With(zap.String("url", deleteURL), zap.String("storyID", storyID.String()), zap.String("playerID", playerID.String()))

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, nil)
	if err != nil {
		log.Error("Failed to create delete player game state HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.doAdminRequest(ctx, req, adminAccessToken)
	if err != nil {
		log.Error("HTTP request for delete player game state failed", zap.Error(err))
		return fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		log.Warn("Player game state not found for deletion", zap.Int("status", resp.StatusCode))
		return models.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Warn("Received non-OK status for delete player game state", zap.Int("status", resp.StatusCode), zap.ByteString("body", bodyBytes))
		return fmt.Errorf("received unexpected status %d from gameplay service for delete player game state", resp.StatusCode)
	}

	log.Info("Player game state deleted successfully")
	return nil
}

// GetActiveStoryCount получает количество активных (готовых к игре) историй из gameplay-service.
func (c *gameplayClient) GetActiveStoryCount(ctx context.Context, adminAccessToken string) (int, error) {
	countURL := fmt.Sprintf("%s/internal/stories/active/count", c.baseURL)
	log := c.logger.With(zap.String("url", countURL))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, countURL, nil)
	if err != nil {
		log.Error("Failed to create get active story count HTTP request", zap.Error(err))
		return 0, fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	log.Debug("Sending get active story count request to gameplay-service")
	httpResp, err := c.doAdminRequest(ctx, httpReq, adminAccessToken)
	if err != nil {
		log.Error("HTTP request for get active story count failed", zap.Error(err))
		return 0, fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read get active story count response body", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return 0, fmt.Errorf("failed to read gameplay service response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		log.Warn("Received non-OK status for get active story count", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		return 0, fmt.Errorf("received unexpected status %d from gameplay service for get active story count", httpResp.StatusCode)
	}

	type countResponse struct {
		Count int `json:"count"`
	}
	var resp countResponse
	if err := json.Unmarshal(respBodyBytes, &resp); err != nil {
		log.Error("Failed to unmarshal get active story count response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
		return 0, fmt.Errorf("invalid active story count response format from gameplay service: %w", err)
	}

	log.Info("Active story count retrieved successfully", zap.Int("count", resp.Count))
	return resp.Count, nil
}

// DeleteDraft удаляет черновик пользователя.
func (c *gameplayClient) DeleteDraft(ctx context.Context, userID, draftID uuid.UUID) error {
	// Эндпоинт в gameplay-service: DELETE /stories/{id}
	deleteURL := fmt.Sprintf("%s/stories/%s", c.baseURL, draftID.String())
	log := c.logger.With(zap.String("url", deleteURL), zap.String("draftID", draftID.String()), zap.String("userID", userID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, nil)
	if err != nil {
		log.Error("Failed to create delete draft HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}
	// Устанавливаем заголовок X-User-ID, так как эндпоинт требует аутентификации пользователя
	httpReq.Header.Set("X-User-ID", userID.String())
	httpReq.Header.Set("Accept", "application/json")

	log.Debug("Sending delete draft request to gameplay-service")
	// Используем стандартный httpClient.Do, так как это пользовательский эндпоинт, а не внутренний
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Error("HTTP request for delete draft failed", zap.Error(err))
		return fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusNoContent && httpResp.StatusCode != http.StatusOK {
		respBodyBytes, _ := io.ReadAll(httpResp.Body)
		log.Warn("Received non-OK/NoContent status for delete draft", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		if httpResp.StatusCode == http.StatusNotFound {
			return models.ErrNotFound
		} else if httpResp.StatusCode == http.StatusForbidden {
			return models.ErrForbidden
		}
		return fmt.Errorf("received unexpected status %d from gameplay service for delete draft", httpResp.StatusCode)
	}

	log.Info("Draft deleted successfully")
	return nil
}

// RetryDraftGeneration повторяет генерацию черновика.
func (c *gameplayClient) RetryDraftGeneration(ctx context.Context, userID, draftID uuid.UUID) error {
	// Эндпоинт в gameplay-service: POST /stories/drafts/{draft_id}/retry
	retryURL := fmt.Sprintf("%s/stories/drafts/%s/retry", c.baseURL, draftID.String())
	log := c.logger.With(zap.String("url", retryURL), zap.String("draftID", draftID.String()), zap.String("userID", userID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, retryURL, nil) // Тело запроса не нужно
	if err != nil {
		log.Error("Failed to create retry draft generation HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}
	// Устанавливаем заголовок X-User-ID
	httpReq.Header.Set("X-User-ID", userID.String())
	httpReq.Header.Set("Accept", "application/json")

	log.Debug("Sending retry draft generation request to gameplay-service")
	// Используем стандартный httpClient.Do
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Error("HTTP request for retry draft generation failed", zap.Error(err))
		return fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK && httpResp.StatusCode != http.StatusAccepted { // 200 OK или 202 Accepted
		respBodyBytes, _ := io.ReadAll(httpResp.Body)
		log.Warn("Received non-OK/Accepted status for retry draft generation", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		if httpResp.StatusCode == http.StatusNotFound {
			return models.ErrNotFound
		} else if httpResp.StatusCode == http.StatusForbidden {
			return models.ErrForbidden
		} else if httpResp.StatusCode == http.StatusConflict {
			// Попытка извлечь код ошибки из тела ответа
			var errResp models.ErrorResponse
			if json.Unmarshal(respBodyBytes, &errResp) == nil {
				if errResp.Code == models.ErrCodeCannotRetry {
					return models.ErrCannotRetry
				} else if errResp.Code == models.ErrCodeUserHasActiveGeneration {
					return models.ErrUserHasActiveGeneration
				}
			}
			// Если не удалось распознать код ошибки, возвращаем общую ошибку конфликта (ErrCannotRetry)
			return fmt.Errorf("%w: cannot retry draft in current state (status %d)", models.ErrCannotRetry, httpResp.StatusCode)
		}
		return fmt.Errorf("received unexpected status %d from gameplay service for retry draft generation", httpResp.StatusCode)
	}

	log.Info("Retry draft generation request sent successfully via gameplay-service")
	return nil
}

// DeletePublishedStory удаляет опубликованную историю пользователя.
func (c *gameplayClient) DeletePublishedStory(ctx context.Context, userID, storyID uuid.UUID) error {
	// Эндпоинт в gameplay-service: DELETE /published-stories/{id}
	deleteURL := fmt.Sprintf("%s/published-stories/%s", c.baseURL, storyID.String())
	log := c.logger.With(zap.String("url", deleteURL), zap.String("storyID", storyID.String()), zap.String("userID", userID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, nil)
	if err != nil {
		log.Error("Failed to create delete published story HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}
	// Устанавливаем заголовок X-User-ID
	httpReq.Header.Set("X-User-ID", userID.String())
	httpReq.Header.Set("Accept", "application/json")

	log.Debug("Sending delete published story request to gameplay-service")
	// Используем стандартный httpClient.Do
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Error("HTTP request for delete published story failed", zap.Error(err))
		return fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusNoContent && httpResp.StatusCode != http.StatusOK {
		respBodyBytes, _ := io.ReadAll(httpResp.Body)
		log.Warn("Received non-OK/NoContent status for delete published story", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		if httpResp.StatusCode == http.StatusNotFound {
			return models.ErrNotFound
		} else if httpResp.StatusCode == http.StatusForbidden {
			return models.ErrForbidden
		}
		return fmt.Errorf("received unexpected status %d from gameplay service for delete published story", httpResp.StatusCode)
	}

	log.Info("Published story deleted successfully via gameplay-service")
	return nil
}
