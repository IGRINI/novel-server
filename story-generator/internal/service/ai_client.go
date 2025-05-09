package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
	"github.com/pkoukk/tiktoken-go"
	openaigo "github.com/sashabaranov/go-openai"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"novel-server/shared/configservice"
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
	GenerateText(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams) (string, UsageInfo, error)
	GenerateTextStream(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams, chunkHandler func(string) error) (UsageInfo, error)
}

func calculateCost(configService *configservice.ConfigService, promptTokens, completionTokens int) float64 {
	pricePerMillionInput := configService.GetFloat(configservice.ConfigKeyInputCost, configservice.DefaultInputCost)
	pricePerMillionOutput := configService.GetFloat(configservice.ConfigKeyOutputCost, configservice.DefaultOutputCost)
	inputCost := float64(promptTokens) * pricePerMillionInput / 1_000_000.0
	outputCost := float64(completionTokens) * pricePerMillionOutput / 1_000_000.0
	return inputCost + outputCost
}

type openAIClient struct {
	client        *openaigo.Client
	configService *configservice.ConfigService
	provider      string
	apiKey        string
}

func (c *openAIClient) GenerateText(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams) (string, UsageInfo, error) {
	usageInfo := UsageInfo{}
	currentModel := c.configService.GetString(configservice.ConfigKeyAIModel, configservice.DefaultAIModel)
	currentBaseURL := c.configService.GetString(configservice.ConfigKeyAIBaseURL, configservice.DefaultAIBaseURL)
	log.Printf("[DEBUG] Preparing OpenAI request. BaseURL: %s, Model: %s, UserID: %s", currentBaseURL, currentModel, userID)
	if c.apiKey == "" && c.provider == "OpenAI" {
		log.Printf("[ERROR] OpenAI API key was not configured during client initialization. Cannot make request.")
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_missing_key", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: OpenAI API key is not configured", ErrAIGenerationFailed)
	}
	if strings.TrimSpace(systemPrompt) == "" {
		log.Printf("Ошибка: Системный промт пуст после подготовки. userID: %s", userID)
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: системный промт пуст", ErrAIGenerationFailed)
	}
	messages := []openaigo.ChatCompletionMessage{
		{
			Role:    openaigo.ChatMessageRoleSystem,
			Content: systemPrompt,
		},
	}
	if userInput != "" {
		messages = append(messages, openaigo.ChatCompletionMessage{
			Role:    openaigo.ChatMessageRoleUser,
			Content: userInput,
		})
	}

	// Определяем температуру: приоритет у params, иначе из конфига
	var finalTemperature float32
	if params.Temperature != nil {
		finalTemperature = float32(*params.Temperature)
		log.Printf("[DEBUG] Using temperature from params: %.2f (UserID: %s)", finalTemperature, userID)
	} else {
		finalTemperature = float32(c.configService.GetFloat(configservice.ConfigKeyAITemperature, configservice.DefaultAITemperature))
		log.Printf("[DEBUG] Using temperature from config: %.2f (UserID: %s)", finalTemperature, userID)
	}

	startTime := time.Now()
	log.Printf("Отправка запроса к %s: Model=%s, SystemPrompt=%d bytes, UserInput=%d bytes, UserID: %s",
		c.provider, currentModel, len(systemPrompt), len(userInput), userID)
	log.Printf("[DEBUG_INPUT] System Prompt for %s (UserID: %s):\n--- START ---\n%s\n--- END ---", c.provider, userID, systemPrompt)
	log.Printf("[DEBUG_INPUT] User Input for %s (UserID: %s):\n--- START ---\n%s\n--- END ---", c.provider, userID, userInput)
	request := openaigo.ChatCompletionRequest{
		Model:       currentModel,
		Messages:    messages,
		Temperature: finalTemperature,
		MaxTokens:   intVal(params.MaxTokens),
		TopP:        float32Val(params.TopP),
		ResponseFormat: &openaigo.ChatCompletionResponseFormat{
			Type: openaigo.ChatCompletionResponseFormatTypeJSONObject,
		},
	}
	requestJSON, _ := json.Marshal(request)
	log.Printf("[DEBUG] OpenAI Request Body: %s", string(requestJSON))
	resp, err := c.client.CreateChatCompletion(
		ctx,
		request,
	)
	duration := time.Since(startTime)
	if err != nil {
		log.Printf("Ошибка от %s API за %v (userID: %s). Error Type: %T, Error: %v",
			c.provider, duration, userID, err, err)
		var apiError *openaigo.APIError
		if errors.As(err, &apiError) {
			log.Printf("[DEBUG] OpenAI API Error Details: StatusCode=%d, Type=%s, Code=%v, Message=%s",
				apiError.HTTPStatusCode, apiError.Type, apiError.Code, apiError.Message)
		}
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: %v", ErrAIGenerationFailed, err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		log.Printf("[DEBUG] Received response object from %s (before empty content check, userID: %s): %+v", c.provider, userID, resp)
		log.Printf("%s API вернул пустой ответ за %v (userID: %s)", c.provider, duration, userID)
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_empty_response", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: получен пустой ответ", ErrAIGenerationFailed)
	}
	aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "success", "user_id": userID}).Inc()
	aiRequestDuration.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(duration.Seconds())
	generatedText := resp.Choices[0].Message.Content
	log.Printf("Ответ от %s API получен за %v. Длина ответа: %d символов. (userID: %s)", c.provider, duration, len(generatedText), userID)
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

