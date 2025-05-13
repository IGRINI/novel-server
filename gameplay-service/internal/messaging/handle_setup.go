package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"novel-server/shared/constants"
	"novel-server/shared/database"
	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	"novel-server/shared/models"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/notifications"
	"novel-server/shared/utils"
)

// SetupPromptResult - структура для разбора JSON результата от PromptTypeStorySetup
type SetupPromptResult struct {
	Result        string `json:"result"` // Текст первой сцены (повествование)
	PreviewPrompt string `json:"pr"`     // Промпт для генерации превью-изображения
}

func (p *NotificationProcessor) publishStoryUpdateViaRabbitMQ(ctx context.Context, story *sharedModels.PublishedStory, eventType string, errorMsg *string) {
	if story == nil {
		p.logger.Error("Attempted to publish story update for nil PublishedStory")
		return
	}

	clientUpdate := sharedModels.ClientStoryUpdate{
		ID:           story.ID.String(), // Всегда ID PublishedStory
		UserID:       story.UserID.String(),
		UpdateType:   sharedModels.UpdateTypeStory, // Всегда Story
		Status:       string(story.Status),
		ErrorDetails: errorMsg,
		StoryTitle:   story.Title,
	}

	// Логирование перед отправкой
	p.logger.Info("Attempting to publish client story update via RabbitMQ...",
		zap.String("update_type", string(clientUpdate.UpdateType)),
		zap.String("id", clientUpdate.ID),
		zap.String("status", clientUpdate.Status),
		zap.String("ws_event", eventType), // Передаем исходное событие для логов
	)

	// Отправка через RabbitMQ паблишер
	wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer wsCancel()
	if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdate); errWs != nil {
		p.logger.Error("Error sending ClientStoryUpdate (Story) via RabbitMQ", zap.Error(errWs),
			zap.String("storyID", clientUpdate.ID),
		)
	} else {
		p.logger.Info("ClientStoryUpdate (Story) sent successfully via RabbitMQ",
			zap.String("storyID", clientUpdate.ID),
			zap.String("status", clientUpdate.Status),
			zap.String("ws_event", eventType),
		)
	}

	// Отправка через NATS не требуется, т.к. websocket-service слушает RabbitMQ.
}

