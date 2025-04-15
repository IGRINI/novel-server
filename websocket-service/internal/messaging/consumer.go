package messaging

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog"

	"novel-server/websocket-service/internal/config"
	"novel-server/websocket-service/internal/handler"
)

// RabbitMQConsumer отвечает за получение сообщений из RabbitMQ и их обработку.
type RabbitMQConsumer struct {
	cfg         *config.RabbitMQConfig
	manager     *handler.ConnectionManager
	logger      zerolog.Logger
	conn        *amqp091.Connection
	channel     *amqp091.Channel
	stopChannel chan struct{}
	notifyClose chan *amqp091.Error // Канал для отслеживания закрытия соединения
	isConnected bool
}

// NewRabbitMQConsumer создает нового консьюмера RabbitMQ.
func NewRabbitMQConsumer(cfg *config.RabbitMQConfig, manager *handler.ConnectionManager, logger zerolog.Logger) (*RabbitMQConsumer, error) {
	c := &RabbitMQConsumer{
		cfg:         cfg,
		manager:     manager,
		logger:      logger.With().Str("component", "RabbitMQConsumer").Logger(),
		stopChannel: make(chan struct{}),
	}
	// Первоначальное подключение
	err := c.connect()
	if err != nil {
		c.logger.Error().Err(err).Msg("Initial connection to RabbitMQ failed")
		// Не возвращаем ошибку, консьюмер попытается переподключиться в StartConsuming
	}
	return c, nil
}

// connect устанавливает соединение и канал с RabbitMQ.
func (c *RabbitMQConsumer) connect() error {
	var err error
	c.conn, err = amqp091.Dial(c.cfg.URL)
	if err != nil {
		return fmt.Errorf("failed to dial RabbitMQ: %w", err)
	}

	c.channel, err = c.conn.Channel()
	if err != nil {
		c.conn.Close() // Закрываем соединение, если не удалось открыть канал
		return fmt.Errorf("failed to open RabbitMQ channel: %w", err)
	}

	// Обработка закрытия соединения
	c.notifyClose = make(chan *amqp091.Error)
	c.conn.NotifyClose(c.notifyClose)

	c.isConnected = true
	c.logger.Info().Msg("Successfully connected to RabbitMQ")
	return nil
}

// StartConsuming начинает прослушивание очереди уведомлений.
func (c *RabbitMQConsumer) StartConsuming() error {
	for {
		select {
		case <-c.stopChannel:
			c.logger.Info().Msg("Stopping consumer")
			return nil
		default:
			if !c.isConnected {
				c.logger.Info().Msg("Not connected, attempting to reconnect...")
				err := c.connect()
				if err != nil {
					c.logger.Error().Err(err).Msg("Reconnect failed, will retry...")
					time.Sleep(5 * time.Second) // Пауза перед следующей попыткой
					continue
				}
			}

			// Объявляем очередь (на случай, если она еще не создана)
			q, err := c.channel.QueueDeclare(
				c.cfg.QueueName,
				true,  // durable
				false, // delete when unused
				false, // exclusive
				false, // no-wait
				nil,   // arguments
			)
			if err != nil {
				c.logger.Error().Err(err).Str("queue", c.cfg.QueueName).Msg("Failed to declare queue, closing connection")
				c.closeConnection()
				continue
			}

			// Устанавливаем QoS
			err = c.channel.Qos(1, 0, false)
			if err != nil {
				c.logger.Error().Err(err).Msg("Failed to set QoS, closing connection")
				c.closeConnection()
				continue
			}

			msgs, err := c.channel.Consume(
				q.Name,
				"websocket-service-consumer", // consumer tag
				false,                        // auto-ack
				false,                        // exclusive
				false,                        // no-local
				false,                        // no-wait
				nil,                          // args
			)
			if err != nil {
				c.logger.Error().Err(err).Msg("Failed to register consumer, closing connection")
				c.closeConnection()
				continue
			}

			c.logger.Info().Str("queue", q.Name).Msg("Consumer started, waiting for messages...")

			// Цикл обработки сообщений и отслеживания закрытия
			for {
				select {
				case d, ok := <-msgs:
					if !ok {
						c.logger.Warn().Msg("Message channel closed by RabbitMQ, attempting reconnect")
						c.closeConnection()
						break // Выход из внутреннего цикла для переподключения
					}
					c.handleDelivery(d)

				case err := <-c.notifyClose:
					c.logger.Error().Err(err).Msg("RabbitMQ connection closed, attempting reconnect")
					c.closeConnection()
					break // Выход из внутреннего цикла для переподключения

				case <-c.stopChannel:
					c.logger.Info().Msg("Stopping consumer loop")
					return nil
				}
				// Если вышли из select из-за break, выходим и из for
				if !c.isConnected {
					break
				}
			}
		}
	}
}

