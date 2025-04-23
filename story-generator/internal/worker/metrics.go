package worker

import (
	"fmt"
	"log"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/push"
)

const (
	jobName = "story_generator_worker"
)

var (
	// Общий реестр для всех метрик этого воркера
	registry = prometheus.NewRegistry()

	// Стандартные метрики Prometheus
	// Мы используем promauto.With(registry) чтобы метрики регистрировались в нашем
	// локальном реестре, а не в глобальном prometheus.DefaultRegistry
	tasksReceived = promauto.With(registry).NewCounter(
		prometheus.CounterOpts{
			Name: "story_generator_tasks_received_total",
			Help: "Total number of tasks received by the story generator worker.",
		},
	)
	tasksFailed = promauto.With(registry).NewCounterVec(
		prometheus.CounterOpts{
			Name: "story_generator_tasks_failed_total",
			Help: "Total number of tasks failed, partitioned by failure reason.",
		},
		[]string{"reason"},
	)
	tasksSucceeded = promauto.With(registry).NewCounter(
		prometheus.CounterOpts{
			Name: "story_generator_tasks_succeeded_total",
			Help: "Total number of tasks successfully processed.",
		},
	)
	tokensUsed = promauto.With(registry).NewCounter(
		prometheus.CounterOpts{
			Name: "story_generator_ai_tokens_used_total",
			Help: "Total number of AI tokens used for generation.",
		},
	)

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
	pusher = push.New(pushgatewayURL, jobName).Gatherer(registry).Grouping("instance", instanceID)

	// Попробуем сразу отправить метрики (с нулевыми значениями), чтобы проверить соединение
	if err := pusher.Push(); err != nil {
		return fmt.Errorf("could not push initial metrics to Pushgateway: %w", err)
	}
	log.Printf("[Metrics] Initial push to Pushgateway successful.")
	return nil
}

// pushMetrics отправляет текущие метрики в Pushgateway.
func pushMetrics() {
	if pusher == nil {
		log.Println("[Metrics] Error: Pusher not initialized.")
		return
	}
	err := pusher.Push()
	if err != nil {
		// Логируем ошибку, но не падаем. Следующий инкремент попробует снова.
		log.Printf("[Metrics] Error pushing metrics to Pushgateway: %v", err)
	}
}

// MetricsIncrementTasksReceived увеличивает счетчик полученных задач и отправляет метрики.
func MetricsIncrementTasksReceived() {
	tasksReceived.Inc()
	pushMetrics()
}

// MetricsIncrementTaskFailed увеличивает счетчик неудачных задач для указанной причины и отправляет метрики.
func MetricsIncrementTaskFailed(reason string) {
	tasksFailed.WithLabelValues(reason).Inc()
	pushMetrics()
}

// MetricsIncrementTaskSucceeded увеличивает счетчик успешно выполненных задач и отправляет метрики.
func MetricsIncrementTaskSucceeded() {
	tasksSucceeded.Inc()
	pushMetrics()
}

// MetricsAddTokensUsed добавляет использованные токены к счетчику и отправляет метрики.
func MetricsAddTokensUsed(count float64) {
	tokensUsed.Add(count)
	pushMetrics()
}

// CleanupMetrics удаляет метрики этого инстанса из Pushgateway.
// Должна вызываться при graceful shutdown.
func CleanupMetrics() error {
	if pusher == nil {
		log.Println("[Metrics] Cleanup: Pusher not initialized, nothing to delete.")
		return nil
	}
	log.Printf("[Metrics] Deleting metrics from Pushgateway for job '%s', grouping key: %v", jobName, groupingKey)
	err := pusher.Delete()
	if err != nil {
		return fmt.Errorf("could not delete metrics from Pushgateway: %w", err)
	}
	log.Printf("[Metrics] Successfully deleted metrics from Pushgateway.")
	return nil
}
