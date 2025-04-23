package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"novel-server/shared/messaging"
	"novel-server/story-generator/internal/api"
	"novel-server/story-generator/internal/config"
	"novel-server/story-generator/internal/repository"
	"novel-server/story-generator/internal/service"
	"novel-server/story-generator/internal/worker"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	// Имя очереди для задач генерации
	taskQueueName = "story_generation_tasks"
	// Имена для Dead Letter Exchange и Queue
	deadLetterExchange = "tasks_dlx"            // Общий DLX для задач
	deadLetterQueue    = taskQueueName + "_dlq" // DLQ для этой конкретной очереди
	// Порт для метрик Prometheus
	metricsPort = "9091"
	// <<< ДОБАВЛЕНО: Имена для DLX/DLQ >>>
	dlxName       = "story_generation_tasks_dlx" // Dead Letter Exchange
	dlqName       = "story_generation_tasks_dlq" // Dead Letter Queue
	dlqRoutingKey = "dlq"                        // Routing key for DLQ
	// <<< КОНЕЦ ДОБАВЛЕНИЯ >>>
)

func main() {
	log.Println("Запуск сервиса генерации историй (воркер + API)...")

	// --- Запуск HTTP-сервера для метрик Prometheus в отдельной горутине ---
	go startMetricsServer()

	// Загружаем конфигурацию
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Ошибка загрузки конфигурации: %v", err)
	}

	// Инициализация зависимостей (выносим AIClient выше, т.к. он нужен и API)
	log.Println("Инициализация AI клиента...")
	aiClient, err := service.NewAIClient(cfg)
	if err != nil {
		log.Fatalf("Ошибка инициализации AI клиента: %v", err)
	}

	// Подключаемся к PostgreSQL
	log.Println("Подключение к PostgreSQL...")
	dbPool, err := setupDatabase(cfg)
	if err != nil {
		log.Fatalf("Не удалось подключиться к базе данных: %v", err)
	}
	defer dbPool.Close()
	log.Println("Успешное подключение к PostgreSQL")

	// Подключаемся к RabbitMQ с логикой повторных попыток
	conn, err := connectRabbitMQ(cfg.RabbitMQURL)
	if err != nil {
		log.Fatalf("Не удалось подключиться к RabbitMQ: %v", err)
	}
	defer conn.Close()
	log.Println("Успешное подключение к RabbitMQ")

	// Открываем канал RabbitMQ (нужен для Notifier и Consumer)
	ch, err := conn.Channel()
	if err != nil {
		log.Fatalf("Не удалось открыть канал: %v", err)
	}
	defer ch.Close()
	log.Println("Канал успешно открыт")

	// --- Настройка Dead Letter Queue (DLQ) ---
	log.Printf("Настройка Dead Letter Exchange ('%s') и Queue ('%s')...", deadLetterExchange, deadLetterQueue)

	// Объявляем Dead Letter Exchange (DLX)
	err = ch.ExchangeDeclare(
		dlxName,  // name
		"direct", // type
		true,     // durable
		false,    // auto-deleted
		false,    // internal
		false,    // no-wait
		nil,      // arguments
	)
	if err != nil {
		log.Fatalf("Не удалось объявить DLX: %v", err)
	}
	log.Printf("DLX '%s' успешно объявлен.", dlxName)

	// Объявляем Dead Letter Queue (DLQ)
	_, err = ch.QueueDeclare(
		dlqName, // name
		true,    // durable
		false,   // delete when unused
		false,   // exclusive
		false,   // no-wait
		nil,     // arguments
	)
	if err != nil {
		log.Fatalf("Не удалось объявить Dead Letter Queue '%s': %v", dlqName, err)
	}
	log.Printf("DLQ '%s' успешно объявлена.", dlqName)

	// Связываем DLQ с DLX. Используем имя основной очереди как ключ маршрутизации.
	err = ch.QueueBind(
		dlqName,       // queue name
		dlqRoutingKey, // routing key
		dlxName,       // exchange
		false,
		nil,
	)
	if err != nil {
		log.Fatalf("Не удалось связать DLQ '%s' с DLX '%s': %v", dlqName, dlxName, err)
	}
	log.Printf("DLQ '%s' успешно связана с DLX '%s' с ключом '%s'.", dlqName, dlxName, dlqRoutingKey)

	// --- Объявляем основные очереди (добавляем аргументы для DLX) ---
	queues := []string{taskQueueName, "game_over_tasks", "draft_story_tasks"}
	for _, qName := range queues {
		args := amqp.Table{
			"x-queue-mode": "lazy", // Используем lazy queues для экономии памяти
		}
		// <<< ДОБАВЛЕНО: Аргументы DLX для основной очереди задач >>>
		if qName == taskQueueName {
			args["x-dead-letter-exchange"] = dlxName
			args["x-dead-letter-routing-key"] = dlqRoutingKey
			log.Printf("Настройка DLX для очереди '%s'", qName)
		}
		// <<< КОНЕЦ ДОБАВЛЕНИЯ >>>

		_, err = ch.QueueDeclare(
			qName, // name
			true,  // durable
			false, // delete when unused
			false, // exclusive
			false, // no-wait
			args,  // arguments (с добавленными аргументами DLX для taskQueueName)
		)
		if err != nil {
			log.Fatalf("Не удалось объявить очередь '%s': %v", qName, err)
		}
		log.Printf("Очередь '%s' успешно объявлена.", qName)
	}

	// Устанавливаем QoS
	err = ch.Qos(1, 0, false)
	if err != nil {
		log.Fatalf("Не удалось установить QoS: %v", err)
	}
	log.Println("QoS (prefetch count=1) установлен")

	// Инициализация зависимостей для воркера
	log.Println("Инициализация репозитория и нотификатора...")
	resultRepo := repository.NewPostgresResultRepository(dbPool)
	notifier, err := service.NewRabbitMQNotifier(ch, cfg)
	if err != nil {
		log.Fatalf("Не удалось создать notifier: %v", err)
	}

	// Создаем обработчик задач воркера
	taskHandler := worker.NewTaskHandler(cfg, aiClient, resultRepo, notifier)

	// --- Инициализация и запуск HTTP API сервера ---
	apiHandler := api.NewAPIHandler(aiClient, cfg)
	httpServer := startHTTPServer(apiHandler)
	log.Printf("HTTP API сервер запущен на порту %s", cfg.HTTPServerPort)
	// -----------------------------------------------

	// Начинаем потреблять сообщения из очереди для воркера
	msgs, err := ch.Consume(
		taskQueueName, "", false, false, false, false, nil)
	if err != nil {
		log.Fatalf("Не удалось зарегистрировать консьюмера: %v", err)
	}

	// Канал для graceful shutdown
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, syscall.SIGINT, syscall.SIGTERM)

	// Канал для синхронизации завершения горутины обработки сообщений
	done := make(chan struct{})

	log.Println(" [*] Ожидание сообщений и API запросов. Для выхода нажмите CTRL+C")

	// Запускаем горутину для обработки сообщений
	go func() {
		defer close(done) // Сигнализируем о завершении горутины
		for msg := range msgs {
			// <<< Метрика: Счетчик полученных задач >>>
			worker.IncrementTasksReceived()

			var payload messaging.GenerationTaskPayload
			err := json.Unmarshal(msg.Body, &payload)
			if err != nil {
				log.Printf("[TaskID: %s] Ошибка десериализации JSON: %v. Отклоняем сообщение (nack, no requeue).", "N/A", err)
				// <<< Метрика: Задача с ошибкой (десериализация) >>>
				worker.IncrementTaskFailed("deserialization")
				msg.Nack(false, false) // Nack(multiple, requeue=false) - не возвращаем в очередь
				continue
			}

			// Вызываем обработчик
			err = taskHandler.Handle(payload)
			if err != nil {
				// Ошибка при обработке - отклоняем сообщение
				// Requeue=false, чтобы избежать бесконечных циклов для 'плохих' задач.
				// В идеале, такие задачи должны попадать в Dead Letter Queue.
				log.Printf("[TaskID: %s] Ошибка обработки задачи: %v. Отклоняем сообщение (nack, no requeue).", payload.TaskID, err)
				msg.Nack(false, false)
			} else {
				// Успешная обработка - подтверждаем сообщение
				log.Printf("[TaskID: %s] Задача успешно обработана, сохранена и уведомление отправлено. Подтверждаем сообщение (ack).", payload.TaskID)
				msg.Ack(false) // Ack(multiple=false)
			}
		}
		log.Println("Канал сообщений закрыт, горутина обработки завершается.")
	}()

	// Ожидаем завершения горутины обработки сообщений
	log.Println("Ожидание завершения обработки текущих сообщений...")
	<-done

	log.Println("Получен сигнал завершения. Завершение работы...")

	// --- Graceful Shutdown для HTTP сервера ---
	log.Println("Остановка HTTP API сервера...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second) // Даем больше времени
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("Ошибка при остановке HTTP сервера: %v", err)
	} else {
		log.Println("HTTP API сервер успешно остановлен.")
	}
	// ----------------------------------------

	// --- Закрытие соединений RabbitMQ и DB ---
	// (defer conn.Close() и defer ch.Close() сработают)
	log.Println("Закрытие соединения с RabbitMQ...")
	// conn.Close() и ch.Close() вызываются через defer
	log.Println("Закрытие соединения с PostgreSQL...")
	// dbPool.Close() вызывается через defer
	log.Println("Сервис генерации историй остановлен.")
}

