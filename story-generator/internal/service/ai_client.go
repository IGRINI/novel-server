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
	"novel-server/shared/interfaces"
	"novel-server/shared/models"
	"novel-server/story-generator/internal/config"
	"strconv"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
	"github.com/pkoukk/tiktoken-go"
	openaigo "github.com/sashabaranov/go-openai"

	// Prometheus imports
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// <<< УДАЛЕНЫ КОНСТАНТЫ ЦЕН >>>
/*
const (
	pricePerMillionInputTokensUSD  = 0.1 // Цена за 1М входных токенов в USD
	pricePerMillionOutputTokensUSD = 0.4 // Цена за 1М выходных токенов в USD
)
*/

// <<< ДОБАВЛЕНЫ КЛЮЧИ КОНФИГУРАЦИИ И ЗНАЧЕНИЯ ПО УМОЛЧАНИЮ >>>
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
)

// <<< КОНЕЦ ДОБАВЛЕНИЯ >>>

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

// <<< ДОБАВЛЕНО: Функция получения значения конфига с fallback >>>
func getConfigValueFloat(ctx context.Context, repo interfaces.DynamicConfigRepository, key string, defaultValue float64) float64 {
	config, err := repo.GetByKey(ctx, key)
	if err != nil {
		if !errors.Is(err, models.ErrNotFound) {
			log.Printf("[WARN] Ошибка получения значения конфига '%s': %v. Используется значение по умолчанию: %f", key, err, defaultValue)
		} else {
			log.Printf("[INFO] Значение конфига '%s' не найдено. Используется значение по умолчанию: %f", key, defaultValue)
		}
		return defaultValue
	}

	value, err := strconv.ParseFloat(config.Value, 64)
	if err != nil {
		log.Printf("[WARN] Ошибка парсинга значения конфига '%s' ('%s'): %v. Используется значение по умолчанию: %f", key, config.Value, err, defaultValue)
		return defaultValue
	}
	log.Printf("[DEBUG] Получено значение конфига '%s': %f", key, value)
	return value
}

// <<< КОНЕЦ ДОБАВЛЕНИЯ >>>

// <<< ИЗМЕНЕНО: calculateCost теперь принимает репозиторий и контекст >>>
// calculateCost рассчитывает оценочную стоимость запроса на основе токенов.
func calculateCost(ctx context.Context, repo interfaces.DynamicConfigRepository, promptTokens, completionTokens int) float64 {
	pricePerMillionInput := getConfigValueFloat(ctx, repo, configKeyInputCost, defaultInputCost)
	pricePerMillionOutput := getConfigValueFloat(ctx, repo, configKeyOutputCost, defaultOutputCost)

	inputCost := float64(promptTokens) * pricePerMillionInput / 1_000_000.0
	outputCost := float64(completionTokens) * pricePerMillionOutput / 1_000_000.0
	return inputCost + outputCost
}

// <<< КОНЕЦ ИЗМЕНЕНИЯ >>>

// <<< ДОБАВЛЕНО: Функция получения строкового значения конфига с fallback >>>
func getConfigValueString(ctx context.Context, repo interfaces.DynamicConfigRepository, key string, defaultValue string) string {
	config, err := repo.GetByKey(ctx, key)
	if err != nil {
		if !errors.Is(err, models.ErrNotFound) {
			log.Printf("[WARN] Ошибка получения значения конфига '%s': %v. Используется значение по умолчанию: %s", key, err, defaultValue)
		} else {
			log.Printf("[INFO] Значение конфига '%s' не найдено. Используется значение по умолчанию: %s", key, defaultValue)
		}
		return defaultValue
	}
	if config.Value == "" {
		log.Printf("[INFO] Значение конфига '%s' пустое. Используется значение по умолчанию: %s", key, defaultValue)
		return defaultValue
	}
	log.Printf("[DEBUG] Получено значение конфига '%s': %s", key, config.Value)
	return config.Value
}

