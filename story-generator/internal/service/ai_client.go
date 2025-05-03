package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
	"github.com/pkoukk/tiktoken-go"
	openaigo "github.com/sashabaranov/go-openai"

	// Prometheus imports
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"novel-server/shared/configservice"
)

// <<< УДАЛЕНЫ КОНСТАНТЫ И ДЕФОЛТЫ, т.к. они теперь в shared/configservice >>>
/*
const (
	// Экспортируем ключи и дефолты, которые используются в main.go
	ConfigKeyAIMaxAttempts    = "ai.max_attempts"
	ConfigKeyAIBaseRetryDelay = "ai.base_retry_delay"
	ConfigKeyAITimeout        = "ai.timeout"
	DefaultAIMaxAttempts      = 3
	DefaultAIBaseRetryDelay   = 1 * time.Second
	DefaultAITimeout          = 120 * time.Second

	// Остальные можно оставить неэкспортированными, если они используются только внутри пакета service
	configKeyInputCost  = "generation.token_input_cost"
	configKeyOutputCost = "generation.token_output_cost"
	defaultInputCost    = 0.1
	defaultOutputCost   = 0.4

	configKeyAIModel   = "ai.model"
	configKeyAIBaseURL = "ai.base_url"
	defaultAIModel     = "meta-llama/llama-4-scout:free"
	defaultAIBaseURL   = "https://openrouter.ai/api/v1"

	configKeyAIClientType = "ai.client_type"
	defaultAIClientType   = "openai"

	configKeyAIAPIKey = "ai.api_key" // Ключ для получения API ключа
)
*/
// <<< КОНЕЦ УДАЛЕНИЯ >>>

// <<< Структура для параметров генерации (дублируем из admin-service/client) >>>
// Используем указатели, чтобы отличить 0/0.0 от отсутствия.
type GenerationParams struct {
	Temperature *float64
	MaxTokens   *int
	TopP        *float64
}

// <<< Конец >>>

// ErrAIGenerationFailed - ошибка при генерации текста AI
var ErrAIGenerationFailed = errors.New("ошибка генерации текста AI")

// --- Pricing Constants Removed ---
// Цены удалены, так как их следует брать из конфигурации.
// Расчет стоимости пока отключен.
// const (
// 	pricePerMillionInputTokensNano  = 0.10
// 	pricePerMillionOutputTokensNano = 0.40
// )

// --- End Pricing Constants ---

var (
	aiRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "story_generator_ai_requests_total",
			Help: "Total number of requests to the AI API.",
		},
		[]string{"model", "status", "user_id"}, // Labels: model used, success/error, user_id
	)
	aiRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "story_generator_ai_request_duration_seconds",
			Help:    "Histogram of AI API request durations.",
			Buckets: prometheus.DefBuckets, // Default buckets: .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10
		},
		[]string{"model", "user_id"}, // Labels: model used, user_id
	)
	aiPromptTokens = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "story_generator_ai_prompt_tokens",
			Help:    "Histogram of prompt token counts.",
			Buckets: prometheus.LinearBuckets(250, 250, 20), // 250, 500, ..., 5000
		},
		[]string{"model", "user_id"}, // Labels: model used, user_id
	)
	aiCompletionTokens = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "story_generator_ai_completion_tokens",
			Help:    "Histogram of completion token counts.",
			Buckets: prometheus.LinearBuckets(100, 100, 20), // 100, 200, ..., 2000
		},
		[]string{"model", "user_id"}, // Labels: model used, user_id
	)
	aiTotalTokens = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "story_generator_ai_total_tokens",
			Help:    "Histogram of total token counts (prompt + completion).",
			Buckets: prometheus.LinearBuckets(350, 350, 20), // 350, 700, ..., 7000
		},
		[]string{"model", "user_id"}, // Labels: model used, user_id
	)
	// <<< РАСКОММЕНТИРОВАНО: Метрика стоимости >>>
	aiEstimatedCostUSD = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "story_generator_ai_estimated_cost_usd_total",
			Help: "Estimated total cost of AI requests in USD.",
		},
		[]string{"model", "user_id"},
	)
	// <<< КОНЕЦ РАСКОММЕНТИРОВАНИЯ >>>
)

