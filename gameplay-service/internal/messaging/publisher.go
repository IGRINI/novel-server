package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	sharedMessaging "novel-server/shared/messaging" // Используем общие структуры
	"time"

	sharedModels "novel-server/shared/models"

	amqp "github.com/rabbitmq/amqp091-go"
)

// TaskPublisher defines the interface for publishing tasks to the generation queue.
type TaskPublisher interface {
	PublishGenerationTask(ctx context.Context, payload sharedMessaging.GenerationTaskPayload) error
	PublishGameOverTask(ctx context.Context, payload sharedMessaging.GameOverTaskPayload) error
	// Close() error // Optional: If the publisher needs explicit closing
}

// <<< НОВЫЙ ИНТЕРФЕЙС >>>
// CharacterImageTaskPublisher defines the interface for publishing character image generation tasks.
type CharacterImageTaskPublisher interface {
	PublishCharacterImageTask(ctx context.Context, payload sharedMessaging.CharacterImageTaskPayload) error
}

// CharacterImageTaskBatchPublisher defines the interface for publishing batches of character image generation tasks.
type CharacterImageTaskBatchPublisher interface {
	PublishCharacterImageTaskBatch(ctx context.Context, payload sharedMessaging.CharacterImageTaskBatchPayload) error
}

// ClientUpdatePublisher defines the interface for publishing updates to the client.
type ClientUpdatePublisher interface {
	PublishClientUpdate(ctx context.Context, payload sharedModels.ClientStoryUpdate) error
}

// PushNotificationPublisher defines the interface for publishing push notification requests.
type PushNotificationPublisher interface {
	PublishPushNotification(ctx context.Context, payload sharedModels.PushNotificationPayload) error
}

// rabbitMQPublisher implements the TaskPublisher, ClientUpdatePublisher, PushNotificationPublisher, CharacterImageTaskPublisher interfaces for RabbitMQ.
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
	// <<< ИЗМЕНЕНО: Используем QueueDeclare вместо QueueDeclarePassive >>>
	// Паблишер ТЕПЕРЬ будет создавать очередь, если она не существует.
	// Это делает систему более устойчивой к порядку запуска сервисов.
	// Важно, чтобы параметры очереди совпадали с теми, что у консьюмера!
	// <<< ДОБАВЛЕНО: Аргументы DLX, как у консьюмера (story-generator) >>>
	args := amqp.Table{
		"x-queue-mode":              "lazy",
		"x-dead-letter-exchange":    "story_generation_tasks_dlx", // Должно совпадать с DLX в story-generator
		"x-dead-letter-routing-key": "dlq",                        // Должно совпадать с DLQ routing key в story-generator
	}
	_, err = ch.QueueDeclare(
		queueName, // name
		true,      // durable
		false,     // delete when unused
		false,     // exclusive
		false,     // no-wait
		args,      // <<< Используем аргументы DLX >>>
	)
	if err != nil {
		// Если объявление очереди не удалось, это серьезная проблема.
		log.Printf("TaskPublisher ERROR: Не удалось объявить очередь '%s': %v", queueName, err)
		ch.Close() // Закрываем канал при ошибке
		return nil, fmt.Errorf("task publisher: не удалось объявить очередь '%s': %w", queueName, err)
	}
	log.Printf("TaskPublisher: Очередь '%s' успешно объявлена/найдена.", queueName)
	// Канал не закрываем здесь, он должен управляться извне или при остановке приложения
	return &rabbitMQPublisher{channel: ch, queueName: queueName}, nil
}

// NewRabbitMQCharacterImageTaskPublisher creates a new instance of CharacterImageTaskPublisher.
func NewRabbitMQCharacterImageTaskPublisher(conn *amqp.Connection, queueName string) (CharacterImageTaskPublisher, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("character image task publisher: не удалось открыть канал: %w", err)
	}
	// <<< ИЗМЕНЕНО: Удален вызов QueueDeclare >>>
	// Паблишер не должен объявлять очередь, параметры которой (DLX) устанавливает консьюмер.
	// _, err = ch.QueueDeclare(
	// 	queueName, // name
	// 	true,      // durable
	// 	false,     // delete when unused
	// 	false,     // exclusive
	// 	false,     // no-wait
	// 	nil,       // arguments
	// )
	// if err != nil {
	// 	log.Printf("CharacterImageTaskPublisher ERROR: Не удалось объявить очередь '%s': %v", queueName, err)
	// 	ch.Close()
	// 	return nil, fmt.Errorf("character image task publisher: не удалось объявить очередь '%s': %w", queueName, err)
	// }
	log.Printf("CharacterImageTaskPublisher: Инициализирован для очереди '%s' (объявление пропускается).", queueName)
	return &rabbitMQPublisher{channel: ch, queueName: queueName}, nil
}