// handleNovelSetupNotification обрабатывает результат задачи PromptTypeStorySetup.
// Обновляет Setup, определяет следующий статус, обновляет PublishedStory в транзакции
// и вызывает gameLoopService.DispatchNextGenerationTask для запуска следующего шага.
func (p *NotificationProcessor) handleNovelSetupNotification(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) (err error) {
	taskID := notification.TaskID
	operationCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	log := p.logger.With(
		zap.String("task_id", taskID),
		zap.String("published_story_id", publishedStoryID.String()),
		zap.String("prompt_type", string(notification.PromptType)),
	)
	log.Info("Processing NovelSetup result")

	// Переменные для данных, которые понадобятся ПОСЛЕ коммита
	var finalSetupResult SetupPromptResult
	var finalErrorDetails *string // Для уведомления об ошибке
	var commitSuccessful bool = false
	var storyForNotification *sharedModels.PublishedStory // Для уведомления после ошибки

	// <<< НАЧАЛО: Транзакционная логика >>>
	tx, err := p.db.Begin(operationCtx)
	if err != nil {
		log.Error("Failed to begin transaction for handleNovelSetupNotification", zap.Error(err))
		return fmt.Errorf("failed to begin db transaction: %w", err) // NACK
	}
	// Гарантируем Rollback или Commit
	defer func() {
		if r := recover(); r != nil {
			log.Error("Panic recovered during handleNovelSetupNotification, rolling back transaction", zap.Any("panic", r))
			_ = tx.Rollback(context.Background())                  // Используем background context для rollback при панике
			err = fmt.Errorf("panic during setup handling: %v", r) // Перезаписываем err
			// Уведомление об ошибке после отката
			errStr := err.Error()
			finalErrorDetails = &errStr
			// Попытаться получить ID пользователя для уведомления
			// (storyForNotification может быть nil, если паника до его получения)
			if storyForNotification != nil {
				go p.publishStoryUpdateViaRabbitMQ(context.Background(), storyForNotification, constants.WSEventSetupError, finalErrorDetails)
			}

		} else if err != nil {
			// Если err уже установлен (NACK или другая ошибка), откатываем
			log.Warn("Rolling back transaction due to error during handleNovelSetupNotification", zap.Error(err))
			rollbackErr := tx.Rollback(operationCtx)
			if rollbackErr != nil {
				log.Error("Failed to rollback transaction after error", zap.Error(rollbackErr))
			}
			// Уведомление об ошибке после отката
			if finalErrorDetails != nil && storyForNotification != nil { // Если ошибка была обработана через handleNovelSetupErrorTx
				go p.publishStoryUpdateViaRabbitMQ(context.Background(), storyForNotification, constants.WSEventSetupError, finalErrorDetails)
			}
		} else {
			// Если ошибок не было, коммитим
			log.Info("Attempting to commit handleNovelSetupNotification transaction")
			commitErr := tx.Commit(operationCtx)
			if commitErr != nil {
				log.Error("Failed to commit handleNovelSetupNotification transaction", zap.Error(commitErr))
				err = fmt.Errorf("failed to commit db transaction: %w", commitErr) // Устанавливаем err -> NACK
				// Уведомление об ошибке после неудачного коммита (используем старое состояние)
				if storyForNotification != nil {
					errStr := err.Error()
					go p.publishStoryUpdateViaRabbitMQ(context.Background(), storyForNotification, constants.WSEventSetupError, &errStr)
				}
			} else {
				log.Info("handleNovelSetupNotification transaction committed successfully")
				commitSuccessful = true // Флаг для запуска задач после коммита
			}
		}
	}() // Конец defer

	// Используем транзакционные репозитории
	publishedRepoTx := database.NewPgPublishedStoryRepository(tx, p.logger)
	genResultRepoTx := database.NewPgGenerationResultRepository(tx, p.logger)
	imageReferenceRepoTx := database.NewPgImageReferenceRepository(tx, p.logger)

	// Получаем историю ВНУТРИ транзакции
	publishedStory, errGetStory := publishedRepoTx.GetByID(operationCtx, tx, publishedStoryID)
	if errGetStory != nil {
		log.Error("CRITICAL ERROR: Error getting PublishedStory for Setup update within transaction", zap.Error(errGetStory))
		err = fmt.Errorf("error getting PublishedStory %s: %w", publishedStoryID, errGetStory) // NACK
		return err                                                                             // Вызовет Rollback
	}
	storyForNotification = publishedStory // Сохраняем для уведомлений в defer

	// Обработка статуса уведомления (ошибки)
	if notification.Status == sharedMessaging.NotificationStatusError {
		log.Warn("NovelSetup task failed via notification status", zap.String("error_details", notification.ErrorDetails))
		finalErrorDetails = &notification.ErrorDetails                                                    // Сохраняем для defer
		err = p.handleNovelSetupErrorTx(operationCtx, tx, publishedStory, notification.ErrorDetails, log) // Используем новую Tx-функцию
		if err != nil {
			log.Error("Error updating story status to Error within transaction", zap.Error(err))
			// err уже установлен, defer вызовет Rollback
		} else {
			err = nil // Успешно обработали ошибку, коммитим обновление статуса на Error
		}
		return err // Вызовет Commit или Rollback в зависимости от err
	}
	if notification.Status != sharedMessaging.NotificationStatusSuccess {
		log.Warn("Unknown notification status for NovelSetup. Ignoring.", zap.String("status", string(notification.Status)))
		// Не делаем ничего, просто коммитим (err == nil)
		return nil // Вызовет Commit
	}

	// Проверка статуса истории
	if publishedStory.Status != sharedModels.StatusSetupPending {
		log.Warn("PublishedStory not in SetupPending status, Setup Success update cancelled.", zap.String("current_status", string(publishedStory.Status)))
		// Не делаем ничего, просто коммитим (err == nil)
		return nil // Вызовет Commit
	}
	log.Info("Setup Success notification received, proceeding with update", zap.String("status_when_received", string(publishedStory.Status)))

	// 1. Получение и проверка результата генерации (используем tx репо)
	var rawGeneratedText string
	var genErr error

	genResult, errGetGen := genResultRepoTx.GetByTaskID(operationCtx, taskID)
	if errGetGen != nil {
		log.Error("DB ERROR (Setup): Could not get GenerationResult by TaskID", zap.Error(errGetGen))
		genErr = fmt.Errorf("failed to fetch generation result: %w", errGetGen)
	} else if genResult.Error != "" {
		log.Error("TASK ERROR (Setup): GenerationResult indicates an error", zap.String("gen_error", genResult.Error))
		genErr = errors.New(genResult.Error)
	} else {
		rawGeneratedText = genResult.GeneratedText
		// Используем переменную для сохранения результата
		if errUnmarshal := utils.DecodeStrict([]byte(rawGeneratedText), &finalSetupResult); errUnmarshal != nil {
			log.Error("SETUP UNMARSHAL ERROR", zap.Error(errUnmarshal), zap.String("json_snippet", utils.StringShort(rawGeneratedText, 200)))
			genErr = fmt.Errorf("generated setup JSON validation failed: %w", errUnmarshal)
		} else if finalSetupResult.Result == "" {
			log.Error("SETUP VALIDATION ERROR: 'result' field is empty")
			genErr = fmt.Errorf("setup JSON is missing 'result' field")
		}
	}

	if genErr != nil {
		log.Error("Processing NovelSetup failed during result fetch/validation", zap.Error(genErr))
		errStr := genErr.Error()
		finalErrorDetails = &errStr // Сохраняем для defer
		err = p.handleNovelSetupErrorTx(operationCtx, tx, publishedStory, errStr, log)
		if err != nil {
			log.Error("Error updating story status to Error within transaction (fetch/validation)", zap.Error(err))
		} else {
			err = nil
		}
		return err
	}
	log.Info("Successfully fetched and validated setup prompt result JSON")

	// 2. Обновление Setup в PublishedStory
	currentSetup := make(map[string]interface{})
	if len(publishedStory.Setup) > 0 && string(publishedStory.Setup) != "null" && string(publishedStory.Setup) != "{}" {
		if errUnmarshal := json.Unmarshal(publishedStory.Setup, &currentSetup); errUnmarshal != nil {
			log.Warn("Failed to unmarshal existing PublishedStory.Setup, starting fresh", zap.Error(errUnmarshal))
			currentSetup = make(map[string]interface{})
		}
	}
	currentSetup["initial_narrative"] = finalSetupResult.Result // Используем сохраненный результат
	if finalSetupResult.PreviewPrompt != "" {
		currentSetup["spi"] = finalSetupResult.PreviewPrompt
		log.Info("Added/Updated 'spi' (StoryPreviewImagePrompt)")
	}
	currentSetup["full_story_setup_result"] = finalSetupResult

	newSetupBytes, errMarshal := json.Marshal(currentSetup)
	if errMarshal != nil {
		log.Error("Failed to marshal updated Setup JSON", zap.Error(errMarshal))
		err = fmt.Errorf("failed to marshal updated setup: %w", errMarshal)
		errStr := err.Error()
		finalErrorDetails = &errStr                                                    // Сохраняем для defer
		err = p.handleNovelSetupErrorTx(operationCtx, tx, publishedStory, errStr, log) // Попытка обновить статус на Error
		if err != nil {
			log.Error("Error updating story status to Error within transaction (marshal)", zap.Error(err))
		} else {
			err = nil
		}
		return err
	}
	log.Info("Marshalled updated setup successfully")

	// 3. Определение необходимости генерации превью-изображения (используем tx репо)
	var needsPreviewImage bool = false
	if finalSetupResult.PreviewPrompt != "" { // Используем сохраненный результат
		imageRef := fmt.Sprintf("history_preview_%s", publishedStoryID.String())
		_, errCheck := imageReferenceRepoTx.GetImageURLByReference(operationCtx, imageRef)
		if errors.Is(errCheck, sharedModels.ErrNotFound) {
			log.Debug("Preview image needs generation", zap.String("image_ref", imageRef))
			needsPreviewImage = true
		} else if errCheck != nil {
			log.Error("Error checking Preview ImageRef in DB", zap.String("image_ref", imageRef), zap.Error(errCheck))
			// Не фатально, но логируем. Продолжаем без превью.
		} else {
			log.Debug("Preview image already exists", zap.String("image_ref", imageRef))
		}
	} else {
		log.Info("StoryPreviewImagePrompt (pr) is empty, no preview needed.")
	}

	// 4. Определение следующего статуса и флагов
	var nextStatus sharedModels.StoryStatus
	var areImagesPending bool
	var isFirstScenePending bool = false // Сцена (initial_narrative) готова к генерации JSON

	if needsPreviewImage {
		nextStatus = sharedModels.StatusImageGenerationPending
		areImagesPending = true
	} else {
		// Если изображение не нужно, сразу переходим к ожиданию генерации JSON
		nextStatus = sharedModels.StatusJsonGenerationPending
		areImagesPending = false
	}

	// 5. Обновление статуса, флагов, Setup и InternalStep в БД (используем tx репо)
	var nextInternalStep models.InternalGenerationStep
	if nextStatus == sharedModels.StatusImageGenerationPending {
		nextInternalStep = models.StepCoverImageGeneration
	} else {
		nextInternalStep = models.StepInitialSceneJSON
	}
	errUpdateSetup := publishedRepoTx.UpdateStatusFlagsAndSetup(operationCtx, tx, publishedStoryID, nextStatus, newSetupBytes, isFirstScenePending, areImagesPending, &nextInternalStep)
	if errUpdateSetup != nil {
		log.Error("CRITICAL ERROR: Failed to update status, flags and Setup", zap.Error(errUpdateSetup))
		err = fmt.Errorf("error updating story %s: %w", publishedStoryID, errUpdateSetup) // NACK
		return err                                                                        // Вызовет Rollback
	}
	log.Info("PublishedStory status, flags, setup and step updated",
		zap.String("new_status", string(nextStatus)),
		zap.Bool("is_first_scene_pending", isFirstScenePending),
		zap.Bool("are_images_pending", areImagesPending),
		zap.Any("internal_step", nextInternalStep),
	)

	// 6. Вызов диспетчера задач УДАЛЕН ОТСЮДА - будет вызван после коммита

	log.Info("NovelSetup processing completed successfully within transaction block.")
	// Если дошли сюда без ошибок, err == nil, defer вызовет Commit

	// Запускаем задачи/уведомления ПОСЛЕ успешного коммита
	if commitSuccessful {
		go p.dispatchTasksAfterSetupCommit(publishedStoryID, finalSetupResult)
	}

	return err // err будет nil, если коммит успешен, иначе - ошибка коммита
}