// <<< ДОБАВЛЕНО: Хелперы для Duration и Int >>>
func getConfigValueDuration(ctx context.Context, repo interfaces.DynamicConfigRepository, key string, defaultValue time.Duration) time.Duration {
	strValue := getConfigValueString(ctx, repo, key, "") // Получаем строку
	if strValue == "" {
		return defaultValue // Если пусто или не найдено, используем дефолт
	}
	duration, err := time.ParseDuration(strValue)
	if err != nil {
		log.Printf("[WARN] Ошибка парсинга duration конфига '%s' ('%s'): %v. Используется значение по умолчанию: %v", key, strValue, err, defaultValue)
		return defaultValue
	}
	log.Printf("[DEBUG] Получено значение duration конфига '%s': %v", key, duration)
	return duration
}

func getConfigValueInt(ctx context.Context, repo interfaces.DynamicConfigRepository, key string, defaultValue int) int {
	strValue := getConfigValueString(ctx, repo, key, "") // Получаем строку
	if strValue == "" {
		return defaultValue // Если пусто или не найдено, используем дефолт
	}
	intValue, err := strconv.Atoi(strValue)
	if err != nil {
		log.Printf("[WARN] Ошибка парсинга int конфига '%s' ('%s'): %v. Используется значение по умолчанию: %d", key, strValue, err, defaultValue)
		return defaultValue
	}
	log.Printf("[DEBUG] Получено значение int конфига '%s': %d", key, intValue)
	return intValue
}

// --- OpenAI Client Implementation ---

// openAIClient реализует AIClient с использованием go-openai
type openAIClient struct {
	client   *openaigo.Client
	model    string
	repo     interfaces.DynamicConfigRepository
	provider string
}

// GenerateText генерирует текст на основе системного промта и ввода пользователя
func (c *openAIClient) GenerateText(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams) (string, UsageInfo, error) {
	usageInfo := UsageInfo{} // Инициализируем пустую структуру

	if strings.TrimSpace(systemPrompt) == "" {
		log.Printf("Ошибка: Системный промт пуст после подготовки. userID: %s", userID)
		aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "error", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: системный промт пуст", ErrAIGenerationFailed)
	}

	messages := []openaigo.ChatCompletionMessage{
		{
			Role:    openaigo.ChatMessageRoleSystem,
			Content: systemPrompt,
		},
	}
	// Добавляем ввод пользователя, если он есть
	if userInput != "" {
		messages = append(messages, openaigo.ChatCompletionMessage{
			Role:    openaigo.ChatMessageRoleUser,
			Content: userInput,
		})
	}

	startTime := time.Now()
	log.Printf("Отправка запроса к %s: Model=%s, SystemPrompt=%d bytes, UserInput=%d bytes, UserID: %s",
		c.provider, c.model, len(systemPrompt), len(userInput), userID)

	// <<< ДОБАВЛЕНО: Логирование полного запроса >>>
	log.Printf("[DEBUG_INPUT] System Prompt for %s (UserID: %s):\n--- START ---\n%s\n--- END ---", c.provider, userID, systemPrompt)
	log.Printf("[DEBUG_INPUT] User Input for %s (UserID: %s):\n--- START ---\n%s\n--- END ---", c.provider, userID, userInput)
	// <<< КОНЕЦ ДОБАВЛЕНИЯ >>>

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openaigo.ChatCompletionRequest{
			Model:    c.model,
			Messages: messages,
			// <<< Передаем параметры в API >>>
			Temperature: float32Val(params.Temperature), // Конвертируем *float64 в float32
			MaxTokens:   intVal(params.MaxTokens),       // Конвертируем *int в int
			TopP:        float32Val(params.TopP),        // Конвертируем *float64 в float32
		},
	)

	duration := time.Since(startTime)

	if err != nil {
		log.Printf("Ошибка от %s API за %v (userID: %s): %v", c.provider, duration, userID, err)
		// <<< Prometheus Metrics: Increment error counter >>>
		aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "error", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: %v", ErrAIGenerationFailed, err)
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		log.Printf("%s API вернул пустой ответ за %v (userID: %s)", c.provider, duration, userID)
		// <<< Prometheus Metrics: Increment error counter (empty response treated as error) >>>
		aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "error_empty_response", "user_id": userID}).Inc() // More specific status
		return "", usageInfo, fmt.Errorf("%w: получен пустой ответ", ErrAIGenerationFailed)
	}

	// <<< Prometheus Metrics: Increment success counter and observe metrics >>>
	aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "success", "user_id": userID}).Inc()
	aiRequestDuration.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(duration.Seconds())

	generatedText := resp.Choices[0].Message.Content
	log.Printf("Ответ от %s API получен за %v. Длина ответа: %d символов. (userID: %s)", c.provider, duration, len(generatedText), userID)

	// Дополнительно можно логировать Usage info: resp.Usage
	log.Printf("[DEBUG] AI Usage received (userID: %s): %+v", userID, resp.Usage)
	if resp.Usage.TotalTokens > 0 {
		log.Printf("AI Usage (userID: %s): PromptTokens=%d, CompletionTokens=%d, TotalTokens=%d",
			userID, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
		// <<< Prometheus Metrics: Observe token counts >>>
		aiPromptTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(resp.Usage.PromptTokens))
		aiCompletionTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(resp.Usage.CompletionTokens))
		aiTotalTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(resp.Usage.TotalTokens))

		// Заполняем UsageInfo
		usageInfo.PromptTokens = resp.Usage.PromptTokens
		usageInfo.CompletionTokens = resp.Usage.CompletionTokens
		usageInfo.TotalTokens = resp.Usage.TotalTokens
		usageInfo.EstimatedCostUSD = calculateCost(ctx, c.repo, resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
		log.Printf("[DEBUG] Calculated cost (userID: %s, model: %s): %.8f", userID, c.model, usageInfo.EstimatedCostUSD)
		if usageInfo.EstimatedCostUSD > 0 {
			aiEstimatedCostUSD.With(prometheus.Labels{"model": c.model, "user_id": userID}).Add(usageInfo.EstimatedCostUSD)
			log.Printf("%s Usage Cost (estimated, userID: %s): $%.6f", c.provider, userID, usageInfo.EstimatedCostUSD)
		}
	}

	return generatedText, usageInfo, nil
}