// NewRabbitMQCharacterImageTaskBatchPublisher creates a new instance of CharacterImageTaskBatchPublisher.
// It uses the same queue as the single task publisher.
func NewRabbitMQCharacterImageTaskBatchPublisher(conn *amqp.Connection, queueName string) (CharacterImageTaskBatchPublisher, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("character image task batch publisher: не удалось открыть канал: %w", err)
	}
	// <<< ИЗМЕНЕНО: Удален вызов QueueDeclare >>>
	// Паблишер не должен объявлять очередь, параметры которой (DLX) устанавливает консьюмер.
	// _, err = ch.QueueDeclare(
	// 	queueName, // name
	// 	true,      // durable
	// 	false,     // delete when unused
	// 	false,     // exclusive
	// 	false,     // no-wait
	// 	nil,       // arguments
	// )
	// if err != nil {
	// 	log.Printf("CharacterImageTaskBatchPublisher ERROR: Не удалось объявить очередь '%s': %v", queueName, err)
	// 	ch.Close()
	// 	return nil, fmt.Errorf("character image task batch publisher: не удалось объявить очередь '%s': %w", queueName, err)
	// }
	log.Printf("CharacterImageTaskBatchPublisher: Инициализирован для очереди '%s' (объявление пропускается).", queueName)
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

// NewRabbitMQPushNotificationPublisher creates a new instance of PushNotificationPublisher.
func NewRabbitMQPushNotificationPublisher(conn *amqp.Connection, queueName string) (PushNotificationPublisher, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("push notification publisher: не удалось открыть канал: %w", err)
	}
	// Объявляем очередь здесь (можно без DLX, если обработка ошибок на стороне notification-service)
	_, err = ch.QueueDeclare(queueName, true, false, false, false, nil)
	if err != nil {
		ch.Close()
		return nil, fmt.Errorf("push notification publisher: не удалось объявить очередь '%s': %w", queueName, err)
	}
	log.Printf("PushNotificationPublisher: очередь '%s' успешно объявлена/найдена", queueName)
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

// PublishCharacterImageTask publishes a character image generation task.
func (p *rabbitMQPublisher) PublishCharacterImageTask(ctx context.Context, payload sharedMessaging.CharacterImageTaskPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[CharTaskID: %s][CharID: %s] Ошибка сериализации CharacterImageTaskPayload: %v", payload.TaskID, payload.CharacterID, err)
		return fmt.Errorf("ошибка сериализации задачи генерации изображения для CharID %s: %w", payload.CharacterID, err)
	}

	err = p.publishMessage(ctx, body)
	if err != nil {
		log.Printf("[CharTaskID: %s][CharID: %s] Ошибка публикации CharacterImageTask: %v", payload.TaskID, payload.CharacterID, err)
		return fmt.Errorf("ошибка публикации задачи генерации изображения для CharID %s: %w", payload.CharacterID, err)
	}
	return nil
}

// PublishCharacterImageTaskBatch publishes a batch of character image generation tasks.
func (p *rabbitMQPublisher) PublishCharacterImageTaskBatch(ctx context.Context, payload sharedMessaging.CharacterImageTaskBatchPayload) error {
	if len(payload.Tasks) == 0 {
		log.Printf("[BatchID: %s] Попытка публикации пустого батча задач генерации изображений.", payload.BatchID)
		return nil // Нет задач для публикации
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[BatchID: %s] Ошибка сериализации CharacterImageTaskBatchPayload: %v", payload.BatchID, err)
		return fmt.Errorf("ошибка сериализации батча задач генерации изображений BatchID %s: %w", payload.BatchID, err)
	}

	err = p.publishMessage(ctx, body)
	if err != nil {
		log.Printf("[BatchID: %s] Ошибка публикации CharacterImageTaskBatch: %v", payload.BatchID, err)
		return fmt.Errorf("ошибка публикации батча задач генерации изображений BatchID %s: %w", payload.BatchID, err)
	}
	log.Printf("[BatchID: %s] Батч из %d задач генерации изображений успешно опубликован.", payload.BatchID, len(payload.Tasks))
	return nil
}

// PublishClientUpdate publishes an update to the client.
func (p *rabbitMQPublisher) PublishClientUpdate(ctx context.Context, payload sharedModels.ClientStoryUpdate) error {
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

// PublishPushNotification publishes a push notification request.
func (p *rabbitMQPublisher) PublishPushNotification(ctx context.Context, payload sharedModels.PushNotificationPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("PushPublisher: Ошибка маршалинга PushNotificationPayload для UserID %s: %v", payload.UserID, err)
		return fmt.Errorf("ошибка подготовки сообщения PushNotification: %w", err)
	}
	// Отправляем в очередь push_notifications
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

	var err error
	// Попытка публикации с retry до 3 раз
	for attempt := 1; attempt <= 3; attempt++ {
		err = p.channel.PublishWithContext(ctx,
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
		if err == nil {
			log.Printf("Сообщение успешно опубликовано в очередь '%s' (attempt %d)", p.queueName, attempt)
			break
		}
		log.Printf("Ошибка публикации (attempt %d) в очередь '%s': %v", attempt, p.queueName, err)
		time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("ошибка публикации в очередь %s после retries: %w", p.queueName, err)
	}
	log.Printf("Сообщение успешно опубликовано в очередь '%s'", p.queueName)
	return nil
}
