package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"

	"novel-server/image-generator/internal/config"
	"novel-server/image-generator/internal/service"
	"novel-server/image-generator/internal/worker"
	"novel-server/shared/logger"
	// Для интерфейса Publisher
)

const (
	maxReconnectAttempts = 50
	reconnectDelay       = 5 * time.Second
	dlxName              = "image_generation_tasks_dlx" // Dead Letter Exchange
	dlqName              = "image_generation_tasks_dlq" // Dead Letter Queue
	dlqRoutingKey        = "image-dlq"                  // Routing key для DLQ (можно использовать имя очереди)
)

func main() {
	// --- 1. Загрузка конфигурации ---
	cfg := config.Load()

	// --- 2. Инициализация логгера ---
	appLogger, err := logger.New(cfg.Logger)
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer appLogger.Sync()
	appLogger.Info("Logger initialized", zap.String("level", cfg.Logger.Level))

	// <<< ДОБАВЛЕНО: Лог для проверки имени очереди задач >>>
	appLogger.Info("Loaded RabbitMQ Task Queue Config",
		zap.String("task_queue_name_from_config", cfg.RabbitMQ.TaskQueue.Name),
		zap.Bool("task_queue_durable", cfg.RabbitMQ.TaskQueue.Durable),
	)
	// <<< КОНЕЦ ДОБАВЛЕНИЯ >>>

	appLogger.Info("Starting Image Generator Worker...", zap.String("env", cfg.AppEnv))

	// --- 3. Инициализация сервиса генерации изображений ---
	// NewImageGenerationService теперь возвращает ошибку, нужно обработать
	imageService, err := service.NewImageGenerationService(appLogger, cfg)
	if err != nil {
		appLogger.Fatal("Failed to initialize image generation service", zap.Error(err))
	}
	appLogger.Info("Image generation service initialized")

	// --- 4. Инициализация RabbitMQ ---
	mqCtx, mqCancel := context.WithCancel(context.Background())
	defer mqCancel()

	var initialConn *amqp091.Connection
	// var resultPublisher messaging.Publisher // Инициализируем позже
	// var sharedChannel *amqp091.Channel // Инициализируем позже
	var errMq error

	// Шаг 1: Подключаемся к RabbitMQ
	initialConn, errMq = connectRabbitMQWithRetry(mqCtx, appLogger, cfg.RabbitMQ.URL)
	if errMq != nil {
		appLogger.Fatal("Failed initial RabbitMQ connection", zap.Error(errMq))
	}
	defer initialConn.Close() // Закрываем соединение при выходе
	appLogger.Info("RabbitMQ connected successfully")

	// Шаг 2: Открываем общий канал
	sharedChannel, errMq := initialConn.Channel()
	if errMq != nil {
		appLogger.Fatal("Failed to open RabbitMQ channel", zap.Error(errMq))
	}
	defer sharedChannel.Close() // Закрываем канал при выходе
	appLogger.Info("RabbitMQ channel opened successfully")

	// Шаг 3: Настраиваем DLX/DLQ
	if err := setupDLX(appLogger, sharedChannel); err != nil {
		appLogger.Fatal("Failed to setup DLX/DLQ", zap.Error(err))
	}
	appLogger.Info("DLX/DLQ setup complete")

	// Шаг 4: Инициализируем Result Publisher
	resultPublisher, errMq := newRabbitMQPublisher(sharedChannel, cfg.RabbitMQ.ResultExchange, cfg.RabbitMQ.ResultRoutingKey, cfg.RabbitMQ.ResultQueueName)
	if errMq != nil {
		appLogger.Fatal("Failed to create RabbitMQ result publisher", zap.Error(errMq))
	}
	appLogger.Info("RabbitMQ result publisher initialized")

	// --- ЗАПУСК МОНИТОРИНГА (ПОКА ЗАКОММЕНТИРОВАНО) ---
	var wg sync.WaitGroup
	// wg.Add(1)
	// go func() {
	// 	defer wg.Done()
	// monitorRabbitMQConnection(mqCtx, appLogger, cfg.RabbitMQ, initialConn, sharedChannel, resultPublisher.(*rabbitMQPublisher)) // Нужна доработка monitor
	// 	appLogger.Info("RabbitMQ connection monitor exited")
	// }()

	// --- 5. Инициализация обработчика сообщений ---
	messageHandler := worker.NewHandler(appLogger, imageService, resultPublisher, cfg.PushGatewayURL)
	appLogger.Info("Message handler initialized")

	// --- 6. Запуск Consumer'а ---
	wg.Add(1) // Добавляем в группу ожидания Consumer'а
	go func() {
		defer wg.Done()
		startConsumer(mqCtx, appLogger, cfg.RabbitMQ, sharedChannel, messageHandler)
		appLogger.Info("RabbitMQ consumer exited")
	}()

	appLogger.Info("Image Generator Worker started successfully")

	// --- 7. Ожидание сигнала завершения ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	appLogger.Info("Shutting down Image Generator Worker...")

	// --- 8. Graceful Shutdown ---
	mqCancel()

	// Ожидаем завершения горутин RabbitMQ
	appLogger.Info("Waiting for background tasks to finish...")
	wg.Wait()

	appLogger.Info("Image Generator Worker shut down gracefully")
}