// GenerateTextStream генерирует текст в потоковом режиме, вызывая chunkHandler.
// Возвращает UsageInfo с токенами (если удалось их получить) и ошибку.
func (c *openAIClient) GenerateTextStream(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams, chunkHandler func(string) error) (UsageInfo, error) {
	usageInfo := UsageInfo{} // Инициализируем пустую структуру
	if strings.TrimSpace(systemPrompt) == "" {
		log.Printf("Ошибка стриминга: Системный промт пуст после подготовки. userID: %s", userID)
		// Не инкрементируем метрику здесь, т.к. запрос не будет отправлен
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
		Model:       c.model,
		Messages:    messages,
		Stream:      true,
		Temperature: float32Val(params.Temperature),
		MaxTokens:   intVal(params.MaxTokens),
		TopP:        float32Val(params.TopP),
	}

	log.Printf("Отправка STREAM запроса к %s: Model=%s, SystemPrompt=%d bytes, UserInput=%d bytes, UserID: %s",
		c.provider, c.model, len(systemPrompt), len(userInput), userID)

	stream, err := c.client.CreateChatCompletionStream(ctx, request)
	if err != nil {
		log.Printf("Ошибка создания стрима от %s API (userID: %s): %v", c.provider, userID, err)
		aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "error_stream_init", "user_id": userID}).Inc()
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
			aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "error_stream_read", "user_id": userID}).Inc()
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
			tke, err := tiktoken.EncodingForModel(c.model)
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
		usageInfo.EstimatedCostUSD = calculateCost(ctx, c.repo, promptTokensCount, completionTokensCount)
		log.Printf("%s Stream Usage (from final block, userID: %s): Prompt=%d, Completion=%d, Total=%d",
			c.provider, userID, promptTokensCount, completionTokensCount, finalUsage.TotalTokens)
		// Обновляем метрики
		aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "success_stream", "user_id": userID}).Inc()
		aiRequestDuration.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(duration.Seconds())
		aiPromptTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(promptTokensCount))
		aiCompletionTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(completionTokensCount))
		aiTotalTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(finalUsage.TotalTokens))
	} else {
		// Если финальный Usage не пришел (зависит от модели/API)
		// Используем примерный подсчет completion токенов и оцениваем prompt токены
		log.Printf("[WARN] Final usage block not received in stream (userID: %s). Using estimated token counts.", userID)
		tke, err := tiktoken.EncodingForModel(c.model)
		if err == nil {
			// Оцениваем prompt токены
			promptTokensCount = len(tke.Encode(systemPrompt, nil, nil)) + len(tke.Encode(userInput, nil, nil))
			// Общее количество токенов (примерное)
			totalTokens := promptTokensCount + completionTokensCount
			usageInfo.PromptTokens = promptTokensCount
			usageInfo.CompletionTokens = completionTokensCount
			usageInfo.TotalTokens = totalTokens
			usageInfo.EstimatedCostUSD = calculateCost(ctx, c.repo, promptTokensCount, completionTokensCount)
			log.Printf("%s Stream Usage (estimated, userID: %s): Prompt≈%d, Completion≈%d, Total≈%d",
				c.provider, userID, promptTokensCount, completionTokensCount, totalTokens)
			// Обновляем метрики (с примерными значениями)
			aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "success_stream_estimated", "user_id": userID}).Inc()
			aiRequestDuration.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(duration.Seconds())
			aiPromptTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(promptTokensCount))
			aiCompletionTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(completionTokensCount))
			aiTotalTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(totalTokens))
		} else {
			log.Printf("[ERROR] Could not get tokenizer for model %s to estimate stream tokens (userID: %s). Skipping token metrics for this stream.", c.model, userID)
			// Обновляем только счетчик запросов без токенов/стоимости
			aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "success_stream_no_tokens", "user_id": userID}).Inc()
			aiRequestDuration.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(duration.Seconds())
		}
	}

	if usageInfo.EstimatedCostUSD > 0 {
		aiEstimatedCostUSD.With(prometheus.Labels{"model": c.model, "user_id": userID}).Add(usageInfo.EstimatedCostUSD)
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
	client  *api.Client
	model   string
	timeout time.Duration // Храним таймаут для контекста
}

