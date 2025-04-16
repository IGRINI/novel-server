package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"novel-server/gameplay-service/internal/models" // Локальные модели
	"novel-server/gameplay-service/internal/repository"
	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging" // Общие структуры сообщений
	sharedModels "novel-server/shared/models"       // !!! ДОБАВЛЕНО
	"strconv"                                       // Добавлен strconv для UserID
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

// --- NotificationProcessor ---

// NotificationProcessor обрабатывает логику уведомлений.
// Вынесен в отдельную структуру для тестируемости.
type NotificationProcessor struct {
	repo          repository.StoryConfigRepository    // Для StoryConfig
	publishedRepo interfaces.PublishedStoryRepository // !!! ДОБАВЛЕНО: Для PublishedStory
	sceneRepo     interfaces.StorySceneRepository     // !!! ДОБАВЛЕНО: Для StoryScene
	clientPub     ClientUpdatePublisher               // Для отправки обновлений клиенту
	taskPub       TaskPublisher                       // !!! ДОБАВЛЕНО: Для отправки новых задач генерации
}

// NewNotificationProcessor создает новый экземпляр NotificationProcessor.
func NewNotificationProcessor(
	repo repository.StoryConfigRepository,
	publishedRepo interfaces.PublishedStoryRepository,
	sceneRepo interfaces.StorySceneRepository, // !!! Добавлено sceneRepo
	clientPub ClientUpdatePublisher,
	taskPub TaskPublisher) *NotificationProcessor {
	return &NotificationProcessor{
		repo:          repo,
		publishedRepo: publishedRepo,
		sceneRepo:     sceneRepo,
		clientPub:     clientPub,
		taskPub:       taskPub,
	}
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

	// *** ИЗМЕНЕНИЕ: Определяем ID и тип задачи ***
	var isStoryConfigTask bool
	var storyConfigID uuid.UUID
	var publishedStoryID uuid.UUID
	var parseIDErr error

	if notification.StoryConfigID != "" {
		storyConfigID, parseIDErr = uuid.Parse(notification.StoryConfigID)
		if parseIDErr == nil {
			isStoryConfigTask = true
		}
	}
	if !isStoryConfigTask && notification.PublishedStoryID != "" {
		publishedStoryID, parseIDErr = uuid.Parse(notification.PublishedStoryID)
		// isStoryConfigTask остается false
	}

	if parseIDErr != nil || (storyConfigID == uuid.Nil && publishedStoryID == uuid.Nil) {
		log.Printf("[processor][TaskID: %s] Не удалось определить ID (StoryConfigID: '%s', PublishedStoryID: '%s') или ID некорректен: %v. Nack.",
			taskID, notification.StoryConfigID, notification.PublishedStoryID, parseIDErr)
		// Не можем обработать без валидного ID
		return fmt.Errorf("невалидный или отсутствующий ID в уведомлении")
	}
	// *** КОНЕЦ ИЗМЕНЕНИЯ ***

	// *** ИЗМЕНЕНИЕ: Обработка по типу промпта ***
	switch notification.PromptType {
	case sharedMessaging.PromptTypeNarrator:
		// --- Логика обработки для StoryConfig (существующий код) ---
		log.Printf("[processor][TaskID: %s] Обработка PromptTypeNarrator для StoryConfigID: %s", taskID, storyConfigID)
		if !isStoryConfigTask {
			log.Printf("[processor][TaskID: %s] Ошибка: PromptTypeNarrator получен без StoryConfigID. PublishedStoryID: %s", taskID, publishedStoryID)
			return fmt.Errorf("некорректное уведомление: Narrator без StoryConfigID")
		}

		config, err := p.repo.GetByIDInternal(dbCtx, storyConfigID)
		if err != nil {
			log.Printf("[processor][TaskID: %s] Ошибка получения StoryConfig %s для обновления Narrator: %v", taskID, storyConfigID, err)
			return fmt.Errorf("ошибка получения StoryConfig %s: %w", storyConfigID, err)
		}

		var updateErr error
		var clientUpdate ClientStoryUpdate
		var parseErr error

		if notification.Status == sharedMessaging.NotificationStatusSuccess {
			// <<< Проверяем статус ТОЛЬКО для Success сценария >>>
			if config.Status != models.StatusGenerating {
				log.Printf("[processor][TaskID: %s] StoryConfig %s уже не в статусе Generating (текущий: %s), обновление Narrator Success отменено.", taskID, storyConfigID, config.Status)
				return nil // Игнорируем устаревшее успешное уведомление
			}
			// <<< Конец проверки статуса >>>

			log.Printf("[processor][TaskID: %s] Уведомление Narrator Success для StoryConfig %s.", taskID, storyConfigID)

			// Сначала парсим JSON, и только если успешно - обновляем поля
			var generatedConfig map[string]interface{}
			configBytes := []byte(notification.GeneratedText)
			parseErr = json.Unmarshal(configBytes, &generatedConfig)

			if parseErr == nil {
				// Парсинг успешен - обновляем Config, Title, Description
				config.Config = json.RawMessage(configBytes) // Сохраняем валидный JSON
				if title, ok := generatedConfig["t"].(string); ok {
					config.Title = title
				} else {
					log.Printf("[processor][TaskID: %s] Не удалось извлечь 't' (title) из JSON для StoryConfig %s", taskID, storyConfigID)
				}
				if desc, ok := generatedConfig["sd"].(string); ok {
					config.Description = desc
				} else {
					log.Printf("[processor][TaskID: %s] Не удалось извлечь 'sd' (description) из JSON для StoryConfig %s", taskID, storyConfigID)
				}
			} else {
				// Парсинг НЕ удался - логируем, Config НЕ обновляем, Title/Desc НЕ обновляем
				log.Printf("[processor][TaskID: %s] ОШИБКА ПАРСИНГА: Не удалось распарсить JSON из GeneratedText для StoryConfig %s: %v. Содержимое: '%s'. Config НЕ будет обновлен.", taskID, storyConfigID, parseErr, string(configBytes))
				// Устанавливаем статус Error при ошибке парсинга
				config.Status = models.StatusError
				// config.Config = []byte("{}") // Оставляем старый конфиг
			}

			// Статус и время обновляем в любом случае (успешное уведомление получено)
			// config.Status = models.StatusDraft // Убрано, статус ставится выше
			config.UpdatedAt = time.Now().UTC()

		} else if notification.Status == sharedMessaging.NotificationStatusError { // Явное условие для Error
			log.Printf("[processor][TaskID: %s] Уведомление Narrator Error для StoryConfig %s. Details: %s", taskID, storyConfigID, notification.ErrorDetails)
			config.Status = models.StatusError
			config.UpdatedAt = time.Now().UTC()
			// Title/Description/Config не меняем при ошибке
		} else {
			// Обработка неизвестного статуса уведомления (на всякий случай)
			log.Printf("[processor][TaskID: %s] Получен неизвестный статус уведомления (%s) для StoryConfig %s. Игнорируется.", taskID, notification.Status, storyConfigID)
			return nil // Не обновляем БД и не отправляем клиенту
		}

		// Обновляем БД только если config был изменен (успех или ошибка)
		updateErr = p.repo.Update(dbCtx, config)
		if updateErr != nil {
			log.Printf("[processor][TaskID: %s] КРИТ. ОШИБКА: Не удалось сохранить обновления StoryConfig %s (Narrator): %v", taskID, storyConfigID, updateErr)
			return fmt.Errorf("ошибка сохранения StoryConfig %s: %w", storyConfigID, updateErr)
		}
		log.Printf("[processor][TaskID: %s] StoryConfig %s (Narrator) успешно обновлен в БД до статуса %s.", taskID, storyConfigID, config.Status)

		// Формируем и отправляем обновление клиенту (код без изменений)
		clientUpdate = ClientStoryUpdate{
			ID:          config.ID.String(),
			UserID:      strconv.FormatUint(config.UserID, 10),
			Status:      string(config.Status),
			Title:       config.Title,       // Будет старый title, если парсинг JSON не удался
			Description: config.Description, // Будет старое description, если парсинг JSON не удался
		}
		if config.Status == models.StatusError {
			if notification.ErrorDetails != "" {
				errDetails := notification.ErrorDetails
				clientUpdate.ErrorDetails = &errDetails
			} else if parseErr != nil {
				// Добавляем ошибку парсинга, если она была причиной статуса Error
				errDetails := fmt.Sprintf("JSON parsing error: %v", parseErr)
				clientUpdate.ErrorDetails = &errDetails
			} else {
				clientUpdate.ErrorDetails = nil
			}
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
				log.Printf("[processor][TaskID: %s] Ошибка повторного парсинга JSON Narrator для StoryConfig %s: %v", taskID, storyConfigID, err)
			}
		}
		pubCtx, pubCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pubCancel()
		if err := p.clientPub.PublishClientUpdate(pubCtx, clientUpdate); err != nil {
			log.Printf("[processor][TaskID: %s] Ошибка отправки ClientStoryUpdate (Narrator) для StoryID %s: %v", taskID, config.ID.String(), err)
		} else {
			log.Printf("[processor][TaskID: %s] ClientStoryUpdate (Narrator) для StoryID %s успешно отправлен.", taskID, config.ID.String())
		}
		// --- Конец логики Narrator ---

	case sharedMessaging.PromptTypeNovelSetup:
		// --- Логика обработки для PublishedStory (Setup) ---
		log.Printf("[processor][TaskID: %s] Обработка PromptTypeNovelSetup для PublishedStoryID: %s", taskID, publishedStoryID)
		if isStoryConfigTask {
			log.Printf("[processor][TaskID: %s] Ошибка: PromptTypeNovelSetup получен с StoryConfigID: %s.", taskID, storyConfigID)
			return fmt.Errorf("некорректное уведомление: Setup с StoryConfigID")
		}

		if notification.Status == sharedMessaging.NotificationStatusSuccess {
			log.Printf("[processor][TaskID: %s] Уведомление Setup Success для PublishedStory %s.", taskID, publishedStoryID)
			setupJSON := json.RawMessage(notification.GeneratedText)
			newStatus := sharedModels.StatusFirstScenePending
			// Обновляем Setup и Status, используя новый метод
			if err := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, newStatus, setupJSON, nil, nil); err != nil {
				log.Printf("[processor][TaskID: %s] КРИТ. ОШИБКА: Не удалось сохранить Setup/Status для PublishedStory %s: %v", taskID, publishedStoryID, err)
				// Пытаемся обновить статус на Error
				errMsg := fmt.Sprintf("Failed to update setup/status: %v", err)
				if errRollback := p.publishedRepo.UpdateStatusDetails(context.Background(), publishedStoryID, sharedModels.StatusError, nil, &errMsg, nil); errRollback != nil {
					log.Printf("[processor][TaskID: %s] КРИТ. ОШИБКА 2: Не удалось откатить статус PublishedStory %s на Error: %v", taskID, publishedStoryID, errRollback)
				}
				return fmt.Errorf("ошибка сохранения Setup/Status для PublishedStory %s: %w", publishedStoryID, err)
			}
			log.Printf("[processor][TaskID: %s] PublishedStory %s успешно обновлен Setup, статус -> %s.", taskID, publishedStoryID, newStatus)

			// Запускаем генерацию первой сцены
			log.Printf("[processor][TaskID: %s] Запуск генерации первой сцены для PublishedStory %s...", taskID, publishedStoryID)
			// Получаем Config, чтобы отправить его вместе с Setup
			publishedStory, errGet := p.publishedRepo.GetByID(dbCtx, publishedStoryID)
			if errGet != nil {
				log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось получить PublishedStory %s для запуска генерации первой сцены: %v", taskID, publishedStoryID, errGet)
				// Setup сохранен, но не можем запустить следующий шаг. Статус остается FirstScenePending.
				// TODO: Возможно, нужна система ретраев или ручное вмешательство?
				return nil // Не возвращаем ошибку наверх, Setup уже сохранен.
			}

			nextTaskPayload := sharedMessaging.GenerationTaskPayload{
				TaskID:           uuid.New().String(),
				UserID:           strconv.FormatUint(publishedStory.UserID, 10),
				PromptType:       sharedMessaging.PromptTypeNovelFirstSceneCreator,
				PublishedStoryID: publishedStoryID.String(),
				InputData: map[string]interface{}{
					"config": string(publishedStory.Config),
					"setup":  string(setupJSON), // Используем свежий setup
				},
				StateHash: sharedModels.InitialStateHash, // <<< Запускаем генерацию для начального хеша
			}

			if errPub := p.taskPub.PublishGenerationTask(ctx, nextTaskPayload); errPub != nil {
				log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось отправить задачу генерации первой сцены для PublishedStory %s: %v", taskID, publishedStoryID, errPub)
				// Setup сохранен, статус FirstScenePending, но задача не ушла.
				// TODO: Реакция? Ретраи? Уведомление админу?
			} else {
				log.Printf("[processor][TaskID: %s] Задача генерации первой сцены для PublishedStory %s успешно отправлена (TaskID: %s).", taskID, publishedStoryID, nextTaskPayload.TaskID)
			}

		} else { // notification.Status == Error для Setup
			log.Printf("[processor][TaskID: %s] Уведомление Setup Error для PublishedStory %s. Details: %s", taskID, publishedStoryID, notification.ErrorDetails)
			// Обновляем статус и детали ошибки, используя новый метод
			if err := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, sharedModels.StatusError, nil, &notification.ErrorDetails, nil); err != nil {
				log.Printf("[processor][TaskID: %s] КРИТ. ОШИБКА: Не удалось обновить статус PublishedStory %s на Error: %v", taskID, publishedStoryID, err)
				return fmt.Errorf("ошибка обновления статуса Error для PublishedStory %s: %w", publishedStoryID, err)
			}
			log.Printf("[processor][TaskID: %s] Статус PublishedStory %s обновлен на Error.", taskID, publishedStoryID)
			// TODO: Отправить уведомление клиенту об ошибке генерации Setup?
		}
		// --- Конец логики Setup ---

	case sharedMessaging.PromptTypeNovelFirstSceneCreator, sharedMessaging.PromptTypeNovelCreator, sharedMessaging.PromptTypeNovelGameOverCreator:
		// --- Логика обработки для PublishedStory (Генерация Сцены/Концовки) ---
		log.Printf("[processor][TaskID: %s] Обработка %s для PublishedStoryID: %s, StateHash: %s",
			taskID, notification.PromptType, publishedStoryID, notification.StateHash)

		if isStoryConfigTask {
			log.Printf("[processor][TaskID: %s] Ошибка: %s получен с StoryConfigID: %s.", taskID, notification.PromptType, storyConfigID)
			return fmt.Errorf("некорректное уведомление: %s с StoryConfigID", notification.PromptType)
		}
		if notification.StateHash == "" {
			log.Printf("[processor][TaskID: %s] Ошибка: %s получен без StateHash для PublishedStoryID: %s.", taskID, notification.PromptType, publishedStoryID)
			// Не можем сохранить сцену без хеша
			return fmt.Errorf("некорректное уведомление: %s без StateHash", notification.PromptType)
		}

		if notification.Status == sharedMessaging.NotificationStatusSuccess {
			log.Printf("[processor][TaskID: %s] Уведомление %s Success для PublishedStory %s, StateHash %s.",
				taskID, notification.PromptType, publishedStoryID, notification.StateHash)

			sceneContentJSON := json.RawMessage(notification.GeneratedText)
			newStatus := sharedModels.StatusReady
			var endingText *string

			// Если это генерация концовки, парсим текст и меняем статус
			if notification.PromptType == sharedMessaging.PromptTypeNovelGameOverCreator {
				newStatus = sharedModels.StatusCompleted
				var endingContent struct {
					EndingText string `json:"et"`
				}
				if err := json.Unmarshal(sceneContentJSON, &endingContent); err != nil {
					log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось распарсить JSON концовки для PublishedStory %s: %v", taskID, publishedStoryID, err)
					// Продолжаем, но EndingText не будет сохранен
				} else {
					endingText = &endingContent.EndingText
					log.Printf("[processor][TaskID: %s] Извлечен EndingText для PublishedStory %s.", taskID, publishedStoryID)
				}
			}

			// Создаем и сохраняем новую сцену (даже для концовки, там может быть доп. инфо)
			newScene := &sharedModels.StoryScene{
				ID:               uuid.New(),
				PublishedStoryID: publishedStoryID,
				StateHash:        notification.StateHash,
				Content:          sceneContentJSON,
				CreatedAt:        time.Now().UTC(),
			}

			if err := p.sceneRepo.Create(dbCtx, newScene); err != nil {
				log.Printf("[processor][TaskID: %s] КРИТ. ОШИБКА: Не удалось сохранить StoryScene для PublishedStory %s, Hash %s: %v",
					taskID, publishedStoryID, notification.StateHash, err)
				// Пытаемся обновить статус PublishedStory на Error
				errMsg := fmt.Sprintf("Failed to save scene for hash %s: %v", notification.StateHash, err)
				if errRollback := p.publishedRepo.UpdateStatusDetails(context.Background(), publishedStoryID, sharedModels.StatusError, nil, &errMsg, nil); errRollback != nil {
					log.Printf("[processor][TaskID: %s] КРИТ. ОШИБКА 2: Не удалось откатить статус PublishedStory %s на Error после ошибки сохранения сцены: %v", taskID, publishedStoryID, errRollback)
				}
				return fmt.Errorf("ошибка сохранения StoryScene для PublishedStory %s, Hash %s: %w", publishedStoryID, notification.StateHash, err)
			}
			log.Printf("[processor][TaskID: %s] StoryScene для PublishedStory %s, Hash %s успешно сохранена.", taskID, publishedStoryID, notification.StateHash)

			// Обновляем статус PublishedStory (и текст концовки, если нужно)
			if err := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, newStatus, nil, nil, endingText); err != nil {
				log.Printf("[processor][TaskID: %s] КРИТ. ОШИБКА: Не удалось обновить статус PublishedStory %s на %s после сохранения сцены: %v", taskID, publishedStoryID, newStatus, err)
				// Сцена сохранена, но статус не обновился. Это проблема.
				// TODO: Как обрабатывать? Пометить как-то?
				return fmt.Errorf("ошибка обновления статуса PublishedStory %s на %s: %w", publishedStoryID, newStatus, err)
			}
			log.Printf("[processor][TaskID: %s] PublishedStory %s успешно обновлен статус -> %s.", taskID, publishedStoryID, newStatus)

			// Отправляем WebSocket уведомление клиенту
			// Сначала получаем UserID
			pubStory, getErr := p.publishedRepo.GetByID(dbCtx, publishedStoryID)
			if getErr != nil {
				// Статус обновлен, но не можем отправить уведомление клиенту. Не критично, но плохо.
				log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось получить PublishedStory %s для отправки ClientUpdate: %v", taskID, publishedStoryID, getErr)
			} else {
				clientUpdate := ClientStoryUpdate{
					ID:          publishedStoryID.String(),
					UserID:      strconv.FormatUint(pubStory.UserID, 10),
					Status:      string(newStatus),
					IsCompleted: newStatus == sharedModels.StatusCompleted, // Завершено или нет
					EndingText:  endingText,                                // Текст концовки (может быть nil)
				}
				pubCtx, pubCancel := context.WithTimeout(context.Background(), 10*time.Second)
				if errPub := p.clientPub.PublishClientUpdate(pubCtx, clientUpdate); errPub != nil {
					log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось отправить ClientStoryUpdate для PublishedStory %s (Status: %s): %v", taskID, publishedStoryID, newStatus, errPub)
				} else {
					log.Printf("[processor][TaskID: %s] ClientStoryUpdate для PublishedStory %s (Status: %s) успешно отправлен.", taskID, publishedStoryID, newStatus)
				}
				pubCancel() // Отменяем контекст явно
			}

		} else { // notification.Status == Error для Сцены/Концовки
			log.Printf("[processor][TaskID: %s] Уведомление %s Error для PublishedStory %s. Details: %s", taskID, notification.PromptType, publishedStoryID, notification.ErrorDetails)
			// Обновляем статус PublishedStory на Error
			if err := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, sharedModels.StatusError, nil, &notification.ErrorDetails, nil); err != nil {
				log.Printf("[processor][TaskID: %s] КРИТ. ОШИБКА: Не удалось обновить статус PublishedStory %s на Error после ошибки генерации сцены: %v", taskID, publishedStoryID, err)
				return fmt.Errorf("ошибка обновления статуса Error для PublishedStory %s: %w", publishedStoryID, err)
			}
		}
		// --- Конец логики Сцены/Концовки ---

	default:
		log.Printf("[processor][TaskID: %s] Неизвестный PromptType: %s. Уведомление проигнорировано.", taskID, notification.PromptType)
	}
	// *** КОНЕЦ ИЗМЕНЕНИЯ ***

	log.Printf("[processor][TaskID: %s] Уведомление успешно обработано.", taskID)
	return nil // Если дошли сюда, значит, основная логика выполнена
}

