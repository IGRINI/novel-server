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
	"novel-server/story-generator/internal/config"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
	"github.com/pkoukk/tiktoken-go"
	openaigo "github.com/sashabaranov/go-openai"

	// Prometheus imports
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

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

// --- Pricing Constants (Example for gpt-4.1-nano variants) ---
// TODO: Move pricing to configuration?
const (
	pricePerMillionInputTokensNano  = 0.10
	pricePerMillionOutputTokensNano = 0.40
)

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
	// <<< Новая метрика для стоимости >>>
	aiEstimatedCostUSD = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "story_generator_ai_estimated_cost_usd_total",
			Help: "Estimated cost of AI token usage in USD.",
		},
		[]string{"model", "user_id"}, // Labels: model used, user_id
	)
)

// AIClient интерфейс для взаимодействия с AI API
type AIClient interface {
	// GenerateText генерирует текст на основе системного промта, ввода пользователя и параметров.
	GenerateText(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams) (string, error)
	// GenerateTextStream генерирует текст и вызывает chunkHandler для каждого полученного фрагмента.
	GenerateTextStream(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams, chunkHandler func(string) error) error
}

// --- OpenAI Client Implementation ---

// openAIClient реализует AIClient с использованием go-openai
type openAIClient struct {
	client *openaigo.Client
	model  string
}

// GenerateText генерирует текст на основе системного промта и ввода пользователя
func (c *openAIClient) GenerateText(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams) (string, error) {
	if strings.TrimSpace(systemPrompt) == "" {
		log.Printf("Ошибка: Системный промт пуст после подготовки. userID: %s", userID)
		aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "error", "user_id": userID}).Inc()
		return "", fmt.Errorf("%w: системный промт пуст", ErrAIGenerationFailed)
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
	log.Printf("Отправка запроса к AI: Model=%s, SystemPrompt=%d bytes, UserInput=%d bytes, UserID: %s",
		c.model, len(systemPrompt), len(userInput), userID)

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
		log.Printf("Ошибка от AI API за %v (userID: %s): %v", duration, userID, err)
		// <<< Prometheus Metrics: Increment error counter >>>
		aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "error", "user_id": userID}).Inc()
		return "", fmt.Errorf("%w: %v", ErrAIGenerationFailed, err)
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		log.Printf("AI API вернул пустой ответ за %v (userID: %s)", duration, userID)
		// <<< Prometheus Metrics: Increment error counter (empty response treated as error) >>>
		aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "error", "user_id": userID}).Inc()
		return "", fmt.Errorf("%w: получен пустой ответ", ErrAIGenerationFailed)
	}

	// <<< Prometheus Metrics: Increment success counter and observe metrics >>>
	aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "success", "user_id": userID}).Inc()
	aiRequestDuration.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(duration.Seconds())

	generatedText := resp.Choices[0].Message.Content
	log.Printf("Ответ от AI API получен за %v. Длина ответа: %d символов. (userID: %s)", duration, len(generatedText), userID)

	// Дополнительно можно логировать Usage info: resp.Usage
	log.Printf("[DEBUG] AI Usage received (userID: %s): %+v", userID, resp.Usage)
	if resp.Usage.TotalTokens > 0 {
		log.Printf("AI Usage (userID: %s): PromptTokens=%d, CompletionTokens=%d, TotalTokens=%d",
			userID, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
		// <<< Prometheus Metrics: Observe token counts >>>
		aiPromptTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(resp.Usage.PromptTokens))
		aiCompletionTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(resp.Usage.CompletionTokens))
		aiTotalTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(resp.Usage.TotalTokens))
		// <<< Рассчитываем и обновляем стоимость >>>
		cost := calculateCost(c.model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
		log.Printf("[DEBUG] Calculated cost (userID: %s, model: %s): %.8f", userID, c.model, cost)
		if cost > 0 {
			aiEstimatedCostUSD.With(prometheus.Labels{"model": c.model, "user_id": userID}).Add(cost)
			log.Printf("AI Usage Cost (estimated, userID: %s): $%.6f", userID, cost)
		}
	}

	return generatedText, nil
}