// UsageInfo содержит информацию об использовании токенов и стоимости
type UsageInfo struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	EstimatedCostUSD float64 // Оценочная стоимость
}

// AIClient интерфейс для взаимодействия с AI API
type AIClient interface {
	// GenerateText генерирует текст на основе системного промта, ввода пользователя и параметров.
	// Возвращает сгенерированный текст, информацию об использовании и ошибку.
	GenerateText(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams) (string, UsageInfo, error)
	// GenerateTextStream генерирует текст и вызывает chunkHandler для каждого полученного фрагмента.
	// Возвращает информацию об использовании (может быть неполной или отсутствовать для stream) и ошибку.
	GenerateTextStream(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams, chunkHandler func(string) error) (UsageInfo, error)
}

// <<< ИЗМЕНЕНО: calculateCost теперь принимает *configservice.ConfigService >>>
// calculateCost рассчитывает оценочную стоимость запроса на основе токенов.
func calculateCost(configService *configservice.ConfigService, promptTokens, completionTokens int) float64 {
	pricePerMillionInput := configService.GetFloat(configservice.ConfigKeyInputCost, configservice.DefaultInputCost)
	pricePerMillionOutput := configService.GetFloat(configservice.ConfigKeyOutputCost, configservice.DefaultOutputCost)

	inputCost := float64(promptTokens) * pricePerMillionInput / 1_000_000.0
	outputCost := float64(completionTokens) * pricePerMillionOutput / 1_000_000.0
	return inputCost + outputCost
}

// <<< КОНЕЦ ИЗМЕНЕНИЯ >>>

// --- OpenAI Client Implementation ---

// openAIClient реализует AIClient с использованием go-openai
type openAIClient struct {
	client        *openaigo.Client
	configService *configservice.ConfigService // <<< ИЗМЕНЕНО: Тип на shared configservice >>>
	provider      string
}

