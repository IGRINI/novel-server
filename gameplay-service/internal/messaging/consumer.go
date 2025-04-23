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
	"novel-server/gameplay-service/internal/config" // <<< ИСПРАВЛЕН ИМПОРТ
	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging" // Общие структуры сообщений
	sharedModels "novel-server/shared/models"       // !!! ДОБАВЛЕНО

	// Добавлен strconv для UserID
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go" // <<< ВОЗВРАЩАЕМ ПСЕВДОНИМ
)

// Типы обновлений для ClientStoryUpdate
const (
	UpdateTypeDraft = "draft_update"
	UpdateTypeStory = "story_update"
)

// <<< Регулярное выражение для извлечения JSON из ```json ... ``` блока >>>
// (?s) - флаг: '.' совпадает с символом новой строки
// \x60\x60\x60 - открывающие ```
// (?:\w+)? - опциональный идентификатор языка (json, yaml и т.д.), незахватываемый
// \s* - пробелы
// (.*?) - НЕЖАДНАЯ захватывающая группа 1: любой текст (минимально возможный)
// \s* - пробелы
// \x60\x60\x60 - закрывающие ```
var jsonBlockRegex = regexp.MustCompile(`(?s)` + "```" + `(?:\w+)?\s*(.*?)\s*` + "```")

// userChoiceInfo больше не используется, комментарий удален
// type userChoiceInfo struct { ... } // REMOVED

// --- NotificationProcessor ---

// NotificationProcessor обрабатывает логику уведомлений.
// Вынесен в отдельную структуру для тестируемости.
type NotificationProcessor struct {
	repo                interfaces.StoryConfigRepository     // Используем shared интерфейс
	publishedRepo       interfaces.PublishedStoryRepository  // !!! ДОБАВЛЕНО: Для PublishedStory
	sceneRepo           interfaces.StorySceneRepository      // !!! ДОБАВЛЕНО: Для StoryScene
	playerGameStateRepo interfaces.PlayerGameStateRepository // <<< ДОБАВЛЕНО: Для PlayerGameState
	clientPub           ClientUpdatePublisher                // Для отправки обновлений клиенту
	taskPub             TaskPublisher                        // !!! ДОБАВЛЕНО: Для отправки новых задач генерации
	pushPub             PushNotificationPublisher            // <<< Добавляем издателя push-уведомлений
}