// startMetricsServer запускает HTTP-сервер для эндпоинта /metrics
func startMetricsServer() {
	http.Handle("/metrics", promhttp.Handler())

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok"}`))
	})

	go func() {
		log.Printf("Запуск HTTP-сервера для метрик Prometheus и health на :%s...", metricsPort)
		if err := http.ListenAndServe(":"+metricsPort, nil); err != nil {
			log.Fatalf("Ошибка запуска HTTP-сервера для метрик: %v", err)
		}
	}()
}

// setupDatabase инициализирует и возвращает пул соединений с БД
func setupDatabase(cfg *config.Config) (*pgxpool.Pool, error) {
	var dbPool *pgxpool.Pool
	var err error
	maxRetries := 50 // Количество попыток
	retryDelay := 3 * time.Second

	dsn := cfg.GetDSN()
	poolConfig, parseErr := pgxpool.ParseConfig(dsn)
	if parseErr != nil {
		// DSN некорректен, нет смысла пытаться дальше
		return nil, fmt.Errorf("ошибка парсинга DSN: %w", parseErr)
	}
	poolConfig.MaxConns = int32(cfg.DBMaxConns)
	poolConfig.MaxConnIdleTime = cfg.DBIdleTimeout

	log.Printf("Попытка подключения к PostgreSQL (до %d раз с интервалом %v)...", maxRetries, retryDelay)

	for i := 0; i < maxRetries; i++ {
		attempt := i + 1
		log.Printf("Попытка %d/%d подключения к PostgreSQL...", attempt, maxRetries)

		// Таймаут на одну попытку подключения и пинга
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		dbPool, err = pgxpool.NewWithConfig(ctx, poolConfig)
		if err != nil {
			log.Printf("[Попытка %d/%d] Не удалось создать пул соединений: %v", attempt, maxRetries, err)
			cancel()
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
			}
			continue // Переходим к следующей попытке
		}

		// Пытаемся пинговать
		if err = dbPool.Ping(ctx); err != nil {
			log.Printf("[Попытка %d/%d] Не удалось выполнить ping к PostgreSQL: %v", attempt, maxRetries, err)
			dbPool.Close() // Закрываем неудачный пул
			cancel()
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
			}
			continue // Переходим к следующей попытке
		}

		// Если дошли сюда, подключение и пинг успешны
		cancel() // Отменяем таймаут текущей попытки
		log.Printf("Успешное подключение и ping к PostgreSQL (попытка %d)", attempt)
		return dbPool, nil
	}

	// Если цикл завершился без успешного подключения
	log.Printf("Не удалось подключиться к PostgreSQL после %d попыток.", maxRetries)
	return nil, fmt.Errorf("не удалось подключиться к БД после %d попыток: %w", maxRetries, err) // Возвращаем последнюю ошибку
}

