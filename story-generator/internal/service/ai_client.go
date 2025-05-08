package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	openaigo "github.com/sashabaranov/go-openai"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"novel-server/shared/configservice"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"
)

type GenerationParams struct {
	Temperature *float64
	MaxTokens   *int
	TopP        *float64
}

// --- Вспомогательный тип для добавления заголовков ---
type headerTransport struct {
	Transport http.RoundTripper
	Headers   map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Клонируем заголовки запроса, чтобы не модифицировать оригинальный req.Header напрямую
	// (на случай, если он используется где-то еще)
	newHeader := req.Header.Clone()
	// Добавляем кастомные заголовки
	for key, value := range t.Headers {
		// Используем Add, а не Set, чтобы не перезаписывать существующие заголовки с тем же именем,
		// хотя для HTTP-Referer и X-Title это обычно не критично.
		// Set тоже подойдет.
		newHeader.Set(key, value)
	}
	req.Header = newHeader // Устанавливаем модифицированные заголовки

	// Выполняем запрос через базовый транспорт
	// Если Transport не задан, используем http.DefaultTransport
	transport := t.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return transport.RoundTrip(req)
}

// --- Конец вспомогательного типа ---

var ErrAIGenerationFailed = errors.New("ошибка генерации текста AI")

var (
	aiRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "story_generator_ai_requests_total",
			Help: "Total number of requests to the AI API.",
		},
		[]string{"model", "status", "user_id"},
	)
	aiRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "story_generator_ai_request_duration_seconds",
			Help:    "Histogram of AI API request durations.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"model", "user_id"},
	)
	aiPromptTokens = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "story_generator_ai_prompt_tokens",
			Help:    "Histogram of prompt token counts.",
			Buckets: prometheus.LinearBuckets(250, 250, 20),
		},
		[]string{"model", "user_id"},
	)
	aiCompletionTokens = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "story_generator_ai_completion_tokens",
			Help:    "Histogram of completion token counts.",
			Buckets: prometheus.LinearBuckets(100, 100, 20),
		},
		[]string{"model", "user_id"},
	)
	aiTotalTokens = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "story_generator_ai_total_tokens",
			Help:    "Histogram of total token counts (prompt + completion).",
			Buckets: prometheus.LinearBuckets(350, 350, 20),
		},
		[]string{"model", "user_id"},
	)
	aiEstimatedCostUSD = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "story_generator_ai_estimated_cost_usd_total",
			Help: "Estimated total cost of AI requests in USD.",
		},
		[]string{"model", "user_id"},
	)
)

type UsageInfo struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	EstimatedCostUSD float64
}

type AIClient interface {
	GenerateText(ctx context.Context, userID string, systemPrompt string, userInput string, promptType models.PromptType, params GenerationParams) (string, UsageInfo, error)
}

func calculateCost(configService *configservice.ConfigService, promptTokens, completionTokens int) float64 {
	pricePerMillionInput := configService.GetFloat(configservice.ConfigKeyInputCost, configservice.DefaultInputCost)
	pricePerMillionOutput := configService.GetFloat(configservice.ConfigKeyOutputCost, configservice.DefaultOutputCost)
	inputCost := float64(promptTokens) * pricePerMillionInput / 1_000_000.0
	outputCost := float64(completionTokens) * pricePerMillionOutput / 1_000_000.0
	return inputCost + outputCost
}

// openAIClient теперь будет использовать http.Client напрямую
type openAIClient struct {
	httpClient        *http.Client
	configService     *configservice.ConfigService
	provider          string
	apiKey            string
	dynamicConfigRepo interfaces.DynamicConfigRepository
	db                interfaces.DBTX
}

// schemaHolder помогает передать map[string]interface{} как json.Marshaler
type schemaHolder struct {
	Schema map[string]interface{}
}

// MarshalJSON реализует интерфейс json.Marshaler
func (s schemaHolder) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Schema)
}

