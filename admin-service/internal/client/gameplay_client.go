package client

import (
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

// <<< ДОБАВЛЕНО: Реализация GetDraftDetails >>>
func (c *gameplayClient) GetDraftDetails(ctx context.Context, userID, draftID uuid.UUID) (*models.StoryConfig, error) {
	detailURL := fmt.Sprintf("%s/internal/users/%s/drafts/%s", c.baseURL, userID.String(), draftID.String())
	log := c.logger.With(zap.String("url", detailURL), zap.String("userID", userID.String()), zap.String("draftID", draftID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, detailURL, nil)
	if err != nil {
		log.Error("Failed to create get draft details HTTP request", zap.Error(err))
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

	log.Debug("Sending get draft details request to gameplay-service")
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Error("HTTP request for get draft details failed", zap.Error(err))
		return nil, fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read get draft details response body", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return nil, fmt.Errorf("failed to read gameplay service response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		log.Warn("Received non-OK status for get draft details", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		// TODO: Parse error response from gameplay-service if available
		if httpResp.StatusCode == http.StatusNotFound {
			return nil, models.ErrNotFound // Возвращаем стандартную ошибку
		}
		return nil, fmt.Errorf("received unexpected status %d from gameplay service for get draft details", httpResp.StatusCode)
	}

	var resp models.StoryConfig
	if err := json.Unmarshal(respBodyBytes, &resp); err != nil {
		log.Error("Failed to unmarshal get draft details response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
		return nil, fmt.Errorf("invalid draft details response format from gameplay service: %w", err)
	}

	log.Info("Draft details retrieved successfully")
	return &resp, nil
}

// <<< ДОБАВЛЕНО: Реализация GetPublishedStoryDetails >>>
func (c *gameplayClient) GetPublishedStoryDetails(ctx context.Context, userID, storyID uuid.UUID) (*models.PublishedStory, error) {
	detailURL := fmt.Sprintf("%s/internal/users/%s/stories/%s", c.baseURL, userID.String(), storyID.String())
	log := c.logger.With(zap.String("url", detailURL), zap.String("userID", userID.String()), zap.String("storyID", storyID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, detailURL, nil)
	if err != nil {
		log.Error("Failed to create get published story details HTTP request", zap.Error(err))
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

	log.Debug("Sending get published story details request to gameplay-service")
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Error("HTTP request for get published story details failed", zap.Error(err))
		return nil, fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read get published story details response body", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return nil, fmt.Errorf("failed to read gameplay service response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		log.Warn("Received non-OK status for get published story details", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		if httpResp.StatusCode == http.StatusNotFound {
			return nil, models.ErrNotFound // Возвращаем стандартную ошибку
		}
		return nil, fmt.Errorf("received unexpected status %d from gameplay service for get published story details", httpResp.StatusCode)
	}

	var resp models.PublishedStory
	if err := json.Unmarshal(respBodyBytes, &resp); err != nil {
		log.Error("Failed to unmarshal get published story details response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
		return nil, fmt.Errorf("invalid published story details response format from gameplay service: %w", err)
	}

	log.Info("Published story details retrieved successfully")
	return &resp, nil
}

// <<< ДОБАВЛЕНО: Реализация ListStoryScenes >>>
func (c *gameplayClient) ListStoryScenes(ctx context.Context, userID, storyID uuid.UUID) ([]models.StoryScene, error) {
	scenesURL := fmt.Sprintf("%s/internal/users/%s/stories/%s/scenes", c.baseURL, userID.String(), storyID.String())
	log := c.logger.With(zap.String("url", scenesURL), zap.String("userID", userID.String()), zap.String("storyID", storyID.String()))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, scenesURL, nil)
	if err != nil {
		log.Error("Failed to create list story scenes HTTP request", zap.Error(err))
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

	log.Debug("Sending list story scenes request to gameplay-service")
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Error("HTTP request for list story scenes failed", zap.Error(err))
		return nil, fmt.Errorf("failed to communicate with gameplay service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read list story scenes response body", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return nil, fmt.Errorf("failed to read gameplay service response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		log.Warn("Received non-OK status for list story scenes", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes))
		// Не возвращаем NotFound, так как пустой список - валидный ответ
		return nil, fmt.Errorf("received unexpected status %d from gameplay service for list story scenes", httpResp.StatusCode)
	}

	var resp []models.StoryScene
	if err := json.Unmarshal(respBodyBytes, &resp); err != nil {
		log.Error("Failed to unmarshal list story scenes response", zap.Int("status", httpResp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
		return nil, fmt.Errorf("invalid story scenes response format from gameplay service: %w", err)
	}

	// Пустой слайс - валидный ответ
	if resp == nil {
		resp = make([]models.StoryScene, 0)
	}

	log.Info("Story scenes retrieved successfully", zap.Int("count", len(resp)))
	return resp, nil
}
