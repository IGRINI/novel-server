package client

import (
	"bytes"
	"context"
	"encoding/json"
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
}

// NewGameplayServiceClient создает новый клиент для gameplay-service.
func NewGameplayServiceClient(baseURL string, timeout time.Duration, logger *zap.Logger) (GameplayServiceClient, error) {
	_, err := url.ParseRequestURI(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL for gameplay service: %w", err)
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	return &gameplayClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		logger: logger.Named("GameplayServiceClient"),
	}, nil
}

// SetInterServiceToken устанавливает JWT токен для последующих запросов.
func (c *gameplayClient) SetInterServiceToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logger.Debug("Inter-service token set for GameplayServiceClient")
	c.interServiceToken = token
}

// --- Реализация методов интерфейса --- //

// DTO для ответа /internal/users/{user_id}/drafts
type listDraftsResponse struct {
	Data       []models.StoryConfig `json:"data"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

// ListUserDrafts получает список черновиков пользователя.
func (c *gameplayClient) ListUserDrafts(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]models.StoryConfig, string, error) {
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

	c.mu.RLock()
	token := c.interServiceToken
	c.mu.RUnlock()
	if token == "" {
		log.Warn("Inter-service token is not set, API call might fail")
	} else {
		httpReq.Header.Set("X-Internal-Service-Token", token)
	}

	log.Debug("Sending list drafts request to gameplay-service")
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// TODO: Handle context errors (timeout, cancellation)
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
		// TODO: Parse error response from gameplay-service if available
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
func (c *gameplayClient) ListUserPublishedStories(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*models.PublishedStory, bool, error) {
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

	c.mu.RLock()
	token := c.interServiceToken
	c.mu.RUnlock()
	if token == "" {
		log.Warn("Inter-service token is not set, API call might fail")
	} else {
		httpReq.Header.Set("X-Internal-Service-Token", token)
	}

	log.Debug("Sending list stories request to gameplay-service")
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// TODO: Handle context errors
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
		// TODO: Parse error response
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

// <<< ОБНОВЛЕНО: Реализация GetDraftDetailsInternal >>>
func (c *gameplayClient) GetDraftDetailsInternal(ctx context.Context, draftID uuid.UUID) (*models.StoryConfig, error) {
	detailURL := fmt.Sprintf("%s/internal/drafts/%s", c.baseURL, draftID.String())
	log := c.logger.With(zap.String("url", detailURL), zap.String("draftID", draftID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, detailURL, nil)
	if err != nil {
		log.Error("Failed to create get draft details internal HTTP request", zap.Error(err))
		return nil, fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	c.mu.RLock()
	token := c.interServiceToken
	c.mu.RUnlock()
	if token == "" {
		log.Warn("Inter-service token is not set, API call might fail")
	} else {
		httpReq.Header.Set("X-Internal-Service-Token", token)
	}

	log.Debug("Sending get draft details internal request to gameplay-service")
	httpResp, err := c.httpClient.Do(httpReq)
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

// <<< ОБНОВЛЕНО: Реализация GetPublishedStoryDetailsInternal >>>
func (c *gameplayClient) GetPublishedStoryDetailsInternal(ctx context.Context, storyID uuid.UUID) (*models.PublishedStory, error) {
	detailURL := fmt.Sprintf("%s/internal/stories/%s", c.baseURL, storyID.String())
	log := c.logger.With(zap.String("url", detailURL), zap.String("storyID", storyID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, detailURL, nil)
	if err != nil {
		log.Error("Failed to create get published story details internal HTTP request", zap.Error(err))
		return nil, fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	c.mu.RLock()
	token := c.interServiceToken
	c.mu.RUnlock()
	if token == "" {
		log.Warn("Inter-service token is not set, API call might fail")
	} else {
		httpReq.Header.Set("X-Internal-Service-Token", token)
	}

	log.Debug("Sending get published story details internal request to gameplay-service")
	httpResp, err := c.httpClient.Do(httpReq)
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

// <<< ОБНОВЛЕНО: Реализация ListStoryScenesInternal >>>
func (c *gameplayClient) ListStoryScenesInternal(ctx context.Context, storyID uuid.UUID) ([]models.StoryScene, error) {
	scenesURL := fmt.Sprintf("%s/internal/stories/%s/scenes", c.baseURL, storyID.String())
	log := c.logger.With(zap.String("url", scenesURL), zap.String("storyID", storyID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, scenesURL, nil)
	if err != nil {
		log.Error("Failed to create list story scenes internal HTTP request", zap.Error(err))
		return nil, fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	c.mu.RLock()
	token := c.interServiceToken
	c.mu.RUnlock()
	if token == "" {
		log.Warn("Inter-service token is not set, API call might fail")
	} else {
		httpReq.Header.Set("X-Internal-Service-Token", token)
	}

	log.Debug("Sending list story scenes internal request to gameplay-service")
	httpResp, err := c.httpClient.Do(httpReq)
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

// <<< ОБНОВЛЕНО: Реализация UpdateDraftInternal >>>
func (c *gameplayClient) UpdateDraftInternal(ctx context.Context, draftID uuid.UUID, configJSON, userInputJSON string, status models.StoryStatus) error {
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

	c.mu.RLock()
	token := c.interServiceToken
	c.mu.RUnlock()
	if token == "" {
		log.Warn("Inter-service token is not set, API call might fail")
	} else {
		httpReq.Header.Set("X-Internal-Service-Token", token)
	}

	log.Debug("Sending update draft internal request to gameplay-service")
	httpResp, err := c.httpClient.Do(httpReq)
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

// <<< ОБНОВЛЕНО: Реализация UpdateStoryInternal >>>
func (c *gameplayClient) UpdateStoryInternal(ctx context.Context, storyID uuid.UUID, configJSON, setupJSON string, status models.StoryStatus) error {
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, updateURL, bodyReader)
	if err != nil {
		log.Error("Failed to create update story internal HTTP request", zap.Error(err))
		return fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	c.mu.RLock()
	token := c.interServiceToken
	c.mu.RUnlock()
	if token == "" {
		log.Warn("Inter-service token is not set, API call might fail")
	} else {
		httpReq.Header.Set("X-Internal-Service-Token", token)
	}

	log.Debug("Sending update story internal request to gameplay-service")
	httpResp, err := c.httpClient.Do(httpReq)
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

// <<< ОБНОВЛЕНО: Реализация UpdateSceneInternal >>>
func (c *gameplayClient) UpdateSceneInternal(ctx context.Context, sceneID uuid.UUID, contentJSON string) error {
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

	c.mu.RLock()
	token := c.interServiceToken
	c.mu.RUnlock()
	if token == "" {
		log.Warn("Inter-service token is not set, API call might fail")
	} else {
		httpReq.Header.Set("X-Internal-Service-Token", token)
	}

	log.Debug("Sending update scene internal request to gameplay-service")
	httpResp, err := c.httpClient.Do(httpReq)
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
