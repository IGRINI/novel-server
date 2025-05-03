package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"novel-server/shared/interfaces"
	"time"

	"github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog/log"
)

const (
	// ExchangePromptUpdates - Имя exchange для обновлений промптов.
	ExchangePromptUpdates = "prompt_updates"
)

// RabbitMQPromptPublisher реализует интерфейс PromptEventPublisher для RabbitMQ.
type RabbitMQPromptPublisher struct {
	conn *amqp091.Connection
	ch   *amqp091.Channel
}

// NewRabbitMQPromptPublisher создает нового издателя событий промптов.
// Важно: предполагается, что соединение conn уже установлено и обработка ошибок/переподключений
// управляется внешним кодом, который передает сюда стабильное соединение.
func NewRabbitMQPromptPublisher(conn *amqp091.Connection) (*RabbitMQPromptPublisher, error) {
	if conn == nil {
		return nil, fmt.Errorf("rabbitmq connection is nil")
	}

	ch, err := conn.Channel()
	if err != nil {
		log.Error().Err(err).Msg("Failed to open a channel")
		return nil, fmt.Errorf("failed to open a channel: %w", err)
	}

	// Объявляем fanout exchange. Если он уже существует, ничего не произойдет.
	// Делаем его durable, чтобы он пережил перезапуск брокера.
	err = ch.ExchangeDeclare(
		ExchangePromptUpdates, // name
		"fanout",              // type
		true,                  // durable
		false,                 // auto-deleted
		false,                 // internal
		false,                 // no-wait
		nil,                   // arguments
	)
	if err != nil {
		_ = ch.Close() // Попытаемся закрыть канал
		log.Error().Err(err).Str("exchange", ExchangePromptUpdates).Msg("Failed to declare exchange")
		return nil, fmt.Errorf("failed to declare exchange '%s': %w", ExchangePromptUpdates, err)
	}

	log.Info().Str("exchange", ExchangePromptUpdates).Msg("Prompt update exchange declared successfully")

	return &RabbitMQPromptPublisher{conn: conn, ch: ch}, nil
}

// PublishPromptEvent публикует событие изменения промпта в RabbitMQ.
func (p *RabbitMQPromptPublisher) PublishPromptEvent(ctx context.Context, event interfaces.PromptEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		log.Error().Err(err).Interface("event", event).Msg("Failed to marshal prompt event")
		return fmt.Errorf("failed to marshal prompt event: %w", err)
	}

	err = p.ch.PublishWithContext(ctx,
		ExchangePromptUpdates, // exchange
		"",                    // routing key (не используется для fanout)
		false,                 // mandatory
		false,                 // immediate
		amqp091.Publishing{
			ContentType: "application/json",
			Body:        body,
			Timestamp:   time.Now(), // Добавляем временную метку
			// Можно добавить MessageId, если нужно для дедупликации или трассировки
			// MessageId: uuid.NewString(),
		},
	)

	if err != nil {
		log.Error().Err(err).Interface("event", event).Msg("Failed to publish prompt event")
		// Здесь может быть логика retry или обработки ошибок канала/соединения
		return fmt.Errorf("failed to publish prompt event: %w", err)
	}

	log.Debug().Interface("event", event).Msg("Prompt event published")
	return nil
}

// Close закрывает канал RabbitMQ.
func (p *RabbitMQPromptPublisher) Close() error {
	if p.ch != nil {
		return p.ch.Close()
	}
	return nil
}
