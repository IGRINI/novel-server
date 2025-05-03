package consumer // Changed package name

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"novel-server/shared/configservice" // Updated import path
	"novel-server/shared/messaging"     // Updated import path
	"novel-server/shared/models"        // Updated import path
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

// ConfigUpdateConsumer слушает сообщения об обновлении конфигурации.
type ConfigUpdateConsumer struct {
	conn        *amqp.Connection
	configSvc   *configservice.ConfigService // Use shared ConfigService type
	logger      *zap.Logger
	queueName   string
	channel     *amqp.Channel
	consumerTag string
	mu          sync.Mutex
	cancelFunc  context.CancelFunc // Для остановки горутины
	stopChan    chan struct{}      // Сигнал о завершении горутины
}

// NewConfigUpdateConsumer создает новый экземпляр ConfigUpdateConsumer.
func NewConfigUpdateConsumer(
	conn *amqp.Connection,
	configSvc *configservice.ConfigService, // Use shared ConfigService type
	logger *zap.Logger,
	queueName string,
) *ConfigUpdateConsumer {
	return &ConfigUpdateConsumer{
		conn:      conn,
		configSvc: configSvc,
		logger:    logger.Named("ConfigUpdateConsumer"),
		queueName: queueName,
		stopChan:  make(chan struct{}),
	}
}

// Start запускает консьюмера в отдельной горутине.
func (c *ConfigUpdateConsumer) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.channel != nil {
		return errors.New("ConfigUpdateConsumer уже запущен")
	}

	ch, err := c.conn.Channel()
	if err != nil {
		return fmt.Errorf("ошибка открытия канала для ConfigUpdateConsumer: %w", err)
	}
	c.channel = ch

	// Используем переданный контекст для возможности отмены извне
	localCtx, cancel := context.WithCancel(ctx)
	c.cancelFunc = cancel

	// Объявляем очередь (на случай, если она еще не создана)
	// Это не обязательно, если main.go уже ее объявляет, но делает консьюмера более самодостаточным.
	_, err = ch.QueueDeclare(
		c.queueName, // name
		true,        // durable
		false,       // delete when unused
		false,       // exclusive
		false,       // no-wait
		// nil,      // arguments (здесь не нужны специфичные аргументы типа DLX)
		amqp.Table{"x-queue-mode": "lazy"}, // Добавляем lazy mode
	)
	if err != nil {
		ch.Close() // Закрываем канал при ошибке
		c.channel = nil
		return fmt.Errorf("ошибка объявления очереди '%s': %w", c.queueName, err)
	}

	// Устанавливаем QoS для этого канала
	if err := ch.Qos(1, 0, false); err != nil {
		ch.Close()
		c.channel = nil
		return fmt.Errorf("ошибка установки QoS для ConfigUpdateConsumer: %w", err)
	}

	// Уникальный consumer tag
	c.consumerTag = fmt.Sprintf("config-consumer-%d", time.Now().UnixNano())

	msgs, err := ch.Consume(
		c.queueName,
		c.consumerTag,
		false, // autoAck = false
		false, // exclusive
		false, // noLocal - не поддерживается RabbitMQ
		false, // noWait
		nil,   // args
	)
	if err != nil {
		ch.Close()
		c.channel = nil
		return fmt.Errorf("ошибка регистрации консьюмера '%s': %w", c.queueName, err)
	}

	c.logger.Info("ConfigUpdateConsumer запущен, ожидание сообщений", zap.String("queue", c.queueName))

	// Запускаем горутину для обработки сообщений
	go func() {
		defer close(c.stopChan)  // Сигнализируем о завершении при выходе
		defer c.cleanupChannel() // Гарантируем закрытие канала
		for {
			select {
			case <-localCtx.Done(): // Если контекст отменен снаружи
				c.logger.Info("Контекст отменен, ConfigUpdateConsumer останавливается...")
				return
			case msg, ok := <-msgs:
				if !ok {
					c.logger.Warn("Канал сообщений RabbitMQ закрыт, ConfigUpdateConsumer останавливается.")
					return // Канал закрыт, выходим
				}
				c.handleMessage(msg)
			}
		}
	}()

	return nil
}

// handleMessage обрабатывает одно сообщение.
func (c *ConfigUpdateConsumer) handleMessage(msg amqp.Delivery) {
	c.logger.Debug("Получено сообщение об обновлении конфига", zap.Uint64("deliveryTag", msg.DeliveryTag))

	var payload messaging.ConfigUpdatePayload // Use shared messaging payload
	if err := json.Unmarshal(msg.Body, &payload); err != nil {
		c.logger.Error("Ошибка десериализации ConfigUpdatePayload",
			zap.Error(err),
			zap.Uint64("deliveryTag", msg.DeliveryTag),
			zap.ByteString("body", msg.Body),
		)
		// Отклоняем сообщение (не ставим в requeue, т.к. оно битое)
		if nackErr := msg.Nack(false, false); nackErr != nil {
			c.logger.Error("Ошибка при Nack сообщения с ошибкой десериализации", zap.Error(nackErr), zap.Uint64("deliveryTag", msg.DeliveryTag))
		}
		return
	}

	c.logger.Info("Обработка обновления конфигурации",
		zap.String("key", payload.Key),
		zap.String("new_value", payload.Value),
		zap.Uint64("deliveryTag", msg.DeliveryTag),
	)

	// Обновляем кэш в ConfigService
	configToUpdate := models.DynamicConfig{ // Use shared models type
		Key:   payload.Key,
		Value: payload.Value,
	}
	c.configSvc.Update(configToUpdate)

	// Подтверждаем сообщение
	if ackErr := msg.Ack(false); ackErr != nil {
		c.logger.Error("Ошибка при Ack сообщения об обновлении конфига", zap.Error(ackErr), zap.Uint64("deliveryTag", msg.DeliveryTag))
	}
}

// Stop останавливает консьюмера.
func (c *ConfigUpdateConsumer) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.channel == nil {
		return errors.New("ConfigUpdateConsumer не запущен")
	}

	c.logger.Info("Остановка ConfigUpdateConsumer...")

	// Отменяем контекст, чтобы завершить горутину обработки
	if c.cancelFunc != nil {
		c.cancelFunc()
	}

	// Ждем завершения горутины (с таймаутом)
	select {
	case <-c.stopChan:
		c.logger.Info("Горутина ConfigUpdateConsumer успешно завершена.")
	case <-time.After(5 * time.Second):
		c.logger.Warn("Таймаут ожидания завершения горутины ConfigUpdateConsumer.")
	}

	// Отмена подписки не нужна, т.к. горутина завершилась сама из-за контекста
	// и cleanupChannel() уже должен был быть вызван.

	c.channel = nil // Сбрасываем канал
	c.cancelFunc = nil
	c.stopChan = make(chan struct{}) // Пересоздаем канал для возможного перезапуска

	c.logger.Info("ConfigUpdateConsumer остановлен.")
	return nil
}

// cleanupChannel закрывает канал RabbitMQ.
func (c *ConfigUpdateConsumer) cleanupChannel() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.channel != nil {
		c.logger.Debug("Закрытие канала ConfigUpdateConsumer...")
		if err := c.channel.Close(); err != nil {
			c.logger.Error("Ошибка закрытия канала ConfigUpdateConsumer", zap.Error(err))
		}
		c.channel = nil
	}
}