// GenerateText генерирует текст на основе системного промта и ввода пользователя
func (c *openAIClient) GenerateText(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams) (string, UsageInfo, error) {
	usageInfo := UsageInfo{} // Инициализируем пустую структуру

	// <<< ИЗМЕНЕНО: Получаем актуальные настройки из ConfigService >>>
	currentModel := c.configService.GetString(configservice.ConfigKeyAIModel, configservice.DefaultAIModel)
	currentBaseURL := c.configService.GetString(configservice.ConfigKeyAIBaseURL, configservice.DefaultAIBaseURL)
	// <<< ВАЖНО: API ключ теперь тоже читаем из ConfigService >>>
	currentAPIKey := c.configService.GetString(configservice.ConfigKeyAIAPIKey, "") // По умолчанию пустой ключ
	log.Printf("[DEBUG] Preparing OpenAI request. BaseURL: %s, Model: %s, UserID: %s", currentBaseURL, currentModel, userID)

	if currentAPIKey == "" && c.provider == "OpenAI" { // Проверяем ключ только для OpenAI
		log.Printf("[ERROR] OpenAI API key is missing in dynamic config (key: %s). Cannot make request.", configservice.ConfigKeyAIAPIKey)
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_missing_key", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: OpenAI API key is not configured", ErrAIGenerationFailed)
	}

	// Валидация системного промпта
	if strings.TrimSpace(systemPrompt) == "" {
		log.Printf("Ошибка: Системный промт пуст после подготовки. userID: %s", userID)
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error", "user_id": userID}).Inc() // Используем currentModel
		return "", usageInfo, fmt.Errorf("%w: системный промт пуст", ErrAIGenerationFailed)
	}

	// Формирование сообщений
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

	startTime := time.Now()
	log.Printf("Отправка запроса к %s: Model=%s, SystemPrompt=%d bytes, UserInput=%d bytes, UserID: %s",
		c.provider, currentModel, len(systemPrompt), len(userInput), userID) // Используем currentModel

	// Логирование
	log.Printf("[DEBUG_INPUT] System Prompt for %s (UserID: %s):\n--- START ---\n%s\n--- END ---", c.provider, userID, systemPrompt)
	log.Printf("[DEBUG_INPUT] User Input for %s (UserID: %s):\n--- START ---\n%s\n--- END ---", c.provider, userID, userInput)

	// Создание запроса
	request := openaigo.ChatCompletionRequest{
		Model:       currentModel, // <<< Используем актуальную модель >>>
		Messages:    messages,
		Temperature: float32Val(params.Temperature),
		MaxTokens:   intVal(params.MaxTokens),
		TopP:        float32Val(params.TopP),
	}

	requestJSON, _ := json.Marshal(request)
	log.Printf("[DEBUG] OpenAI Request Body: %s", string(requestJSON))

	// <<< ВАЖНО: Создаем или обновляем клиент OpenAI с актуальным API ключом и URL >>>
	openaiConfig := openaigo.DefaultConfig(currentAPIKey)
	openaiConfig.BaseURL = currentBaseURL
	// Используем HTTP клиент с таймаутом, полученным из ConfigService
	aiTimeout := c.configService.GetDuration(configservice.ConfigKeyAITimeout, configservice.DefaultAITimeout)
	httpClient := &http.Client{
		Timeout: aiTimeout,
	}
	openaiConfig.HTTPClient = httpClient
	// Создаем временный клиент для этого конкретного запроса
	// TODO: Оптимизировать, если ключ/URL меняются редко (например, кэшировать клиент)
	tempClient := openaigo.NewClientWithConfig(openaiConfig)

	resp, err := tempClient.CreateChatCompletion(
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
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error", "user_id": userID}).Inc() // Используем currentModel
		return "", usageInfo, fmt.Errorf("%w: %v", ErrAIGenerationFailed, err)
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		log.Printf("[DEBUG] Received response object from %s (before empty content check, userID: %s): %+v", c.provider, userID, resp)
		log.Printf("%s API вернул пустой ответ за %v (userID: %s)", c.provider, duration, userID)
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_empty_response", "user_id": userID}).Inc() // Используем currentModel
		return "", usageInfo, fmt.Errorf("%w: получен пустой ответ", ErrAIGenerationFailed)
	}

	// Prometheus Metrics
	aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "success", "user_id": userID}).Inc()    // Используем currentModel
	aiRequestDuration.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(duration.Seconds()) // Используем currentModel

	generatedText := resp.Choices[0].Message.Content
	log.Printf("Ответ от %s API получен за %v. Длина ответа: %d символов. (userID: %s)", c.provider, duration, len(generatedText), userID)
	log.Printf("[DEBUG] AI Usage received (userID: %s): %+v", userID, resp.Usage)
	if resp.Usage.TotalTokens > 0 {
		log.Printf("AI Usage (userID: %s): PromptTokens=%d, CompletionTokens=%d, TotalTokens=%d",
			userID, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
		aiPromptTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(resp.Usage.PromptTokens))         // Используем currentModel
		aiCompletionTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(resp.Usage.CompletionTokens)) // Используем currentModel
		aiTotalTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(resp.Usage.TotalTokens))           // Используем currentModel

		usageInfo.PromptTokens = resp.Usage.PromptTokens
		usageInfo.CompletionTokens = resp.Usage.CompletionTokens
		usageInfo.TotalTokens = resp.Usage.TotalTokens
		// <<< ИЗМЕНЕНО: Передаем configService в calculateCost >>>
		usageInfo.EstimatedCostUSD = calculateCost(c.configService, resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
		log.Printf("[DEBUG] Calculated cost (userID: %s, model: %s): %.8f", userID, currentModel, usageInfo.EstimatedCostUSD) // Используем currentModel
		if usageInfo.EstimatedCostUSD > 0 {
			aiEstimatedCostUSD.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Add(usageInfo.EstimatedCostUSD) // Используем currentModel
			log.Printf("%s Usage Cost (estimated, userID: %s): $%.6f", c.provider, userID, usageInfo.EstimatedCostUSD)
		}
	}

	return generatedText, usageInfo, nil
}

