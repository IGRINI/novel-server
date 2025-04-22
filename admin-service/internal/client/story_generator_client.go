package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"go.uber.org/zap"
)

// generateRequest - структура для тела запроса /generate/stream
type generateRequest struct {
	SystemPrompt string `json:"system_prompt"`
	UserPrompt   string `json:"user_prompt"`
	// Добавляем параметры генерации
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
}

type storyGeneratorClient struct {
	baseURL           string
	httpClient        *http.Client
	logger            *zap.Logger
	interServiceToken string
	mu                sync.RWMutex
	authClient        AuthServiceHttpClient
}

// NewStoryGeneratorClient создает новый клиент для story-generator.
func NewStoryGeneratorClient(baseURL string, timeout time.Duration, logger *zap.Logger, authClient AuthServiceHttpClient) (StoryGeneratorClient, error) {
	_, err := url.ParseRequestURI(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL for story generator service: %w", err)
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	if authClient == nil {
		return nil, fmt.Errorf("auth client cannot be nil")
	}

	return &storyGeneratorClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		logger:     logger.Named("StoryGeneratorClient"),
		authClient: authClient,
	}, nil
}

// doRequestWithTokenRefresh выполняет HTTP запрос с автоматическим обновлением токена при получении 401
func (c *storyGeneratorClient) doRequestWithTokenRefresh(ctx context.Context, req *http.Request) (*http.Response, error) {
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

// GenerateStream генерирует историю в потоковом режиме.
func (c *storyGeneratorClient) GenerateStream(ctx context.Context, systemPrompt, userPrompt string, params GenerationParams) (io.ReadCloser, error) {
	generateURL := fmt.Sprintf("%s/generate/stream", c.baseURL)
	log := c.logger.With(zap.String("url", generateURL))

	body := map[string]interface{}{
		"systemPrompt": systemPrompt,
		"userPrompt":   userPrompt,
		"params":       params,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		log.Error("Failed to marshal generate stream request body", zap.Error(err))
		return nil, fmt.Errorf("internal error marshaling request: %w", err)
	}

	bodyReader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, generateURL, bodyReader)
	if err != nil {
		log.Error("Failed to create generate stream HTTP request", zap.Error(err))
		return nil, fmt.Errorf("internal error creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	log.Debug("Sending generate stream request to story-generator-service")
	resp, err := c.doRequestWithTokenRefresh(ctx, req)
	if err != nil {
		log.Error("HTTP request for generate stream failed", zap.Error(err))
		return nil, fmt.Errorf("failed to communicate with story generator service: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		log.Warn("Received non-OK status for generate stream", zap.Int("status", resp.StatusCode))
		return nil, fmt.Errorf("received unexpected status %d from story generator service for generate stream", resp.StatusCode)
	}

	return resp.Body, nil
}

// GenerateText генерирует историю в текстовом режиме.
func (c *storyGeneratorClient) GenerateText(ctx context.Context, systemPrompt, userPrompt string, params GenerationParams) (string, error) {
	generateURL := fmt.Sprintf("%s/generate/text", c.baseURL)
	log := c.logger.With(zap.String("url", generateURL))

	body := map[string]interface{}{
		"systemPrompt": systemPrompt,
		"userPrompt":   userPrompt,
		"params":       params,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		log.Error("Failed to marshal generate text request body", zap.Error(err))
		return "", fmt.Errorf("internal error marshaling request: %w", err)
	}

	bodyReader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, generateURL, bodyReader)
	if err != nil {
		log.Error("Failed to create generate text HTTP request", zap.Error(err))
		return "", fmt.Errorf("internal error creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	log.Debug("Sending generate text request to story-generator-service")
	resp, err := c.doRequestWithTokenRefresh(ctx, req)
	if err != nil {
		log.Error("HTTP request for generate text failed", zap.Error(err))
		return "", fmt.Errorf("failed to communicate with story generator service: %w", err)
	}
	defer resp.Body.Close()

	respBodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error("Failed to read generate text response body", zap.Int("status", resp.StatusCode), zap.Error(err))
		return "", fmt.Errorf("failed to read story generator service response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Warn("Received non-OK status for generate text", zap.Int("status", resp.StatusCode), zap.ByteString("body", respBodyBytes))
		return "", fmt.Errorf("received unexpected status %d from story generator service for generate text", resp.StatusCode)
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(respBodyBytes, &result); err != nil {
		log.Error("Failed to unmarshal generate text response", zap.Int("status", resp.StatusCode), zap.ByteString("body", respBodyBytes), zap.Error(err))
		return "", fmt.Errorf("invalid generate text response format from story generator service: %w", err)
	}

	log.Info("Text generated successfully", zap.Int("length", len(result.Text)))
	return result.Text, nil
}

// SetInterServiceToken устанавливает межсервисный токен
func (c *storyGeneratorClient) SetInterServiceToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.interServiceToken = token
}