// newOllamaClient создает новый клиент для взаимодействия с Ollama
func newOllamaClient(baseURL, model string, timeout time.Duration) (AIClient, error) {
	httpClient := &http.Client{
		Timeout: timeout, // <<< Используем переданный таймаут
	}

	// api.NewClient требует URL без суффикса /v1
	ollamaBaseURL := strings.TrimSuffix(baseURL, "/v1") // <<< Используем переданный baseURL
	ollamaBaseURL = strings.TrimSuffix(ollamaBaseURL, "/")

	parsedURL, err := url.Parse(ollamaBaseURL)
	if err != nil {
		return nil, fmt.Errorf("ошибка парсинга Ollama Base URL '%s': %w", ollamaBaseURL, err)
	}

	client := api.NewClient(parsedURL, httpClient)

	log.Printf("Ollama Клиент создан. Используемый BaseURL: %s, Model: %s, Timeout: %v", ollamaBaseURL, model, timeout) // <<< Используем переданные параметры

	return &ollamaClient{
		client:  client,
		model:   model,   // <<< Используем переданный model
		timeout: timeout, // Сохраняем таймаут
	}, nil
}

// GenerateText генерирует текст с использованием Ollama
func (c *ollamaClient) GenerateText(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams) (string, UsageInfo, error) {
	usageInfo := UsageInfo{EstimatedCostUSD: 0} // Ollama API не возвращает стоимость

	if strings.TrimSpace(systemPrompt) == "" {
		log.Printf("Ошибка: Системный промт пуст. Невозможно отправить запрос к Ollama. userID: %s", userID)
		aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "error", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: системный промт пуст", ErrAIGenerationFailed)
	}

	messages := []api.Message{
		{Role: "system", Content: systemPrompt},
	}
	if userInput != "" {
		messages = append(messages, api.Message{Role: "user", Content: userInput})
	}

	req := &api.ChatRequest{
		Model:    c.model,
		Messages: messages,
		Stream:   func(b bool) *bool { return &b }(false), // Не стримим
		Options: map[string]interface{}{
			"temperature": params.Temperature,       // Передаем *float64 напрямую
			"top_p":       params.TopP,              // Передаем *float64 напрямую
			"num_predict": intVal(params.MaxTokens), // <<< Возвращаем num_predict, т.к. ollamaClient использует нативный API
		},
	}

	// Создаем контекст с таймаутом, специфичным для этого запроса
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	startTime := time.Now()
	log.Printf("Отправка запроса к Ollama: Model=%s, SystemPrompt=%d bytes, UserInput=%d bytes, UserID: %s",
		c.model, len(systemPrompt), len(userInput), userID)

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
			log.Printf("Ошибка таймаута (%v) от Ollama API за %v (userID: %s): %v", c.timeout, duration, userID, err)
		} else {
			log.Printf("Ошибка от Ollama API за %v (userID: %s): %v", duration, userID, err)
		}
		aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "error", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: %v", ErrAIGenerationFailed, err)
	}

	if resp.Message.Content == "" {
		// <<< DEBUG: Логирование ответа при пустом контенте >>>
		log.Printf("[OLLAMA_DEBUG] Empty content in final response: %+v", resp)
		// <<< END DEBUG >>>
		log.Printf("Ollama API вернул пустой ответ за %v (userID: %s)", duration, userID)
		aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "error", "user_id": userID}).Inc()
		return "", usageInfo, fmt.Errorf("%w: получен пустой ответ", ErrAIGenerationFailed)
	}

	// <<< DEBUG: Логирование успешного финального ответа >>>
	log.Printf("[OLLAMA_DEBUG] Successful final response: %+v", resp)
	// <<< END DEBUG >>>

	aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "success", "user_id": userID}).Inc()
	aiRequestDuration.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(duration.Seconds())

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
		aiPromptTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(usageInfo.PromptTokens))
		aiCompletionTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(usageInfo.CompletionTokens))
		aiTotalTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(usageInfo.TotalTokens))
		// Не обновляем стоимость, так как она 0
	}

	return generatedText, usageInfo, nil
}

