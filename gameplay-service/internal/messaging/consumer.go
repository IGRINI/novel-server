package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"

	// "novel-server/gameplay-service/internal/models" // Удален
	// "novel-server/gameplay-service/internal/repository" // Удален
	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging" // Общие структуры сообщений
	sharedModels "novel-server/shared/models"       // !!! ДОБАВЛЕНО

	// Добавлен strconv для UserID
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

// Типы обновлений для ClientStoryUpdate
const (
	UpdateTypeDraft = "draft_update"
	UpdateTypeStory = "story_update"
)

// <<< Регулярное выражение для извлечения JSON из ```json ... ``` блока >>>
// (?s) - флаг: '.' совпадает с символом новой строки
// \x60 - символ `
// (?:json)? - опциональная группа "json" (незахватывающая)
// \s* - ноль или более пробельных символов
// (\{.*\}) - Захватывающая группа 1: сам JSON объект (от { до })
var jsonBlockRegex = regexp.MustCompile("(?s)\\x60\\x60\\x60(?:json)?\\s*(\\{.*\\})\\s*\\x60\\x60\\x60")

// --- NotificationProcessor ---

// NotificationProcessor обрабатывает логику уведомлений.
// Вынесен в отдельную структуру для тестируемости.
type NotificationProcessor struct {
	repo          interfaces.StoryConfigRepository    // Используем shared интерфейс
	publishedRepo interfaces.PublishedStoryRepository // !!! ДОБАВЛЕНО: Для PublishedStory
	sceneRepo     interfaces.StorySceneRepository     // !!! ДОБАВЛЕНО: Для StoryScene
	clientPub     ClientUpdatePublisher               // Для отправки обновлений клиенту
	taskPub       TaskPublisher                       // !!! ДОБАВЛЕНО: Для отправки новых задач генерации
	pushPub       PushNotificationPublisher           // <<< Добавляем издателя push-уведомлений
}