// --- Изменения в startHTTPServer ---
// Убираем cfg из параметров, так как он будет доступен через apiHandler, если понадобится
func startHTTPServer(apiHandler *api.APIHandler) *http.Server {
	mux := http.NewServeMux()

	// Регистрируем обработчики API
	apiHandler.RegisterRoutes(mux) // Предполагаем, что есть метод RegisterRoutes

	// Создаем HTTP сервер
	server := &http.Server{
		Addr:         ":" + apiHandler.GetPort(), // Получаем порт из хендлера (нужно будет добавить метод GetPort в APIHandler)
		Handler:      mux,
		ReadTimeout:  10 * time.Second,  // Пример таймаута чтения
		WriteTimeout: 60 * time.Second,  // Пример таймаута записи (для стриминга может быть больше)
		IdleTimeout:  120 * time.Second, // Пример таймаута простоя
	}

	// Запускаем сервер в отдельной горутине
	go func() {
		log.Printf("Запуск HTTP API сервера на порту %s...", apiHandler.GetPort())
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Ошибка запуска HTTP API сервера: %v", err)
		}
	}()

	return server
}

// --- Конец изменений в startHTTPServer ---

// ----------------------------------------------------------

// <<< ВОЗВРАЩАЮ ЛОКАЛЬНУЮ ФУНКЦИЮ >>>
// connectRabbitMQ пытается подключиться к RabbitMQ с несколькими попытками
func connectRabbitMQ(url string) (*amqp.Connection, error) {
	var conn *amqp.Connection
	var err error
	maxRetries := 5
	retryDelay := 5 * time.Second

	for i := 0; i < maxRetries; i++ {
		conn, err = amqp.Dial(url)
		if err == nil {
			return conn, nil // Успешное подключение
		}
		log.Printf("Не удалось подключиться к RabbitMQ (попытка %d/%d): %v. Повтор через %v...", i+1, maxRetries, err, retryDelay)
		time.Sleep(retryDelay)
	}
	return nil, err // Не удалось подключиться после всех попыток
}

// <<< КОНЕЦ ВОЗВРАТА >>>