// GenerateTextStream генерирует текст с использованием Ollama в потоковом режиме
func (c *ollamaClient) GenerateTextStream(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams, chunkHandler func(string) error) (UsageInfo, error) {
	usageInfo := UsageInfo{EstimatedCostUSD: 0} // Ollama API не возвращает стоимость

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
		Model:    c.model,
		Messages: messages,
		Stream:   func(b bool) *bool { return &b }(true), // Стримим
		Options: map[string]interface{}{
			"temperature": params.Temperature,
			"top_p":       params.TopP,
			"num_predict": intVal(params.MaxTokens),
		},
	}

	// Создаем контекст с таймаутом
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	startTime := time.Now()
	log.Printf("Отправка STREAM запроса к Ollama: Model=%s, SystemPrompt=%d bytes, UserInput=%d bytes, UserID: %s",
		c.model, len(systemPrompt), len(userInput), userID)

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
			log.Printf("Ошибка таймаута (%v) во время стриминга Ollama за %v (userID: %s): %v", c.timeout, duration, userID, err)
		} else {
			log.Printf("Ошибка во время стриминга Ollama за %v (userID: %s): %v", duration, userID, err)
		}
		aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "error_stream", "user_id": userID}).Inc()
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
	aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "success_stream", "user_id": userID}).Inc()
	aiRequestDuration.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(duration.Seconds())

	if promptTokens > 0 || completionTokens > 0 {
		log.Printf("Ollama Stream Usage (userID: %s): PromptTokens=%d, CompletionTokens=%d, TotalTokens=%d",
			userID, promptTokens, completionTokens, promptTokens+completionTokens)
		aiPromptTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(promptTokens))
		aiCompletionTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(completionTokens))
		aiTotalTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(promptTokens + completionTokens))
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
// Принимает репозиторий конфигов для получения модели, URL, типа клиента и таймаута
func NewAIClient(cfg *config.Config, repo interfaces.DynamicConfigRepository) (AIClient, error) {

	ctx := context.Background()
	aiModel := getConfigValueString(ctx, repo, configKeyAIModel, defaultAIModel)
	aiBaseURL := getConfigValueString(ctx, repo, configKeyAIBaseURL, defaultAIBaseURL)
	aiClientType := getConfigValueString(ctx, repo, configKeyAIClientType, defaultAIClientType)
	aiTimeout := getConfigValueDuration(ctx, repo, ConfigKeyAITimeout, DefaultAITimeout)
	// --- Получение остальных параметров (max_attempts, base_retry_delay) будет в main.go при создании TaskHandler ---

	switch strings.ToLower(aiClientType) { // <<< Используем aiClientType из конфига
	case "openai":
		log.Printf("Используется реализация AI клиента: OpenAI")
		openaiConfig := openaigo.DefaultConfig(cfg.AIAPIKey) // AIAPIKey все еще берем из старого конфига/секретов
		openaiConfig.BaseURL = aiBaseURL
		httpClient := &http.Client{
			Timeout: aiTimeout, // <<< Используем aiTimeout из конфига
		}
		openaiConfig.HTTPClient = httpClient
		client := openaigo.NewClientWithConfig(openaiConfig)
		log.Printf("OpenAI Клиент создан. Используемый BaseURL: %s, Model: %s, Timeout: %v", aiBaseURL, aiModel, aiTimeout)
		return &openAIClient{
			client:   client,
			model:    aiModel,
			repo:     repo,
			provider: "OpenAI",
		}, nil
	case "ollama":
		log.Printf("Используется реализация AI клиента: Ollama")
		// <<< Передаем параметры в newOllamaClient >>>
		return newOllamaClient(aiBaseURL, aiModel, aiTimeout) // <<< Используем aiTimeout
	default:
		return nil, fmt.Errorf("неизвестный тип AI клиента: '%s'", aiClientType)
	}
}

