package messaging

import (
	"context"
	"fmt"
	"novel-server/auth/internal/domain/dto"
	interfaces "novel-server/shared/interfaces" // Используем константы, если они там есть
	"time"

	"github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

// TokenDeletionConsumer слушает очередь и удаляет невалидные токены.
type TokenDeletionConsumer struct {
	conn        *amqp091.Connection
	ch          *amqp091.Channel
	tokenSvc    interfaces.DeviceTokenService
	logger      *zap.Logger
	queueName   string
	consumerTag string
	done        chan error // Сигнал для остановки
}

// NewTokenDeletionConsumer создает нового консьюмера.
func NewTokenDeletionConsumer(
	conn *amqp091.Connection,
	tokenSvc interfaces.DeviceTokenService,
	logger *zap.Logger,
) (*TokenDeletionConsumer, error) {
	if conn == nil {
		return nil, fmt.Errorf("RabbitMQ connection is nil")
	}
	if tokenSvc == nil {
		return nil, fmt.Errorf("DeviceTokenService is nil")
	}

	consumerTag := fmt.Sprintf("token_deletion_consumer_%d", time.Now().UnixNano())
	// TODO: Вынести имя очереди в константы shared/messaging?
	queueName := "auth_token_deletions"

	consumer := &TokenDeletionConsumer{
		conn:        conn,
		tokenSvc:    tokenSvc,
		logger:      logger.Named("TokenDeletionConsumer").With(zap.String("consumerTag", consumerTag), zap.String("queue", queueName)),
		queueName:   queueName,
		consumerTag: consumerTag,
		done:        make(chan error),
	}

	if err := consumer.setupChannelAndQueue(); err != nil {
		return nil, fmt.Errorf("failed to setup channel and queue: %w", err)
	}

	consumer.logger.Info("TokenDeletionConsumer инициализирован")
	return consumer, nil
}

// setupChannelAndQueue создает канал и объявляет очередь.
func (c *TokenDeletionConsumer) setupChannelAndQueue() error {
	var err error
	c.ch, err = c.conn.Channel()
	if err != nil {
		return fmt.Errorf("failed to open channel: %w", err)
	}
	c.logger.Info("RabbitMQ channel opened")

	// Объявляем очередь (durable)
	// Если очередь уже существует с другими параметрами, будет ошибка
	_, err = c.ch.QueueDeclare(
		c.queueName,
		true,  // durable
		false, // delete when unused
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		_ = c.ch.Close() // Пытаемся закрыть канал при ошибке
		return fmt.Errorf("failed to declare queue '%s': %w", c.queueName, err)
	}
	c.logger.Info("RabbitMQ queue declared", zap.String("queue", c.queueName))

	// Устанавливаем QoS (Quality of Service) - обрабатывать по одному сообщению за раз
	err = c.ch.Qos(
		1,     // prefetch count
		0,     // prefetch size
		false, // global
	)
	if err != nil {
		_ = c.ch.Close()
		return fmt.Errorf("failed to set QoS: %w", err)
	}
	c.logger.Info("RabbitMQ QoS set", zap.Int("prefetchCount", 1))

	return nil
}

// StartConsuming запускает процесс получения и обработки сообщений.
// Блокирует выполнение до тех пор, пока консьюмер не будет остановлен или не произойдет ошибка.
func (c *TokenDeletionConsumer) StartConsuming() error {
	if c.ch == nil {
		return fmt.Errorf("channel is not initialized, call setupChannelAndQueue first")
	}
	c.logger.Info("Начало прослушивания очереди удаления токенов...")

	deliveries, err := c.ch.Consume(
		c.queueName,
		c.consumerTag,
		false, // auto-ack (устанавливаем в false, будем подтверждать вручную)
		false, // exclusive
		false, // no-local (не релевантно для прямого обмена/очереди)
		false, // no-wait
		nil,   // args
	)
	if err != nil {
		c.logger.Error("Ошибка запуска consumer'а", zap.Error(err))
		c.done <- err // Отправляем ошибку в канал done для остановки
		return fmt.Errorf("failed to register a consumer: %w", err)
	}

	// Горутина для обработки сообщений
	go c.handleDeliveries(deliveries)

	// Горутина для отслеживания закрытия канала
	go func() {
		notifyClose := make(chan *amqp091.Error)
		c.ch.NotifyClose(notifyClose)
		select {
		case err := <-notifyClose:
			if err != nil {
				c.logger.Error("RabbitMQ channel closed unexpectedly", zap.Error(err))
				c.done <- err // Сигнализируем об ошибке
			} else {
				c.logger.Info("RabbitMQ channel closed gracefully.")
				c.done <- nil // Сигнализируем об обычной остановке
			}
		case <-c.done: // Если Stop() был вызван раньше
			c.logger.Info("Received stop signal while waiting for channel close.")
		}
	}()

	c.logger.Info("Consumer запущен и ожидает сообщений", zap.String("tag", c.consumerTag))
	// Ожидаем сигнала об ошибке или остановке
	return <-c.done
}

// handleDeliveries обрабатывает входящие сообщения.
func (c *TokenDeletionConsumer) handleDeliveries(deliveries <-chan amqp091.Delivery) {
	for d := range deliveries {
		log := c.logger.With(zap.Uint64("deliveryTag", d.DeliveryTag))
		log.Debug("Получено сообщение для удаления токена")

		tokenToDelete := string(d.Body)
		if tokenToDelete == "" {
			log.Warn("Получено пустое сообщение, отклоняем (Nack)")
			if err := d.Nack(false, false); err != nil { // Не переставляем в очередь
				log.Error("Ошибка при отклонении (Nack) пустого сообщения", zap.Error(err))
			}
			continue
		}

		log = log.With(zap.String("tokenPrefix", getTokenPrefix(tokenToDelete))) // Логируем только начало токена

		// Создаем DTO для сервиса
		dto := dto.UnregisterDeviceTokenInput{
			Token: tokenToDelete,
		}

		// Вызываем сервис для удаления
		// Используем новый контекст с таймаутом для операции удаления
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second) // Таймаут 10 сек
		err := c.tokenSvc.UnregisterDeviceToken(ctx, &dto)
		cancel() // Освобождаем ресурсы контекста

		if err != nil {
			// Если произошла ошибка при удалении (не считая "не найдено"),
			// то не подтверждаем сообщение (Nack) и просим переотправить его позже (requeue=true)
			log.Error("Ошибка при вызове UnregisterDeviceToken, сообщение будет переотправлено (Nack, requeue)", zap.Error(err))
			if nackErr := d.Nack(false, true); nackErr != nil { // requeue = true
				log.Error("Ошибка при отклонении (Nack) сообщения после ошибки сервиса", zap.Error(nackErr))
			}
			// Можно добавить задержку перед обработкой следующего сообщения, если ошибки частые
			time.Sleep(1 * time.Second)
		} else {
			// Если удаление прошло успешно (или токен уже был удален), подтверждаем сообщение (Ack)
			log.Info("Токен успешно обработан (удален или не найден), подтверждаем (Ack)")
			if ackErr := d.Ack(false); ackErr != nil {
				log.Error("Ошибка при подтверждении (Ack) сообщения", zap.Error(ackErr))
				// Здесь уже сложно что-то сделать, сообщение может быть обработано повторно
			}
		}
	}
	c.logger.Info("Канал deliveries закрыт, обработка сообщений завершена.")
	// Если handleDeliveries завершается (например, из-за закрытия канала),
	// сигнализируем об этом, если Stop() еще не был вызван.
	select {
	case c.done <- nil:
	default: // Канал done уже закрыт или заполнен
	}
}

