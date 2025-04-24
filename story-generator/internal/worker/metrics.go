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

// StartMetricsPusher запускает горутину для периодической отправки метрик.
func StartMetricsPusher(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for range ticker.C {
			if pusher == nil {
				ticker.Stop()
				log.Println("[Metrics] Pusher is nil, stopping periodic push.")
				return
			}
			if err := pushMetrics(); err != nil {
				// Ошибка уже логируется внутри pushMetrics
			}
		}
	}()
	log.Printf("[Metrics] Started periodic pusher with interval %v", interval)
}

// pushMetrics отправляет текущие метрики в Pushgateway.
func pushMetrics() error {
	if pusher == nil {
		// Не инициализирован или была ошибка
		// log.Println("[Metrics] Error: Pusher not initialized.") // Не логируем здесь, чтобы не спамить
		return errors.New("pusher not initialized")
	}

	// Пушим все зарегистрированные метрики
	err := pusher.Push()
	if err != nil {
		log.Printf("[Metrics] Error pushing metrics to Pushgateway: %v", err)
		return err
	}
	log.Println("[Metrics] Metrics pushed successfully.")
	return nil
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