// NewNotificationProcessor создает новый экземпляр NotificationProcessor.
func NewNotificationProcessor(
	repo interfaces.StoryConfigRepository, // Используем shared интерфейс
	publishedRepo interfaces.PublishedStoryRepository,
	sceneRepo interfaces.StorySceneRepository, // !!! Добавлено sceneRepo
	playerGameStateRepo interfaces.PlayerGameStateRepository, // <<< ДОБАВЛЕНО
	clientPub ClientUpdatePublisher,
	taskPub TaskPublisher,
	pushPub PushNotificationPublisher) *NotificationProcessor {
	return &NotificationProcessor{
		repo:                repo,
		publishedRepo:       publishedRepo,
		sceneRepo:           sceneRepo,
		playerGameStateRepo: playerGameStateRepo, // <<< ДОБАВЛЕНО
		clientPub:           clientPub,
		taskPub:             taskPub,
		pushPub:             pushPub, // <<< Сохраняем pushPub
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

			// <<< Улучшенное извлечение JSON >>>
			rawGeneratedText := notification.GeneratedText
			jsonToParse := "" // Инициализируем пустой строкой

			matches := jsonBlockRegex.FindStringSubmatch(rawGeneratedText)
			if len(matches) > 1 {
				// Если нашли блок ```...```, берем содержимое группы 1 (нежадно)
				jsonToParse = strings.TrimSpace(matches[1])
				log.Printf("[processor][TaskID: %s] Извлечен контент из блока ``` для StoryConfig %s.", taskID, storyConfigID)
			} else {
				// Если блок не найден, используем исходный текст (обрезав пробелы)
				jsonToParse = strings.TrimSpace(rawGeneratedText)
				log.Printf("[processor][TaskID: %s] Блок ``` не найден, попытка парсинга исходного/обрезанного текста для StoryConfig %s.", taskID, storyConfigID)
			}

			// <<< Убрали fallback проверку на '{' и '}' >>>

			configBytes := []byte(jsonToParse) // Используем извлеченный или исходный текст для парсинга
			// <<< Конец улучшенного извлечения JSON >>>

			// Пытаемся распарсить JSON
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
		publishedStory, err := p.publishedRepo.GetByID(dbCtx, publishedStoryID)
		if err != nil {
			log.Printf("[processor][TaskID: %s] Ошибка получения PublishedStory %s для обновления Setup: %v", taskID, publishedStoryID, err)
			return fmt.Errorf("ошибка получения PublishedStory %s: %w", publishedStoryID, err)
		}

		if notification.Status == sharedMessaging.NotificationStatusSuccess {
			// <<< Проверяем статус ТОЛЬКО для Success сценария >>>
			if publishedStory.Status != sharedModels.StatusSetupGenerating {
				log.Printf("[processor][TaskID: %s] PublishedStory %s уже не в статусе SetupGenerating (текущий: %s), обновление Setup Success отменено.", taskID, publishedStoryID, publishedStory.Status)
				return nil // Игнорируем устаревшее
			}
			// <<< Конец проверки статуса >>>

			log.Printf("[processor][TaskID: %s] Уведомление Setup Success для PublishedStory %s.", taskID, publishedStoryID)

			// <<< Улучшенное извлечение JSON >>>
			rawGeneratedText := notification.GeneratedText
			jsonToParse := ""
			matches := jsonBlockRegex.FindStringSubmatch(rawGeneratedText)
			if len(matches) > 1 {
				jsonToParse = strings.TrimSpace(matches[1])
				log.Printf("[processor][TaskID: %s] Извлечен контент из блока ``` для Setup PublishedStory %s.", taskID, publishedStoryID)
			} else {
				jsonToParse = strings.TrimSpace(rawGeneratedText)
				log.Printf("[processor][TaskID: %s] Блок ``` не найден для Setup PublishedStory %s, используется исходный/обрезанный текст.", taskID, publishedStoryID)
			}
			setupBytes := []byte(jsonToParse)
			// <<< Конец улучшенного извлечения JSON >>>

			// Валидация: Проверяем, что setupBytes - валидный JSON
			var temp map[string]interface{}
			if err := json.Unmarshal(setupBytes, &temp); err != nil {
				// Если невалидный JSON, обновляем статус на Error
				log.Printf("[processor][TaskID: %s] ОШИБКА ПАРСИНГА SETUP: Невалидный JSON получен для Setup PublishedStory %s: %v. Текст: '%s'", taskID, publishedStoryID, err, jsonToParse)
				errDetails := fmt.Sprintf("Invalid JSON received for Setup: %v", err)
				if updateErr := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, sharedModels.StatusError, nil, nil, nil, &errDetails); updateErr != nil {
					log.Printf("[processor][TaskID: %s] КРИТ. ОШИБКА: Не удалось обновить статус PublishedStory %s на Error после ошибки парсинга Setup: %v", taskID, publishedStoryID, updateErr)
					return fmt.Errorf("ошибка обновления статуса Error для PublishedStory %s: %w", publishedStoryID, updateErr)
				}
				// Отправляем обновление клиенту об ошибке?
				return nil // Завершаем обработку для этого сообщения
			}

			// Валидный JSON, обновляем статус и Setup
			// Обновляем статус на FirstScenePending, т.к. теперь нужно сгенерировать первую сцену
			if err := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, sharedModels.StatusFirstScenePending, setupBytes, nil, nil, nil); err != nil {
				log.Printf("[processor][TaskID: %s] КРИТ. ОШИБКА: Не удалось обновить статус и Setup для PublishedStory %s: %v", taskID, publishedStoryID, err)
				return fmt.Errorf("ошибка обновления статуса/Setup для PublishedStory %s: %w", publishedStoryID, err)
			}
			log.Printf("[processor][TaskID: %s] PublishedStory %s успешно обновлен статус -> FirstScenePending и Setup сохранен.", taskID, publishedStoryID)

			// <<< ИЗМЕНЕНИЕ: Отправка задачи на генерацию ПЕРВОЙ СЦЕНЫ >>>
			// Формируем JSON для userInput следующей задачи: объединяем Config и только что полученный Setup
			configBytes := publishedStory.Config
			combinedInputMap := make(map[string]interface{})
			// Парсим Config
			if len(configBytes) > 0 && string(configBytes) != "null" {
				if err := json.Unmarshal(configBytes, &combinedInputMap); err != nil {
					log.Printf("[processor][TaskID: %s] ПРЕДУПРЕЖДЕНИЕ: Не удалось распарсить Config для задачи FirstScene PublishedStory %s: %v. Задача будет отправлена без Config.", taskID, publishedStoryID, err)
					combinedInputMap = make(map[string]interface{}) // Очищаем карту в случае ошибки
				}
			} else {
				log.Printf("[processor][TaskID: %s] ПРЕДУПРЕЖДЕНИЕ: Config отсутствует или null для задачи FirstScene PublishedStory %s. Задача будет отправлена без Config.", taskID, publishedStoryID)
			}
			// Парсим Setup (который мы только что получили и проверили на валидность)
			var setupMap map[string]interface{}
			_ = json.Unmarshal(setupBytes, &setupMap) // Ошибку парсинга игнорируем, т.к. JSON уже валиден
			// Добавляем Setup в общую карту
			combinedInputMap["stp"] = setupMap // Используем ключ "stp" для setup
			// Извлекаем начальные статы из setupMap для передачи в AI
			initialCoreStats := make(map[string]int)
			if csd, ok := setupMap["csd"].(map[string]interface{}); ok {
				for key, val := range csd {
					if statDef, okDef := val.(map[string]interface{}); okDef {
						if initVal, okVal := statDef["iv"].(float64); okVal { // JSON парсит числа как float64
							initialCoreStats[key] = int(initVal)
						}
					}
				}
			}
			combinedInputMap["cs"] = initialCoreStats                // Добавляем начальные статы
			combinedInputMap["sv"] = make(map[string]interface{})    // Пустые переменные
			combinedInputMap["gf"] = []string{}                      // Пустые флаги
			combinedInputMap["uc"] = []sharedModels.UserChoiceInfo{} // <<< USE SHARED MODEL (Пусто)
			combinedInputMap["pss"] = ""                             // Пустые предыдущие саммари
			combinedInputMap["pfd"] = ""
			combinedInputMap["pvis"] = ""
			// Убираем дублирующиеся поля Config из корневого уровня, если они есть
			delete(combinedInputMap, "t")
			delete(combinedInputMap, "sd")
			delete(combinedInputMap, "gn")
			delete(combinedInputMap, "ln")
			// ... другие поля конфига ...

			combinedInputBytes, errMarshal := json.Marshal(combinedInputMap)
			if errMarshal != nil {
				log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось смастерить JSON для задачи FirstScene PublishedStory %s: %v", taskID, publishedStoryID, errMarshal)
				return nil // Не можем продолжить без payload
			}
			combinedInputJSON := string(combinedInputBytes)

			nextTaskPayload := sharedMessaging.GenerationTaskPayload{
				TaskID:           uuid.New().String(),
				UserID:           publishedStory.UserID.String(),
				PromptType:       sharedMessaging.PromptTypeNovelFirstSceneCreator,
				PublishedStoryID: publishedStoryID.String(),
				UserInput:        combinedInputJSON,             // <<< Передаем объединенный JSON сюда
				StateHash:        sharedModels.InitialStateHash, // <<< Запускаем генерацию для начального хеша
			}

			if errPub := p.taskPub.PublishGenerationTask(ctx, nextTaskPayload); errPub != nil {
				// Улучшено логирование, TODO удален. Потенциально нужна система алертов/мониторинга.
				log.Printf("[processor][TaskID: %s] КРИТ. ОШИБКА: Не удалось отправить задачу генерации первой сцены для PublishedStory %s (TaskID: %s): %v. Статус истории остался %s.",
					taskID, publishedStoryID, nextTaskPayload.TaskID, errPub, sharedModels.StatusFirstScenePending)
				// Setup сохранен, статус FirstScenePending, но задача не ушла.
			} else {
				log.Printf("[processor][TaskID: %s] Задача генерации первой сцены для PublishedStory %s успешно отправлена (TaskID: %s).", taskID, publishedStoryID, nextTaskPayload.TaskID)
			}

		} else { // Ошибка генерации Setup
			log.Printf("[processor][TaskID: %s] Уведомление Setup Error для PublishedStory %s. Details: %s", taskID, publishedStoryID, notification.ErrorDetails)
			// Обновляем статус PublishedStory на Error
			if err := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, sharedModels.StatusError, nil, nil, nil, &notification.ErrorDetails); err != nil {
				log.Printf("[processor][TaskID: %s] КРИТ. ОШИБКА: Не удалось обновить статус PublishedStory %s на Error после ошибки генерации Setup: %v", taskID, publishedStoryID, err)
				return fmt.Errorf("ошибка обновления статуса Error для PublishedStory %s: %w", publishedStoryID, err)
			}
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

			// <<< Добавляем извлечение JSON для Scene/GameOver >>>
			rawGeneratedText := notification.GeneratedText
			jsonToParse := ""
			matches := jsonBlockRegex.FindStringSubmatch(rawGeneratedText)
			if len(matches) > 1 {
				jsonToParse = strings.TrimSpace(matches[1])
				log.Printf("[processor][TaskID: %s] Извлечен контент из блока ``` для %s PublishedStory %s.", taskID, notification.PromptType, publishedStoryID)
			} else {
				jsonToParse = strings.TrimSpace(rawGeneratedText)
				log.Printf("[processor][TaskID: %s] Блок ``` не найден для %s PublishedStory %s, используется исходный/обрезанный текст.", taskID, notification.PromptType, publishedStoryID)
			}
			sceneContentJSON := json.RawMessage(jsonToParse) // Используем очищенный JSON
			// <<< Конец извлечения JSON для Scene/GameOver >>>

			newStatus := sharedModels.StatusReady // Статус по умолчанию для PublishedStory
			var endingText *string

			// Если это генерация концовки, парсим текст и меняем статус
			if notification.PromptType == sharedMessaging.PromptTypeNovelGameOverCreator {
				// Status for PublishedStory doesn't change on game over, only PlayerGameState does.
				// newStatus = sharedModels.StatusCompleted // REMOVED
				var endingContent struct {
					EndingText string `json:"et"`
				}
				// <<< Парсим из уже очищенного sceneContentJSON >>>
				if err := json.Unmarshal(sceneContentJSON, &endingContent); err != nil {
					log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось распарсить JSON концовки для PublishedStory %s: %v", taskID, publishedStoryID, err)
					// Продолжаем, но EndingText не будет сохранен
				} else {
					endingText = &endingContent.EndingText
					log.Printf("[processor][TaskID: %s] Извлечен EndingText для PublishedStory %s.", taskID, publishedStoryID)
				}
			}

			// Создаем и сохраняем новую сцену (даже для концовки, там может быть доп. инфо)
			scene := &sharedModels.StoryScene{
				ID:               uuid.New(),
				PublishedStoryID: publishedStoryID,
				StateHash:        notification.StateHash,
				Content:          sceneContentJSON, // <<< Сохраняем очищенный JSON
				CreatedAt:        time.Now().UTC(),
			}

			// <<< ИЗМЕНЕНИЕ: Используем Upsert вместо Create >>>
			upsertErr := p.sceneRepo.Upsert(dbCtx, scene)
			if upsertErr != nil {
				log.Printf("[processor][TaskID: %s] КРИТ. ОШИБКА: Не удалось сохранить/обновить сцену (Hash: %s) для PublishedStory %s: %v", taskID, notification.StateHash, publishedStoryID, upsertErr)
				// Если не удалось сохранить сцену, нет смысла обновлять статус истории/игрока
				return fmt.Errorf("ошибка Upsert сцены для PublishedStory %s, Hash %s: %w", publishedStoryID, notification.StateHash, upsertErr)
			}
			log.Printf("[processor][TaskID: %s] Сцена (Hash: %s) успешно сохранена/обновлена (SceneID: %s) для PublishedStory %s.", taskID, notification.StateHash, scene.ID, publishedStoryID)

			// <<< ИЗМЕНЕНИЕ: Обновляем статус PublishedStory только если это не конец игры >>>
			if notification.PromptType != sharedMessaging.PromptTypeNovelGameOverCreator {
				// Используем правильную сигнатуру UpdateStatusDetails
				if err := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, newStatus, nil, nil, nil, nil); err != nil {
					// Улучшено логирование, TODO удален. Потенциально нужна система алертов/мониторинга для отслеживания несогласованности.
					log.Printf("[processor][TaskID: %s] КРИТ. ОШИБКА (Несогласованность данных!): Сцена (Hash: %s, SceneID: %s) сохранена, но НЕ удалось обновить статус PublishedStory %s на %s: %v",
						taskID, notification.StateHash, scene.ID, publishedStoryID, newStatus, err)
					return fmt.Errorf("ошибка обновления статуса %s для PublishedStory %s: %w", newStatus, publishedStoryID, err)
				}
				log.Printf("[processor][TaskID: %s] Статус PublishedStory %s обновлен на %s.", taskID, publishedStoryID, newStatus)
			}
			// <<< Конец изменения >>>

			// <<< НАЧАЛО: Обновление PlayerGameState >>>
			if notification.GameStateID != "" {
				gameStateID, errParse := uuid.Parse(notification.GameStateID)
				if errParse != nil {
					log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось распарсить GameStateID '%s' из уведомления: %v", taskID, notification.GameStateID, errParse)
					// Продолжаем без обновления GameState?
				} else {
					// Используем GetByID, который нужно добавить в репозиторий
					gameState, errGetState := p.playerGameStateRepo.GetByID(ctx, gameStateID)
					if errGetState != nil {
						log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось получить PlayerGameState по ID %s: %v", taskID, gameStateID, errGetState)
						// Продолжаем без обновления GameState?
					} else {
						// Обновляем PlayerGameState
						if notification.PromptType == sharedMessaging.PromptTypeNovelGameOverCreator {
							gameState.PlayerStatus = sharedModels.PlayerStatusCompleted
							gameState.EndingText = endingText
							now := time.Now().UTC()
							gameState.CompletedAt = &now
							gameState.CurrentSceneID = &scene.ID // Ссылка на сцену с концовкой
						} else { // NovelCreator or NovelFirstSceneCreator
							gameState.PlayerStatus = sharedModels.PlayerStatusPlaying
							gameState.CurrentSceneID = &scene.ID // Ссылка на новую сцену
						}
						gameState.ErrorDetails = nil // Очищаем ошибку при успехе
						gameState.LastActivityAt = time.Now().UTC()

						if _, errSaveState := p.playerGameStateRepo.Save(ctx, gameState); errSaveState != nil {
							log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось сохранить обновленный PlayerGameState ID %s: %v", taskID, gameStateID, errSaveState)
							// Продолжаем, но состояние игрока не обновилось
						} else {
							log.Printf("[processor][TaskID: %s] PlayerGameState ID %s успешно обновлен.", taskID, gameStateID)
						}
					}
				}
			} else {
				log.Printf("[processor][TaskID: %s] GameStateID отсутствует в уведомлении %s, статус игрока не обновлен.", taskID, notification.PromptType)
			}
			// <<< КОНЕЦ: Обновление PlayerGameState >>>

			// Отправка уведомления клиенту.
			// Текущий подход: Отправляем PUSH/WebSocket об обновлении статуса PublishedStory (если он изменился)
			// или об обновлении PlayerGameState. Клиент должен сам перезапросить актуальную сцену
			// (через getPublishedStoryScene) при получении такого уведомления.
			// TODO удален.
			var pubStory *sharedModels.PublishedStory
			var getErr error
			pubStory, getErr = p.publishedRepo.GetByID(dbCtx, publishedStoryID)
			if getErr != nil {
				log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось получить PublishedStory %s для отправки Push: %v", taskID, publishedStoryID, getErr)
			} else {
				// <<< Убираем отправку пушей для StatusCompleted, т.к. PublishedStory статус не меняется >>>
				// if newStatus == sharedModels.StatusReady || newStatus == sharedModels.StatusCompleted || newStatus == sharedModels.StatusError {
				if newStatus == sharedModels.StatusReady || newStatus == sharedModels.StatusError { // Отправляем только для Ready и Error
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
							// } else if newStatus == sharedModels.StatusCompleted { // Убрано
							// 	pushPayload.Notification.Title = "История завершена!"
							// 	pushPayload.Notification.Body = fmt.Sprintf("Прохождение истории '%s' завершено.", storyTitle)
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
			}
			// <<< Конец изменения PUSH >>>
		} else { // notification.Status == Error для Сцены/Концовки
			log.Printf("[processor][TaskID: %s] Уведомление %s Error для PublishedStory %s. Details: %s", taskID, notification.PromptType, publishedStoryID, notification.ErrorDetails)
			// Обновляем статус PublishedStory на Error
			// REMOVED: Не обновляем статус основной истории при ошибке генерации сцены/концовки
			// if err := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, sharedModels.StatusError, nil, nil, nil, &notification.ErrorDetails); err != nil { ... }

			// <<< НАЧАЛО: Обновление PlayerGameState при ошибке >>>
			if notification.GameStateID != "" {
				gameStateID, errParse := uuid.Parse(notification.GameStateID)
				if errParse != nil {
					log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось распарсить GameStateID '%s' из уведомления об ошибке: %v", taskID, notification.GameStateID, errParse)
				} else {
					// Используем GetByID, который нужно добавить в репозиторий
					gameState, errGetState := p.playerGameStateRepo.GetByID(ctx, gameStateID)
					if errGetState != nil {
						log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось получить PlayerGameState по ID %s для обновления ошибки: %v", taskID, gameStateID, errGetState)
					} else {
						gameState.PlayerStatus = sharedModels.PlayerStatusError
						gameState.ErrorDetails = &notification.ErrorDetails
						gameState.LastActivityAt = time.Now().UTC()
						if _, errSaveState := p.playerGameStateRepo.Save(ctx, gameState); errSaveState != nil {
							log.Printf("[processor][TaskID: %s] ОШИБКА: Не удалось сохранить PlayerGameState ID %s со статусом Error: %v", taskID, gameStateID, errSaveState)
						} else {
							log.Printf("[processor][TaskID: %s] PlayerGameState ID %s успешно обновлен на статус Error.", taskID, gameStateID)
						}
					}
				}
			} else {
				log.Printf("[processor][TaskID: %s] GameStateID отсутствует в уведомлении об ошибке %s, статус игрока не обновлен.", taskID, notification.PromptType)
			}
			// <<< КОНЕЦ: Обновление PlayerGameState при ошибке >>>
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
	ctx         context.Context    // <<< ДОБАВЛЕНО: Контекст для управления горутинами
	cancelFunc  context.CancelFunc // <<< ДОБАВЛЕНО: Функция отмены контекста
	// !!! ДОБАВЛЕНО: Зависимости для передачи в процессор
	storyRepo           interfaces.StoryConfigRepository // Используем shared интерфейс
	publishedRepo       interfaces.PublishedStoryRepository
	sceneRepo           interfaces.StorySceneRepository // !!! Добавлено sceneRepo
	playerGameStateRepo interfaces.PlayerGameStateRepository
	clientPub           ClientUpdatePublisher
	taskPub             TaskPublisher
	pushPub             PushNotificationPublisher // <<< Добавляем издателя push-уведомлений
	config              *config.Config            // <<< ДОБАВЛЕНО: Ссылка на конфиг
}