// GenerateTextStream генерирует текст в потоковом режиме, вызывая chunkHandler.
// Возвращает UsageInfo с токенами (если удалось их получить) и ошибку.
func (c *openAIClient) GenerateTextStream(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams, chunkHandler func(string) error) (UsageInfo, error) {
	usageInfo := UsageInfo{} // Инициализируем пустую структуру

	// <<< ИЗМЕНЕНО: Получаем актуальные настройки из ConfigService >>>
	currentModel := c.configService.GetString(configservice.ConfigKeyAIModel, configservice.DefaultAIModel)
	currentBaseURL := c.configService.GetString(configservice.ConfigKeyAIBaseURL, configservice.DefaultAIBaseURL)
	currentAPIKey := c.configService.GetString(configservice.ConfigKeyAIAPIKey, "")

	if currentAPIKey == "" && c.provider == "OpenAI" {
		log.Printf("[ERROR] OpenAI API key is missing for stream (key: %s).", configservice.ConfigKeyAIAPIKey)
		// Не инкрементируем метрику запроса, т.к. он не будет сделан
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

	request := openaigo.ChatCompletionRequest{
		Model:       currentModel,
		Messages:    messages,
		Stream:      true,
		Temperature: float32Val(params.Temperature),
		MaxTokens:   intVal(params.MaxTokens),
		TopP:        float32Val(params.TopP),
	}

	log.Printf("Отправка STREAM запроса к %s: Model=%s, SystemPrompt=%d bytes, UserInput=%d bytes, UserID: %s",
		c.provider, currentModel, len(systemPrompt), len(userInput), userID)

	// <<< ВАЖНО: Создаем или обновляем клиент OpenAI с актуальным API ключом и URL >>>
	openaiConfig := openaigo.DefaultConfig(currentAPIKey)
	openaiConfig.BaseURL = currentBaseURL
	aiTimeout := c.configService.GetDuration(configservice.ConfigKeyAITimeout, configservice.DefaultAITimeout)
	httpClient := &http.Client{
		Timeout: aiTimeout, // Timeout for the entire stream request?
	}
	openaiConfig.HTTPClient = httpClient
	tempClient := openaigo.NewClientWithConfig(openaiConfig)

	stream, err := tempClient.CreateChatCompletionStream(ctx, request)
	if err != nil {
		log.Printf("Ошибка создания стрима от %s API (userID: %s): %v", c.provider, userID, err)
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_stream_init", "user_id": userID}).Inc()
		return usageInfo, fmt.Errorf("%w: ошибка создания стрима: %v", ErrAIGenerationFailed, err)
	}
	defer stream.Close()

	log.Printf("Стрим от %s API успешно инициирован. Чтение... (userID: %s)", c.provider, userID)
	startTime := time.Now()
	completionTokensCount := 0              // Считаем токены ответа по мере поступления
	promptTokensCount := 0                  // Попытаемся получить из Usage в конце
	var finalUsage openaigo.Usage           // Для сохранения финального Usage
	var responseTextBuilder strings.Builder // <<< Для сбора полного текста ответа

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

		// В OpenAI API Usage иногда приходит в конце стрима
		if response.Usage != nil && response.Usage.TotalTokens > 0 { // Проверяем указатель на nil
			finalUsage = *response.Usage // Разыменовываем указатель
			log.Printf("[DEBUG] Received final usage info in stream (userID: %s): %+v", userID, finalUsage)
		}

		if len(response.Choices) > 0 {
			chunk := response.Choices[0].Delta.Content
			responseTextBuilder.WriteString(chunk) // <<< Собираем полный текст
			// Примерный подсчет токенов на лету (менее точный, чем Usage)
			tke, err := tiktoken.EncodingForModel(currentModel)
			if err == nil {
				completionTokensCount += len(tke.Encode(chunk, nil, nil))
			}

			if chunkHandler != nil {
				if err := chunkHandler(chunk); err != nil {
					log.Printf("Ошибка обработчика чанка стрима (userID: %s): %v", userID, err)
					// Не прерываем стрим AI, но логируем ошибку обработчика
					// Возможно, стоит добавить метрику для ошибок chunkHandler
				}
			}
		}
	}

	duration := time.Since(startTime)
	log.Printf("Чтение стрима %s завершено за %v. (userID: %s)", c.provider, duration, userID)

	// Если получили финальный Usage, используем его
	if finalUsage.TotalTokens > 0 {
		promptTokensCount = finalUsage.PromptTokens
		completionTokensCount = finalUsage.CompletionTokens // Используем точное значение из Usage
		usageInfo.PromptTokens = promptTokensCount
		usageInfo.CompletionTokens = completionTokensCount
		usageInfo.TotalTokens = finalUsage.TotalTokens
		// <<< ИЗМЕНЕНО: Передаем configService в calculateCost >>>
		usageInfo.EstimatedCostUSD = calculateCost(c.configService, promptTokensCount, completionTokensCount)
		log.Printf("%s Stream Usage (from final block, userID: %s): Prompt=%d, Completion=%d, Total=%d",
			c.provider, userID, promptTokensCount, completionTokensCount, finalUsage.TotalTokens)
		// Обновляем метрики
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "success_stream", "user_id": userID}).Inc()
		aiRequestDuration.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(duration.Seconds())
		aiPromptTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(promptTokensCount))
		aiCompletionTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(completionTokensCount))
		aiTotalTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(finalUsage.TotalTokens))
	} else {
		// Если финальный Usage не пришел (зависит от модели/API)
		// Используем примерный подсчет completion токенов и оцениваем prompt токены
		log.Printf("[WARN] Final usage block not received in stream (userID: %s). Using estimated token counts.", userID)
		tke, err := tiktoken.EncodingForModel(currentModel)
		if err == nil {
			// Оцениваем prompt токены
			promptTokensCount = len(tke.Encode(systemPrompt, nil, nil)) + len(tke.Encode(userInput, nil, nil))
			// Общее количество токенов (примерное)
			totalTokens := promptTokensCount + completionTokensCount
			usageInfo.PromptTokens = promptTokensCount
			usageInfo.CompletionTokens = completionTokensCount
			usageInfo.TotalTokens = totalTokens
			// <<< ИЗМЕНЕНО: Передаем configService в calculateCost >>>
			usageInfo.EstimatedCostUSD = calculateCost(c.configService, promptTokensCount, completionTokensCount)
			log.Printf("%s Stream Usage (estimated, userID: %s): Prompt≈%d, Completion≈%d, Total≈%d",
				c.provider, userID, promptTokensCount, completionTokensCount, totalTokens)
			// Обновляем метрики (с примерными значениями)
			aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "success_stream_estimated", "user_id": userID}).Inc()
			aiRequestDuration.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(duration.Seconds())
			aiPromptTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(promptTokensCount))
			aiCompletionTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(completionTokensCount))
			aiTotalTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(totalTokens))
		} else {
			log.Printf("[ERROR] Could not get tokenizer for model %s to estimate stream tokens (userID: %s). Skipping token metrics for this stream.", currentModel, userID)
			// Обновляем только счетчик запросов без токенов/стоимости
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

// --- Вспомогательная функция для конвертации *float64 в float32 ---
func float32Val(f64 *float64) float32 { // Возвращает float32, а не *float32
	if f64 == nil {
		// Возвращаем значение по умолчанию для OpenAI API, если не передано
		// Для Temperature и TopP это обычно 1.0, но лучше уточнить в документации API
		// или оставить 0.0, если API сам подставляет дефолт при 0
		return 1.0 // Или 0.0? Уточнить дефолт API!
	}
	f32 := float32(*f64)
	return f32
}

// --- Вспомогательная функция для конвертации *int в int ---
func intVal(i *int) int {
	if i == nil {
		// Возвращаем 0 или другое значение по умолчанию, если API его ожидает при отсутствии
		return 0 // Если 0 - значит "не установлено" или "без лимита" (уточнить по API!)
	}
	return *i
}

// -----------------------------------------------------------------

// --- Ollama Client Implementation ---

// ollamaClient реализует AIClient с использованием ollama/api
type ollamaClient struct {
	client        *api.Client
	configService *configservice.ConfigService // <<< ИЗМЕНЕНО: Тип на shared configservice >>>
}

// newOllamaClient создает новый клиент для взаимодействия с Ollama
func newOllamaClient(configService *configservice.ConfigService) (AIClient, error) { // <<< Принимает *configservice.ConfigService >>>
	aiBaseURL := configService.GetString(configservice.ConfigKeyAIBaseURL, configservice.DefaultAIBaseURL)   // Получаем из кэша
	aiTimeout := configService.GetDuration(configservice.ConfigKeyAITimeout, configservice.DefaultAITimeout) // Получаем из кэша

	httpClient := &http.Client{
		Timeout: aiTimeout, // Используем таймаут из кэша
	}

	// api.NewClient требует URL без суффикса /v1
	ollamaBaseURL := strings.TrimSuffix(aiBaseURL, "/v1") // <<< Используем aiBaseURL из кэша
	ollamaBaseURL = strings.TrimSuffix(ollamaBaseURL, "/")

	parsedURL, err := url.Parse(ollamaBaseURL)
	if err != nil {
		return nil, fmt.Errorf("ошибка парсинга Ollama Base URL '%s': %w", ollamaBaseURL, err)
	}

	client := api.NewClient(parsedURL, httpClient)

	log.Printf("Ollama Клиент создан. Используемый BaseURL: %s, Timeout: %v", ollamaBaseURL, aiTimeout) // <<< Используем параметры из кэша

	return &ollamaClient{
		client:        client,
		configService: configService, // <<< Сохраняем *configservice.ConfigService
	}, nil
}

// GenerateText генерирует текст с использованием Ollama
func (c *ollamaClient) GenerateText(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams) (string, UsageInfo, error) {
	usageInfo := UsageInfo{EstimatedCostUSD: 0} // Ollama API не возвращает стоимость

	// <<< Получаем модель и таймаут из ConfigService >>>
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

	req := &api.ChatRequest{
		Model:    currentModel, // Используем модель из кэша
		Messages: messages,
		Stream:   func(b bool) *bool { return &b }(false), // Не стримим
		Options: map[string]interface{}{ // <<< ИСПРАВЛЕНО: Передаем значения, а не указатели >>>
			"temperature": float32Val(params.Temperature),
			"top_p":       float32Val(params.TopP),
			"num_predict": intVal(params.MaxTokens),
		},
	}

	// Создаем контекст с таймаутом, специфичным для этого запроса
	requestCtx, cancel := context.WithTimeout(ctx, aiTimeout) // Используем таймаут из кэша
	defer cancel()

	startTime := time.Now()
	log.Printf("Отправка запроса к Ollama: Model=%s, SystemPrompt=%d bytes, UserInput=%d bytes, UserID: %s",
		currentModel, len(systemPrompt), len(userInput), userID)

	// <<< DEBUG: Логирование полного запроса перед отправкой >>>
	log.Printf("[OLLAMA_DEBUG] Request Messages: %+v", req.Messages)
	log.Printf("[OLLAMA_DEBUG] Request Options: %+v", req.Options)
	// <<< END DEBUG >>>

	// <<< DEBUG: Логирование полного JSON запроса >>>
	jsonData, jsonErr := json.Marshal(req) // Используем пакет encoding/json (должен быть импортирован)
	if jsonErr != nil {
		log.Printf("[OLLAMA_DEBUG] Error marshalling request to JSON: %v", jsonErr)
	} else {
		log.Printf("[OLLAMA_DEBUG] Sending JSON request: %s", string(jsonData))
	}
	// <<< END DEBUG >>>

	var resp api.ChatResponse
	err := c.client.Chat(requestCtx, req, func(r api.ChatResponse) error {
		// <<< DEBUG: Логирование каждого полученного ответа/чанка >>>
		log.Printf("[OLLAMA_DEBUG] Received response chunk: %+v", r)
		// <<< END DEBUG >>>
		resp = r // Сохраняем последний (полный) ответ
		// Здесь можно добавить обработку ошибок, специфичных для ответа, если нужно
		// Например, проверить r.Error
		return nil // Возвращаем nil, чтобы Chat продолжил работу
	})

	duration := time.Since(startTime)

	if err != nil {
		// <<< DEBUG: Логирование ошибки и последнего ответа при ошибке >>>
		log.Printf("[OLLAMA_DEBUG] Error during Chat call. Last Response: %+v, Error: %v", resp, err)
		// <<< END DEBUG >>>
		// Проверяем, не связана ли ошибка с таймаутом контекста
		if errors.Is(err, context.DeadlineExceeded) {
			log.Printf("Ошибка таймаута (%v) от Ollama API за %v (userID: %s): %v", aiTimeout, duration, userID, err)
		} else {
			log.Printf("Ошибка от Ollama API за %v (userID: %s): %v", duration, userID, err)
		}
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: %v", ErrAIGenerationFailed, err)
	}

	if resp.Message.Content == "" {
		// <<< DEBUG: Логирование ответа при пустом контенте >>>
		log.Printf("[OLLAMA_DEBUG] Empty content in final response: %+v", resp)
		// <<< END DEBUG >>>
		log.Printf("Ollama API вернул пустой ответ за %v (userID: %s)", duration, userID)
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: получен пустой ответ", ErrAIGenerationFailed)
	}

	// <<< DEBUG: Логирование успешного финального ответа >>>
	log.Printf("[OLLAMA_DEBUG] Successful final response: %+v", resp)
	// <<< END DEBUG >>>

	aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "success", "user_id": userID}).Inc()
	aiRequestDuration.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(duration.Seconds())

	generatedText := resp.Message.Content
	log.Printf("Ответ от Ollama API получен за %v. Длина ответа: %d символов. (userID: %s)", duration, len(generatedText), userID)

	// Заполняем UsageInfo из ответа Ollama
	usageInfo.PromptTokens = resp.PromptEvalCount
	// Ollama API v0.1.29+ возвращает EvalCount как токены ответа
	usageInfo.CompletionTokens = resp.EvalCount
	usageInfo.TotalTokens = resp.PromptEvalCount + resp.EvalCount
	usageInfo.EstimatedCostUSD = 0 // Ollama обычно локальный, стоимость 0

	// Обновляем метрики токенов
	if usageInfo.TotalTokens > 0 {
		log.Printf("Ollama Usage (userID: %s): PromptTokens=%d, CompletionTokens=%d, TotalTokens=%d",
			userID, usageInfo.PromptTokens, usageInfo.CompletionTokens, usageInfo.TotalTokens)
		aiPromptTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(usageInfo.PromptTokens))
		aiCompletionTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(usageInfo.CompletionTokens))
		aiTotalTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(usageInfo.TotalTokens))
		// Не обновляем стоимость, так как она 0
	}

	return generatedText, usageInfo, nil
}