// handleNovelSetupErrorTx - версия handleNovelSetupError, работающая внутри транзакции
func (p *NotificationProcessor) handleNovelSetupErrorTx(ctx context.Context, tx interfaces.DBTX, story *sharedModels.PublishedStory, errorDetails string, logger *zap.Logger) error {
	logger.Error("Handling NovelSetup error within transaction", zap.String("error_details", errorDetails))

	publishedRepoTx := database.NewPgPublishedStoryRepository(tx, p.logger)
	// Передаем nil для internalStep при ошибке
	errUpdate := publishedRepoTx.UpdateStatusFlagsAndDetails(ctx, tx, story.ID, sharedModels.StatusError, false, false, &errorDetails, nil)
	if errUpdate != nil {
		logger.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error within transaction", zap.Error(errUpdate))
		// Возвращаем ошибку, чтобы вызвавшая функция знала о проблеме и откатила транзакцию
		return fmt.Errorf("failed to update story status to Error: %w", errUpdate)
	}
	logger.Info("PublishedStory status updated to Error due to setup processing error (within tx)")

	// Отправка WebSocket уведомления об ошибке (происходит ПОСЛЕ коммита/отката основной функции)
	// Эта горутина запускается уже после возврата из handleNovelSetupErrorTx
	// go p.publishStoryUpdateViaRabbitMQ(context.Background(), story, constants.WSEventSetupError, &errorDetails)

	return nil // Успешно обновили статус на Error в рамках транзакции
}

