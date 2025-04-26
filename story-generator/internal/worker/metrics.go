package worker

import (
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/push"
)

const (
	jobName = "story_generator_worker"
)

// Define standard buckets for histograms, adjust if needed
var (
	// Default buckets: .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10
	defaultDurationBuckets = prometheus.DefBuckets

	// Buckets for tokens (example, adjust based on typical values)
	tokenBuckets = []float64{10, 50, 100, 200, 500, 1000, 2000, 5000, 10000}

	// Buckets for cost in USD (example, adjust based on typical values)
	costBuckets = []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0}
)

var (
	// Общий реестр для всех метрик этого воркера
	registry = prometheus.NewRegistry()

	// --- Task Processing Metrics ---
	tasksReceived = promauto.With(registry).NewCounter(
		prometheus.CounterOpts{
			Name: "story_generator_tasks_received_total",
			Help: "Total number of tasks received by the story generator worker.",
		},
	)
	tasksFailed = promauto.With(registry).NewCounterVec(
		prometheus.CounterOpts{
			Name: "story_generator_tasks_failed_total",
			Help: "Total number of tasks failed, partitioned by error type.", // Changed from 'reason' to 'error_type' to match dashboard
		},
		[]string{"error_type"},
	)
	tasksSucceeded = promauto.With(registry).NewCounter(
		prometheus.CounterOpts{
			Name: "story_generator_tasks_succeeded_total",
			Help: "Total number of tasks successfully processed.",
		},
	)
	// Renamed from taskProcessingDuration to avoid conflict
	taskDurationHistogram = promauto.With(registry).NewHistogram( // No labels needed as per dashboard
		prometheus.HistogramOpts{
			Name:    "story_generator_task_processing_duration_seconds",
			Help:    "Histogram of task processing duration in seconds (from receive to success/failure).",
			Buckets: defaultDurationBuckets,
		},
	)

	// --- AI Request Metrics ---
	aiRequests = promauto.With(registry).NewCounterVec(
		prometheus.CounterOpts{
			Name: "story_generator_ai_requests_total",
			Help: "Total number of AI API requests attempted.",
		},
		[]string{"model", "status"}, // status: "success", "error", "retry" etc.
	)
	aiRequestDuration = promauto.With(registry).NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "story_generator_ai_request_duration_seconds",
			Help:    "Histogram of AI API request latency in seconds.",
			Buckets: defaultDurationBuckets,
		},
		[]string{"model"}, // Dashboard query only uses model label here
	)

	// --- Token Metrics (per AI request/response) ---
	aiPromptTokensSummary = promauto.With(registry).NewSummaryVec( // Using Summary as dashboards use avg (_sum/_count)
		prometheus.SummaryOpts{
			Name:       "story_generator_ai_prompt_tokens",
			Help:       "Summary of prompt tokens used per AI request.",
			Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001}, // Example objectives
		},
		[]string{"model"},
	)
	aiCompletionTokensSummary = promauto.With(registry).NewSummaryVec(
		prometheus.SummaryOpts{
			Name:       "story_generator_ai_completion_tokens",
			Help:       "Summary of completion tokens generated per AI request.",
			Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
		},
		[]string{"model"},
	)
	aiTotalTokensSummary = promauto.With(registry).NewSummaryVec(
		prometheus.SummaryOpts{
			Name:       "story_generator_ai_total_tokens",
			Help:       "Summary of total tokens (prompt + completion) per AI request.",
			Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
		},
		[]string{"model"},
	)

	// --- Token Metrics (per Task) ---
	taskTokensCounter = promauto.With(registry).NewCounterVec(
		prometheus.CounterOpts{
			Name: "story_generator_task_tokens_total",
			Help: "Total number of tokens processed per task, partitioned by prompt and token type.",
		},
		[]string{"prompt_type", "token_type"}, // token_type: "prompt", "completion"
	)
	// Renamed from taskTokensPerTask to avoid conflict
	taskTokensHistogram = promauto.With(registry).NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "story_generator_task_tokens_per_task",
			Help:    "Histogram of tokens (prompt or completion) used per task.",
			Buckets: tokenBuckets, // Use token specific buckets
		},
		[]string{"prompt_type", "token_type"},
	)

	// --- Cost Metrics (per AI request/response) ---
	aiEstimatedCostCounter = promauto.With(registry).NewCounterVec(
		prometheus.CounterOpts{
			Name: "story_generator_ai_estimated_cost_usd_total",
			Help: "Total estimated cost in USD for AI API calls, partitioned by model.",
		},
		[]string{"model"},
	)

	// --- Cost Metrics (per Task) ---
	taskEstimatedCostCounter = promauto.With(registry).NewCounterVec(
		prometheus.CounterOpts{
			Name: "story_generator_task_estimated_cost_usd_total",
			Help: "Total estimated cost in USD per task, partitioned by prompt type.",
		},
		[]string{"prompt_type"},
	)
	taskEstimatedCostHistogram = promauto.With(registry).NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "story_generator_task_estimated_cost_usd_per_task",
			Help:    "Histogram of estimated cost in USD per task.",
			Buckets: costBuckets, // Use cost specific buckets
		},
		[]string{"prompt_type"},
	)

	// --- DEPRECATED? Keeping for now, but prefer specific token types ---
	// tokensUsed = promauto.With(registry).NewCounter(
	// 	prometheus.CounterOpts{
	// 		Name: "story_generator_ai_tokens_used_total", // Name collision with new token metrics
	// 		Help: "DEPRECATED: Total number of AI tokens used for generation.",
	// 	},
	// )

	// Pusher для отправки метрик в Pushgateway
	pusher *push.Pusher

	// Группировочные метки для Pushgateway
	groupingKey map[string]string
)