// connectRabbitMQWithRetry пытается подключиться к RabbitMQ с несколькими попытками.
// Возвращает только соединение или ошибку.
func connectRabbitMQWithRetry(ctx context.Context, logger *zap.Logger, url string) (*amqp091.Connection, error) {
	var conn *amqp091.Connection
	var err error

	logger.Info("Attempting to connect to RabbitMQ...",
		zap.Int("max_attempts", maxReconnectAttempts),
		zap.Duration("delay", reconnectDelay),
	)

	for attempt := 1; attempt <= maxReconnectAttempts; attempt++ {
		conn, err = amqp091.Dial(url)
		if err == nil {
			logger.Info("RabbitMQ connected successfully", zap.Int("attempt", attempt))
			// --- УСПЕХ: Возвращаем только соединение ---
			return conn, nil
		}

		logger.Error("Failed to connect to RabbitMQ",
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", maxReconnectAttempts),
			zap.Error(err),
		)

		// Не последняя попытка, ждем перед следующей
		if attempt < maxReconnectAttempts {
			select {
			case <-time.After(reconnectDelay):
				logger.Info("Retrying RabbitMQ connection...")
			case <-ctx.Done():
				logger.Info("Context cancelled during connect retry, stopping attempts")
				return nil, ctx.Err() // Возвращаем ошибку контекста
			}
		}
	}

	// Если цикл завершился без успеха
	finalErr := fmt.Errorf("failed to connect to RabbitMQ after %d attempts: %w", maxReconnectAttempts, err)
	logger.Error("Shutting down due to persistent RabbitMQ connection failure", zap.Error(finalErr))
	return nil, finalErr // Возвращаем финальную ошибку
}

// --- НОВАЯ ФУНКЦИЯ МОНИТОРИНГА (пока не используется, нужно доработать) ---
// func monitorRabbitMQConnection(
// ...
// ) {
// ...
// }