func (c *openAIClient) GenerateTextStream(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams, chunkHandler func(string) error) (UsageInfo, error) {
	usageInfo := UsageInfo{}
	currentModel := c.configService.GetString(configservice.ConfigKeyAIModel, configservice.DefaultAIModel)
	if c.apiKey == "" && c.provider == "OpenAI" {
		log.Printf("[ERROR] OpenAI API key was not configured during client initialization for stream.")
		return usageInfo, fmt.Errorf("%w: OpenAI API key is not configured for stream", ErrAIGenerationFailed)
	}
	if strings.TrimSpace(systemPrompt) == "" {
		log.Printf("Ошибка стриминга: Системный промт пуст после подготовки. userID: %s", userID)
		return usageInfo, fmt.Errorf("%w: системный промт пуст для стриминга", ErrAIGenerationFailed)
	}
	messages := []openaigo.ChatCompletionMessage{
		{
			Role:    openaigo.ChatMessageRoleSystem,
			Content: systemPrompt,
		},
	}
	if userInput != "" {
		messages = append(messages, openaigo.ChatCompletionMessage{
			Role:    openaigo.ChatMessageRoleUser,
			Content: userInput,
		})
	}

	// Определяем температуру: приоритет у params, иначе из конфига
	var finalTemperature float32
	if params.Temperature != nil {
		finalTemperature = float32(*params.Temperature)
		log.Printf("[DEBUG] Using stream temperature from params: %.2f (UserID: %s)", finalTemperature, userID)
	} else {
		finalTemperature = float32(c.configService.GetFloat(configservice.ConfigKeyAITemperature, configservice.DefaultAITemperature))
		log.Printf("[DEBUG] Using stream temperature from config: %.2f (UserID: %s)", finalTemperature, userID)
	}

	request := openaigo.ChatCompletionRequest{
		Model:       currentModel,
		Messages:    messages,
		Stream:      true,
		Temperature: finalTemperature,
		MaxTokens:   intVal(params.MaxTokens),
		TopP:        float32Val(params.TopP),
	}
	log.Printf("Отправка STREAM запроса к %s: Model=%s, SystemPrompt=%d bytes, UserInput=%d bytes, UserID: %s",
		c.provider, currentModel, len(systemPrompt), len(userInput), userID)
	stream, err := c.client.CreateChatCompletionStream(ctx, request)
	if err != nil {
		log.Printf("Ошибка создания стрима от %s API (userID: %s): %v", c.provider, userID, err)
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_stream_init", "user_id": userID}).Inc()
		return usageInfo, fmt.Errorf("%w: ошибка создания стрима: %v", ErrAIGenerationFailed, err)
	}
	defer stream.Close()
	log.Printf("Стрим от %s API успешно инициирован. Чтение... (userID: %s)", c.provider, userID)
	startTime := time.Now()
	completionTokensCount := 0
	promptTokensCount := 0
	var finalUsage openaigo.Usage
	var responseTextBuilder strings.Builder
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			log.Printf("Стрим %s завершен. (userID: %s)", c.provider, userID)
			break
		}
		if err != nil {
			log.Printf("Ошибка чтения из стрима %s (userID: %s): %v", c.provider, userID, err)
			aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_stream_read", "user_id": userID}).Inc()
			return usageInfo, fmt.Errorf("%w: ошибка чтения стрима: %v", ErrAIGenerationFailed, err)
		}
		if response.Usage != nil && response.Usage.TotalTokens > 0 {
			finalUsage = *response.Usage
			log.Printf("[DEBUG] Received final usage info in stream (userID: %s): %+v", userID, finalUsage)
		}
		if len(response.Choices) > 0 {
			chunk := response.Choices[0].Delta.Content
			responseTextBuilder.WriteString(chunk)
			tke, err := tiktoken.EncodingForModel(currentModel)
			if err == nil {
				completionTokensCount += len(tke.Encode(chunk, nil, nil))
			}
			if chunkHandler != nil {
				if err := chunkHandler(chunk); err != nil {
					log.Printf("Ошибка обработчика чанка стрима (userID: %s): %v", userID, err)
				}
			}
		}
	}
	duration := time.Since(startTime)
	log.Printf("Чтение стрима %s завершено за %v. (userID: %s)", c.provider, duration, userID)
	if finalUsage.TotalTokens > 0 {
		promptTokensCount = finalUsage.PromptTokens
		completionTokensCount = finalUsage.CompletionTokens
		usageInfo.PromptTokens = promptTokensCount
		usageInfo.CompletionTokens = completionTokensCount
		usageInfo.TotalTokens = finalUsage.TotalTokens
		usageInfo.EstimatedCostUSD = calculateCost(c.configService, promptTokensCount, completionTokensCount)
		log.Printf("%s Stream Usage (from final block, userID: %s): Prompt=%d, Completion=%d, Total=%d",
			c.provider, userID, promptTokensCount, completionTokensCount, finalUsage.TotalTokens)
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "success_stream", "user_id": userID}).Inc()
		aiRequestDuration.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(duration.Seconds())
		aiPromptTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(promptTokensCount))
		aiCompletionTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(completionTokensCount))
		aiTotalTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(finalUsage.TotalTokens))
	} else {
		log.Printf("[WARN] Final usage block not received in stream (userID: %s). Using estimated token counts.", userID)
		tke, err := tiktoken.EncodingForModel(currentModel)
		if err == nil {
			promptTokensCount = len(tke.Encode(systemPrompt, nil, nil)) + len(tke.Encode(userInput, nil, nil))
			totalTokens := promptTokensCount + completionTokensCount
			usageInfo.PromptTokens = promptTokensCount
			usageInfo.CompletionTokens = completionTokensCount
			usageInfo.TotalTokens = totalTokens
			usageInfo.EstimatedCostUSD = calculateCost(c.configService, promptTokensCount, completionTokensCount)
			log.Printf("%s Stream Usage (estimated, userID: %s): Prompt≈%d, Completion≈%d, Total≈%d",
				c.provider, userID, promptTokensCount, completionTokensCount, totalTokens)
			aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "success_stream_estimated", "user_id": userID}).Inc()
			aiRequestDuration.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(duration.Seconds())
			aiPromptTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(promptTokensCount))
			aiCompletionTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(completionTokensCount))
			aiTotalTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(totalTokens))
		} else {
			log.Printf("[ERROR] Could not get tokenizer for model %s to estimate stream tokens (userID: %s). Skipping token metrics for this stream.", currentModel, userID)
			aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "success_stream_no_tokens", "user_id": userID}).Inc()
			aiRequestDuration.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(duration.Seconds())
		}
	}
	if usageInfo.EstimatedCostUSD > 0 {
		aiEstimatedCostUSD.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Add(usageInfo.EstimatedCostUSD)
		log.Printf("%s Stream Usage Cost (estimated, userID: %s): $%.6f", c.provider, userID, usageInfo.EstimatedCostUSD)
	}
	return usageInfo, nil
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