// InitMetricsPusher инициализирует клиент Pushgateway.
// pushgatewayURL: адрес Pushgateway (e.g., "http://localhost:9091")
func InitMetricsPusher(pushgatewayURL string) error {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
		log.Printf("[Metrics] Warning: could not get hostname: %v", err)
	}
	pid := os.Getpid()
	instanceID := fmt.Sprintf("%s-%d", hostname, pid)

	groupingKey = map[string]string{
		"instance": instanceID,
	}

	log.Printf("[Metrics] Initializing Pushgateway pusher for job '%s' with instance '%s' to %s", jobName, instanceID, pushgatewayURL)

	// Создаем Pusher, который будет использовать наш registry и группировочные метки
	// We need to register the collectors explicitly if not using the default registry
	// pusher = push.New(pushgatewayURL, jobName).Gatherer(registry).Grouping("instance", instanceID)
	// Instead, create a pusher and add collectors manually or push the entire registry
	pusher = push.New(pushgatewayURL, jobName).Gatherer(registry).Grouping("instance", instanceID)

	// Попробуем сразу отправить метрики (с нулевыми значениями), чтобы проверить соединение
	if err := pushMetrics(); err != nil { // Call internal pushMetrics which checks pusher != nil
		return fmt.Errorf("could not push initial metrics to Pushgateway: %w", err)
	}
	log.Printf("[Metrics] Initial push to Pushgateway successful.")
	return nil
}

// StartMetricsPusher запускает горутину для периодической отправки метрик.
func StartMetricsPusher(interval time.Duration) {
	if pusher == nil {
		log.Println("[Metrics] Pusher not initialized, cannot start periodic push.")
		return
	}
	ticker := time.NewTicker(interval)
	go func() {
		for range ticker.C {
			// No need to check pusher == nil here again, checked at start
			if err := pushMetrics(); err != nil {
				// Ошибка уже логируется внутри pushMetrics
			}
		}
		log.Println("[Metrics] Periodic pusher ticker stopped.") // Log when loop exits
	}()
	log.Printf("[Metrics] Started periodic pusher with interval %v", interval)
}

// pushMetrics отправляет текущие метрики в Pushgateway.
func pushMetrics() error {
	if pusher == nil {
		// Не инициализирован или была ошибка
		// log.Println("[Metrics] Error: Pusher not initialized.") // Avoid spamming logs
		return errors.New("pusher not initialized")
	}

	// Пушим все зарегистрированные метрики из registry
	err := pusher.Push() // Push collects from the registry associated with the pusher
	if err != nil {
		log.Printf("[Metrics] Error pushing metrics to Pushgateway: %v", err)
		return err
	}
	// log.Println("[Metrics] Metrics pushed successfully.") // Reduce log noise, remove success message
	return nil
}

// --- Public Functions to Update Metrics ---