// setupDLX функция настройки DLX/DLQ
func setupDLX(logger *zap.Logger, ch *amqp091.Channel) error {
	logger.Info("Setting up Dead Letter Exchange and Queue", zap.String("dlx", dlxName), zap.String("dlq", dlqName))

	// Объявляем Dead Letter Exchange (DLX)
	err := ch.ExchangeDeclare(
		dlxName,  // name
		"direct", // type
		true,     // durable
		false,    // auto-deleted
		false,    // internal
		false,    // no-wait
		nil,      // arguments
	)
	if err != nil {
		return fmt.Errorf("failed to declare DLX '%s': %w", dlxName, err)
	}
	logger.Debug("DLX declared", zap.String("dlx", dlxName))

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
		return fmt.Errorf("failed to declare DLQ '%s': %w", dlqName, err)
	}
	logger.Debug("DLQ declared", zap.String("dlq", dlqName))

	// Связываем DLQ с DLX
	err = ch.QueueBind(
		dlqName,       // queue name
		dlqRoutingKey, // routing key
		dlxName,       // exchange
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to bind DLQ '%s' to DLX '%s': %w", dlqName, dlxName, err)
	}
	logger.Debug("DLQ bound to DLX", zap.String("dlq", dlqName), zap.String("dlx", dlxName), zap.String("key", dlqRoutingKey))
	return nil
}

// startConsumer запускает прослушивание очереди задач.
func startConsumer(ctx context.Context, logger *zap.Logger, cfg config.RabbitMQConfig, ch *amqp091.Channel, handler *worker.Handler) {
	if ch == nil {
		logger.Error("Cannot start consumer, RabbitMQ channel is nil")
		return
	}

	// Канал уже открыт и передан нам
	// defer ch.Close() // Не закрываем общий канал здесь

	// Аргументы для DLX
	queueArgs := amqp091.Table{
		"x-dead-letter-exchange":    dlxName,
		"x-dead-letter-routing-key": dlqRoutingKey,
		// Можно добавить другие аргументы по умолчанию, если нужно, например, x-queue-mode: lazy
	}

	// Объявляем очередь задач с DLX аргументами
	q, err := ch.QueueDeclare(
		cfg.TaskQueue.Name,
		cfg.TaskQueue.Durable,
		cfg.TaskQueue.AutoDelete,
		cfg.TaskQueue.Exclusive,
		cfg.TaskQueue.NoWait,
		queueArgs, // Используем аргументы с DLX
	)
	if err != nil {
		logger.Error("Failed to declare task queue with DLX args", zap.String("queue", cfg.TaskQueue.Name), zap.Error(err))
		return
	}
	logger.Info("Task queue declared with DLX args", zap.String("queue", q.Name), zap.Int("messages", q.Messages), zap.Int("consumers", q.Consumers))

	// Настраиваем Quality of Service (prefetch count)
	if err := ch.Qos(1, 0, false); err != nil {
		logger.Error("Failed to set QoS", zap.Error(err))
		return
	}

	// --- Вызов Consume ---
	// Используем q.Name вместо cfg.TaskQueue.Name, так как q - это результат QueueDeclare
	msgs, err := ch.Consume(
		q.Name,           // queue name - ИСПОЛЬЗУЕМ ИМЯ ИЗ РЕЗУЛЬТАТА DECLARE
		cfg.ConsumerName, // consumer tag
		false,            // auto-ack (false - подтверждаем вручную)
		cfg.TaskQueue.Exclusive,
		false, // no-local (обычно false для RabbitMQ)
		cfg.TaskQueue.NoWait,
		nil, // arguments
	)
	if err != nil {
		logger.Error("Failed to register a consumer", zap.String("queue", q.Name), zap.Error(err))
		// TODO: Нужно ли здесь как-то сигнализировать об ошибке и останавливать воркер?
		// Возможно, стоит вернуть ошибку или использовать канал для сигнализации.
		return // Выходим из горутины, если не удалось зарегистрировать консьюмера
	}

	logger.Info("Consumer started, waiting for messages on shared channel...", zap.String("queue", q.Name)) // <-- Используем q.Name для лога

	// --- Цикл обработки сообщений ---
	for {
		select {
		case msg, ok := <-msgs:
			if !ok {
				logger.Warn("Consumer channel closed by RabbitMQ")
				// Канал закрылся, manageRabbitMQConnection должен обработать это.
				return
			}
			logger.Debug("Received a message", zap.Uint64("delivery_tag", msg.DeliveryTag))
			if handler.HandleDelivery(ctx, msg) {
				if ackErr := msg.Ack(false); ackErr != nil {
					logger.Error("Failed to ack message", zap.Uint64("delivery_tag", msg.DeliveryTag), zap.Error(ackErr))
				}
			} else {
				if nackErr := msg.Nack(false, false); nackErr != nil {
					logger.Error("Failed to nack message", zap.Uint64("delivery_tag", msg.DeliveryTag), zap.Error(nackErr))
				}
			}
		case <-ctx.Done():
			logger.Info("Context cancelled, stopping consumer...")
			return
		}
	}
}