// --- NotificationConsumer ---

// NotificationConsumer отвечает за получение уведомлений из RabbitMQ.
type NotificationConsumer struct {
	conn        *amqp.Connection
	processor   *NotificationProcessor // Используем процессор
	queueName   string
	stopChannel chan struct{}
	// !!! ДОБАВЛЕНО: Зависимости для передачи в процессор
	storyRepo     repository.StoryConfigRepository
	publishedRepo interfaces.PublishedStoryRepository
	sceneRepo     interfaces.StorySceneRepository // !!! Добавлено sceneRepo
	clientPub     ClientUpdatePublisher
	taskPub       TaskPublisher
}

// NewNotificationConsumer создает нового консьюмера уведомлений.
func NewNotificationConsumer(
	conn *amqp.Connection,
	repo repository.StoryConfigRepository,
	publishedRepo interfaces.PublishedStoryRepository,
	sceneRepo interfaces.StorySceneRepository, // !!! Добавлено sceneRepo
	clientPub ClientUpdatePublisher,
	taskPub TaskPublisher,
	queueName string) (*NotificationConsumer, error) {
	// Создаем процессор с новыми зависимостями
	processor := NewNotificationProcessor(repo, publishedRepo, sceneRepo, clientPub, taskPub)
	return &NotificationConsumer{
		conn:          conn,
		processor:     processor,
		queueName:     queueName,
		stopChannel:   make(chan struct{}),
		storyRepo:     repo,
		publishedRepo: publishedRepo,
		sceneRepo:     sceneRepo,
		clientPub:     clientPub,
		taskPub:       taskPub,
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

			// *** ИЗМЕНЕНИЕ: Пытаемся извлечь PublishedStoryID или StoryConfigID ***
			var preliminary map[string]interface{}
			storyConfigUUID := uuid.Nil
			publishedStoryUUID := uuid.Nil // <<< Добавили для PublishedStoryID

			if json.Unmarshal(d.Body, &preliminary) == nil {
				if idStr, ok := preliminary["story_config_id"].(string); ok {
					storyConfigUUID, _ = uuid.Parse(idStr)
				}
				// Ищем PublishedStoryID, если StoryConfigID не найден или пуст
				if storyConfigUUID == uuid.Nil {
					if idStr, ok := preliminary["published_story_id"].(string); ok {
						publishedStoryUUID, _ = uuid.Parse(idStr)
					}
				}
			}

			// Определяем, какой ID использовать для логирования и обработки
			targetUUID := storyConfigUUID
			if targetUUID == uuid.Nil {
				targetUUID = publishedStoryUUID
			}

			if targetUUID == uuid.Nil {
				log.Printf("Consumer: Уведомление (DeliveryTag: %d) не содержит ни 'story_config_id', ни 'published_story_id'. Отправка в nack.", d.DeliveryTag)
				_ = d.Nack(false, false) // Requeue = false
				continue
			}
			// *** КОНЕЦ ИЗМЕНЕНИЯ ***

			// Запускаем обработку в отдельной горутине
			go func(body []byte, id uuid.UUID) {
				// Передаем targetUUID в процессор
				if err := c.processor.Process(context.Background(), body, id); err != nil {
					// Логируем критические ошибки из процессора
					log.Printf("Consumer: критическая ошибка при обработке уведомления для ID %s: %v", id, err)
					// TODO: Возможно, нужна стратегия ретраев или DLQ здесь?
				}
			}(d.Body, targetUUID) // <<< Передаем targetUUID

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
