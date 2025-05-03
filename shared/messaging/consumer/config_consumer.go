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

const (
	configUpdateExchange     = "config_update_exchange"
	configUpdateExchangeType = "fanout"
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
) *ConfigUpdateConsumer {
	return &ConfigUpdateConsumer{
		conn:      conn,
		configSvc: configSvc,
		logger:    logger.Named("ConfigUpdateConsumer"),
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

	// Объявляем Exchange (fanout, durable)
	err = ch.ExchangeDeclare(
		configUpdateExchange,
		configUpdateExchangeType,
		true,  // durable
		false, // auto-deleted
		false, // internal
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		c.cleanupChannel()
		return fmt.Errorf("ошибка объявления exchange '%s': %w", configUpdateExchange, err)
	}

	// Объявляем временную, эксклюзивную очередь (durable=false, exclusive=true, autoDelete=true)
	q, err := ch.QueueDeclare(
		"",    // name (пустое для автогенерации)
		false, // durable
		true,  // delete when unused (auto-delete)
		true,  // exclusive (только это соединение)
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		c.cleanupChannel()
		return fmt.Errorf("ошибка объявления временной очереди для config updates: %w", err)
	}
	c.queueName = q.Name // Сохраняем имя временной очереди
	c.logger.Info("Объявлена временная очередь для config updates", zap.String("queueName", c.queueName))

	// Биндим очередь к exchange
	err = ch.QueueBind(
		c.queueName,
		"", // routing key (не используется для fanout)
		configUpdateExchange,
		false,
		nil,
	)
	if err != nil {
		c.cleanupChannel()
		return fmt.Errorf("ошибка биндинга очереди '%s' к exchange '%s': %w", c.queueName, configUpdateExchange, err)
	}

	// Используем переданный контекст для возможности отмены извне
	localCtx, cancel := context.WithCancel(ctx)
	c.cancelFunc = cancel

	// Устанавливаем QoS для этого канала
	if err := ch.Qos(1, 0, false); err != nil {
		c.cleanupChannel()
		return fmt.Errorf("ошибка установки QoS для ConfigUpdateConsumer: %w", err)
	}

	// Уникальный consumer tag
	c.consumerTag = fmt.Sprintf("config-consumer-%d", time.Now().UnixNano())

	msgs, err := ch.Consume(
		c.queueName,
		c.consumerTag,
		false, // autoAck = false
		false, // exclusive
		false, // noLocal
		false, // noWait
		nil,   // args
	)
	if err != nil {
		c.cleanupChannel()
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