// publishPushNotificationForSetupPending использует notifications.BuildSetupPendingPushPayload
func (p *NotificationProcessor) publishPushNotificationForSetupPending(ctx context.Context, story *sharedModels.PublishedStory) {
	payload, err := notifications.BuildSetupPendingPushPayload(story)
	if err != nil {
		p.logger.Error("Failed to build push notification payload for setup pending", zap.Error(err))
		return
	}

	if err := p.pushPub.PublishPushNotification(ctx, *payload); err != nil {
		p.logger.Error("Failed to publish push notification event for setup pending",
			zap.String("userID", payload.UserID.String()),
			zap.String("publishedStoryID", story.ID.String()),
			zap.Error(err),
		)
	} else {
		p.logger.Info("Push notification event for setup pending published successfully",
			zap.String("userID", payload.UserID.String()),
			zap.String("publishedStoryID", story.ID.String()),
		)
	}
}

// <<< НОВОЕ: Функция для запуска задач ПОСЛЕ коммита >>>
func (p *NotificationProcessor) dispatchTasksAfterSetupCommit(storyID uuid.UUID, setupResult SetupPromptResult) {
	log := p.logger.With(zap.String("published_story_id", storyID.String()))
	log.Info("Dispatching next task via GameLoopService (after commit)")

	// Создаем новый контекст для вызова сервиса, т.к. транзакционный контекст завершен
	dispatchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Используем обычный pool соединений, т.к. транзакция завершена
	// gameLoopService должен иметь доступ к pool или создавать его сам
	// Важно: DispatchNextGenerationTask должен быть потокобезопасным и
	// корректно работать вне транзакции, используя pool.
	// Он должен сам загрузить актуальное состояние PublishedStory по ID.
	// Внутри DispatchNextGenerationTask не должно быть зависимостей от переданной Tx.
	// Если DispatchNextGenerationTask ТРЕБУЕТ транзакцию, этот подход не сработает
	// и потребуется более сложная логика (например, отдельная очередь для диспетчеризации).
	// ПРЕДПОЛАГАЕМ, ЧТО DispatchNextGenerationTask МОЖЕТ РАБОТАТЬ ВНЕ ТРАНЗАКЦИИ:

	// NOTE: DispatchNextGenerationTask теперь вызывается без транзакции (tx=nil)
	// и ему нужно будет самому получить актуальное состояние истории.
	// Передаем narrative (setupResult.Result) как и раньше.
	errDispatch := p.gameLoopService.DispatchNextGenerationTask(dispatchCtx, p.db, storyID, nil, nil, setupResult.Result)
	if errDispatch != nil {
		log.Error("Failed to dispatch next generation task AFTER COMMIT", zap.Error(errDispatch))
		// Ошибка на этом этапе критична, т.к. статус обновлен, а задача не ушла.
		// Требуется мониторинг или механизм retry для таких случаев.
		// Можно попытаться обновить статус обратно на Error, но это может вызвать циклы.
	} else {
		log.Info("Next task dispatched successfully (after commit)")
	}

	// Уведомления клиенту также здесь, после успешного диспетча (или независимо?)
	// Получаем свежее состояние для уведомлений
	freshStory, errGet := p.publishedRepo.GetByID(dispatchCtx, p.db, storyID)
	if errGet != nil {
		log.Error("Failed to get fresh story state for notifications after setup commit", zap.Error(errGet))
		// Не критично для основного потока, но логируем
	} else {
		go p.publishStoryUpdateViaRabbitMQ(context.Background(), freshStory, constants.WSEventSetupGenerated, nil)
		go p.publishPushNotificationForSetupPending(context.Background(), freshStory)
	}
}
