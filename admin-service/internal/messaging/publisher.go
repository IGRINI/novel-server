package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"novel-server/shared/constants"
	"novel-server/shared/interfaces"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"

	sharedModels "novel-server/shared/models"
)

// PushNotificationPayload - структура сообщения для отправки push-уведомления.
// Скопировано из notification-service/gameplay-service. В идеале - общий пакет.
type PushNotificationPayload struct {
	UserID       uuid.UUID         `json:"user_id"`
	Notification PushNotification  `json:"notification"`
	Data         map[string]string `json:"data,omitempty"`
}

// PushNotification содержит видимые части push-сообщения.
type PushNotification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// --- Реализация для RabbitMQ ---
type rabbitMQPushPublisher struct {
	conn      *amqp.Connection
	channel   *amqp.Channel
	queueName string
	logger    *zap.Logger
}

// NewRabbitMQPushPublisher создает новый экземпляр паблишера.
func NewRabbitMQPushPublisher(conn *amqp.Connection, queueName string, logger *zap.Logger) (*rabbitMQPushPublisher, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("push publisher: не удалось открыть канал: %w", err)
	}

	// Объявляем очередь здесь, чтобы убедиться, что она существует.
	// Параметры должны совпадать с консьюмером в notification-service (durable=true).
	_, err = ch.QueueDeclare(
		queueName,
		true,  // durable
		false, // delete when unused
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		ch.Close() // Закрываем канал, если очередь не удалось объявить
		return nil, fmt.Errorf("push publisher: не удалось объявить очередь '%s': %w", queueName, err)
	}

	logger.Info("RabbitMQPushPublisher инициализирован", zap.String("queue", queueName))
	return &rabbitMQPushPublisher{
		conn:      conn,
		channel:   ch,
		queueName: queueName,
		logger:    logger.Named("push_publisher"),
	}, nil
}

func (p *rabbitMQPushPublisher) PublishPushNotification(ctx context.Context, payload sharedModels.PushNotificationPayload) error {
	if p.channel == nil {
		p.logger.Error("Канал RabbitMQ не инициализирован (nil)")
		return errors.New("канал RabbitMQ не инициализирован")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		p.logger.Error("Ошибка маршалинга PushNotificationPayload",
			zap.String("user_id", payload.UserID.String()),
			zap.Error(err))
		return fmt.Errorf("ошибка подготовки сообщения PushNotification: %w", err)
	}

	// Устанавливаем таймаут на публикацию
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	err = p.channel.PublishWithContext(ctx,
		"",          // exchange (используем default)
		p.queueName, // routing key (имя очереди)
		false,       // mandatory
		false,       // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent, // Сообщение должно быть persistent
			Body:         body,
			Timestamp:    time.Now(),
			AppId:        "admin-service", // Идентификатор отправителя
		},
	)
	if err != nil {
		p.logger.Error("Ошибка публикации сообщения в очередь",
			zap.String("queue", p.queueName),
			zap.String("user_id", payload.UserID.String()),
			zap.Error(err))
		return fmt.Errorf("ошибка публикации в очередь %s: %w", p.queueName, err)
	}

	p.logger.Info("Сообщение успешно опубликовано в очередь",
		zap.String("queue", p.queueName),
		zap.String("user_id", payload.UserID.String()),
	)
	return nil
}

// PublishPushEvent реализует интерфейс interfaces.PushEventPublisher.
// Он преобразует событие и вызывает существующий метод PublishPushNotification.
func (p *rabbitMQPushPublisher) PublishPushEvent(ctx context.Context, event interfaces.PushNotificationEvent) error {
	p.logger.Info("Received PushNotificationEvent, converting and publishing...",
		zap.String("userID", event.UserID),
		zap.Any("data", event.Data),
	)

	// Преобразуем UserID string в uuid.UUID. Если не удается, логируем ошибку.
	userID, err := uuid.Parse(event.UserID)
	if err != nil {
		p.logger.Error("Failed to parse UserID string to UUID in PublishPushEvent",
			zap.String("rawUserID", event.UserID),
			zap.Error(err),
		)
		// Возвращаем ошибку, т.к. без валидного UUID не можем создать payload
		return fmt.Errorf("invalid user ID format '%s': %w", event.UserID, err)
	}

	// Извлекаем fallback title/body из Data
	fallbackTitle := "Notification"
	fallbackBody := "You have a new notification."
	if title, ok := event.Data[constants.PushFallbackTitleKey]; ok {
		fallbackTitle = title
		// Удаляем из Data, чтобы не дублировать?
		// delete(event.Data, constants.PushFallbackTitleKey)
	}
	if body, ok := event.Data[constants.PushFallbackBodyKey]; ok {
		fallbackBody = body
		// Удаляем из Data, чтобы не дублировать?
		// delete(event.Data, constants.PushFallbackBodyKey)
	}

	// Создаем payload для старого метода, используя fallback значения
	payload := sharedModels.PushNotificationPayload{
		UserID: userID,
		Notification: sharedModels.PushNotification{
			Title: fallbackTitle,
			Body:  fallbackBody,
		},
		Data: event.Data,
	}

	// Вызываем существующий метод публикации
	return p.PublishPushNotification(ctx, payload)
}

// Close закрывает канал RabbitMQ.
func (p *rabbitMQPushPublisher) Close() error {
	if p.channel != nil {
		p.logger.Info("Закрытие канала RabbitMQ паблишера...")
		return p.channel.Close()
	}
	return nil
}
