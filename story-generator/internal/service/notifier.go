package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"novel-server/shared/messaging"
	"novel-server/story-generator/internal/config"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Notifier определяет интерфейс для отправки уведомлений о завершении задачи.
type Notifier interface {
	// Notify отправляет уведомление в соответствующую очередь.
	Notify(ctx context.Context, payload messaging.NotificationPayload) error
}

// rabbitMQNotifier реализует Notifier для отправки сообщений в RabbitMQ.
type rabbitMQNotifier struct {
	channel   *amqp.Channel
	queueName string
}

// NewRabbitMQNotifier создает новый экземпляр Notifier для RabbitMQ.
// Важно: предполагается, что канал уже открыт и будет закрыт в другом месте (например, в main.go).
func NewRabbitMQNotifier(ch *amqp.Channel, cfg *config.Config) (Notifier, error) {
	queueName := cfg.InternalUpdatesQueueName

	// Объявляем очередь уведомлений (делаем ее durable)
	_, err := ch.QueueDeclare(
		queueName,
		true,
		false,
		false,
		false,
		amqp.Table{"x-queue-mode": "lazy"},
	)
	if err != nil {
		return nil, fmt.Errorf("не удалось объявить очередь уведомлений '%s': %w", queueName, err)
	}
	log.Printf("Очередь уведомлений '%s' успешно объявлена/найдена", queueName)

	return &rabbitMQNotifier{channel: ch, queueName: queueName}, nil
}

// Notify публикует уведомление в очередь RabbitMQ.
func (n *rabbitMQNotifier) Notify(ctx context.Context, payload messaging.NotificationPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[TaskID: %s] Ошибка сериализации NotificationPayload: %v", payload.TaskID, err)
		return fmt.Errorf("ошибка сериализации уведомления для TaskID %s: %w", payload.TaskID, err)
	}

	err = n.channel.PublishWithContext(ctx,
		"",
		n.queueName,
		false,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
			Timestamp:    time.Now(),
			AppId:        "story-generator",
			MessageId:    payload.TaskID + "-notif",
		},
	)

	if err != nil {
		log.Printf("[TaskID: %s] Ошибка публикации уведомления в RabbitMQ: %v", payload.TaskID, err)
		return fmt.Errorf("ошибка публикации уведомления для TaskID %s: %w", payload.TaskID, err)
	}

	log.Printf("[TaskID: %s] Уведомление успешно отправлено в очередь '%s'. Status: %s", payload.TaskID, n.queueName, payload.Status)
	return nil
}