// Stop корректно останавливает консьюмера.
func (c *TokenDeletionConsumer) Stop() error {
	if c.ch == nil {
		c.logger.Warn("Попытка остановить консьюмер с незакрытым каналом")
		return nil // Или вернуть ошибку?
	}
	c.logger.Info("Остановка TokenDeletionConsumer...")

	// 1. Отменяем подписку consumer'а
	err := c.ch.Cancel(c.consumerTag, false) // noWait = false
	if err != nil {
		c.logger.Error("Ошибка при отмене consumer'а", zap.String("tag", c.consumerTag), zap.Error(err))
		// Продолжаем закрытие канала
	} else {
		c.logger.Info("Consumer успешно отменен", zap.String("tag", c.consumerTag))
	}

	// 2. Закрываем канал
	if err := c.ch.Close(); err != nil {
		c.logger.Error("Ошибка при закрытии канала RabbitMQ", zap.Error(err))
		// Не возвращаем ошибку здесь, т.к. соединение все равно нужно закрыть
	} else {
		c.logger.Info("Канал RabbitMQ успешно закрыт")
	}

	// 3. Сигнализируем об остановке (если еще не было ошибки)
	select {
	case c.done <- nil:
		c.logger.Info("Сигнал об успешной остановке отправлен.")
	default:
		c.logger.Info("Канал done уже закрыт или содержит ошибку.")
	}

	c.logger.Info("TokenDeletionConsumer остановлен.")
	// Не возвращаем ошибку от Cancel или Close, т.к. основная цель - остановить.
	// Ошибки уже залогированы.
	return nil
}

// getTokenPrefix возвращает начало токена для логирования (чтобы не логировать весь токен).
func getTokenPrefix(token string) string {
	prefixLen := 10
	if len(token) < prefixLen {
		return token
	}
	return token[:prefixLen] + "..."
}

// --- Убедимся, что Consumer реализует некий интерфейс Worker/Stopper ---
// Например:
// type StoppableWorker interface {
//     Start() error
//     Stop() error
// }
// var _ StoppableWorker = (*TokenDeletionConsumer)(nil) // Раскомментировать и адаптировать, если такой интерфейс есть