type ollamaClient struct {
	client        *api.Client
	configService *configservice.ConfigService
}

func newOllamaClient(configService *configservice.ConfigService) (AIClient, error) {
	aiBaseURL := configService.GetString(configservice.ConfigKeyAIBaseURL, configservice.DefaultAIBaseURL)
	aiTimeout := configService.GetDuration(configservice.ConfigKeyAITimeout, configservice.DefaultAITimeout)
	httpClient := &http.Client{
		Timeout: aiTimeout,
	}
	ollamaBaseURL := strings.TrimSuffix(aiBaseURL, "/v1")
	ollamaBaseURL = strings.TrimSuffix(ollamaBaseURL, "/")
	parsedURL, err := url.Parse(ollamaBaseURL)
	if err != nil {
		return nil, fmt.Errorf("ошибка парсинга Ollama Base URL '%s': %w", ollamaBaseURL, err)
	}
	client := api.NewClient(parsedURL, httpClient)
	log.Printf("Ollama Клиент создан. Используемый BaseURL: %s, Timeout: %v", ollamaBaseURL, aiTimeout)
	return &ollamaClient{
		client:        client,
		configService: configService,
	}, nil
}

func (c *ollamaClient) GenerateText(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams) (string, UsageInfo, error) {
	usageInfo := UsageInfo{EstimatedCostUSD: 0}
	currentModel := c.configService.GetString(configservice.ConfigKeyAIModel, "")
	if currentModel == "" {
		log.Printf("[ERROR] Ollama model is not configured (key: %s)", configservice.ConfigKeyAIModel)
		return "", usageInfo, fmt.Errorf("%w: Ollama model name is not configured", ErrAIGenerationFailed)
	}
	aiTimeout := c.configService.GetDuration(configservice.ConfigKeyAITimeout, configservice.DefaultAITimeout)
	if strings.TrimSpace(systemPrompt) == "" {
		log.Printf("Ошибка: Системный промт пуст. Невозможно отправить запрос к Ollama. userID: %s", userID)
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: системный промт пуст", ErrAIGenerationFailed)
	}
	messages := []api.Message{
		{Role: "system", Content: systemPrompt},
	}
	if userInput != "" {
		messages = append(messages, api.Message{Role: "user", Content: userInput})
	}

	// Определяем температуру: приоритет у params, иначе из конфига
	var finalTemperature float64
	if params.Temperature != nil {
		finalTemperature = *params.Temperature
		log.Printf("[DEBUG] Using Ollama temperature from params: %.2f (UserID: %s)", finalTemperature, userID)
	} else {
		finalTemperature = c.configService.GetFloat(configservice.ConfigKeyAITemperature, configservice.DefaultAITemperature)
		log.Printf("[DEBUG] Using Ollama temperature from config: %.2f (UserID: %s)", finalTemperature, userID)
	}

	log.Printf("Sending request to Ollama: Model=%s, UserID: %s", currentModel, userID)
	startTime := time.Now()
	req := &api.ChatRequest{
		Model:    currentModel,
		Messages: messages,
		Stream:   func(b bool) *bool { return &b }(false),
		Options: map[string]interface{}{
			"temperature": finalTemperature,
			"num_predict": intVal(params.MaxTokens),
			"top_p":       params.TopP,
		},
	}

	requestCtx, cancel := context.WithTimeout(ctx, aiTimeout)
	defer cancel()
	err := c.client.Chat(requestCtx, req, func(resp api.ChatResponse) error {
		if resp.Message.Content != "" {
			log.Printf("User Input (Ollama) (len %d): '%s'", len(resp.Message.Content), resp.Message.Content)
		}
		if resp.Done {
			usageInfo.PromptTokens = resp.PromptEvalCount
			usageInfo.CompletionTokens = resp.EvalCount
			usageInfo.TotalTokens = resp.PromptEvalCount + resp.EvalCount
			usageInfo.EstimatedCostUSD = 0
			if usageInfo.TotalTokens > 0 {
				log.Printf("Ollama Usage (userID: %s): PromptTokens=%d, CompletionTokens=%d, TotalTokens=%d",
					userID, usageInfo.PromptTokens, usageInfo.CompletionTokens, usageInfo.TotalTokens)
				aiPromptTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(usageInfo.PromptTokens))
				aiCompletionTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(usageInfo.CompletionTokens))
				aiTotalTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(usageInfo.TotalTokens))
			}
		}
		return nil
	})
	duration := time.Since(startTime)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			log.Printf("Ошибка таймаута (%v) во время генерации Ollama за %v (userID: %s): %v", aiTimeout, duration, userID, err)
		} else {
			log.Printf("Ошибка во время генерации Ollama за %v (userID: %s): %v", duration, userID, err)
		}
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: %v", ErrAIGenerationFailed, err)
	}
	if usageInfo.TotalTokens == 0 {
		log.Printf("[WARN] Final usage block not received in Ollama response (userID: %s). Using estimated token counts.", userID)
		tke, err := tiktoken.EncodingForModel(currentModel)
		if err == nil {
			promptTokensCount := len(tke.Encode(systemPrompt, nil, nil)) + len(tke.Encode(userInput, nil, nil))
			totalTokens := promptTokensCount + usageInfo.CompletionTokens
			usageInfo.PromptTokens = promptTokensCount
			usageInfo.CompletionTokens = usageInfo.CompletionTokens
			usageInfo.TotalTokens = totalTokens
			usageInfo.EstimatedCostUSD = calculateCost(c.configService, promptTokensCount, usageInfo.CompletionTokens)
			log.Printf("%s Stream Usage (estimated, userID: %s): Prompt≈%d, Completion≈%d, Total≈%d",
				"Ollama", userID, promptTokensCount, usageInfo.CompletionTokens, totalTokens)
			aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "success_stream_estimated", "user_id": userID}).Inc()
			aiRequestDuration.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(duration.Seconds())
			aiPromptTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(promptTokensCount))
			aiCompletionTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(usageInfo.CompletionTokens))
			aiTotalTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(totalTokens))
		} else {
			log.Printf("[ERROR] Could not get tokenizer for model %s to estimate stream tokens (userID: %s). Skipping token metrics for this stream.", currentModel, userID)
			aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "success_stream_no_tokens", "user_id": userID}).Inc()
			aiRequestDuration.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(duration.Seconds())
		}
	}
	if usageInfo.EstimatedCostUSD > 0 {
		aiEstimatedCostUSD.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Add(usageInfo.EstimatedCostUSD)
		log.Printf("%s Usage Cost (estimated, userID: %s): $%.6f", "Ollama", userID, usageInfo.EstimatedCostUSD)
	}
	return "", usageInfo, nil
}

