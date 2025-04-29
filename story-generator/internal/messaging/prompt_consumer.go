package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"

	"novel-server/shared/interfaces"
	"novel-server/shared/messaging"
	"novel-server/story-generator/internal/service"
)

// PromptConsumer handles receiving prompt update events from RabbitMQ.
type PromptConsumer struct {
	conn           *amqp091.Connection
	promptProvider *service.PromptProvider
	logger         *zap.Logger
	done           chan struct{}    // Канал для сигнализации о завершении
	channel        *amqp091.Channel // Канал для управления подпиской
}

// PromptUpdater defines the interface for updating the prompt cache.
// (Помещен сюда временно, лучше вынести в shared/interfaces или в prompt_provider.go)

// NewPromptConsumer creates a new PromptConsumer.
func NewPromptConsumer(conn *amqp091.Connection, provider *service.PromptProvider, logger *zap.Logger) *PromptConsumer {
	if logger == nil {
		// Обработка nil логгера
		panic("Logger is nil for PromptConsumer") // Или используем глобальный, но лучше паниковать
	}
	return &PromptConsumer{
		conn:           conn,
		promptProvider: provider,
		logger:         logger.Named("PromptConsumer"),
		done:           make(chan struct{}),
	}
}

// Start begins consuming messages from the prompt update queue.
func (c *PromptConsumer) Start(ctx context.Context) error {
	var err error
	c.channel, err = c.conn.Channel()
	if err != nil {
		c.logger.Error("Failed to open channel for prompt consumer", zap.Error(err))
		return fmt.Errorf("failed to open channel: %w", err)
	}

	// Объявляем fanout exchange (на всякий случай, если его еще нет)
	err = c.channel.ExchangeDeclare(
		messaging.ExchangePromptUpdates, // name from shared/messaging
		"fanout",                        // type
		true,                            // durable
		false,                           // auto-deleted
		false,                           // internal
		false,                           // no-wait
		nil,                             // arguments
	)
	if err != nil {
		_ = c.channel.Close()
		c.logger.Error("Failed to declare prompt update exchange", zap.Error(err), zap.String("exchange", messaging.ExchangePromptUpdates))
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	// Объявляем временную, эксклюзивную очередь для получения broadcast сообщений
	// Имя будет сгенерировано RabbitMQ
	q, err := c.channel.QueueDeclare(
		"",    // name (empty for auto-generated)
		false, // durable (non-durable for temporary queue)
		true,  // delete when unused (auto-delete)
		true,  // exclusive (only this consumer can use it)
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		_ = c.channel.Close()
		c.logger.Error("Failed to declare exclusive queue for prompt updates", zap.Error(err))
		return fmt.Errorf("failed to declare queue: %w", err)
	}
	c.logger.Info("Declared exclusive queue for prompt updates", zap.String("queueName", q.Name))

	// Привязываем очередь к exchange
	err = c.channel.QueueBind(
		q.Name,                          // queue name
		"",                              // routing key (not used for fanout)
		messaging.ExchangePromptUpdates, // exchange
		false,
		nil,
	)
	if err != nil {
		_ = c.channel.Close()
		c.logger.Error("Failed to bind queue to exchange", zap.Error(err), zap.String("queue", q.Name), zap.String("exchange", messaging.ExchangePromptUpdates))
		return fmt.Errorf("failed to bind queue: %w", err)
	}
	c.logger.Info("Successfully bound queue to prompt update exchange", zap.String("queueName", q.Name), zap.String("exchange", messaging.ExchangePromptUpdates))

	// Начинаем потреблять сообщения
	msgs, err := c.channel.Consume(
		q.Name, // queue
		"",     // consumer (empty for auto-generated)
		true,   // auto-ack (просто обновляем кеш, потеря сообщения не критична)
		true,   // exclusive
		false,  // no-local (not applicable here)
		false,  // no-wait
		nil,    // args
	)
	if err != nil {
		_ = c.channel.Close()
		c.logger.Error("Failed to register prompt consumer", zap.Error(err), zap.String("queue", q.Name))
		return fmt.Errorf("failed to register consumer: %w", err)
	}

	c.logger.Info("Prompt consumer started, waiting for events...")

	// Горутина для обработки входящих сообщений
	go func() {
		defer func() {
			if r := recover(); r != nil {
				c.logger.Error("Panic recovered in prompt consumer goroutine", zap.Any("panic", r))
			}
			c.logger.Info("Prompt consumer goroutine stopping...")
			close(c.done)
			if c.channel != nil {
				_ = c.channel.Close()
			}
		}()

		for {
			select {
			case msg, ok := <-msgs:
				if !ok {
					c.logger.Info("Prompt consumer channel closed, exiting goroutine.")
					return
				}
				c.handleMessage(msg)
			case <-ctx.Done():
				c.logger.Info("Context cancelled, stopping prompt consumer goroutine.")
				return
			}
		}
	}()

	return nil
}

// handleMessage processes a single message from RabbitMQ.
func (c *PromptConsumer) handleMessage(msg amqp091.Delivery) {
	var event interfaces.PromptEvent
	err := json.Unmarshal(msg.Body, &event)
	if err != nil {
		c.logger.Error("Failed to unmarshal prompt event message", zap.Error(err), zap.String("messageBody", string(msg.Body)))
		return
	}

	c.logger.Debug("Received prompt update event", zap.Any("event", event))
	c.promptProvider.UpdateCache(event)
}

// Stop gracefully stops the consumer.
func (c *PromptConsumer) Stop() error {
	c.logger.Info("Stopping prompt consumer...")
	if c.channel != nil {
		err := c.channel.Cancel("", false)
		if err != nil {
			c.logger.Error("Error cancelling prompt consumer channel", zap.Error(err))
		}
	}

	select {
	case <-c.done:
		c.logger.Info("Prompt consumer goroutine finished.")
	case <-time.After(5 * time.Second):
		c.logger.Warn("Timeout waiting for prompt consumer goroutine to stop.")
	}

	if c.channel != nil {
		if err := c.channel.Close(); err != nil {
			c.logger.Warn("Error closing prompt consumer channel during stop", zap.Error(err))
		}
	}
	c.logger.Info("Prompt consumer stopped.")
	return nil
}
