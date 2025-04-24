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
	"novel-server/shared/messaging" // Для интерфейса Publisher
)

const (
	maxReconnectAttempts = 5
	reconnectDelay       = 5 * time.Second
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
	appLogger.Info("Starting Image Generator Worker...", zap.String("env", cfg.AppEnv))

	// --- 3. Инициализация сервиса генерации изображений ---
	// NewImageGenerationService теперь возвращает ошибку, нужно обработать
	imageService, err := service.NewImageGenerationService(appLogger, cfg)
	if err != nil {
		appLogger.Fatal("Failed to initialize image generation service", zap.Error(err))
	}
	appLogger.Info("Image generation service initialized")

	// --- 4. Инициализация RabbitMQ ---
	// Используем отдельный контекст для управления подключением RabbitMQ
	mqCtx, mqCancel := context.WithCancel(context.Background())
	defer mqCancel() // Отменяем контекст при выходе из main

	var wg sync.WaitGroup
	var conn *amqp091.Connection
	var resultPublisher messaging.Publisher

	// Запускаем управление подключением в отдельной горутине
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, resultPublisher = manageRabbitMQConnection(mqCtx, appLogger, cfg.RabbitMQ)
		appLogger.Info("RabbitMQ connection manager exited")
	}()

	// --- 5. Инициализация обработчика сообщений ---
	// Ждем, пока publisher будет инициализирован (первое подключение)
	for resultPublisher == nil {
		appLogger.Info("Waiting for RabbitMQ result publisher initialization...")
		time.Sleep(1 * time.Second)
		if mqCtx.Err() != nil { // Проверяем, не отменился ли контекст
			appLogger.Fatal("Failed to initialize RabbitMQ publisher within context deadline")
		}
	}
	messageHandler := worker.NewHandler(appLogger, imageService, resultPublisher, cfg.PushGatewayURL)
	appLogger.Info("Message handler initialized")

	// --- 6. Запуск Consumer'а ---
	// Запускаем прослушивание очереди в отдельной горутине
	wg.Add(1)
	go func() {
		defer wg.Done()
		startConsumer(mqCtx, appLogger, cfg.RabbitMQ, conn, messageHandler)
		appLogger.Info("RabbitMQ consumer exited")
	}()

	appLogger.Info("Image Generator Worker started successfully")

	// --- 7. Ожидание сигнала завершения ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	appLogger.Info("Shutting down Image Generator Worker...")

	// --- 8. Graceful Shutdown ---
	mqCancel() // Сигнализируем горутинам RabbitMQ о завершении

	// Ожидаем завершения горутин RabbitMQ
	appLogger.Info("Waiting for background tasks to finish...")
	wg.Wait()

	appLogger.Info("Image Generator Worker shut down gracefully")
}

// manageRabbitMQConnection управляет подключением и переподключением к RabbitMQ,
// а также инициализирует resultPublisher.
func manageRabbitMQConnection(ctx context.Context, logger *zap.Logger, cfg config.RabbitMQConfig) (*amqp091.Connection, messaging.Publisher) {
	var conn *amqp091.Connection
	var err error
	var publisher *rabbitMQPublisher

	for attempt := 1; ; attempt++ {
		conn, err = amqp091.Dial(cfg.URL)
		if err == nil {
			logger.Info("RabbitMQ connected successfully")

			// Инициализируем publisher при успешном подключении
			publisher, err = newRabbitMQPublisher(conn, cfg.ResultExchange, cfg.ResultRoutingKey, cfg.ResultQueueName)
			if err != nil {
				logger.Error("Failed to create RabbitMQ publisher", zap.Error(err))
				conn.Close() // Закрываем соединение, если паблишер не создался
				conn = nil
			} else {
				logger.Info("RabbitMQ result publisher initialized")
				break // Успешно подключились и создали паблишер
			}
		}

		logger.Error("Failed to connect to RabbitMQ", zap.Int("attempt", attempt), zap.Error(err))
		if attempt >= maxReconnectAttempts {
			logger.Fatal("Max reconnect attempts reached, shutting down")
			return nil, nil // Не должно достигнуть из-за Fatal
		}

		select {
		case <-time.After(reconnectDelay):
			logger.Info("Retrying RabbitMQ connection...")
		case <-ctx.Done():
			logger.Info("Context cancelled, stopping RabbitMQ connection attempts")
			return nil, nil
		}
	}

	// Следим за разрывом соединения
	notifyClose := make(chan *amqp091.Error)
	conn.NotifyClose(notifyClose)

	select {
	case closeErr := <-notifyClose:
		logger.Warn("RabbitMQ connection closed", zap.Error(closeErr))
		// Попытка переподключения
		return manageRabbitMQConnection(ctx, logger, cfg) // Рекурсивный вызов для переподключения
	case <-ctx.Done():
		logger.Info("Context cancelled, closing RabbitMQ connection")
		if conn != nil {
			conn.Close()
		}
		if publisher != nil {
			publisher.Close()
		}
		return nil, nil
	}
}