// handleDelivery обрабатывает одно сообщение из RabbitMQ.
func (c *RabbitMQConsumer) handleDelivery(d amqp091.Delivery) {
	c.logger.Debug().Uint64("deliveryTag", d.DeliveryTag).Int("bodySize", len(d.Body)).Msg("Received notification")

	var clientUpdate map[string]interface{}
	err := json.Unmarshal(d.Body, &clientUpdate)
	if err != nil {
		c.logger.Error().Err(err).Uint64("deliveryTag", d.DeliveryTag).Msg("Failed to unmarshal message body. Nacking.")
		_ = d.Nack(false, false) // Не переотправлять
		return
	}

	userIDRaw, ok := clientUpdate["user_id"]
	if !ok {
		c.logger.Warn().Uint64("deliveryTag", d.DeliveryTag).Msg("'user_id' field not found. Nacking.")
		_ = d.Nack(false, false)
		return
	}

	userIDStr, ok := userIDRaw.(string)
	if !ok {
		if userIDFloat, okFloat := userIDRaw.(float64); okFloat {
			userIDStr = fmt.Sprintf("%.0f", userIDFloat)
			ok = true
		} else {
			c.logger.Warn().Uint64("deliveryTag", d.DeliveryTag).Interface("userIdType", fmt.Sprintf("%T", userIDRaw)).Msg("'user_id' field has incorrect type. Nacking.")
			_ = d.Nack(false, false)
			return
		}
	}

	sent := c.manager.SendToUser(userIDStr, d.Body)
	if sent {
		c.logger.Info().Str("userID", userIDStr).Uint64("deliveryTag", d.DeliveryTag).Msg("Notification successfully sent to user")
		_ = d.Ack(false) // Подтверждаем
	} else {
		c.logger.Warn().Str("userID", userIDStr).Uint64("deliveryTag", d.DeliveryTag).Msg("Failed to send notification (offline or error). Nacking.")
		_ = d.Nack(false, false) // Не переотправлять, пользователь оффлайн
	}
}

// Stop останавливает консьюмер.
func (c *RabbitMQConsumer) StopConsuming() error {
	c.logger.Info().Msg("Stopping RabbitMQ consumer...")
	close(c.stopChannel)
	return c.closeConnection()
}

// closeConnection закрывает соединение и канал.
func (c *RabbitMQConsumer) closeConnection() error {
	var firstErr error
	c.isConnected = false
	if c.channel != nil {
		if err := c.channel.Close(); err != nil {
			c.logger.Error().Err(err).Msg("Failed to close RabbitMQ channel")
			if firstErr == nil {
				firstErr = err
			}
		}
		c.channel = nil
	}
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			c.logger.Error().Err(err).Msg("Failed to close RabbitMQ connection")
			if firstErr == nil {
				firstErr = err
			}
		}
		c.conn = nil
	}
	c.logger.Info().Msg("RabbitMQ connection closed")
	return firstErr
}