// MetricsIncrementTasksReceived увеличивает счетчик полученных задач.
func MetricsIncrementTasksReceived() {
	tasksReceived.Inc()
	// pushMetrics() // REMOVED - Rely on periodic push or push at end of task
}

// MetricsIncrementTaskFailed увеличивает счетчик неудачных задач для указанного типа ошибки.
func MetricsIncrementTaskFailed(errorType string) {
	tasksFailed.WithLabelValues(errorType).Inc()
	// pushMetrics() // REMOVED
}

// MetricsIncrementTaskSucceeded увеличивает счетчик успешно выполненных задач.
func MetricsIncrementTaskSucceeded() {
	tasksSucceeded.Inc()
	// pushMetrics() // REMOVED
}

// MetricsRecordTaskProcessingDuration записывает длительность обработки задачи.
func MetricsRecordTaskProcessingDuration(duration time.Duration) {
	taskDurationHistogram.Observe(duration.Seconds()) // Use renamed variable
	// pushMetrics() // REMOVED
}

// MetricsRecordAIRequest увеличивает счетчик AI запросов и записывает длительность.
func MetricsRecordAIRequest(model, status string, duration time.Duration) {
	aiRequests.WithLabelValues(model, status).Inc()
	// Only record duration for successful/failed requests? Grafana query doesn't filter by status for duration.
	aiRequestDuration.WithLabelValues(model).Observe(duration.Seconds())
	// pushMetrics() // REMOVED
}

// MetricsRecordAITokens записывает количество токенов, использованных AI.
func MetricsRecordAITokens(model string, promptTokens, completionTokens float64) {
	total := promptTokens + completionTokens
	aiPromptTokensSummary.WithLabelValues(model).Observe(promptTokens)         // Use renamed variable
	aiCompletionTokensSummary.WithLabelValues(model).Observe(completionTokens) // Use renamed variable
	aiTotalTokensSummary.WithLabelValues(model).Observe(total)                 // Use renamed variable
	// pushMetrics() // REMOVED
}

// MetricsRecordTaskTokens увеличивает счетчики токенов для задачи и записывает распределение.
func MetricsRecordTaskTokens(promptType, tokenType string, count float64) {
	taskTokensCounter.WithLabelValues(promptType, tokenType).Add(count)       // Use renamed variable
	taskTokensHistogram.WithLabelValues(promptType, tokenType).Observe(count) // Use renamed variable
	// pushMetrics() // REMOVED
}

// MetricsAddAICost увеличивает счетчик стоимости AI запросов.
func MetricsAddAICost(model string, cost float64) {
	aiEstimatedCostCounter.WithLabelValues(model).Add(cost) // Use renamed variable
	// pushMetrics() // REMOVED
}

// MetricsRecordTaskCost увеличивает счетчик общей стоимости задачи и записывает распределение.
func MetricsRecordTaskCost(promptType string, cost float64) {
	taskEstimatedCostCounter.WithLabelValues(promptType).Add(cost)       // Use renamed variable
	taskEstimatedCostHistogram.WithLabelValues(promptType).Observe(cost) // Use renamed variable
	// pushMetrics() // REMOVED
}

/* DEPRECATED ?
// MetricsAddTokensUsed добавляет использованные токены к счетчику и отправляет метрики.
func MetricsAddTokensUsed(count float64) {
	// tokensUsed.Add(count) // Commented out to avoid name collision if kept
	// pushMetrics() // REMOVED
}
*/

// PushMetricsNow принудительно отправляет текущие метрики. Может быть полезно в конце обработки задачи.
func PushMetricsNow() error {
	log.Println("[Metrics] Attempting to push metrics now...")
	return pushMetrics()
}

// CleanupMetrics удаляет метрики этого инстанса из Pushgateway.
// Должна вызываться через defer в main.
func CleanupMetrics() {
	if pusher == nil {
		log.Println("[Metrics] Cleanup skipped: Pusher not initialized.")
		return
	}

	log.Printf("[Metrics] Deleting metrics from Pushgateway for job '%s', grouping key: %v", jobName, groupingKey)
	err := pusher.Delete()
	if err != nil {
		// Ошибка может быть, если Pushgateway недоступен, но мы все равно пытаемся удалить
		log.Printf("[Metrics] Error deleting metrics from Pushgateway: %v", err)
	} else {
		log.Printf("[Metrics] Successfully deleted metrics from Pushgateway.")
	}
}