// startConsumer запускает прослушивание очереди задач.
func startConsumer(ctx context.Context, logger *zap.Logger, cfg config.RabbitMQConfig, conn *amqp091.Connection, handler *worker.Handler) {
	if conn == nil {
		logger.Error("Cannot start consumer, RabbitMQ connection is nil")
		return
	}

	ch, err := conn.Channel()
	if err != nil {
		logger.Error("Failed to open RabbitMQ channel for consumer", zap.Error(err))
		// Соединение могло закрыться, manageRabbitMQConnection должно переподключиться
		return
	}
	defer ch.Close()

	// Объявляем очередь задач
	q, err := ch.QueueDeclare(
		cfg.TaskQueue.Name,
		cfg.TaskQueue.Durable,
		cfg.TaskQueue.AutoDelete,
		cfg.TaskQueue.Exclusive,
		cfg.TaskQueue.NoWait,
		nil, // arguments
	)
	if err != nil {
		logger.Error("Failed to declare task queue", zap.String("queue", cfg.TaskQueue.Name), zap.Error(err))
		return
	}
	logger.Info("Task queue declared", zap.String("queue", q.Name), zap.Int("messages", q.Messages), zap.Int("consumers", q.Consumers))

	// Настраиваем Quality of Service (prefetch count)
	// Это важно, чтобы воркер не брал слишком много задач сразу
	if err := ch.Qos(1, 0, false); err != nil {
		logger.Error("Failed to set QoS", zap.Error(err))
		return
	}

	msgs, err := ch.Consume(
		q.Name,           // queue
		cfg.ConsumerName, // consumer tag
		false,            // auto-ack (false, мы подтверждаем вручную)
		cfg.TaskQueue.Exclusive,
		false, // no-local (не используется с очередями)
		cfg.TaskQueue.NoWait,
		nil, // args
	)
	if err != nil {
		logger.Error("Failed to register consumer", zap.String("queue", q.Name), zap.Error(err))
		return
	}

	logger.Info("Consumer started, waiting for messages...")

	for {
		select {
		case msg, ok := <-msgs:
			if !ok {
				logger.Warn("Consumer channel closed by RabbitMQ")
				// Канал закрылся, возможно, из-за разрыва соединения.
				// manageRabbitMQConnection должен обработать это и перезапустить нас.
				return
			}
			logger.Debug("Received a message", zap.Uint64("delivery_tag", msg.DeliveryTag))
			if handler.HandleDelivery(ctx, msg) {
				if ackErr := msg.Ack(false); ackErr != nil { // Ack - подтверждение успешной обработки
					logger.Error("Failed to ack message", zap.Uint64("delivery_tag", msg.DeliveryTag), zap.Error(ackErr))
				}
			} else {
				if nackErr := msg.Nack(false, true); nackErr != nil { // Nack - сообщение не обработано, requeue=true (можно настроить)
					logger.Error("Failed to nack message", zap.Uint64("delivery_tag", msg.DeliveryTag), zap.Error(nackErr))
				}
			}
		case <-ctx.Done():
			logger.Info("Context cancelled, stopping consumer...")
			return
		}
	}
}

// --- Простая реализация RabbitMQ Publisher ---

type rabbitMQPublisher struct {
	conn         *amqp091.Connection
	ch           *amqp091.Channel
	exchangeName string
	routingKey   string
	queueName    string // Используется для объявления очереди, если exchange пустой
	logger       *zap.Logger
	mu           sync.Mutex
}

func newRabbitMQPublisher(conn *amqp091.Connection, exchange, routingKey, queueName string) (*rabbitMQPublisher, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("failed to open channel for publisher: %w", err)
	}

	// Если exchange не указан, объявляем очередь (предполагаем direct to queue)
	if exchange == "" && queueName != "" {
		_, err := ch.QueueDeclare(
			queueName,
			true,  // durable
			false, // autoDelete
			false, // exclusive
			false, // noWait
			nil,   // arguments
		)
		if err != nil {
			ch.Close()
			return nil, fmt.Errorf("failed to declare result queue %s: %w", queueName, err)
		}
		// Если exchange не задан, routing key должен быть именем очереди
		if routingKey == "" {
			routingKey = queueName
		}
	} // TODO: Добавить объявление Exchange, если exchangeName не пустой

	return &rabbitMQPublisher{
		conn:         conn,
		ch:           ch,
		exchangeName: exchange,
		routingKey:   routingKey,
		queueName:    queueName,
		logger:       zap.L().Named("rabbitmq_publisher"), // Используем глобальный логгер
	}, nil
}

func (p *rabbitMQPublisher) Publish(ctx context.Context, payload interface{}, correlationID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.ch == nil {
		return errors.New("publisher channel is closed")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	err = p.ch.PublishWithContext(ctx,
		p.exchangeName,
		p.routingKey,
		false, // mandatory
		false, // immediate
		amqp091.Publishing{
			ContentType:   "application/json",
			CorrelationId: correlationID,
			Body:          body,
			DeliveryMode:  amqp091.Persistent, // Делаем сообщения постоянными
		},
	)
	if err != nil {
		// Попытка переподключения канала, если ошибка связана с каналом/соединением
		// (В данной простой реализации этого нет, но можно добавить)
		return fmt.Errorf("failed to publish message: %w", err)
	}
	return nil
}

func (p *rabbitMQPublisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ch != nil {
		err := p.ch.Close()
		p.ch = nil // Устанавливаем в nil после закрытия
		return err
	}
	return nil // Канал уже закрыт
}