// -----------------------------------------------------------------

// <<< ЭКСПОРТИРУЕМ ХЕЛПЕРЫ >>>
// GetConfigValueDuration получает Duration из конфига с fallback
func GetConfigValueDuration(ctx context.Context, repo interfaces.DynamicConfigRepository, key string, defaultValue time.Duration) time.Duration {
	strValue := getConfigValueString(ctx, repo, key, "") // Получаем строку
	if strValue == "" {
		return defaultValue // Если пусто или не найдено, используем дефолт
	}
	duration, err := time.ParseDuration(strValue)
	if err != nil {
		log.Printf("[WARN] Ошибка парсинга duration конфига '%s' ('%s'): %v. Используется значение по умолчанию: %v", key, strValue, err, defaultValue)
		return defaultValue
	}
	log.Printf("[DEBUG] Получено значение duration конфига '%s': %v", key, duration)
	return duration
}

// GetConfigValueInt получает Int из конфига с fallback
func GetConfigValueInt(ctx context.Context, repo interfaces.DynamicConfigRepository, key string, defaultValue int) int {
	strValue := getConfigValueString(ctx, repo, key, "") // Получаем строку
	if strValue == "" {
		return defaultValue // Если пусто или не найдено, используем дефолт
	}
	intValue, err := strconv.Atoi(strValue)
	if err != nil {
		log.Printf("[WARN] Ошибка парсинга int конфига '%s' ('%s'): %v. Используется значение по умолчанию: %d", key, strValue, err, defaultValue)
		return defaultValue
	}
	log.Printf("[DEBUG] Получено значение int конфига '%s': %d", key, intValue)
	return intValue
}

// ... (остальной код) ...