// NewNotificationConsumer создает нового консьюмера уведомлений.
func NewNotificationConsumer(
	conn *amqp.Connection,
	repo interfaces.StoryConfigRepository, // Используем shared интерфейс
	publishedRepo interfaces.PublishedStoryRepository,
	sceneRepo interfaces.StorySceneRepository, // !!! Добавлено sceneRepo
	playerGameStateRepo interfaces.PlayerGameStateRepository, // <<< ДОБАВЛЕНО
	clientPub ClientUpdatePublisher,
	taskPub TaskPublisher,
	pushPub PushNotificationPublisher, // <<< Добавляем pushPub
	queueName string,
	cfg *config.Config) (*NotificationConsumer, error) { // <<< ДОБАВЛЕН ПАРАМЕТР cfg
	processor := NewNotificationProcessor(
		repo,
		publishedRepo,
		sceneRepo,
		playerGameStateRepo, // <<< ПЕРЕДАЕМ
		clientPub,
		taskPub,
		pushPub, // <<< Передаем pushPub
	)

	ctx, cancel := context.WithCancel(context.Background()) // <<< СОЗДАЕМ КОНТЕКСТ ЗДЕСЬ

	consumer := &NotificationConsumer{
		conn:                conn,
		processor:           processor,
		queueName:           queueName,
		stopChannel:         make(chan struct{}),
		ctx:                 ctx,    // <<< СОХРАНЯЕМ КОНТЕКСТ
		cancelFunc:          cancel, // <<< СОХРАНЯЕМ ФУНКЦИЮ ОТМЕНЫ
		storyRepo:           repo,   // Сохраняем зависимости для Stop
		publishedRepo:       publishedRepo,
		sceneRepo:           sceneRepo,
		playerGameStateRepo: playerGameStateRepo, // <<< Сохраняем
		clientPub:           clientPub,
		taskPub:             taskPub,
		pushPub:             pushPub,
		config:              cfg, // <<< СОХРАНЯЕМ КОНФИГ
	}
	return consumer, nil
}