// GenerateTextStream генерирует текст в потоковом режиме, вызывая chunkHandler.
func (c *openAIClient) GenerateTextStream(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams, chunkHandler func(string) error) error {
	if strings.TrimSpace(systemPrompt) == "" {
		log.Printf("Ошибка стриминга: Системный промт пуст после подготовки. userID: %s", userID)
		// Не инкрементируем метрику здесь, т.к. запрос не будет отправлен
		return fmt.Errorf("%w: системный промт пуст для стриминга", ErrAIGenerationFailed)
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

	log.Printf("Отправка STREAM запроса к OpenAI: Model=%s, SystemPrompt=%d bytes, UserInput=%d bytes, UserID: %s",
		c.model, len(systemPrompt), len(userInput), userID)

	stream, err := c.client.CreateChatCompletionStream(ctx, request)
	if err != nil {
		log.Printf("Ошибка создания стрима от OpenAI API (userID: %s): %v", userID, err)
		aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "error_stream_init", "user_id": userID}).Inc()
		return fmt.Errorf("%w: ошибка создания стрима: %v", ErrAIGenerationFailed, err)
	}
	defer stream.Close()

	log.Printf("Стрим от OpenAI API успешно инициирован. Чтение... (userID: %s)", userID)
	startTime := time.Now()
	completionTokens := 0
	promptTokens := 0                       // Попытаемся получить из первого ответа, если возможно
	var responseTextBuilder strings.Builder // <<< Для сбора полного текста ответа

	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			log.Printf("Стрим OpenAI завершен. (userID: %s)", userID)
			break
		}
		if err != nil {
			log.Printf("Ошибка чтения из стрима OpenAI (userID: %s): %v", userID, err)
			aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "error_stream_read", "user_id": userID}).Inc()
			return fmt.Errorf("%w: ошибка чтения стрима: %v", ErrAIGenerationFailed, err)
		}

		// Обычно OpenAI не присылает Usage в стриме, но проверим на всякий случай
		// и/или попробуем получить из первого сообщения, если оно есть
		// if response.Usage.PromptTokens > 0 { promptTokens = response.Usage.PromptTokens }

		if len(response.Choices) > 0 {
			chunk := response.Choices[0].Delta.Content
			if chunk != "" {
				// Считаем completion токены по мере поступления (приблизительно, по количеству чанков/символов)
				// Точный подсчет токенов в стриме сложен без доп. информации от API
				// completionTokens++ // <<< Удаляем очень грубый подсчет
				responseTextBuilder.WriteString(chunk) // <<< Собираем текст ответа
				if err := chunkHandler(chunk); err != nil {
					log.Printf("Ошибка обработки чанка стрима OpenAI (userID: %s): %v", userID, err)
					// Решаем, должна ли ошибка обработчика прерывать стрим
					return fmt.Errorf("ошибка обработчика стрима: %w", err) // Прерываем
				}
			}
		}
	}

	duration := time.Since(startTime)
	log.Printf("Обработка стрима OpenAI завершена за %v. (userID: %s)", duration, userID)

	// Метрики после успешного завершения стрима
	aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "success_stream", "user_id": userID}).Inc()
	aiRequestDuration.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(duration.Seconds())
	// Токены для стрима - сложная задача. OpenAI обычно не дает их в стриме.
	// Можно логировать/метрики по количеству чанков или общей длине текста.
	// <<< Начало: Подсчет токенов с помощью tiktoken >>>
	tke, err := tiktoken.EncodingForModel(c.model)
	if err != nil {
		log.Printf("Ошибка получения кодировщика tiktoken для модели %s: %v. Метрики токенов не будут записаны.", c.model, err)
	} else {
		// Считаем токены промпта
		promptTokens = len(tke.Encode(systemPrompt, nil, nil))
		if userInput != "" {
			promptTokens += len(tke.Encode(userInput, nil, nil))
			// TODO: Учесть служебные токены для сообщений (зависит от модели, см. документацию OpenAI/tiktoken)
		}

		// Считаем токены ответа
		completionText := responseTextBuilder.String()
		completionTokens = len(tke.Encode(completionText, nil, nil))

		log.Printf("OpenAI Stream Usage (tiktoken, userID: %s): PromptTokens=%d, CompletionTokens=%d, TotalTokens=%d",
			userID, promptTokens, completionTokens, promptTokens+completionTokens)

		if promptTokens > 0 {
			aiPromptTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(promptTokens))
		}
		if completionTokens > 0 {
			aiCompletionTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(completionTokens))
		}
		if promptTokens > 0 && completionTokens > 0 {
			aiTotalTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(promptTokens + completionTokens))
		}
		// <<< Рассчитываем и обновляем стоимость для стрима >>>
		cost := calculateCost(c.model, promptTokens, completionTokens)
		if cost > 0 {
			aiEstimatedCostUSD.With(prometheus.Labels{"model": c.model, "user_id": userID}).Add(cost)
			log.Printf("OpenAI Stream Usage Cost (estimated, userID: %s): $%.6f", userID, cost)
		}
	}
	// <<< Конец: Подсчет токенов с помощью tiktoken >>>
	/* <<< Удаляем старую, неработающую логику записи метрик токенов >>>
	if promptTokens > 0 { // Если удалось получить
		aiPromptTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(promptTokens))
	}
	if completionTokens > 0 { // Используем наш грубый подсчет
		aiCompletionTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(completionTokens))
	}
	if promptTokens > 0 && completionTokens > 0 {
		aiTotalTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(promptTokens + completionTokens))
	}
	*/

	return nil
}