func (c *ollamaClient) GenerateTextStream(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams, chunkHandler func(string) error) (UsageInfo, error) {
	usageInfo := UsageInfo{EstimatedCostUSD: 0}
	currentModel := c.configService.GetString(configservice.ConfigKeyAIModel, "")
	if currentModel == "" {
		log.Printf("[ERROR] Ollama model is not configured for stream (key: %s)", configservice.ConfigKeyAIModel)
		return usageInfo, fmt.Errorf("%w: Ollama model name is not configured for stream", ErrAIGenerationFailed)
	}
	aiTimeout := c.configService.GetDuration(configservice.ConfigKeyAITimeout, configservice.DefaultAITimeout)
	if strings.TrimSpace(systemPrompt) == "" {
		log.Printf("Ошибка стриминга Ollama: Системный промт пуст. userID: %s", userID)
		return usageInfo, fmt.Errorf("%w: системный промт пуст для стриминга", ErrAIGenerationFailed)
	}
	messages := []api.Message{
		{Role: "system", Content: systemPrompt},
	}
	if userInput != "" {
		messages = append(messages, api.Message{Role: "user", Content: userInput})
	}

	// Определяем температуру: приоритет у params, иначе из конфига
	var finalTemperature float64
	if params.Temperature != nil {
		finalTemperature = *params.Temperature
		log.Printf("[DEBUG] Using Ollama stream temperature from params: %.2f (UserID: %s)", finalTemperature, userID)
	} else {
		finalTemperature = c.configService.GetFloat(configservice.ConfigKeyAITemperature, configservice.DefaultAITemperature)
		log.Printf("[DEBUG] Using Ollama stream temperature from config: %.2f (UserID: %s)", finalTemperature, userID)
	}

	log.Printf("Sending stream request to Ollama: Model=%s, UserID: %s", currentModel, userID)
	startTime := time.Now()
	req := &api.ChatRequest{
		Model:    currentModel,
		Messages: messages,
		Stream:   func(b bool) *bool { return &b }(true),
		Options: map[string]interface{}{
			"temperature": finalTemperature,
			"num_predict": intVal(params.MaxTokens),
			"top_p":       params.TopP,
		},
	}

	err := c.client.Chat(ctx, req, func(resp api.ChatResponse) error {
		if resp.Message.Content != "" {
			log.Printf("User Input (Ollama Stream) (len %d): '%s'", len(resp.Message.Content), resp.Message.Content)
			if err := chunkHandler(resp.Message.Content); err != nil {
				log.Printf("Ошибка обработки чанка стрима Ollama (userID: %s): %v", userID, err)
				return fmt.Errorf("ошибка обработчика стрима: %w", err)
			}
		}
		if resp.Done {
			usageInfo.PromptTokens = resp.PromptEvalCount
			usageInfo.CompletionTokens = resp.EvalCount
			usageInfo.TotalTokens = resp.PromptEvalCount + resp.EvalCount
			usageInfo.EstimatedCostUSD = 0
			if usageInfo.TotalTokens > 0 {
				log.Printf("Ollama Stream Usage (userID: %s): PromptTokens=%d, CompletionTokens=%d, TotalTokens=%d",
					userID, usageInfo.PromptTokens, usageInfo.CompletionTokens, usageInfo.TotalTokens)
				aiPromptTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(usageInfo.PromptTokens))
				aiCompletionTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(usageInfo.CompletionTokens))
				aiTotalTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(usageInfo.TotalTokens))
			}
		}
		return nil
	})
	duration := time.Since(startTime)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			log.Printf("Ошибка таймаута (%v) во время стриминга Ollama за %v (userID: %s): %v", aiTimeout, duration, userID, err)
		} else {
			log.Printf("Ошибка во время стриминга Ollama за %v (userID: %s): %v", duration, userID, err)
		}
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_stream", "user_id": userID}).Inc()
		return usageInfo, fmt.Errorf("%w: %v", ErrAIGenerationFailed, err)
	}
	log.Printf("Обработка стрима Ollama завершена за %v. (userID: %s)", duration, userID)
	aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "success_stream", "user_id": userID}).Inc()
	aiRequestDuration.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(duration.Seconds())
	if usageInfo.TotalTokens > 0 {
		log.Printf("Ollama Stream Usage (userID: %s): PromptTokens=%d, CompletionTokens=%d, TotalTokens=%d",
			userID, usageInfo.PromptTokens, usageInfo.CompletionTokens, usageInfo.TotalTokens)
		aiPromptTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(usageInfo.PromptTokens))
		aiCompletionTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(usageInfo.CompletionTokens))
		aiTotalTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(usageInfo.TotalTokens))
	}
	return usageInfo, nil
}

