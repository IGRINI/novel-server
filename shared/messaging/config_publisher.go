package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

// RabbitMQConfigUpdatePublisher реализует интерфейс Publisher для отправки обновлений конфигурации.
// Используем общий интерфейс Publisher из payloads.go
type RabbitMQConfigUpdatePublisher struct {
	conn         *amqp091.Connection
	ch           *amqp091.Channel
	logger       *zap.Logger
	exchangeName string
}

// NewRabbitMQConfigUpdatePublisher создает нового издателя для обновлений конфигурации.
func NewRabbitMQConfigUpdatePublisher(conn *amqp091.Connection, logger *zap.Logger) (*RabbitMQConfigUpdatePublisher, error) {
	if conn == nil {
		return nil, fmt.Errorf("rabbitmq connection is nil")
	}

	ch, err := conn.Channel()
	if err != nil {
		logger.Error("Failed to open a channel for config updates", zap.Error(err))
		return nil, fmt.Errorf("failed to open a channel: %w", err)
	}

	// Используем имя и тип exchange, определенные в консьюмере (или вынесенные в общие константы)
	exchangeName := configUpdateExchange
	exchangeType := configUpdateExchangeType

	// Объявляем fanout exchange. Если он уже существует, ничего не произойдет.
	// Делаем его durable.
	err = ch.ExchangeDeclare(
		exchangeName,
		exchangeType,
		true,  // durable
		false, // auto-deleted
		false, // internal
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		_ = ch.Close() // Попытаемся закрыть канал
		logger.Error("Failed to declare config update exchange", zap.String("exchange", exchangeName), zap.Error(err))
		return nil, fmt.Errorf("failed to declare exchange '%s': %w", exchangeName, err)
	}

	logger.Info("Config update exchange declared successfully", zap.String("exchange", exchangeName), zap.String("type", exchangeType))

	return &RabbitMQConfigUpdatePublisher{
		conn:         conn,
		ch:           ch,
		logger:       logger.Named("ConfigUpdatePublisher"),
		exchangeName: exchangeName,
	}, nil
}

// Publish публикует сообщение об обновлении конфигурации.
// payload должен быть типа ConfigUpdatePayload.
// correlationID не используется для fanout.
func (p *RabbitMQConfigUpdatePublisher) Publish(ctx context.Context, payload interface{}, correlationID string) error {
	// Проверяем тип payload
	configPayload, ok := payload.(ConfigUpdatePayload)
	if !ok {
		err := fmt.Errorf("invalid payload type for config update: expected ConfigUpdatePayload, got %T", payload)
		p.logger.Error("Invalid payload type", zap.Error(err))
		return err
	}

	body, err := json.Marshal(configPayload)
	if err != nil {
		p.logger.Error("Failed to marshal config update payload", zap.Error(err), zap.Any("payload", configPayload))
		return fmt.Errorf("failed to marshal config update payload: %w", err)
	}

	err = p.ch.PublishWithContext(ctx,
		p.exchangeName, // exchange
		"",             // routing key (не используется для fanout)
		false,          // mandatory
		false,          // immediate
		amqp091.Publishing{
			ContentType: "application/json",
			Body:        body,
			Timestamp:   time.Now(),
			// MessageId можно добавить для трассировки, если нужно
		},
	)

	if err != nil {
		p.logger.Error("Failed to publish config update event", zap.Error(err), zap.Any("payload", configPayload))
		// Здесь может быть логика retry или обработки ошибок канала/соединения
		return fmt.Errorf("failed to publish config update event: %w", err)
	}

	p.logger.Debug("Config update event published", zap.Any("payload", configPayload))
	return nil
}

// Close закрывает канал RabbitMQ.
func (p *RabbitMQConfigUpdatePublisher) Close() error {
	if p.ch != nil {
		return p.ch.Close()
	}
	return nil
}