// StartConsuming запускает прослушивание очереди уведомлений.
func (c *NotificationConsumer) StartConsuming() error {
	ch, err := c.conn.Channel()
	if err != nil {
		return fmt.Errorf("не удалось открыть канал RabbitMQ: %w", err)
	}
	// Не закрываем канал здесь, так как он может быть нужен для переподключения
	// defer ch.Close()

	q, err := ch.QueueDeclare(
		c.queueName,
		true,  // durable
		false, // delete when unused
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		return fmt.Errorf("не удалось объявить очередь '%s': %w", c.queueName, err)
	}

	// Устанавливаем prefetch count для ограничения количества сообщений, получаемых за раз
	// Используем значение из конфига
	consumerConcurrency := c.config.ConsumerConcurrency
	if consumerConcurrency <= 0 {
		log.Printf("[WARN] ConsumerConcurrency в конфиге <= 0, используется значение по умолчанию 10")
		consumerConcurrency = 10
	}
	if err := ch.Qos(
		consumerConcurrency, // prefetch count
		0,                   // prefetch size
		false,               // global
	); err != nil {
		return fmt.Errorf("не удалось установить Qos: %w", err)
	}

	msgs, err := ch.Consume(
		q.Name,
		"gameplay-consumer", // consumer tag
		false,               // auto-ack (устанавливаем в false для ручного подтверждения)
		false,               // exclusive
		false,               // no-local
		false,               // no-wait
		nil,                 // args
	)
	if err != nil {
		return fmt.Errorf("не удалось зарегистрировать консьюмера: %w", err)
	}

	log.Printf(" [*] Ожидание уведомлений в очереди '%s'. Для выхода нажмите CTRL+C", q.Name)

	// <<< Запускаем обработчики в горутинах >>>
	// Используем consumerConcurrency из конфига
	sem := make(chan struct{}, consumerConcurrency) // Семафор для ограничения concurrency

	go func() {
		for {
			select {
			case d, ok := <-msgs:
				if !ok {
					log.Println("Канал сообщений RabbitMQ закрыт. Завершение работы консьюмера...")
					// Канал закрыт, возможно, из-за Stop() или проблем с соединением
					// Даем существующим обработчикам шанс завершиться
					// c.wg.Wait() // Не нужно здесь, Wait вызывается в Stop()
					return
				}

				// Получаем "слот" для обработки
				sem <- struct{}{}
				c.wg.Add(1) // Увеличиваем счетчик активных обработчиков

				// Запускаем обработку в отдельной горутине
				go func(delivery amqp.Delivery) {
					defer func() {
						<-sem       // Освобождаем слот
						c.wg.Done() // Уменьшаем счетчик
					}()

					log.Printf("[handler] Получено сообщение: TaskID %s", delivery.MessageId)

					// Используем контекст с таймаутом 30с. TODO про настройку удален.
					handlerCtx, handlerCancel := context.WithTimeout(c.ctx, 30*time.Second) // <<< ИСПОЛЬЗУЕМ СОХРАНЕННЫЙ КОНТЕКСТ
					defer handlerCancel()

					// Определяем ID из тела сообщения. TODO про способ передачи удален.
					var tempNotification sharedMessaging.NotificationPayload
					if err := json.Unmarshal(delivery.Body, &tempNotification); err != nil {
						log.Printf("[handler][TaskID: %s] Ошибка парсинга тела сообщения для извлечения ID: %v. Сообщение будет отклонено (Nack, requeue=false).", delivery.MessageId, err)
						_ = delivery.Nack(false, false) // Отклоняем без повторной постановки
						return
					}
					storyConfigUUID, _ := uuid.Parse(tempNotification.StoryConfigID)

					if err := c.processor.Process(handlerCtx, delivery.Body, storyConfigUUID); err != nil {
						// Если Process вернул ошибку, логируем и отправляем Nack
						log.Printf("[handler][TaskID: %s] Ошибка обработки уведомления: %v. Сообщение будет отклонено (Nack, requeue=false).", delivery.MessageId, err)
						// requeue=false используется, т.к. предполагается наличие DLQ для постоянных ошибок.
						// TODO удален.
						_ = delivery.Nack(false, false)
					} else {
						// Успешная обработка, подтверждаем сообщение
						log.Printf("[handler][TaskID: %s] Уведомление успешно обработано. Отправка Ack.", delivery.MessageId)
						_ = delivery.Ack(false)
					}
				}(d)

			case <-c.stopChannel:
				log.Println("Получен сигнал остановки. Завершение работы консьюмера...")
				// Даем существующим обработчикам шанс завершиться
				// c.wg.Wait() // Не нужно здесь, Wait вызывается в Stop()
				return
			case <-c.ctx.Done(): // <<< ИСПОЛЬЗУЕМ СОХРАНЕННЫЙ КОНТЕКСТ
				log.Println("Контекст консьюмера отменен. Завершение работы...")
				return
			}
		}
	}()

	return nil
}

// Stop останавливает консьюмера.
func (c *NotificationConsumer) Stop() {
	log.Println("Остановка NotificationConsumer...")
	close(c.stopChannel)
	c.cancelFunc() // Отменяем контекст для работающих горутин
	c.wg.Wait()    // Ждем завершения всех активных обработчиков
	log.Println("NotificationConsumer остановлен.")
}

// --- Вспомогательные функции ---

// castToStringSlice пытается преобразовать []interface{} в []string.
func castToStringSlice(slice []interface{}) []string {
	if slice == nil {
		return nil
	}
	strSlice := make([]string, 0, len(slice))
	for _, item := range slice {
		if str, ok := item.(string); ok {
			strSlice = append(strSlice, str)
		}
	}
	return strSlice
}