// NewNotificationProcessor создает новый экземпляр NotificationProcessor.
func NewNotificationProcessor(
	repo interfaces.StoryConfigRepository, // Используем shared интерфейс
	publishedRepo interfaces.PublishedStoryRepository,
	sceneRepo interfaces.StorySceneRepository, // !!! Добавлено sceneRepo
	clientPub ClientUpdatePublisher,
	taskPub TaskPublisher,
	pushPub PushNotificationPublisher) *NotificationProcessor {
	return &NotificationProcessor{
		repo:          repo,
		publishedRepo: publishedRepo,
		sceneRepo:     sceneRepo,
		clientPub:     clientPub,
		taskPub:       taskPub,
		pushPub:       pushPub, // <<< Сохраняем pushPub
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
			if config.Status != sharedModels.StatusGenerating {
				log.Printf("[processor][TaskID: %s] StoryConfig %s уже не в статусе Generating (текущий: %s), обновление Narrator Success отменено.", taskID, storyConfigID, config.Status)
				return nil // Игнорируем устаревшее успешное уведомление
			}
			// <<< Конец проверки статуса >>>

			log.Printf("[processor][TaskID: %s] Уведомление Narrator Success для StoryConfig %s.", taskID, storyConfigID)

			// <<< Извлекаем чистый JSON перед парсингом >>>
			rawGeneratedText := notification.GeneratedText
			jsonToParse := rawGeneratedText // По умолчанию используем исходный текст

			matches := jsonBlockRegex.FindStringSubmatch(rawGeneratedText)
			if len(matches) > 1 {
				// Если нашли блок ```json {...} ```, берем содержимое группы 1
				jsonToParse = matches[1]
				log.Printf("[processor][TaskID: %s] Извлечен JSON из блока ```json для StoryConfig %s.", taskID, storyConfigID)
			} else {
				// Если блок не найден, просто обрезаем пробелы
				trimmedText := strings.TrimSpace(rawGeneratedText)
				if strings.HasPrefix(trimmedText, "{") && strings.HasSuffix(trimmedText, "}") {
					jsonToParse = trimmedText // Используем обрезанный, если он похож на JSON
				} else {
					// Оставляем jsonToParse = rawGeneratedText, если обрезка не помогла
					log.Printf("[processor][TaskID: %s] Блок ```json не найден, попытка парсинга исходного/обрезанного текста для StoryConfig %s.", taskID, storyConfigID)
				}
			}

			configBytes := []byte(jsonToParse) // <<< Используем очищенный текст для парсинга
			// <<< Конец извлечения JSON >>>

			// Сначала парсим JSON, и только если успешно - обновляем поля
			var generatedConfig map[string]interface{}
			parseErr = json.Unmarshal(configBytes, &generatedConfig)

			if parseErr == nil {
				// Парсинг успешен, пытаемся извлечь ключевые поля
				title, titleOk := generatedConfig["t"].(string)
				desc, descOk := generatedConfig["sd"].(string)

				// Проверяем наличие, тип и непустое значение ключевых полей
				if titleOk && descOk && title != "" && desc != "" {
					// Все ключевые поля найдены и не пусты - обновляем конфиг и ставим Draft
					config.Config = json.RawMessage(configBytes) // <<< Сохраняем ОЧИЩЕННЫЙ JSON
					config.Title = title
					config.Description = desc
					config.Status = sharedModels.StatusDraft
					log.Printf("[processor][TaskID: %s] JSON успешно распарсен и ключевые поля извлечены для StoryConfig %s.", taskID, storyConfigID)
				} else {
					// Парсинг успешен, но не хватает ключевых полей или они пустые - считаем ошибкой
					log.Printf("[processor][TaskID: %s] ОШИБКА ЗАПОЛНЕНИЯ: JSON распарсен, но 't' (ok: %t, empty: %t) или 'sd' (ok: %t, empty: %t) отсутствуют или пусты для StoryConfig %s. Config НЕ будет обновлен.", taskID, titleOk, title == "", descOk, desc == "", storyConfigID)
					config.Status = sharedModels.StatusError
					// Оставляем старый config.Config, Title, Description
				}
				// Обновляем время в любом случае после успешного парсинга
				config.UpdatedAt = time.Now().UTC()
			} else {
				// Парсинг НЕ удался - логируем, Config НЕ обновляем, Title/Desc НЕ обновляем
				// <<< Логируем текст, который ПЫТАЛИСЬ парсить >>>
				log.Printf("[processor][TaskID: %s] ОШИБКА ПАРСИНГА: Не удалось распарсить JSON из GeneratedText для StoryConfig %s: %v. Текст для парсинга: '%s'. Config НЕ будет обновлен.", taskID, storyConfigID, parseErr, jsonToParse)
				// Устанавливаем статус Error при ошибке парсинга
				config.Status = sharedModels.StatusError // Используем sharedModels
				config.UpdatedAt = time.Now().UTC()      // <<< Время обновляем и при ошибке
				// config.Config = []byte("{}") // Оставляем старый конфиг
			}

			// Статус и время теперь устанавливаются внутри if/else
			// config.Status = sharedModels.StatusDraft // <<< УБИРАЕМ ЭТУ СТРОКУ ОТСЮДА
			// config.UpdatedAt = time.Now().UTC() // <<< И ЭТУ (перенесли выше)

		} else if notification.Status == sharedMessaging.NotificationStatusError { // Явное условие для Error
			log.Printf("[processor][TaskID: %s] Уведомление Narrator Error для StoryConfig %s. Details: %s", taskID, storyConfigID, notification.ErrorDetails)
			config.Status = sharedModels.StatusError // Используем sharedModels
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

		// <<< Отправляем PUSH-уведомление (если статус изменился на Draft или Error) >>>
		if config.Status == sharedModels.StatusDraft || config.Status == sharedModels.StatusError {
			pushPayload := PushNotificationPayload{
				UserID:       config.UserID,
				Notification: PushNotification{},
				Data: map[string]string{
					"type":      UpdateTypeDraft,
					"entity_id": config.ID.String(),
					"status":    string(config.Status),
				},
			}
			if config.Status == sharedModels.StatusDraft {
				pushPayload.Notification.Title = "Черновик готов!"
				if config.Title != "" {
					pushPayload.Notification.Body = fmt.Sprintf("Черновик '%s' готов к публикации.", config.Title)
				} else {
					pushPayload.Notification.Body = "Ваш черновик готов к публикации."
				}
			} else { // StatusError
				pushPayload.Notification.Title = "Ошибка генерации черновика"
				if notification.ErrorDetails != "" {
					pushPayload.Notification.Body = fmt.Sprintf("Произошла ошибка: %s", notification.ErrorDetails)
				} else if parseErr != nil {
					pushPayload.Notification.Body = fmt.Sprintf("Произошла ошибка парсинга JSON: %v", parseErr)
				} else {
					pushPayload.Notification.Body = "При генерации черновика произошла неизвестная ошибка."
				}
			}
			pushCtx, pushCancel := context.WithTimeout(context.Background(), 10*time.Second)
			if errPush := p.pushPub.PublishPushNotification(pushCtx, pushPayload); errPush != nil {
				log.Printf("[processor][TaskID: %s] ОШИБКА отправки Push-уведомления (Narrator) для StoryID %s: %v", taskID, config.ID.String(), errPush)
			} else {
				log.Printf("[processor][TaskID: %s] Push-уведомление (Narrator) для StoryID %s успешно отправлено.", taskID, config.ID.String())
			}
			pushCancel()
		}
		// <<< Конец отправки PUSH-уведомления >>>

		// Формируем и отправляем обновление клиенту
		clientUpdate = ClientStoryUpdate{
			ID:          config.ID.String(),
			UserID:      config.UserID.String(),
			UpdateType:  UpdateTypeDraft, // <<< Используем константу
			Status:      string(config.Status),
			Title:       config.Title,       // Будет старый title, если парсинг JSON не удался
			Description: config.Description, // Будет старое description, если парсинг JSON не удался
		}
		if config.Status == sharedModels.StatusError { // Используем sharedModels
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
		// <<< Устанавливаем тип обновления для истории >>>
		// clientUpdate.UpdateType = UpdateTypeStory // <<< УДАЛЯЕМ ЭТУ ОШИБОЧНУЮ СТРОКУ

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
			if err := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, newStatus, setupJSON, nil, nil, nil); err != nil {
				log.Printf("[processor][TaskID: %s] КРИТ. ОШИБКА: Не удалось сохранить Setup/Status для PublishedStory %s: %v", taskID, publishedStoryID, err)
				// Пытаемся обновить статус на Error
				errMsg := fmt.Sprintf("Failed to update setup/status: %v", err)
				if errRollback := p.publishedRepo.UpdateStatusDetails(context.Background(), publishedStoryID, sharedModels.StatusError, nil, nil, nil, &errMsg); errRollback != nil {
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

			// <<< ДОБАВЛЕНО: Формирование объединенного JSON для UserInput >>>
			var combinedInputJSON string
			configMap := make(map[string]interface{})
			setupMap := make(map[string]interface{})
			combinedData := make(map[string]interface{})

			// 1. Распарсить Config
			if errUnmarshalConfig := json.Unmarshal(publishedStory.Config, &configMap); errUnmarshalConfig != nil {
				log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось распарсрить Config JSON для PublishedStory %s: %v. Невозможно запустить генерацию первой сцены.", taskID, publishedStoryID, errUnmarshalConfig)
				// Setup сохранен, но следующий шаг невозможен. Статус остается FirstScenePending.
				return nil // Не возвращаем ошибку, Setup сохранен.
			}

			// 2. Распарсить Setup
			if errUnmarshalSetup := json.Unmarshal(setupJSON, &setupMap); errUnmarshalSetup != nil {
				log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось распарсрить Setup JSON для PublishedStory %s: %v. Невозможно запустить генерацию первой сцены.", taskID, publishedStoryID, errUnmarshalSetup)
				// Setup сохранен, но следующий шаг невозможен. Статус остается FirstScenePending.
				return nil // Не возвращаем ошибку, Setup сохранен.
			}

			// 3. Удалить 'cs' из Config
			delete(configMap, "cs")         // Предполагаем, что ключ 'cs'
			delete(configMap, "core_stats") // На всякий случай, если ключ другой

			// 4. Объединить данные (Setup перезаписывает Config при совпадении ключей)
			for k, v := range configMap {
				combinedData[k] = v
			}
			for k, v := range setupMap {
				combinedData[k] = v
			}

			// 5. Запаковать обратно в JSON
			combinedBytes, errMarshalCombined := json.Marshal(combinedData)
			if errMarshalCombined != nil {
				log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось запаковать объединенные данные в JSON для PublishedStory %s: %v. Невозможно запустить генерацию первой сцены.", taskID, publishedStoryID, errMarshalCombined)
				// Setup сохранен, но следующий шаг невозможен. Статус остается FirstScenePending.
				return nil // Не возвращаем ошибку, Setup сохранен.
			}
			combinedInputJSON = string(combinedBytes)
			// <<< КОНЕЦ ФОРМИРОВАНИЯ UserInput >>>

			nextTaskPayload := sharedMessaging.GenerationTaskPayload{
				TaskID:           uuid.New().String(),
				UserID:           publishedStory.UserID.String(),
				PromptType:       sharedMessaging.PromptTypeNovelFirstSceneCreator,
				PublishedStoryID: publishedStoryID.String(),
				UserInput:        combinedInputJSON,             // <<< Передаем объединенный JSON сюда
				StateHash:        sharedModels.InitialStateHash, // <<< Запускаем генерацию для начального хеша
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
			if err := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, sharedModels.StatusError, nil, nil, nil, &notification.ErrorDetails); err != nil {
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
				if errRollback := p.publishedRepo.UpdateStatusDetails(context.Background(), publishedStoryID, sharedModels.StatusError, nil, nil, nil, &errMsg); errRollback != nil {
					log.Printf("[processor][TaskID: %s] КРИТ. ОШИБКА 2: Не удалось откатить статус PublishedStory %s на Error после ошибки сохранения сцены: %v", taskID, publishedStoryID, errRollback)
				}
				return fmt.Errorf("ошибка сохранения StoryScene для PublishedStory %s, Hash %s: %w", publishedStoryID, notification.StateHash, err)
			}
			log.Printf("[processor][TaskID: %s] StoryScene для PublishedStory %s, Hash %s успешно сохранена.", taskID, publishedStoryID, notification.StateHash)

			// Обновляем статус PublishedStory (и текст концовки, если нужно)
			if err := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, newStatus, nil, nil, endingText, nil); err != nil {
				log.Printf("[processor][TaskID: %s] КРИТ. ОШИБКА: Не удалось обновить статус PublishedStory %s на %s после сохранения сцены: %v", taskID, publishedStoryID, newStatus, err)
				// Сцена сохранена, но статус не обновился. Это проблема.
				// TODO: Как обрабатывать? Пометить как-то?
				return fmt.Errorf("ошибка обновления статуса PublishedStory %s на %s: %w", publishedStoryID, newStatus, err)
			}
			log.Printf("[processor][TaskID: %s] PublishedStory %s успешно обновлен статус -> %s.", taskID, publishedStoryID, newStatus)

			// <<< Отправляем PUSH-уведомление (если статус Ready или Completed) >>>
			if newStatus == sharedModels.StatusReady || newStatus == sharedModels.StatusCompleted || newStatus == sharedModels.StatusError {
				// Сначала получаем UserID, он нужен для push
				var pubStory *sharedModels.PublishedStory
				var getErr error
				pubStory, getErr = p.publishedRepo.GetByID(dbCtx, publishedStoryID)
				if getErr != nil {
					log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось получить PublishedStory %s для отправки Push-уведомления: %v", taskID, publishedStoryID, getErr)
				} else {
					// Успешно получили, продолжаем
				}

				if pubStory != nil { // Если UserID есть
					pushPayload := PushNotificationPayload{
						UserID:       pubStory.UserID,
						Notification: PushNotification{},
						Data: map[string]string{
							"type":      UpdateTypeStory,
							"entity_id": publishedStoryID.String(),
							"status":    string(newStatus),
						},
					}
					storyTitle := "История"
					if pubStory.Title != nil && *pubStory.Title != "" {
						storyTitle = *pubStory.Title
					}

					if newStatus == sharedModels.StatusReady {
						pushPayload.Notification.Title = "История готова!"
						pushPayload.Notification.Body = fmt.Sprintf("История '%s' готова к прохождению.", storyTitle)
					} else if newStatus == sharedModels.StatusCompleted {
						pushPayload.Notification.Title = "История завершена!"
						pushPayload.Notification.Body = fmt.Sprintf("Прохождение истории '%s' завершено.", storyTitle)
					} else { // StatusError
						pushPayload.Notification.Title = "Ошибка генерации истории"
						if notification.ErrorDetails != "" {
							pushPayload.Notification.Body = fmt.Sprintf("Произошла ошибка при генерации '%s': %s", storyTitle, notification.ErrorDetails)
						} else {
							pushPayload.Notification.Body = fmt.Sprintf("При генерации истории '%s' произошла неизвестная ошибка.", storyTitle)
						}
					}

					pushCtx, pushCancel := context.WithTimeout(context.Background(), 10*time.Second)
					if errPush := p.pushPub.PublishPushNotification(pushCtx, pushPayload); errPush != nil {
						log.Printf("[processor][TaskID: %s] ОШИБКА отправки Push-уведомления (%s) для PublishedStory %s: %v", taskID, notification.PromptType, publishedStoryID, errPush)
					} else {
						log.Printf("[processor][TaskID: %s] Push-уведомление (%s) для PublishedStory %s успешно отправлено.", taskID, notification.PromptType, publishedStoryID)
					}
					pushCancel()
				}
			}
			// <<< Конец отправки PUSH-уведомления >>>

			// Отправляем WebSocket уведомление клиенту
			// Сначала получаем UserID
			pubStory, getErr := p.publishedRepo.GetByID(dbCtx, publishedStoryID)
			if getErr != nil {
				// Статус обновлен, но не можем отправить уведомление клиенту. Не критично, но плохо.
				log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось получить PublishedStory %s для отправки ClientUpdate: %v", taskID, publishedStoryID, getErr)
			} else {
				clientUpdate := ClientStoryUpdate{
					ID:          publishedStoryID.String(),
					UserID:      pubStory.UserID.String(),
					UpdateType:  UpdateTypeStory,
					Status:      string(newStatus),
					IsCompleted: newStatus == sharedModels.StatusCompleted,
					EndingText:  endingText,
				}
				// <<< Устанавливаем тип обновления для истории >>>
				// clientUpdate.UpdateType = "story_update" // <<< Эта строка теперь не нужна

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
			if err := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, sharedModels.StatusError, nil, nil, nil, &notification.ErrorDetails); err != nil {
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

const (
	// Максимальное количество одновременно обрабатываемых сообщений
	maxConcurrentHandlers = 10 // TODO: Сделать настраиваемым через config
)

// NotificationConsumer отвечает за получение уведомлений из RabbitMQ.
type NotificationConsumer struct {
	conn        *amqp.Connection
	processor   *NotificationProcessor // Используем процессор
	queueName   string
	stopChannel chan struct{}
	wg          sync.WaitGroup     // <<< Для ожидания завершения обработчиков
	cancelFunc  context.CancelFunc // <<< Для отмены контекста обработчиков
	// !!! ДОБАВЛЕНО: Зависимости для передачи в процессор
	storyRepo     interfaces.StoryConfigRepository // Используем shared интерфейс
	publishedRepo interfaces.PublishedStoryRepository
	sceneRepo     interfaces.StorySceneRepository // !!! Добавлено sceneRepo
	clientPub     ClientUpdatePublisher
	taskPub       TaskPublisher
	pushPub       PushNotificationPublisher // <<< Добавляем издателя push-уведомлений
}

// NewNotificationConsumer создает нового консьюмера уведомлений.
func NewNotificationConsumer(
	conn *amqp.Connection,
	repo interfaces.StoryConfigRepository, // Используем shared интерфейс
	publishedRepo interfaces.PublishedStoryRepository,
	sceneRepo interfaces.StorySceneRepository, // !!! Добавлено sceneRepo
	clientPub ClientUpdatePublisher,
	taskPub TaskPublisher,
	pushPub PushNotificationPublisher, // <<< Добавляем pushPub
	queueName string) (*NotificationConsumer, error) {
	// Создаем процессор с новыми зависимостями
	processor := NewNotificationProcessor(repo, publishedRepo, sceneRepo, clientPub, taskPub, pushPub) // <<< Передаем pushPub
	// Создаем контекст, который можно будет отменить
	// ctx, cancel := context.WithCancel(context.Background()) // <<< Делаем это в StartConsuming

	return &NotificationConsumer{
		conn:        conn,
		processor:   processor,
		queueName:   queueName,
		stopChannel: make(chan struct{}),
		// wg инициализируется автоматически
		// cancelFunc:    cancel, // <<< Инициализируем в StartConsuming
		storyRepo:     repo,
		publishedRepo: publishedRepo,
		sceneRepo:     sceneRepo,
		clientPub:     clientPub,
		taskPub:       taskPub,
		pushPub:       pushPub, // <<< Сохраняем для возможного использования вне процессора (хотя вряд ли)
	}, nil
}

// StartConsuming начинает прослушивание очереди уведомлений.
// Блокирует выполнение до тех пор, пока консьюмер не будет остановлен или не произойдет ошибка.
func (c *NotificationConsumer) StartConsuming() error {
	// <<< Создаем контекст, который будет отменен при остановке консьюмера >>>
	ctx, cancel := context.WithCancel(context.Background())
	c.cancelFunc = cancel // Сохраняем функцию отмены
	defer cancel()        // Гарантируем отмену контекста при выходе из функции

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
	log.Printf("Consumer: запущен, ожидание уведомлений из очереди '%s' (max concurrent: %d)...", q.Name, maxConcurrentHandlers)

	// <<< Семафор для ограничения количества одновременно работающих обработчиков >>>
	semaphore := make(chan struct{}, maxConcurrentHandlers)

	for {
		select {
		case d, ok := <-msgs:
			if !ok {
				log.Println("Consumer: канал сообщений RabbitMQ закрыт")
				// <<< Ожидаем завершения всех активных обработчиков перед выходом >>>
				c.wg.Wait()
				log.Println("Consumer: все активные обработчики завершены.")
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

			// <<< Занимаем слот в семафоре >>>
			select {
			case semaphore <- struct{}{}:
				// Слот успешно занят
				log.Printf("Consumer: Запуск обработчика для DeliveryTag %d (активно: %d/%d)", d.DeliveryTag, len(semaphore), maxConcurrentHandlers)
			case <-ctx.Done(): // <<< Проверяем отмену контекста перед блокировкой
				log.Println("Consumer: контекст отменен во время ожидания слота семафора. Сообщение Nack(requeue=true). DeliveryTag:", d.DeliveryTag)
				_ = d.Nack(false, true) // Возвращаем в очередь, т.к. не начали обработку
				// <<< Ожидаем завершения всех активных обработчиков перед выходом >>>
				c.wg.Wait()
				log.Println("Consumer: все активные обработчики завершены.")
				return context.Canceled // Возвращаем ошибку отмены
			}

			// <<< Добавляем в WaitGroup ПЕРЕД запуском горутины >>>
			c.wg.Add(1)

			// Запускаем обработку в отдельной горутине
			go func(msg amqp.Delivery, currentCtx context.Context, id uuid.UUID) {
				// <<< Освобождаем слот семафора и уменьшаем счетчик WaitGroup при выходе из горутины >>>
				defer func() {
					<-semaphore
					c.wg.Done()
					log.Printf("Consumer: Обработчик завершен для DeliveryTag %d (активно: %d/%d)", msg.DeliveryTag, len(semaphore), maxConcurrentHandlers)
				}()

				log.Printf("Consumer: Обработка DeliveryTag %d для ID %s...", msg.DeliveryTag, id)
				// <<< Передаем отменяемый контекст в процессор >>>
				if err := c.processor.Process(currentCtx, msg.Body, id); err != nil {
					// Логируем критические ошибки из процессора
					log.Printf("Consumer: Ошибка при обработке уведомления для ID %s (DeliveryTag: %d): %v. Сообщение будет Nack(requeue=false).", id, msg.DeliveryTag, err)
					// TODO: Возможно, нужна стратегия ретраев или DLQ здесь? Рассмотреть requeue=true для временных ошибок.
					_ = msg.Nack(false, false) // <<< Nack при ошибке
				} else {
					// <<< Ack ТОЛЬКО при успешной обработке >>>
					log.Printf("Consumer: Успешная обработка DeliveryTag %d для ID %s. Сообщение будет Ack.", msg.DeliveryTag, id)
					_ = msg.Ack(false)
				}
			}(d, ctx, targetUUID) // <<< Передаем КОПИЮ сообщения и контекст

			// <<< УДАЛЯЕМ немедленный Ack отсюда >>>
			// _ = d.Ack(false)

		case <-c.stopChannel:
			log.Println("Consumer: получен сигнал остановки")
			// <<< Отменяем контекст, чтобы сигнализировать обработчикам >>>
			cancel() // или c.cancelFunc()
			// <<< Ожидаем завершения всех активных обработчиков >>>
			log.Println("Consumer: ожидание завершения активных обработчиков...")
			c.wg.Wait()
			log.Println("Consumer: все активные обработчики завершены.")
			return nil
		}
	}
}

// Stop останавливает консьюмер.
func (c *NotificationConsumer) Stop() {
	log.Println("Consumer: остановка...")
	// <<< Отменяем контекст перед закрытием канала, чтобы обработчики могли среагировать >>>
	if c.cancelFunc != nil {
		c.cancelFunc()
	}
	close(c.stopChannel)
	// <<< Ожидание wg происходит в StartConsuming перед выходом >>>
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