func (c *openAIClient) GenerateText(ctx context.Context, userID string, systemPrompt string, userInput string, promptType models.PromptType, params GenerationParams) (string, UsageInfo, error) {
	usageInfo := UsageInfo{}
	currentModel := c.configService.GetString(configservice.ConfigKeyAIModel, configservice.DefaultAIModel)
	currentBaseURL := c.configService.GetString(configservice.ConfigKeyAIBaseURL, configservice.DefaultAIBaseURL)
	log.Printf("[DEBUG] Preparing HTTP AI request. BaseURL: %s, Model: %s, UserID: %s, PromptType: %s", currentBaseURL, currentModel, userID, promptType)

	if c.apiKey == "" {
		log.Printf("[ERROR] AI API key was not configured. Cannot make request.")
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_missing_key", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: AI API key is not configured", ErrAIGenerationFailed)
	}

	if strings.TrimSpace(systemPrompt) == "" {
		log.Printf("Ошибка: Системный промт пуст после подготовки. userID: %s", userID)
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: системный промт пуст", ErrAIGenerationFailed)
	}

	// --- Определяем температуру ПЕРЕД созданием запроса ---
	var finalTemperature float32
	if params.Temperature != nil {
		finalTemperature = float32(*params.Temperature)
		log.Printf("[DEBUG] Using temperature from params: %.2f (UserID: %s)", finalTemperature, userID)
	} else {
		finalTemperature = float32(c.configService.GetFloat(configservice.ConfigKeyAITemperature, configservice.DefaultAITemperature))
		log.Printf("[DEBUG] Using temperature from config: %.2f (UserID: %s)", finalTemperature, userID)
	}

	// --- Создание тела запроса ---
	// Используем структуры openaigo для удобства, т.к. они соответствуют ожиданиям API
	requestPayload := openaigo.ChatCompletionRequest{
		Model:       currentModel,
		Temperature: finalTemperature,
		MaxTokens:   intVal(params.MaxTokens),
		TopP:        float32Val(params.TopP),
		// ResponseFormat будет установлен ниже
	}

	// Use plain text response format (no JSON enforcement)
	systemPromptContent := systemPrompt

	messages := []openaigo.ChatCompletionMessage{
		{
			Role:    openaigo.ChatMessageRoleSystem,
			Content: systemPromptContent,
		},
	}
	if userInput != "" {
		messages = append(messages, openaigo.ChatCompletionMessage{
			Role:    openaigo.ChatMessageRoleUser,
			Content: userInput,
		})
	}
	requestPayload.Messages = messages

	requestJSON, err := json.Marshal(requestPayload)
	if err != nil {
		log.Printf("[ERROR] Failed to marshal request payload to JSON (userID: %s): %v", userID, err)
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_marshal_request", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: failed to marshal request: %v", ErrAIGenerationFailed, err)
	}
	log.Printf("[DEBUG] AI Request Body: %s", string(requestJSON))

	startTime := time.Now()
	log.Printf("Отправка HTTP запроса к %s: Model=%s, SystemPrompt=%d bytes, UserInput=%d bytes, UserID: %s, PromptType: %s",
		currentBaseURL, currentModel, len(systemPromptContent), len(userInput), userID, promptType)
	log.Printf("[DEBUG_INPUT] System Prompt for %s (UserID: %s):\n--- START ---\n%s\n--- END ---", currentBaseURL, userID, systemPromptContent)
	log.Printf("[DEBUG_INPUT] User Input for %s (UserID: %s):\n--- START ---\n%s\n--- END ---", currentBaseURL, userID, userInput)

	// Создаем HTTP запрос
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, currentBaseURL, bytes.NewBuffer(requestJSON))
	if err != nil {
		log.Printf("[ERROR] Failed to create HTTP request (userID: %s): %v", userID, err)
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_create_request", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: failed to create http request: %v", ErrAIGenerationFailed, err)
	}

	// Устанавливаем заголовки
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	// Кастомные заголовки HTTP-Referer и X-Title уже должны быть добавлены транспортом в c.httpClient

	// Отправляем запрос
	httpResponse, err := c.httpClient.Do(httpRequest)
	duration := time.Since(startTime)

	if err != nil {
		log.Printf("Ошибка при отправке HTTP запроса к %s за %v (userID: %s). Error Type: %T, Error: %v",
			currentBaseURL, duration, userID, err, err)
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			log.Printf("[DEBUG] Request to %s timed out after %v (userID: %s)", currentBaseURL, duration, userID)
			aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_timeout", "user_id": userID}).Inc()
		} else {
			aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_http_request", "user_id": userID}).Inc()
		}
		return "", usageInfo, fmt.Errorf("%w: http request failed: %v", ErrAIGenerationFailed, err)
	}
	defer httpResponse.Body.Close()

	responseBody, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		log.Printf("[ERROR] Failed to read response body from %s (userID: %s, status: %d): %v",
			currentBaseURL, userID, httpResponse.StatusCode, err)
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_read_response_body", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: failed to read response body: %v", ErrAIGenerationFailed, err)
	}
	if httpResponse.StatusCode != http.StatusOK {
		log.Printf("Ошибка от %s API: HTTP Status %d. Тело ответа: %s (userID: %s)",
			currentBaseURL, httpResponse.StatusCode, string(responseBody), userID)
		var apiError openaigo.ErrorResponse
		if jsonErr := json.Unmarshal(responseBody, &apiError); jsonErr == nil && apiError.Error != nil {
			log.Printf("[DEBUG] Parsed API Error Details: Type=%s, Code=%v, Message=%s, Param=%v",
				apiError.Error.Type, apiError.Error.Code, apiError.Error.Message, apiError.Error.Param)
			aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_api_" + httpResponse.Status, "user_id": userID}).Inc()
			return "", usageInfo, fmt.Errorf("%w: API error (%s): %s", ErrAIGenerationFailed, httpResponse.Status, apiError.Error.Message)
		}
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_http_" + httpResponse.Status, "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: received non-200 status %d from API: %s", ErrAIGenerationFailed, httpResponse.StatusCode, string(responseBody))
	}

	var resp openaigo.ChatCompletionResponse
	if err := json.Unmarshal(responseBody, &resp); err != nil {
		log.Printf("[ERROR] Failed to unmarshal JSON response from %s (userID: %s): %v. Response body: %s",
			currentBaseURL, userID, err, string(responseBody))
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_unmarshal_response", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: failed to unmarshal response: %v. Body: %s", ErrAIGenerationFailed, err, string(responseBody))
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		log.Printf("[DEBUG] Received response object from %s (before empty content check, userID: %s): %+v", currentBaseURL, userID, resp)
		log.Printf("%s API вернул пустой ответ или пустое содержимое за %v (userID: %s)", currentBaseURL, duration, userID)
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_empty_response", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: получен пустой ответ", ErrAIGenerationFailed)
	}

	aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "success", "user_id": userID}).Inc()
	aiRequestDuration.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(duration.Seconds())
	generatedText := resp.Choices[0].Message.Content
	log.Printf("Ответ от %s API получен за %v. Длина ответа: %d символов. (userID: %s)", currentBaseURL, duration, len(generatedText), userID)
	log.Printf("[DEBUG] AI Usage received (userID: %s): %+v", userID, resp.Usage)

	if resp.Usage.TotalTokens > 0 {
		log.Printf("AI Usage (userID: %s): PromptTokens=%d, CompletionTokens=%d, TotalTokens=%d",
			userID, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
		aiPromptTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(resp.Usage.PromptTokens))
		aiCompletionTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(resp.Usage.CompletionTokens))
		aiTotalTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(resp.Usage.TotalTokens))
		usageInfo.PromptTokens = resp.Usage.PromptTokens
		usageInfo.CompletionTokens = resp.Usage.CompletionTokens
		usageInfo.TotalTokens = resp.Usage.TotalTokens
		usageInfo.EstimatedCostUSD = calculateCost(c.configService, resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
		log.Printf("[DEBUG] Calculated cost (userID: %s, model: %s): %.8f", userID, currentModel, usageInfo.EstimatedCostUSD)
		if usageInfo.EstimatedCostUSD > 0 {
			aiEstimatedCostUSD.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Add(usageInfo.EstimatedCostUSD)
			log.Printf("%s Usage Cost (estimated, userID: %s): $%.6f", c.provider, userID, usageInfo.EstimatedCostUSD)
		}
	}
	return generatedText, usageInfo, nil
}

