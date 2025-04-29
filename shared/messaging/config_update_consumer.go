package messaging

import (
	"encoding/json"
	"fmt"
	"novel-server/shared/models"
	"time"

	"github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

type ConfigUpdater interface {
	Update(config models.DynamicConfig)
}

const (
	configUpdateExchange     = "config_update_exchange"
	configUpdateExchangeType = "fanout"
)

type ConfigUpdateConsumer struct {
	conn          *amqp091.Connection
	ch            *amqp091.Channel
	configUpdater ConfigUpdater
	logger        *zap.Logger
	exchangeName  string
	queueName     string
	done          chan error
	consumerTag   string
}

func NewConfigUpdateConsumer(
	conn *amqp091.Connection,
	configUpdater ConfigUpdater,
	logger *zap.Logger,
) (*ConfigUpdateConsumer, error) {
	if conn == nil {
		return nil, fmt.Errorf("RabbitMQ connection is nil")
	}
	if configUpdater == nil {
		return nil, fmt.Errorf("ConfigUpdater is nil")
	}

	consumerTag := fmt.Sprintf("config_update_consumer_%d", time.Now().UnixNano())
	queueName := "" // Имя будет установлено в setupChannelAndQueue

	consumer := &ConfigUpdateConsumer{
		conn:          conn,
		configUpdater: configUpdater,
		logger:        logger.Named("ConfigUpdateConsumer").With(zap.String("consumerTag", consumerTag)),
		exchangeName:  configUpdateExchange,
		queueName:     queueName,
		done:          make(chan error),
		consumerTag:   consumerTag,
	}

	if err := consumer.setupChannelAndQueue(); err != nil {
		return nil, err // Ошибка при настройке канала/очереди
	}
	// Теперь consumer.queueName содержит сгенерированное имя

	consumer.logger.Info("ConfigUpdateConsumer инициализирован", zap.String("exchange", consumer.exchangeName), zap.String("generatedQueueName", consumer.queueName))
	return consumer, nil
}

// setupChannelAndQueue создает канал, объявляет exchange, очередь и биндинг.
func (c *ConfigUpdateConsumer) setupChannelAndQueue() error {
	var err error
	c.ch, err = c.conn.Channel()
	if err != nil {
		return fmt.Errorf("failed to open channel: %w", err)
	}

	// Объявляем Exchange (fanout, durable)
	err = c.ch.ExchangeDeclare(
		c.exchangeName,
		configUpdateExchangeType,
		true,  // durable
		false, // auto-deleted
		false, // internal
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		_ = c.ch.Close()
		return fmt.Errorf("failed to declare exchange '%s': %w", c.exchangeName, err)
	}

	// Объявляем временную, эксклюзивную очередь. Брокер сам даст ей имя.
	// durable: false, exclusive: true, autoDelete: true
	q, err := c.ch.QueueDeclare(
		"",    // name (пустое для автогенерации)
		false, // durable
		true,  // delete when unused (auto-delete)
		true,  // exclusive (только это соединение)
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		_ = c.ch.Close()
		return fmt.Errorf("failed to declare queue: %w", err)
	}
	c.queueName = q.Name // Сохраняем сгенерированное имя
	c.logger.Info("Временная очередь для обновлений конфигурации создана", zap.String("queueName", c.queueName))

	// Биндим очередь к exchange
	err = c.ch.QueueBind(
		c.queueName,
		"", // routing key (не используется для fanout)
		c.exchangeName,
		false,
		nil,
	)
	if err != nil {
		_ = c.ch.Close()
		return fmt.Errorf("failed to bind queue '%s' to exchange '%s': %w", c.queueName, c.exchangeName, err)
	}

	return nil
}

// StartConsuming запускает процесс получения и обработки сообщений.
// Блокирует выполнение до тех пор, пока консьюмер не будет остановлен или не произойдет ошибка.
func (c *ConfigUpdateConsumer) StartConsuming() error {
	c.logger.Info("Начало прослушивания сообщений об обновлении конфигурации...")

	deliveries, err := c.ch.Consume(
		c.queueName,
		c.consumerTag,
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to register a consumer: %w", err)
	}

	go func() {
		for d := range deliveries {
			var configUpdate models.DynamicConfig
			err := json.Unmarshal(d.Body, &configUpdate)
			if err != nil {
				c.logger.Error("failed to unmarshal config update message", zap.Error(err))
				continue
			}

			c.configUpdater.Update(configUpdate)

			if err := d.Ack(false); err != nil {
				c.logger.Error("failed to acknowledge message", zap.Error(err))
			}
		}
	}()

	return nil
}

// handleDeliveries обрабатывает входящие сообщения из канала deliveries.
// ... (без изменений)

// handleMessage десериализует и обрабатывает одно сообщение.
// ... (без изменений)

// Stop останавливает консьюмера.
func (c *ConfigUpdateConsumer) Stop() {
	c.logger.Info("Остановка ConfigUpdateConsumer...")
	// Отменяем подписку
	// ... existing code ...
}
