package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	// Имя очереди для задач генерации
	taskQueueName = "story_generation_tasks"
	// Имена для Dead Letter Exchange и Queue
	deadLetterExchange = "tasks_dlx"            // Общий DLX для задач
	deadLetterQueue    = taskQueueName + "_dlq" // DLQ для этой конкретной очереди
)

func main() {
	log.Println("Запуск воркера генерации историй...")

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

	log.Println("Воркер готов к работе. Ожидание задач...")

	// Запускаем горутину для обработки сообщений
	go func() {
		for msg := range msgs {
			log.Printf("[TaskID: %s] Получена задача (DeliveryTag: %d): %s", "N/A", msg.DeliveryTag, msg.Body)

			var payload messaging.GenerationTaskPayload
			err := json.Unmarshal(msg.Body, &payload)
			if err != nil {
				log.Printf("[TaskID: %s] Ошибка десериализации JSON: %v. Отклоняем сообщение (nack, no requeue).", "N/A", err)
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

	// Блокируем до получения сигнала завершения
	<-stopChan

	log.Println("Получен сигнал завершения. Закрытие соединений...")
	// При завершении приложения канал msgs закроется, горутина выше завершится.
	// defer conn.Close() и defer ch.Close() будут вызваны.
	log.Println("Воркер остановлен.")
}

// setupDatabase инициализирует и возвращает пул соединений с БД
func setupDatabase(cfg *config.Config) (*pgxpool.Pool, error) {
	dsn := cfg.GetDSN()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("ошибка парсинга DSN: %w", err)
	}

	config.MaxConns = int32(cfg.DBMaxConns)
	config.MaxConnIdleTime = cfg.DBIdleTimeout

	// Устанавливаем таймаут на подключение
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dbPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать пул соединений: %w", err)
	}

	// Проверяем соединение
	if err = dbPool.Ping(ctx); err != nil {
		dbPool.Close() // Закрываем пул, если пинг не удался
		return nil, fmt.Errorf("не удалось подключиться к БД (ping failed): %w", err)
	}

	return dbPool, nil
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