func float32Val(f64 *float64) float32 {
	if f64 == nil {
		return 1.0
	}
	f32 := float32(*f64)
	return f32
}

func intVal(i *int) int {
	if i == nil {
		return 0
	}
	return *i
}

func NewAIClient(configService *configservice.ConfigService, dynConfRepo interfaces.DynamicConfigRepository, db interfaces.DBTX) (AIClient, error) {
	aiBaseURL := configService.GetString(configservice.ConfigKeyAIBaseURL, configservice.DefaultAIBaseURL)
	// aiClientType больше не определяет библиотеку, а скорее общую конфигурацию.
	// Пока оставим его, но его роль изменилась.
	// aiClientType := configService.GetString(configservice.ConfigKeyAIClientType, configservice.DefaultAIClientType) // Удалено, так как не используется
	aiTimeout := configService.GetDuration(configservice.ConfigKeyAITimeout, configservice.DefaultAITimeout)
	var aiAPIKey string
	secretPath := "/run/secrets/ai_api_key"
	secretBytes, err := os.ReadFile(secretPath)
	if err == nil {
		trimmedKey := bytes.TrimSpace(secretBytes)
		if len(trimmedKey) > 0 {
			aiAPIKey = string(trimmedKey)
			log.Printf("[INFO] AI API Key successfully loaded from Docker secret: %s", secretPath)
		}
	} else if !os.IsNotExist(err) {
		log.Printf("[WARN] Error reading AI API Key secret from %s: %v. Will try dynamic config.", secretPath, err)
	}
	if aiAPIKey == "" {
		log.Printf("[INFO] AI API Key not found in secret file %s, trying dynamic config key '%s'...", secretPath, configservice.ConfigKeyAIAPIKey)
		aiAPIKey = configService.GetString(configservice.ConfigKeyAIAPIKey, "")
		if aiAPIKey != "" {
			log.Printf("[INFO] AI API Key loaded from dynamic config.")
		} else {
			log.Printf("[WARN] AI API Key not found in dynamic config either.")
		}
	}
	log.Printf("Используется HTTP клиент для AI запросов.")
	if aiAPIKey == "" {
		log.Printf("[WARN] AI API Key is NOT configured (checked secret file and dynamic config). Client created, but requests will fail if key is required by the API at %s.", aiBaseURL)
	}

	// --- Модификация HTTP клиента для добавления заголовков ---
	baseTransport := http.DefaultTransport
	if dt, ok := http.DefaultTransport.(*http.Transport); ok && dt != nil {
		clonedTransport := dt.Clone()
		baseTransport = clonedTransport
	}

	customTransport := &headerTransport{
		Transport: baseTransport,
		Headers: map[string]string{
			"HTTP-Referer": "crion.space",
			"X-Title":      "TaleShift",
		},
	}

	httpClient := &http.Client{
		Timeout:   aiTimeout,
		Transport: customTransport,
	}
	log.Printf("HTTP AI Клиент создан. Используемый BaseURL (из конфига): %s, Timeout: %v, Custom Headers Added: HTTP-Referer, X-Title", aiBaseURL, aiTimeout)
	return &openAIClient{
		httpClient:        httpClient,
		configService:     configService,
		provider:          "HTTP_AI_Provider",
		apiKey:            aiAPIKey,
		dynamicConfigRepo: dynConfRepo,
		db:                db,
	}, nil
}