var streamBoolTrue = true

func NewAIClient(configService *configservice.ConfigService) (AIClient, error) {
	aiBaseURL := configService.GetString(configservice.ConfigKeyAIBaseURL, configservice.DefaultAIBaseURL)
	aiClientType := configService.GetString(configservice.ConfigKeyAIClientType, configservice.DefaultAIClientType)
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
	switch strings.ToLower(aiClientType) {
	case "openai":
		log.Printf("Используется реализация AI клиента: OpenAI")
		if aiAPIKey == "" {
			log.Printf("[WARN] OpenAI API Key is NOT configured (checked secret file and dynamic config). Client created, but requests will fail.")
		}
		openaiConfig := openaigo.DefaultConfig(aiAPIKey)
		openaiConfig.BaseURL = aiBaseURL

		// --- Модификация HTTP клиента для добавления заголовков ---
		baseTransport := http.DefaultTransport
		// Проверяем, не равен ли http.DefaultTransport nil и можно ли его клонировать
		if dt, ok := http.DefaultTransport.(*http.Transport); ok && dt != nil {
			baseTransport = dt.Clone()
		} // Иначе используем http.DefaultTransport как есть

		customTransport := &headerTransport{
			Transport: baseTransport,
			Headers: map[string]string{
				"HTTP-Referer": "crion.space",
				"X-Title":      "TaleShift",
			},
		}

		httpClient := &http.Client{
			Timeout:   aiTimeout,
			Transport: customTransport, // Используем кастомный транспорт
		}
		openaiConfig.HTTPClient = httpClient
		// --- Конец модификации ---

		client := openaigo.NewClientWithConfig(openaiConfig)
		log.Printf("OpenAI Клиент создан. Используемый BaseURL: %s, Timeout: %v, Custom Headers Added: HTTP-Referer, X-Title", aiBaseURL, aiTimeout) // Добавлено в лог
		return &openAIClient{
			client:        client,
			configService: configService,
			provider:      "OpenAI",
			apiKey:        aiAPIKey,
		}, nil
	case "ollama":
		log.Printf("Используется реализация AI клиента: Ollama")
		return newOllamaClient(configService)
	default:
		return nil, fmt.Errorf("неизвестный тип AI клиента: '%s'", aiClientType)
	}
}
