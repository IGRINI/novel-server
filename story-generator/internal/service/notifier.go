package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"novel-server/shared/messaging"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Notifier определяет интерфейс для отправки уведомлений о завершении задачи.
type Notifier interface {
	// Notify отправляет уведомление в соответствующую очередь.
	Notify(ctx context.Context, payload messaging.NotificationPayload) error
}

const (
	// Имя очереди для уведомлений пользователям
	notificationQueueName = "user_notifications"
)

// rabbitMQNotifier реализует Notifier для отправки сообщений в RabbitMQ.
type rabbitMQNotifier struct {
	channel *amqp.Channel
}

// NewRabbitMQNotifier создает новый экземпляр Notifier для RabbitMQ.
// Важно: предполагается, что канал уже открыт и будет закрыт в другом месте (например, в main.go).
func NewRabbitMQNotifier(ch *amqp.Channel) (Notifier, error) {
	// Объявляем очередь уведомлений (делаем ее durable)
	_, err := ch.QueueDeclare(
		notificationQueueName, // name
		true,                  // durable
		false,                 // delete when unused
		false,                 // exclusive
		false,                 // no-wait
		nil,                   // arguments
	)
	if err != nil {
		return nil, fmt.Errorf("не удалось объявить очередь уведомлений '%s': %w", notificationQueueName, err)
	}
	log.Printf("Очередь уведомлений '%s' успешно объявлена/найдена", notificationQueueName)

	return &rabbitMQNotifier{channel: ch}, nil
}

// Notify публикует уведомление в очередь RabbitMQ.
func (n *rabbitMQNotifier) Notify(ctx context.Context, payload messaging.NotificationPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[TaskID: %s] Ошибка сериализации NotificationPayload: %v", payload.TaskID, err)
		return fmt.Errorf("ошибка сериализации уведомления для TaskID %s: %w", payload.TaskID, err)
	}

	err = n.channel.PublishWithContext(ctx,
		"",                    // exchange (используем default)
		notificationQueueName, // routing key (имя очереди)
		false,                 // mandatory
		false,                 // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent, // Делаем сообщение persistent
			Body:         body,
			Timestamp:    time.Now(),                // Добавляем временную метку
			AppId:        "story-generator",         // Идентификатор отправителя
			MessageId:    payload.TaskID + "-notif", // Уникальный ID сообщения (опционально)
		},
	)

	if err != nil {
		log.Printf("[TaskID: %s] Ошибка публикации уведомления в RabbitMQ: %v", payload.TaskID, err)
		return fmt.Errorf("ошибка публикации уведомления для TaskID %s: %w", payload.TaskID, err)
	}

	log.Printf("[TaskID: %s] Уведомление успешно отправлено в очередь '%s'. Status: %s", payload.TaskID, notificationQueueName, payload.Status)
	return nil
}