// --- Простая реализация RabbitMQ Publisher (изменения) ---

type rabbitMQPublisher struct {
	// conn         *amqp091.Connection // Больше не храним conn
	ch           *amqp091.Channel // Используем общий канал
	exchangeName string
	routingKey   string
	queueName    string
	logger       *zap.Logger
	mu           sync.Mutex
	isClosed     bool // Флаг, что паблишер неактивен (канал закрыт извне)
}

// Принимает существующий канал
func newRabbitMQPublisher(ch *amqp091.Channel, exchange, routingKey, queueName string) (*rabbitMQPublisher, error) {
	// Канал уже открыт и передан нам
	if ch == nil {
		return nil, errors.New("cannot create publisher with nil channel")
	}

	publisherLogger := zap.L().Named("rabbitmq_publisher")

	// Если exchange не указан, объявляем очередь (предполагаем direct to queue)
	if exchange == "" && queueName != "" {
		publisherLogger.Debug("Declaring result queue", zap.String("queue", queueName))
		_, err := ch.QueueDeclare(
			queueName,
			true,  // durable
			false, // autoDelete
			false, // exclusive
			false, // noWait
			nil,   // arguments
		)
		if err != nil {
			// Не закрываем канал здесь, т.к. он общий!
			return nil, fmt.Errorf("failed to declare result queue %s: %w", queueName, err)
		}
		publisherLogger.Debug("Result queue declared", zap.String("queue", queueName))
		// Если exchange не задан, routing key должен быть именем очереди
		if routingKey == "" {
			routingKey = queueName
		}
	} // TODO: Добавить объявление Exchange, если exchangeName не пустой

	return &rabbitMQPublisher{
		// conn:         conn, // Убрали conn
		ch:           ch, // Сохраняем общий канал
		exchangeName: exchange,
		routingKey:   routingKey,
		queueName:    queueName,
		logger:       publisherLogger,
	}, nil
}

func (p *rabbitMQPublisher) Publish(ctx context.Context, payload interface{}, correlationID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isClosed || p.ch == nil {
		return errors.New("publisher channel is closed or publisher is marked as closed")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Используем общий канал
	err = p.ch.PublishWithContext(ctx,
		p.exchangeName,
		p.routingKey,
		false, // mandatory
		false, // immediate
		amqp091.Publishing{
			ContentType:   "application/json",
			CorrelationId: correlationID,
			Body:          body,
			DeliveryMode:  amqp091.Persistent,
		},
	)
	if err != nil {
		// Ошибка может быть из-за закрытого канала/соединения
		p.logger.Error("Failed to publish message", zap.Error(err))
		// Не пытаемся пересоздать канал здесь, т.к. он управляется извне
		return fmt.Errorf("failed to publish message: %w", err)
	}
	return nil
}

// Close() больше не закрывает канал, а помечает паблишер
func (p *rabbitMQPublisher) Close() error {
	// Этот метод вызывается при штатном завершении, но канал закрывается в manageRabbitMQConnection
	p.MarkClosed()
	return nil
}

// MarkClosed помечает паблишер как неактивный (канал закрыт извне)
func (p *rabbitMQPublisher) MarkClosed() {
	p.mu.Lock()
	p.isClosed = true
	p.ch = nil // Обнуляем ссылку на канал
	p.mu.Unlock()
	p.logger.Info("Publisher marked as closed")
}