// GenerateTextStream генерирует текст с использованием Ollama в потоковом режиме
func (c *ollamaClient) GenerateTextStream(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams, chunkHandler func(string) error) (UsageInfo, error) {
	usageInfo := UsageInfo{EstimatedCostUSD: 0} // Ollama API не возвращает стоимость

	// <<< Получаем модель и таймаут из ConfigService >>>
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

	req := &api.ChatRequest{
		Model:    currentModel, // Используем модель из кэша
		Messages: messages,
		Stream:   func(b bool) *bool { return &b }(true), // Стримим
		Options: map[string]interface{}{ // <<< ИСПРАВЛЕНО: Передаем значения, а не указатели >>>
			"temperature": float32Val(params.Temperature),
			"top_p":       float32Val(params.TopP),
			"num_predict": intVal(params.MaxTokens),
		},
	}

	// Создаем контекст с таймаутом
	requestCtx, cancel := context.WithTimeout(ctx, aiTimeout) // Используем таймаут из кэша
	defer cancel()

	startTime := time.Now()
	log.Printf("Отправка STREAM запроса к Ollama: Model=%s, SystemPrompt=%d bytes, UserInput=%d bytes, UserID: %s",
		currentModel, len(systemPrompt), len(userInput), userID)

	var finalErr error
	var promptTokens, completionTokens int

	err := c.client.Chat(requestCtx, req, func(resp api.ChatResponse) error {
		// Обрабатываем каждый чанк
		if resp.Message.Content != "" {
			if err := chunkHandler(resp.Message.Content); err != nil {
				log.Printf("Ошибка обработки чанка стрима Ollama (userID: %s): %v", userID, err)
				// Прерываем стрим, возвращая ошибку из колбэка
				return fmt.Errorf("ошибка обработчика стрима: %w", err)
			}
		}

		// Если это последний чанк (Done=true), сохраняем статистики токенов
		if resp.Done {
			promptTokens = resp.PromptEvalCount
			completionTokens = resp.EvalCount
			if resp.DoneReason != "" && resp.DoneReason != "stop" {
				log.Printf("Стрим Ollama завершился не по причине 'stop': %s", resp.DoneReason)
				// Можно рассмотреть как ошибку, если необходимо
				// finalErr = fmt.Errorf("стрим завершился некорректно: %s", resp.DoneReason)
			}
			log.Printf("Стрим Ollama завершен. Причина: %s", resp.DoneReason)
		}
		return nil // Продолжаем получать чанки
	})

	duration := time.Since(startTime)

	if err != nil {
		// Если ошибка произошла во время стриминга (не в chunkHandler)
		if errors.Is(err, context.DeadlineExceeded) {
			log.Printf("Ошибка таймаута (%v) во время стриминга Ollama за %v (userID: %s): %v", aiTimeout, duration, userID, err)
		} else {
			log.Printf("Ошибка во время стриминга Ollama за %v (userID: %s): %v", duration, userID, err)
		}
		aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "error_stream", "user_id": userID}).Inc()
		// Если ошибка произошла в chunkHandler, она уже была возвращена выше
		// Если ошибка произошла в самом клиенте Ollama, возвращаем ее
		if finalErr == nil { // Не перезаписываем ошибку из resp.Done, если она была
			finalErr = fmt.Errorf("%w: %v", ErrAIGenerationFailed, err)
		}
	}

	if finalErr != nil {
		return usageInfo, finalErr
	}

	// Стрим успешно завершен (либо обработчиком, либо сам по себе)
	log.Printf("Обработка стрима Ollama завершена за %v. (userID: %s)", duration, userID)
	aiRequestsTotal.With(prometheus.Labels{"model": currentModel, "status": "success_stream", "user_id": userID}).Inc()
	aiRequestDuration.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(duration.Seconds())

	if promptTokens > 0 || completionTokens > 0 {
		log.Printf("Ollama Stream Usage (userID: %s): PromptTokens=%d, CompletionTokens=%d, TotalTokens=%d",
			userID, promptTokens, completionTokens, promptTokens+completionTokens)
		aiPromptTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(promptTokens))
		aiCompletionTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(completionTokens))
		aiTotalTokens.With(prometheus.Labels{"model": currentModel, "user_id": userID}).Observe(float64(promptTokens + completionTokens))
	}

	// Заполняем UsageInfo из последнего ответа Ollama
	usageInfo.PromptTokens = promptTokens
	usageInfo.CompletionTokens = completionTokens
	usageInfo.TotalTokens = promptTokens + completionTokens
	usageInfo.EstimatedCostUSD = 0

	return usageInfo, nil
}