// calculateCost рассчитывает стоимость на основе модели и количества токенов
func calculateCost(modelName string, promptTokens, completionTokens int) float64 {
	// Убрали проверку: if strings.Contains(modelName, "gpt-4.1-nano")
	// Теперь расчет применяется ко всем моделям, используя константы nano
	// TODO: Рассмотреть добавление конфигурации цен для разных моделей в будущем
	log.Printf("[DEBUG] Calculating cost using nano prices for model: %s", modelName) // Добавим лог для ясности
	promptCost := float64(promptTokens) * pricePerMillionInputTokensNano / 1_000_000
	completionCost := float64(completionTokens) * pricePerMillionOutputTokensNano / 1_000_000
	return promptCost + completionCost

	/* <<< Старая логика с проверкой >>>
	if strings.Contains(modelName, "gpt-4.1-nano") {
		promptCost := float64(promptTokens) * pricePerMillionInputTokensNano / 1_000_000
		completionCost := float64(completionTokens) * pricePerMillionOutputTokensNano / 1_000_000
		return promptCost + completionCost
	}
	// Возвращаем 0, если для модели нет цены
	return 0.0
	*/
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
func newOllamaClient(cfg *config.Config) (AIClient, error) {
	httpClient := &http.Client{
		Timeout: cfg.AITimeout,
	}

	// Используем cfg.AIBaseURL, предполагая, что он указывает на Ollama (e.g., http://localhost:11434)
	// api.NewClient требует URL без суффикса /v1
	ollamaBaseURL := strings.TrimSuffix(cfg.AIBaseURL, "/v1") // Убираем /v1 если есть
	ollamaBaseURL = strings.TrimSuffix(ollamaBaseURL, "/")    // Убираем / на конце если есть

	// <<< Парсим строку URL в *url.URL >>>
	parsedURL, err := url.Parse(ollamaBaseURL)
	if err != nil {
		return nil, fmt.Errorf("ошибка парсинга Ollama Base URL '%s': %w", ollamaBaseURL, err)
	}

	// <<< Исправляем присваивание: NewClient возвращает только client >>>
	// <<< Передаем *url.URL вместо string >>>
	client := api.NewClient(parsedURL, httpClient)
	/* <<< Удаляем проверку на ошибку, т.к. NewClient ее не возвращает >>>
	if err != nil {
		return nil, fmt.Errorf("ошибка создания клиента Ollama: %w", err)
	}
	*/

	log.Printf("Ollama Клиент создан. Используемый BaseURL: %s, Model: %s, Timeout: %v", ollamaBaseURL, cfg.AIModel, cfg.AITimeout)

	return &ollamaClient{
		client:  client,
		model:   cfg.AIModel,
		timeout: cfg.AITimeout, // Сохраняем таймаут
	}, nil
}

// GenerateText генерирует текст с использованием Ollama
func (c *ollamaClient) GenerateText(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams) (string, error) {
	if strings.TrimSpace(systemPrompt) == "" {
		log.Printf("Ошибка: Системный промт пуст. Невозможно отправить запрос к Ollama. userID: %s", userID)
		aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "error", "user_id": userID}).Inc()
		return "", fmt.Errorf("%w: системный промт пуст", ErrAIGenerationFailed)
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
		return "", fmt.Errorf("%w: %v", ErrAIGenerationFailed, err)
	}

	if resp.Message.Content == "" {
		// <<< DEBUG: Логирование ответа при пустом контенте >>>
		log.Printf("[OLLAMA_DEBUG] Empty content in final response: %+v", resp)
		// <<< END DEBUG >>>
		log.Printf("Ollama API вернул пустой ответ за %v (userID: %s)", duration, userID)
		aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "error", "user_id": userID}).Inc()
		return "", fmt.Errorf("%w: получен пустой ответ", ErrAIGenerationFailed)
	}

	// <<< DEBUG: Логирование успешного финального ответа >>>
	log.Printf("[OLLAMA_DEBUG] Successful final response: %+v", resp)
	// <<< END DEBUG >>>

	aiRequestsTotal.With(prometheus.Labels{"model": c.model, "status": "success", "user_id": userID}).Inc()
	aiRequestDuration.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(duration.Seconds())

	generatedText := resp.Message.Content
	log.Printf("Ответ от Ollama API получен за %v. Длина ответа: %d символов. (userID: %s)", duration, len(generatedText), userID)

	if resp.PromptEvalCount > 0 || resp.EvalCount > 0 {
		log.Printf("Ollama Usage (userID: %s): PromptTokens=%d, CompletionTokens=%d, TotalTokens=%d",
			userID, resp.PromptEvalCount, resp.EvalCount, resp.PromptEvalCount+resp.EvalCount)
		aiPromptTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(resp.PromptEvalCount))
		aiCompletionTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(resp.EvalCount))
		aiTotalTokens.With(prometheus.Labels{"model": c.model, "user_id": userID}).Observe(float64(resp.PromptEvalCount + resp.EvalCount))
		// <<< Рассчитываем и обновляем стоимость >>>
		cost := calculateCost(c.model, resp.PromptEvalCount, resp.EvalCount)
		if cost > 0 {
			aiEstimatedCostUSD.With(prometheus.Labels{"model": c.model, "user_id": userID}).Add(cost)
			log.Printf("Ollama Usage Cost (estimated, userID: %s): $%.6f", userID, cost)
		}
	}

	return generatedText, nil
}

