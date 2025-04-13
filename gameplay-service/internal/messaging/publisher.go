package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	sharedMessaging "novel-server/shared/messaging" // Используем общие структуры
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// TaskPublisher defines the interface for publishing tasks to the generation queue.
type TaskPublisher interface {
	PublishGenerationTask(ctx context.Context, payload sharedMessaging.GenerationTaskPayload) error
	PublishGameOverTask(ctx context.Context, payload sharedMessaging.GameOverTaskPayload) error
	// Close() error // Optional: If the publisher needs explicit closing
}

// ClientUpdatePublisher defines the interface for publishing updates to the client.
type ClientUpdatePublisher interface {
	PublishClientUpdate(ctx context.Context, payload ClientStoryUpdate) error // Используем новую структуру
}

// Определяем структуру для отфильтрованного сообщения клиенту
// (Пока поля примерные, нужно уточнить точный набор)
type ClientStoryUpdate struct {
	ID                string   `json:"id"`
	UserID            string   `json:"user_id"` // UserID нужен для websocket-service
	Status            string   `json:"status"`
	Title             string   `json:"title,omitempty"`
	Description       string   `json:"description,omitempty"`
	Themes            []string `json:"themes,omitempty"`             // Из pp.th
	WorldLore         []string `json:"world_lore,omitempty"`         // Из pp.wl
	PlayerDescription string   `json:"player_description,omitempty"` // Из p_desc
	IsCompleted       bool     `json:"is_completed"`                 // Флаг завершения истории
	EndingText        *string  `json:"ending_text,omitempty"`        // Текст концовки, если status == completed
	ErrorDetails      *string  `json:"error_details,omitempty"`      // Если status == error
}

// rabbitMQPublisher implements the TaskPublisher and ClientUpdatePublisher interfaces for RabbitMQ.
type rabbitMQPublisher struct {
	channel   *amqp.Channel
	queueName string
}

// NewRabbitMQPublisher creates a new instance of the publisher for the specified queue.
// Important: assumes the channel is already open.
func NewRabbitMQPublisher(ch *amqp.Channel, queueName string) *rabbitMQPublisher {
	// Мы не объявляем очередь здесь, предполагаем, что она уже существует
	// или будет объявлена консьюмером.
	return &rabbitMQPublisher{channel: ch, queueName: queueName}
}

// NewRabbitMQTaskPublisher creates a new instance of TaskPublisher.
// Note: This function may be redundant if NewRabbitMQPublisher is universal.
// Leaving it for possible future specialization.
func NewRabbitMQTaskPublisher(conn *amqp.Connection, queueName string) (TaskPublisher, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("task publisher: не удалось открыть канал: %w", err)
	}
	// Объявляем очередь здесь для TaskPublisher, т.к. он может быть первым
	_, err = ch.QueueDeclare(queueName, true, false, false, false, nil)
	if err != nil {
		ch.Close() // Закрываем канал при ошибке
		return nil, fmt.Errorf("task publisher: не удалось объявить очередь '%s': %w", queueName, err)
	}
	log.Printf("TaskPublisher: очередь '%s' успешно объявлена/найдена", queueName)
	// Канал не закрываем здесь, он должен управляться извне или при остановке приложения
	return &rabbitMQPublisher{channel: ch, queueName: queueName}, nil
}

// NewRabbitMQClientUpdatePublisher creates a new instance of ClientUpdatePublisher.
func NewRabbitMQClientUpdatePublisher(conn *amqp.Connection, queueName string) (ClientUpdatePublisher, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("client update publisher: не удалось открыть канал: %w", err)
	}
	// Объявляем очередь здесь
	_, err = ch.QueueDeclare(queueName, true, false, false, false, nil)
	if err != nil {
		ch.Close()
		return nil, fmt.Errorf("client update publisher: не удалось объявить очередь '%s': %w", queueName, err)
	}
	log.Printf("ClientUpdatePublisher: очередь '%s' успешно объявлена/найдена", queueName)
	return &rabbitMQPublisher{channel: ch, queueName: queueName}, nil
}

// PublishGenerationTask publishes a generation task.
func (p *rabbitMQPublisher) PublishGenerationTask(ctx context.Context, payload sharedMessaging.GenerationTaskPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		// Используем %s для UserID (string)
		log.Printf("[TaskID: %s][UserID: %s] Ошибка сериализации GenerationTaskPayload: %v", payload.TaskID, payload.UserID, err)
		return fmt.Errorf("ошибка сериализации задачи генерации для TaskID %s: %w", payload.TaskID, err)
	}

	err = p.publishMessage(ctx, body)
	if err != nil {
		// Используем %s для UserID (string)
		log.Printf("[TaskID: %s][UserID: %s] Ошибка публикации GenerationTask: %v", payload.TaskID, payload.UserID, err)
		return fmt.Errorf("ошибка публикации задачи генерации для TaskID %s: %w", payload.TaskID, err)
	}
	return nil
}

// PublishClientUpdate publishes an update to the client.
func (p *rabbitMQPublisher) PublishClientUpdate(ctx context.Context, payload ClientStoryUpdate) error {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Publisher: Ошибка маршалинга ClientStoryUpdate: %v", err)
		return fmt.Errorf("ошибка подготовки сообщения ClientStoryUpdate: %w", err)
	}
	// Используем exchange по умолчанию и routing key = имя очереди для client_updates
	return p.publishMessage(ctx, body)
}

// PublishGameOverTask publishes a game over task.
func (p *rabbitMQPublisher) PublishGameOverTask(ctx context.Context, payload sharedMessaging.GameOverTaskPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Publisher: Ошибка маршалинга GameOverTaskPayload: %v", err)
		return fmt.Errorf("ошибка подготовки сообщения GameOverTask: %w", err)
	}
	// Отправляем в ту же очередь задач, что и генерацию сцен
	return p.publishMessage(ctx, body)
}

// publishMessage is a helper method for publishing a message.
func (p *rabbitMQPublisher) publishMessage(ctx context.Context, body []byte) error {
	if p.channel == nil {
		log.Println("Ошибка публикации: канал RabbitMQ не инициализирован (nil)")
		return errors.New("канал RabbitMQ не инициализирован")
	}
	// Устанавливаем таймаут на публикацию
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	err := p.channel.PublishWithContext(ctx,
		"",          // exchange (используем default)
		p.queueName, // routing key (имя очереди)
		false,       // mandatory
		false,       // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
			Timestamp:    time.Now(),
			AppId:        "gameplay-service", // Идентификатор отправителя
		},
	)
	if err != nil {
		log.Printf("Ошибка публикации сообщения в очередь '%s': %v", p.queueName, err)
		// Возвращаем ошибку, чтобы вызывающий код мог ее обработать (например, откатить статус)
		return fmt.Errorf("ошибка публикации в очередь %s: %w", p.queueName, err)
	}
	log.Printf("Сообщение успешно опубликовано в очередь '%s'", p.queueName)
	return nil
}