var streamBoolTrue = true

// --- Factory Function ---

// NewAIClient создает новый клиент для взаимодействия с AI в зависимости от конфигурации
// <<< ИЗМЕНЕНО: Принимает *configservice.ConfigService >>>
func NewAIClient(configService *configservice.ConfigService) (AIClient, error) {

	// <<< Получаем настройки из ConfigService, используем константы из shared >>>
	aiBaseURL := configService.GetString(configservice.ConfigKeyAIBaseURL, configservice.DefaultAIBaseURL)
	aiClientType := configService.GetString(configservice.ConfigKeyAIClientType, configservice.DefaultAIClientType)
	aiTimeout := configService.GetDuration(configservice.ConfigKeyAITimeout, configservice.DefaultAITimeout)
	aiAPIKey := configService.GetString(configservice.ConfigKeyAIAPIKey, "")

	switch strings.ToLower(aiClientType) {
	case "openai":
		log.Printf("Используется реализация AI клиента: OpenAI")
		if aiAPIKey == "" {
			log.Printf("[WARN] OpenAI API ключ не найден в конфигурации (key: %s). Клиент создан, но запросы будут неудачными.", configservice.ConfigKeyAIAPIKey)
			// Не возвращаем ошибку, позволяем создать клиент, но логируем
		}
		openaiConfig := openaigo.DefaultConfig(aiAPIKey) // Используем ключ из конфига
		openaiConfig.BaseURL = aiBaseURL
		httpClient := &http.Client{
			Timeout: aiTimeout,
		}
		openaiConfig.HTTPClient = httpClient
		client := openaigo.NewClientWithConfig(openaiConfig)
		log.Printf("OpenAI Клиент создан. Используемый BaseURL: %s, Timeout: %v", aiBaseURL, aiTimeout)
		return &openAIClient{
			client:        client,
			configService: configService, // <<< Сохраняем *configservice.ConfigService
			provider:      "OpenAI",
		}, nil
	case "ollama":
		log.Printf("Используется реализация AI клиента: Ollama")
		// <<< Передаем *configservice.ConfigService в newOllamaClient >>>
		return newOllamaClient(configService)
	default:
		return nil, fmt.Errorf("неизвестный тип AI клиента: '%s'", aiClientType)
	}
}
