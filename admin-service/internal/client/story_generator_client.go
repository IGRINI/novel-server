package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	baseURL    string
	httpClient *http.Client
	logger     *zap.Logger
	// Добавить межсервисный токен, если story-generator его требует
	// interServiceToken string
	// mu sync.RWMutex
}

// NewStoryGeneratorClient создает новый клиент для story-generator.
func NewStoryGeneratorClient(baseURL string, timeout time.Duration, logger *zap.Logger) (StoryGeneratorClient, error) {
	_, err := url.ParseRequestURI(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL for story generator service: %w", err)
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	return &storyGeneratorClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout, // Таймаут на весь запрос, для стриминга может потребоваться отдельная настройка
		},
		logger: logger.Named("StoryGeneratorClient"),
	}, nil
}

// GenerateStream отправляет запрос на генерацию и возвращает тело ответа для стриминга.
func (c *storyGeneratorClient) GenerateStream(ctx context.Context, systemPrompt, userPrompt string, params GenerationParams) (io.ReadCloser, error) {
	generateURL := c.baseURL + "/generate/stream" // Предполагаемый эндпоинт
	log := c.logger.With(
		zap.String("url", generateURL),
		zap.String("systemPromptLength", fmt.Sprintf("%d chars", len(systemPrompt))),
		zap.String("userPromptLength", fmt.Sprintf("%d chars", len(userPrompt))),
		// Логируем переданные параметры (если они не nil)
		zap.Any("params", params),
	)

	reqPayload := generateRequest{
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		// Добавляем параметры в тело запроса
		Temperature: params.Temperature,
		MaxTokens:   params.MaxTokens,
		TopP:        params.TopP,
	}
	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		log.Error("Failed to marshal story generation request payload", zap.Error(err))
		return nil, fmt.Errorf("internal error marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, generateURL, bytes.NewBuffer(reqBody))
	if err != nil {
		log.Error("Failed to create story generation HTTP request", zap.Error(err))
		return nil, fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/plain") // Ожидаем простой текст
	// TODO: Добавить заголовок авторизации, если story-generator требует (например, X-Internal-Service-Token)
	// c.mu.RLock()
	// token := c.interServiceToken
	// c.mu.RUnlock()
	// if token != "" {
	// 	httpReq.Header.Set("X-Internal-Service-Token", token)
	// } else {
	// 	log.Warn("Inter-service token is not set for story-generator call")
	// }

	log.Debug("Sending story generation request to story-generator")
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// Не используем errors.Is(err, context.DeadlineExceeded) здесь,
		// так как для стриминга соединение может быть долгим.
		// Обработка таймаутов должна быть на уровне чтения/записи потока.
		log.Error("HTTP request to story-generator failed", zap.Error(err))
		return nil, fmt.Errorf("failed to communicate with story generator service: %w", err)
	}

	// Проверяем статус ответа. Ожидаем 200 OK для успешного начала стриминга.
	if httpResp.StatusCode != http.StatusOK {
		defer httpResp.Body.Close()
		respBodyBytes, _ := io.ReadAll(httpResp.Body) // Читаем тело для логирования ошибки
		log.Error("Story generator returned non-OK status",
			zap.Int("status", httpResp.StatusCode),
			zap.ByteString("body", respBodyBytes),
		)
		// TODO: Разобрать структуру ошибки от story-generator, если она есть
		return nil, fmt.Errorf("story generator service returned status %d", httpResp.StatusCode)
	}

	log.Info("Successfully initiated stream connection with story-generator")
	// Возвращаем тело ответа как io.ReadCloser. Вызывающая сторона должна его закрыть.
	return httpResp.Body, nil
}

// GenerateText отправляет запрос на генерацию и возвращает полный текст ответа.
func (c *storyGeneratorClient) GenerateText(ctx context.Context, systemPrompt, userPrompt string, params GenerationParams) (string, error) {
	generateURL := c.baseURL + "/generate" // <<< Новый эндпоинт
	log := c.logger.With(
		zap.String("url", generateURL),
		zap.String("systemPromptLength", fmt.Sprintf("%d chars", len(systemPrompt))),
		zap.String("userPromptLength", fmt.Sprintf("%d chars", len(userPrompt))),
		zap.Any("params", params),
	)

	reqPayload := generateRequest{
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		Temperature:  params.Temperature,
		MaxTokens:    params.MaxTokens,
		TopP:         params.TopP,
	}
	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		log.Error("Failed to marshal story generation request payload", zap.Error(err))
		return "", fmt.Errorf("internal error marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, generateURL, bytes.NewBuffer(reqBody))
	if err != nil {
		log.Error("Failed to create story generation HTTP request", zap.Error(err))
		return "", fmt.Errorf("internal error creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/plain") // <<< Ожидаем простой текст
	// TODO: Добавить заголовок авторизации, если нужно

	log.Debug("Sending story generation request (non-streaming) to story-generator")
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Error("HTTP request to story-generator failed", zap.Error(err))
		if errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("request to story generator service timed out: %w", err)
		}
		return "", fmt.Errorf("failed to communicate with story generator service: %w", err)
	}
	defer httpResp.Body.Close()

	respBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		log.Error("Failed to read response body from story-generator", zap.Int("status", httpResp.StatusCode), zap.Error(err))
		return "", fmt.Errorf("failed to read story generator response: %w", err)
	}

	// Проверяем статус ответа. Ожидаем 200 OK.
	if httpResp.StatusCode != http.StatusOK {
		log.Error("Story generator returned non-OK status",
			zap.Int("status", httpResp.StatusCode),
			zap.ByteString("body", respBodyBytes), // Логируем тело ошибки
		)
		return "", fmt.Errorf("story generator service returned status %d: %s", httpResp.StatusCode, string(respBodyBytes))
	}

	log.Info("Successfully received response from story-generator")
	return string(respBodyBytes), nil // Возвращаем текст
}

// TODO: Добавить метод SetInterServiceToken, если требуется аутентификация
// func (c *storyGeneratorClient) SetInterServiceToken(token string) {
// 	c.mu.Lock()
// 	defer c.mu.Unlock()
// 	c.logger.Info("Inter-service token for story-generator has been set")
// 	c.interServiceToken = token
// }
