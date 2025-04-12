package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"novel-server/shared/messaging" // Используем общие структуры
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// TaskPublisher определяет интерфейс для отправки задач генерации.
type TaskPublisher interface {
	PublishGenerationTask(ctx context.Context, payload messaging.GenerationTaskPayload) error
}

// ClientUpdatePublisher определяет интерфейс для отправки обновлений клиенту.
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
	ErrorDetails      string   `json:"error_details,omitempty"`      // Если status == error
}

// rabbitMQPublisher реализует интерфейсы TaskPublisher и ClientUpdatePublisher для RabbitMQ.
type rabbitMQPublisher struct {
	channel   *amqp.Channel
	queueName string
}

// NewRabbitMQPublisher создает новый экземпляр паблишера для указанной очереди.
// Важно: предполагается, что канал уже открыт.
func NewRabbitMQPublisher(ch *amqp.Channel, queueName string) *rabbitMQPublisher {
	// Мы не объявляем очередь здесь, предполагаем, что она уже существует
	// или будет объявлена консьюмером.
	return &rabbitMQPublisher{channel: ch, queueName: queueName}
}

// NewRabbitMQTaskPublisher создает новый экземпляр TaskPublisher.
// Примечание: Эта функция может быть избыточной, если NewRabbitMQPublisher универсален.
// Оставляем для возможной специализации в будущем.
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

// NewRabbitMQClientUpdatePublisher создает новый экземпляр ClientUpdatePublisher.
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

// PublishGenerationTask публикует задачу генерации.
func (p *rabbitMQPublisher) PublishGenerationTask(ctx context.Context, payload messaging.GenerationTaskPayload) error {
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

// PublishClientUpdate публикует обновление для клиента.
func (p *rabbitMQPublisher) PublishClientUpdate(ctx context.Context, payload ClientStoryUpdate) error {
	body, err := json.Marshal(payload)
	if err != nil {
		// Используем %s для UserID (string)
		log.Printf("[StoryID: %s][UserID: %s] Ошибка сериализации ClientStoryUpdate: %v", payload.ID, payload.UserID, err)
		return fmt.Errorf("ошибка сериализации обновления клиента для StoryID %s: %w", payload.ID, err)
	}

	err = p.publishMessage(ctx, body)
	if err != nil {
		// Используем %s для UserID (string)
		log.Printf("[StoryID: %s][UserID: %s] Ошибка публикации ClientStoryUpdate: %v", payload.ID, payload.UserID, err)
		return fmt.Errorf("ошибка публикации обновления клиента для StoryID %s: %w", payload.ID, err)
	}
	return nil
}

// publishMessage - вспомогательный метод для публикации сообщения.
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
