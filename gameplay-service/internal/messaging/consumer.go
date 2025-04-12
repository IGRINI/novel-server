package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"novel-server/gameplay-service/internal/models" // Локальные модели
	"novel-server/gameplay-service/internal/repository"
	sharedMessaging "novel-server/shared/messaging" // Общие структуры сообщений
	"strconv"                                       // Добавлен strconv для UserID
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

// --- NotificationProcessor ---

// NotificationProcessor обрабатывает логику уведомлений.
// Вынесен в отдельную структуру для тестируемости.
type NotificationProcessor struct {
	repo      repository.StoryConfigRepository
	publisher ClientUpdatePublisher
}

// NewNotificationProcessor создает новый экземпляр NotificationProcessor.
func NewNotificationProcessor(repo repository.StoryConfigRepository, publisher ClientUpdatePublisher) *NotificationProcessor {
	return &NotificationProcessor{repo: repo, publisher: publisher}
}

// Process обрабатывает одно уведомление.
// Возвращает ошибку, если произошла критическая ошибка, которую нужно логировать особо.
func (p *NotificationProcessor) Process(ctx context.Context, body []byte, storyConfigUUID uuid.UUID) error {
	log.Printf("[processor] Обработка уведомления для StoryConfigID: %s", storyConfigUUID)

	var notification sharedMessaging.NotificationPayload
	if err := json.Unmarshal(body, &notification); err != nil {
		log.Printf("[processor] Ошибка десериализации JSON уведомления для StoryConfigID %s: %v. Обработка невозможна.", storyConfigUUID, err)
		// Ошибка парсинга самого сообщения - не можем продолжить.
		return fmt.Errorf("ошибка десериализации уведомления: %w", err)
	}

	taskID := notification.TaskID
	log.Printf("[processor][TaskID: %s] Уведомление распарсено для StoryConfigID: %s", taskID, storyConfigUUID)

	dbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	config, err := p.repo.GetByIDInternal(dbCtx, storyConfigUUID)
	if err != nil {
		log.Printf("[processor][TaskID: %s] Ошибка получения StoryConfig %s для обновления: %v", taskID, storyConfigUUID, err)
		// Ошибка получения из БД - не можем продолжить.
		return fmt.Errorf("ошибка получения StoryConfig %s: %w", storyConfigUUID, err)
	}

	if config.Status != models.StatusGenerating {
		log.Printf("[processor][TaskID: %s] StoryConfig %s уже не в статусе Generating (текущий: %s), обновление отменено.", taskID, storyConfigUUID, config.Status)
		// Не ошибка, просто пропускаем.
		return nil
	}

	var updateErr error
	var clientUpdate ClientStoryUpdate
	var parseErr error

	if notification.Status == sharedMessaging.NotificationStatusSuccess {
		log.Printf("[processor][TaskID: %s] Уведомление Success для StoryConfig %s. Обновляем Config, Title, Description.", taskID, storyConfigUUID)
		config.Config = json.RawMessage(notification.GeneratedText)
		config.Status = models.StatusDraft
		config.UpdatedAt = time.Now().UTC()

		var generatedConfig map[string]interface{}
		parseErr = json.Unmarshal(config.Config, &generatedConfig)
		if parseErr == nil {
			if title, ok := generatedConfig["t"].(string); ok {
				config.Title = title
			} else {
				log.Printf("[processor][TaskID: %s] Не удалось извлечь 't' (title) из JSON для StoryConfig %s", taskID, storyConfigUUID)
			}
			if desc, ok := generatedConfig["sd"].(string); ok {
				config.Description = desc
			} else {
				log.Printf("[processor][TaskID: %s] Не удалось извлечь 'sd' (description) из JSON для StoryConfig %s", taskID, storyConfigUUID)
			}
		} else {
			log.Printf("[processor][TaskID: %s] КРИТИЧЕСКАЯ ОШИБКА: Не удалось распарсить успешно сгенерированный JSON для StoryConfig %s: %v", taskID, storyConfigUUID, parseErr)
			// Не возвращаем ошибку, т.к. конфиг все равно обновился, хоть и без Title/Desc
		}

	} else {
		log.Printf("[processor][TaskID: %s] Уведомление Error для StoryConfig %s. Details: %s", taskID, storyConfigUUID, notification.ErrorDetails)
		config.Status = models.StatusError
		config.UpdatedAt = time.Now().UTC()
	}

	updateErr = p.repo.Update(dbCtx, config)
	if updateErr != nil {
		log.Printf("[processor][TaskID: %s] КРИТИЧЕСКАЯ ОШИБКА: Не удалось сохранить обновления для StoryConfig %s после обработки уведомления: %v", taskID, storyConfigUUID, updateErr)
		log.Printf("!!!!!! DEBUG: Returning error from Update: %v", updateErr)
		return fmt.Errorf("ошибка сохранения StoryConfig %s: %w", storyConfigUUID, updateErr)
	}
	log.Printf("[processor][TaskID: %s] StoryConfig %s успешно обновлен в БД до статуса %s.", taskID, storyConfigUUID, config.Status)

	// Формируем и отправляем обновление клиенту
	clientUpdate = ClientStoryUpdate{
		ID:          config.ID.String(),
		UserID:      strconv.FormatUint(config.UserID, 10),
		Status:      string(config.Status),
		Title:       config.Title,
		Description: config.Description,
	}

	if config.Status == models.StatusError {
		clientUpdate.ErrorDetails = notification.ErrorDetails
	}

	if parseErr == nil && notification.Status == sharedMessaging.NotificationStatusSuccess {
		var generatedConfig map[string]interface{}
		if err := json.Unmarshal(config.Config, &generatedConfig); err == nil {
			if pDesc, ok := generatedConfig["p_desc"].(string); ok {
				clientUpdate.PlayerDescription = pDesc
			}
			if pp, ok := generatedConfig["pp"].(map[string]interface{}); ok {
				if thRaw, ok := pp["th"].([]interface{}); ok {
					clientUpdate.Themes = castToStringSlice(thRaw)
				}
				if wlRaw, ok := pp["wl"].([]interface{}); ok {
					clientUpdate.WorldLore = castToStringSlice(wlRaw)
				}
			}
		} else {
			log.Printf("[processor][TaskID: %s] Ошибка повторного парсинга JSON для извлечения доп. полей StoryConfig %s: %v", taskID, storyConfigUUID, err)
		}
	}

	pubCtx, pubCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pubCancel()

	if err := p.publisher.PublishClientUpdate(pubCtx, clientUpdate); err != nil {
		log.Printf("[processor][TaskID: %s] Ошибка отправки ClientStoryUpdate для StoryID %s: %v", taskID, config.ID.String(), err)
		// Не возвращаем ошибку наверх, т.к. основная логика (обновление БД) прошла успешно.
		// Но логируем, чтобы можно было отследить проблемы с отправкой клиенту.
	} else {
		log.Printf("[processor][TaskID: %s] ClientStoryUpdate для StoryID %s успешно отправлен.", taskID, config.ID.String())
	}

	return nil // Все прошло успешно (или ошибка была некритичной для потока)
}