// GenerateTextStream генерирует текст с использованием Ollama в потоковом режиме
func (c *ollamaClient) GenerateTextStream(ctx context.Context, userID string, systemPrompt string, userInput string, params GenerationParams, chunkHandler func(string) error) error {
	if strings.TrimSpace(systemPrompt) == "" {
		log.Printf("Ошибка стриминга Ollama: Системный промт пуст. userID: %s", userID)
		return fmt.Errorf("%w: системный промт пуст для стриминга", ErrAIGenerationFailed)
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
		return finalErr
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
		// <<< Рассчитываем и обновляем стоимость >>>
		cost := calculateCost(c.model, promptTokens, completionTokens)
		if cost > 0 {
			aiEstimatedCostUSD.With(prometheus.Labels{"model": c.model, "user_id": userID}).Add(cost)
			log.Printf("Ollama Stream Usage Cost (estimated, userID: %s): $%.6f", userID, cost)
		}
	}

	return nil
}

// --- Factory Function ---

// NewAIClient создает новый клиент для взаимодействия с AI в зависимости от конфигурации
func NewAIClient(cfg *config.Config) (AIClient, error) {
	switch strings.ToLower(cfg.AIClientType) {
	case "openai":
		log.Printf("Используется реализация AI клиента: OpenAI")
		openaiConfig := openaigo.DefaultConfig(cfg.AIAPIKey)
		openaiConfig.BaseURL = cfg.AIBaseURL
		httpClient := &http.Client{
			Timeout: cfg.AITimeout,
		}
		openaiConfig.HTTPClient = httpClient
		client := openaigo.NewClientWithConfig(openaiConfig)
		log.Printf("OpenAI Клиент создан. Используемый BaseURL: %s, Model: %s, Timeout: %v", cfg.AIBaseURL, cfg.AIModel, cfg.AITimeout)
		return &openAIClient{
			client: client,
			model:  cfg.AIModel,
		}, nil
	case "ollama":
		log.Printf("Используется реализация AI клиента: Ollama")
		return newOllamaClient(cfg) // Вызываем новую функцию-конструктор
	default:
		return nil, fmt.Errorf("неизвестный тип AI клиента: '%s'", cfg.AIClientType)
	}
}

// -----------------------------------------------------------------
