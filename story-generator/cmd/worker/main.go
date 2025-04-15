package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"novel-server/shared/messaging"
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
)

func main() {
	log.Println("Запуск воркера генерации историй...")

	// --- Запуск HTTP-сервера для метрик Prometheus в отдельной горутине ---
	go startMetricsServer()

	// Загружаем конфигурацию
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Ошибка загрузки конфигурации: %v", err)
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
	conn, err := connectRabbitMQ(cfg.RabbitMQURL) // Используем URL из конфига
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
		deadLetterExchange, // name
		"direct",           // type
		true,               // durable
		false,              // auto-deleted
		false,              // internal
		false,              // no-wait
		nil,                // arguments
	)
	if err != nil {
		log.Fatalf("Не удалось объявить Dead Letter Exchange '%s': %v", deadLetterExchange, err)
	}

	// Объявляем Dead Letter Queue (DLQ)
	_, err = ch.QueueDeclare(
		deadLetterQueue, // name
		true,            // durable
		false,           // delete when unused
		false,           // exclusive
		false,           // no-wait
		nil,             // arguments
	)
	if err != nil {
		log.Fatalf("Не удалось объявить Dead Letter Queue '%s': %v", deadLetterQueue, err)
	}

	// Связываем DLQ с DLX. Используем имя основной очереди как ключ маршрутизации.
	err = ch.QueueBind(
		deadLetterQueue,    // queue name
		taskQueueName,      // routing key (куда DLX будет пересылать сообщения из taskQueueName)
		deadLetterExchange, // exchange
		false,
		nil,
	)
	if err != nil {
		log.Fatalf("Не удалось связать DLQ '%s' с DLX '%s': %v", deadLetterQueue, deadLetterExchange, err)
	}
	log.Printf("DLQ '%s' успешно связана с DLX '%s' с ключом '%s'.", deadLetterQueue, deadLetterExchange, taskQueueName)

	// --- Объявляем основную очередь задач с настройками DLQ ---
	args := amqp.Table{
		"x-dead-letter-exchange":    deadLetterExchange,
		"x-dead-letter-routing-key": taskQueueName, // Ключ, с которым сообщения попадут в DLX
	}
	q, err := ch.QueueDeclare(
		taskQueueName, // name
		true,          // durable
		false,         // delete when unused
		false,         // exclusive
		false,         // no-wait
		args,          // arguments (для DLQ)
	)
	if err != nil {
		log.Fatalf("Не удалось объявить основную очередь задач '%s' с DLQ: %v", taskQueueName, err)
	}
	log.Printf("Основная очередь задач '%s' успешно объявлена/найдена с настройками DLQ.", q.Name)

	// Устанавливаем QoS
	err = ch.Qos(1, 0, false)
	if err != nil {
		log.Fatalf("Не удалось установить QoS: %v", err)
	}
	log.Println("QoS (prefetch count=1) установлен")

	// Инициализация зависимостей
	log.Println("Инициализация сервисов и репозиториев...")
	aiClient := service.NewAIClient(cfg)
	resultRepo := repository.NewPostgresResultRepository(dbPool)

	// Создаем Notifier (используем тот же канал ch)
	notifier, err := service.NewRabbitMQNotifier(ch)
	if err != nil {
		log.Fatalf("Не удалось создать notifier: %v", err) // Ошибка здесь критична при старте
	}

	taskHandler := worker.NewTaskHandler(cfg, aiClient, resultRepo, notifier) // Передаем весь cfg

	// Начинаем потреблять сообщения из очереди
	msgs, err := ch.Consume(
		q.Name, "", false, false, false, false, nil)
	if err != nil {
		log.Fatalf("Не удалось зарегистрировать консьюмера: %v", err)
	}

	// Канал для graceful shutdown
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, syscall.SIGINT, syscall.SIGTERM)

	// Канал для синхронизации завершения горутины обработки сообщений
	done := make(chan struct{})

	log.Println("Воркер готов к работе. Ожидание задач...")

	// Запускаем горутину для обработки сообщений
	go func() {
		defer close(done) // Сигнализируем о завершении горутины
		for msg := range msgs {
			// <<< Метрика: Счетчик полученных задач >>>
			worker.IncrementTasksReceived()

			log.Printf("[TaskID: %s] Получена задача (DeliveryTag: %d): %s", "N/A", msg.DeliveryTag, msg.Body)

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

	log.Println(" [*] Ожидание сообщений. Для выхода нажмите CTRL+C")

	// Ожидаем завершения горутины обработки сообщений
	log.Println("Ожидание завершения обработки текущих сообщений...")
	<-done

	log.Println("Получен сигнал завершения. Закрытие соединений...")
	// При завершении приложения канал msgs закроется, горутина выше завершится.
	// defer conn.Close() и defer ch.Close() будут вызваны.
	log.Println("Воркер остановлен.")
}

// startMetricsServer запускает HTTP-сервер для эндпоинта /metrics
func startMetricsServer() {
	http.Handle("/metrics", promhttp.Handler())
	addr := ":" + metricsPort
	log.Printf("Запуск сервера метрик Prometheus на %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Не удалось запустить сервер метрик: %v", err)
	}
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