// --- NotificationConsumer ---

// NotificationConsumer отвечает за получение уведомлений из RabbitMQ.
type NotificationConsumer struct {
	conn        *amqp.Connection
	processor   *NotificationProcessor // Используем процессор
	queueName   string
	stopChannel chan struct{}
}

// NewNotificationConsumer создает нового консьюмера уведомлений.
func NewNotificationConsumer(conn *amqp.Connection, repo repository.StoryConfigRepository, publisher ClientUpdatePublisher, queueName string) (*NotificationConsumer, error) {
	processor := NewNotificationProcessor(repo, publisher)
	return &NotificationConsumer{
		conn:        conn,
		processor:   processor, // Инициализируем процессор
		queueName:   queueName,
		stopChannel: make(chan struct{}),
	}, nil
}

// StartConsuming начинает прослушивание очереди уведомлений.
func (c *NotificationConsumer) StartConsuming() error {
	ch, err := c.conn.Channel()
	if err != nil {
		return fmt.Errorf("consumer: не удалось открыть канал RabbitMQ: %w", err)
	}
	defer ch.Close()

	q, err := ch.QueueDeclare(
		c.queueName,
		true,  // durable
		false, // delete when unused
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		return fmt.Errorf("consumer: не удалось объявить очередь '%s': %w", c.queueName, err)
	}
	log.Printf("Consumer: очередь '%s' успешно объявлена/найдена", q.Name)

	err = ch.Qos(1, 0, false) // Обрабатываем по одному сообщению
	if err != nil {
		return fmt.Errorf("consumer: не удалось установить QoS: %w", err)
	}

	msgs, err := ch.Consume(
		q.Name,
		"gameplay-consumer", // consumer tag
		false,               // auto-ack = false
		false,               // exclusive
		false,               // no-local
		false,               // no-wait
		nil,                 // args
	)
	if err != nil {
		return fmt.Errorf("consumer: не удалось зарегистрировать консьюмера: %w", err)
	}
	log.Printf("Consumer: запущен, ожидание уведомлений из очереди '%s'...", q.Name)

	for {
		select {
		case d, ok := <-msgs:
			if !ok {
				log.Println("Consumer: канал сообщений RabbitMQ закрыт")
				return nil
			}

			log.Printf("Consumer: получено уведомление (DeliveryTag: %d)", d.DeliveryTag)

			// Пытаемся извлечь StoryConfigID до полной десериализации,
			// чтобы логировать его даже при ошибке парсинга.
			var preliminary map[string]interface{}
			storyConfigUUID := uuid.Nil
			if json.Unmarshal(d.Body, &preliminary) == nil {
				if idStr, ok := preliminary["story_config_id"].(string); ok {
					storyConfigUUID, _ = uuid.Parse(idStr)
				}
			}

			if storyConfigUUID == uuid.Nil {
				log.Printf("Consumer: уведомление не содержит валидный 'story_config_id', отправка в nack.")
				_ = d.Nack(false, false) // Requeue = false
				continue
			}

			// Запускаем обработку в отдельной горутине
			go func(body []byte, id uuid.UUID) {
				if err := c.processor.Process(context.Background(), body, id); err != nil {
					// Логируем критические ошибки из процессора
					log.Printf("Consumer: критическая ошибка при обработке уведомления для StoryID %s: %v", id, err)
					// TODO: Возможно, нужна стратегия ретраев или DLQ здесь?
				}
			}(d.Body, storyConfigUUID)

			// Подтверждаем сообщение (ack) независимо от результата обработки в горутине.
			// Это стратегия "at-most-once" для консьюмера, чтобы избежать повторной обработки
			// в случае падения сервиса во время работы горутины. Обработка ошибок - внутри Process.
			_ = d.Ack(false)

		case <-c.stopChannel:
			log.Println("Consumer: получен сигнал остановки")
			return nil
		}
	}
}

// Stop останавливает консьюмер.
func (c *NotificationConsumer) Stop() {
	log.Println("Consumer: остановка...")
	close(c.stopChannel)
}

// Вспомогательная функция для каста []interface{} в []string
func castToStringSlice(slice []interface{}) []string {
	result := make([]string, 0, len(slice))
	for _, v := range slice {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	return result
}
