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
	"novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/notifications"
)

// handleJsonGenerationResult обрабатывает результат задачи JSON генерации сцены
func (p *NotificationProcessor) handleJsonGenerationResult(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	log := p.logger.With(
		zap.String("task_id", taskID),
		zap.String("published_story_id", publishedStoryID.String()),
		zap.String("prompt_type", string(notification.PromptType)),
	)
	log.Info("Processing JSON generation result")

	// Проверяем статус истории
	publishedStory, err := p.ensureStoryStatus(ctx, publishedStoryID, sharedModels.StatusJsonGenerationPending)
	if err != nil {
		return err
	}
	if publishedStory == nil {
		return nil // История не в нужном статусе
	}

	// Обработка ошибки
	if notification.Status == sharedMessaging.NotificationStatusError {
		userID, _ := uuid.Parse(notification.UserID)
		return p.errorHandler.HandleStoryError(ctx, p.db, publishedStoryID, userID, notification.ErrorDetails, constants.WSEventStoryError)
	}

	// Обработка успешного результата
	if notification.Status == sharedMessaging.NotificationStatusSuccess {
		log.Info("JSON generation task successful, processing results.")

		// Получаем результат генерации
		genResult, genErr := p.genResultRepo.GetByTaskID(ctx, taskID)
		if genErr != nil {
			userID, _ := uuid.Parse(notification.UserID)
			errMsg := fmt.Sprintf("failed to fetch JSON generation result: %v", genErr)
			return p.errorHandler.HandleStoryError(ctx, p.db, publishedStoryID, userID, errMsg, constants.WSEventStoryError)
		}

		if genResult.Error != "" {
			log.Warn("GenerationResult for JSON generation indicates an error", zap.String("gen_error", genResult.Error))
			userID, _ := uuid.Parse(notification.UserID)
			errDetails := fmt.Sprintf("JSON generation error: %s", genResult.Error)
			return p.errorHandler.HandleStoryError(ctx, p.db, publishedStoryID, userID, errDetails, constants.WSEventStoryError)
		}

		// Сохраняем результат JSON генерации
		jsonResult := genResult.GeneratedText
		log.Info("JSON generated successfully", zap.String("json_preview", jsonResult[:min(200, len(jsonResult))]))

		// Используем атомарную операцию для обновления состояния
		return p.txHelper.WithTransaction(ctx, func(ctx context.Context, tx interfaces.DBTX) error {
			// Сохраняем JSON в сцену
			err := p.saveJsonToScene(ctx, publishedStory, jsonResult)
			if err != nil {
				return fmt.Errorf("failed to save JSON to scene: %w", err)
			}

			// Определяем финальный статус на основе StateHash
			var finalStatus sharedModels.StoryStatus
			if p.isInitialScene(publishedStory) {
				finalStatus = sharedModels.StatusReady // Первая сцена -> Ready
			} else {
				finalStatus = sharedModels.StatusReady // Последующие сцены тоже Ready (игра продолжается)
			}

			// Атомарно обновляем статус (без шага, так как это финальное состояние)
			expectedStep := sharedModels.StepInitialSceneJSON
			updatedStory, err := p.stepManager.AtomicUpdateStepAndStatus(
				ctx, tx, publishedStoryID,
				&expectedStep, // ожидаемый текущий шаг
				nil,           // новый шаг (nil для финального состояния)
				finalStatus,   // новый статус
			)
			if err != nil {
				return fmt.Errorf("failed to update story status: %w", err)
			}

			// Уведомляем клиента о завершении
			return p.notifyClientAfterJsonGeneration(ctx, updatedStory, finalStatus)
		})
	}

	log.Warn("Received JSON generation notification with unexpected status",
		zap.String("status", string(notification.Status)))
	return nil
}

// saveJsonToScene сохраняет JSON в соответствующую сцену
func (p *NotificationProcessor) saveJsonToScene(ctx context.Context, story *sharedModels.PublishedStory, jsonContent string) error {
	// Декодируем JSON контент
	var sceneContent sharedModels.SceneContent
	if err := json.Unmarshal([]byte(jsonContent), &sceneContent); err != nil {
		return fmt.Errorf("failed to decode JSON content: %w", err)
	}

	// Определяем StateHash для сцены
	stateHash := sharedModels.InitialStateHash
	if !p.isInitialScene(story) {
		// Для последующих сцен получаем StateHash из задачи генерации
		// StateHash должен передаваться в GenerationTaskPayload при создании задачи
		// Если его нет, используем fallback логику
		stateHash = p.determineStateHashForSubsequentScene(ctx, story)
	}

	// Ищем существующую сцену
	existingScene, err := p.sceneRepo.FindByStoryAndHash(ctx, p.db, story.ID, stateHash)
	if err != nil && !errors.Is(err, sharedModels.ErrNotFound) {
		return fmt.Errorf("failed to find existing scene: %w", err)
	}

	// Кодируем контент для сохранения
	contentBytes, err := json.Marshal(sceneContent)
	if err != nil {
		return fmt.Errorf("failed to marshal scene content: %w", err)
	}

	if existingScene != nil {
		// Обновляем существующую сцену
		if err := p.sceneRepo.UpdateContent(ctx, p.db, existingScene.ID, contentBytes); err != nil {
			return fmt.Errorf("failed to update scene content: %w", err)
		}
		p.logger.Info("Scene content updated",
			zap.String("story_id", story.ID.String()),
			zap.String("scene_id", existingScene.ID.String()),
			zap.String("state_hash", stateHash))
	} else {
		// Создаем новую сцену
		newScene := &sharedModels.StoryScene{
			ID:               uuid.New(),
			PublishedStoryID: story.ID,
			StateHash:        stateHash,
			Content:          json.RawMessage(contentBytes),
			CreatedAt:        time.Now().UTC(),
		}

		if err := p.sceneRepo.Create(ctx, p.db, newScene); err != nil {
			return fmt.Errorf("failed to create new scene: %w", err)
		}
		p.logger.Info("New scene created",
			zap.String("story_id", story.ID.String()),
			zap.String("scene_id", newScene.ID.String()),
			zap.String("state_hash", stateHash))
	}

	return nil
}

// determineStateHashForSubsequentScene определяет StateHash для последующих сцен
func (p *NotificationProcessor) determineStateHashForSubsequentScene(ctx context.Context, story *sharedModels.PublishedStory) string {
	// Получаем последнюю активную игровую сессию для этой истории
	gameStates, err := p.playerGameStateRepo.ListByStoryID(ctx, p.db, story.ID)
	if err != nil || len(gameStates) == 0 {
		p.logger.Warn("No game states found for story, using fallback state hash",
			zap.String("story_id", story.ID.String()))
		return fmt.Sprintf("scene_%d", time.Now().Unix()) // Fallback
	}

	// Ищем активную игровую сессию
	for _, gs := range gameStates {
		if gs.PlayerStatus == sharedModels.PlayerStatusPlaying || gs.PlayerStatus == sharedModels.PlayerStatusGeneratingScene {
			// Получаем прогресс игрока
			progress, err := p.playerProgressRepo.GetByID(ctx, p.db, gs.PlayerProgressID)
			if err == nil {
				return progress.CurrentStateHash
			}
		}
	}

	// Если не найдена активная сессия, берем последнюю
	lastGameState := gameStates[len(gameStates)-1]
	progress, err := p.playerProgressRepo.GetByID(ctx, p.db, lastGameState.PlayerProgressID)
	if err != nil {
		p.logger.Warn("Failed to get progress for last game state, using fallback",
			zap.String("story_id", story.ID.String()),
			zap.Error(err))
		return fmt.Sprintf("scene_%d", time.Now().Unix()) // Fallback
	}

	return progress.CurrentStateHash
}

// isInitialScene проверяет, является ли это начальной сценой
func (p *NotificationProcessor) isInitialScene(story *sharedModels.PublishedStory) bool {
	// Проверяем по флагу IsFirstScenePending
	return story.IsFirstScenePending
}

// notifyClientAfterJsonGeneration отправляет уведомления клиенту после генерации JSON
func (p *NotificationProcessor) notifyClientAfterJsonGeneration(ctx context.Context, story *sharedModels.PublishedStory, finalStatus sharedModels.StoryStatus) error {
	var eventType string
	if p.isInitialScene(story) {
		eventType = constants.WSEventStoryReady // Используем существующую константу
	} else {
		eventType = constants.WSEventSceneGenerated
	}

	// Отправляем обновление через RabbitMQ
	p.publishStoryUpdateViaRabbitMQ(ctx, story, eventType, nil)

	// Отправляем push-уведомления
	if p.isInitialScene(story) {
		p.publishPushNotificationForStoryReady(ctx, story)
	} else {
		p.publishPushNotificationForSceneReady(ctx, story)
	}

	return nil
}

// publishStoryUpdateViaRabbitMQ отправляет обновление истории через RabbitMQ
func (p *NotificationProcessor) publishStoryUpdateViaRabbitMQ(ctx context.Context, story *sharedModels.PublishedStory, eventType string, errorMsg *string) {
	clientUpdate := sharedModels.ClientStoryUpdate{ // Используем sharedModels вместо sharedMessaging
		ID:           story.ID.String(),
		UserID:       story.UserID.String(),
		UpdateType:   sharedModels.UpdateTypeStory, // Используем sharedModels
		Status:       string(story.Status),
		ErrorDetails: errorMsg,
		StoryTitle:   story.Title,
	}

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
}

// publishPushNotificationForStoryReady отправляет push-уведомление о готовности истории
func (p *NotificationProcessor) publishPushNotificationForStoryReady(ctx context.Context, story *sharedModels.PublishedStory) {
	// Создаем функцию для получения имени автора
	// В продакшене можно интегрировать с auth-service для получения имени пользователя
	// Пока возвращаем пустую строку, что является допустимым поведением
	getAuthorName := func(userID uuid.UUID) string {
		// Имя автора не критично для push-уведомлений
		// Notification service может использовать userID для получения имени из своего кэша
		return ""
	}

	payload, err := notifications.BuildStoryReadyPushPayload(story, getAuthorName)
	if err != nil {
		p.logger.Error("Failed to build push notification payload for story ready", zap.Error(err))
		return
	}

	pushCtx, pushCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pushCancel()
	if errPush := p.pushPub.PublishPushNotification(pushCtx, *payload); errPush != nil {
		p.logger.Error("Failed to publish push notification for story ready", zap.Error(errPush))
	} else {
		p.logger.Info("Push notification for story ready sent successfully",
			zap.String("story_id", story.ID.String()))
	}
}

// publishPushNotificationForSceneReady отправляет push-уведомление о готовности сцены
func (p *NotificationProcessor) publishPushNotificationForSceneReady(ctx context.Context, story *sharedModels.PublishedStory) {
	// Получаем реальные gameStateID и sceneID
	gameStateID, sceneID, err := p.getRealGameStateAndSceneIDs(ctx, story)
	if err != nil {
		p.logger.Error("Failed to get real gameStateID and sceneID for push notification",
			zap.String("story_id", story.ID.String()),
			zap.Error(err))
		return
	}

	payload, err := notifications.BuildSceneReadyPushPayload(story, gameStateID, sceneID, nil)
	if err != nil {
		p.logger.Error("Failed to build push notification payload for scene ready", zap.Error(err))
		return
	}

	if err := p.pushPub.PublishPushNotification(ctx, *payload); err != nil {
		p.logger.Error("Failed to publish push notification event for scene ready",
			zap.String("userID", payload.UserID.String()),
			zap.String("publishedStoryID", story.ID.String()),
			zap.Error(err))
	} else {
		p.logger.Info("Push notification event for scene ready published successfully",
			zap.String("userID", payload.UserID.String()),
			zap.String("publishedStoryID", story.ID.String()))
	}
}

// getRealGameStateAndSceneIDs получает реальные ID игрового состояния и сцены
func (p *NotificationProcessor) getRealGameStateAndSceneIDs(ctx context.Context, story *sharedModels.PublishedStory) (uuid.UUID, uuid.UUID, error) {
	// Получаем активные игровые состояния для этой истории
	gameStates, err := p.playerGameStateRepo.ListByStoryID(ctx, p.db, story.ID)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("failed to get game states: %w", err)
	}

	if len(gameStates) == 0 {
		return uuid.Nil, uuid.Nil, fmt.Errorf("no game states found for story")
	}

	// Ищем активное игровое состояние
	var activeGameState sharedModels.PlayerGameState
	var found bool
	for _, gs := range gameStates {
		if gs.PlayerStatus == sharedModels.PlayerStatusPlaying || gs.PlayerStatus == sharedModels.PlayerStatusGeneratingScene {
			activeGameState = gs
			found = true
			break
		}
	}

	// Если нет активного, берем последнее
	if !found {
		activeGameState = gameStates[len(gameStates)-1]
	}

	// Получаем ID сцены
	var sceneID uuid.UUID
	if activeGameState.CurrentSceneID.Valid {
		sceneID = activeGameState.CurrentSceneID.UUID
	} else {
		// Если нет текущей сцены, ищем по StateHash
		progress, err := p.playerProgressRepo.GetByID(ctx, p.db, activeGameState.PlayerProgressID)
		if err != nil {
			return uuid.Nil, uuid.Nil, fmt.Errorf("failed to get player progress: %w", err)
		}

		scene, err := p.sceneRepo.FindByStoryAndHash(ctx, p.db, story.ID, progress.CurrentStateHash)
		if err != nil {
			return uuid.Nil, uuid.Nil, fmt.Errorf("failed to find scene by state hash: %w", err)
		}
		sceneID = scene.ID
	}

	return activeGameState.ID, sceneID, nil
}

// rollbackGameStateOnCriticalError откатывает игровое состояние к предыдущему состоянию при критических ошибках
func (p *NotificationProcessor) rollbackGameStateOnCriticalError(ctx context.Context, gameStateID uuid.UUID, userID string, errorDetails string) {
	log := p.logger.With(
		zap.String("game_state_id", gameStateID.String()),
		zap.String("user_id", userID),
		zap.String("error_details", errorDetails),
	)

	log.Info("Starting rollback for game state due to critical error")

	errTx := p.withTransaction(ctx, func(tx interfaces.DBTX) error {
		// Получаем текущее игровое состояние
		gameState, err := p.playerGameStateRepo.GetByID(ctx, tx, gameStateID)
		if err != nil {
			log.Error("Failed to get game state for rollback", zap.Error(err))
			return err
		}

		// Получаем текущий прогресс
		currentProgress, err := p.playerProgressRepo.GetByID(ctx, tx, gameState.PlayerProgressID)
		if err != nil {
			log.Error("Failed to get current progress for rollback", zap.Error(err))
			return err
		}

		// Ищем предыдущий прогресс по SceneIndex
		if currentProgress.SceneIndex > 0 {
			// Есть предыдущий прогресс, ищем его по SceneIndex - 1
			// Нужно найти прогресс с меньшим SceneIndex для той же истории
			// Поскольку нет прямого метода поиска по SceneIndex, попробуем найти через существующие сцены

			// Получаем все сцены для истории и ищем предыдущую
			scenes, err := p.sceneRepo.ListByStoryID(ctx, tx, gameState.PublishedStoryID)
			if err != nil {
				log.Error("Failed to get scenes for rollback", zap.Error(err))
				// Если не можем получить сцены, устанавливаем ошибку
				gameState.PlayerStatus = sharedModels.PlayerStatusError
				gameState.ErrorDetails = &errorDetails
				gameState.LastActivityAt = time.Now().UTC()
			} else {
				// Ищем предыдущую сцену и её прогресс
				var previousProgress *sharedModels.PlayerProgress
				for _, scene := range scenes {
					if scene.StateHash != currentProgress.CurrentStateHash {
						// Проверяем есть ли прогресс для этой сцены
						tempProgress, tempErr := p.playerProgressRepo.GetByStoryIDAndHash(ctx, tx, gameState.PublishedStoryID, scene.StateHash)
						if tempErr == nil && tempProgress.SceneIndex == currentProgress.SceneIndex-1 {
							previousProgress = tempProgress
							break
						}
					}
				}

				if previousProgress != nil {
					// Откатываемся к предыдущему прогрессу
					gameState.PlayerProgressID = previousProgress.ID
					gameState.PlayerStatus = sharedModels.PlayerStatusPlaying
					gameState.ErrorDetails = nil
					gameState.LastActivityAt = time.Now().UTC()

					// Ищем сцену для предыдущего состояния
					scene, sceneErr := p.sceneRepo.FindByStoryAndHash(ctx, tx, gameState.PublishedStoryID, previousProgress.CurrentStateHash)
					if sceneErr == nil && scene != nil {
						gameState.CurrentSceneID = uuid.NullUUID{UUID: scene.ID, Valid: true}
					} else {
						gameState.CurrentSceneID = uuid.NullUUID{Valid: false}
					}

					log.Info("Rolling back to previous progress",
						zap.String("previous_progress_id", previousProgress.ID.String()),
						zap.String("previous_state_hash", previousProgress.CurrentStateHash),
						zap.Int("previous_scene_index", previousProgress.SceneIndex))
				} else {
					// Не найден предыдущий прогресс, устанавливаем ошибку
					gameState.PlayerStatus = sharedModels.PlayerStatusError
					gameState.ErrorDetails = &errorDetails
					gameState.LastActivityAt = time.Now().UTC()
					log.Warn("No previous progress found for rollback")
				}
			}
		} else {
			// Нет предыдущего прогресса (SceneIndex = 0), устанавливаем ошибку
			gameState.PlayerStatus = sharedModels.PlayerStatusError
			gameState.ErrorDetails = &errorDetails
			gameState.LastActivityAt = time.Now().UTC()
			log.Warn("No previous progress found for rollback, at initial scene")
		}

		// Сохраняем обновленное игровое состояние
		if _, saveErr := p.playerGameStateRepo.Save(ctx, tx, gameState); saveErr != nil {
			log.Error("Failed to save game state during rollback", zap.Error(saveErr))
			return saveErr
		}

		return nil
	})

	if errTx != nil {
		log.Error("Failed to rollback game state", zap.Error(errTx))
		// Если откат не удался, просто устанавливаем ошибку
		p.handleGameStateError(ctx, gameStateID, userID, errorDetails)
		return
	}

	// Уведомляем клиента об изменении состояния
	finalGs, errGetFinal := p.playerGameStateRepo.GetByID(ctx, p.db, gameStateID)
	if errGetFinal != nil {
		log.Error("Failed to get final game state for client notification after rollback", zap.Error(errGetFinal))
		return
	}

	update := sharedModels.ClientStoryUpdate{
		ID:         gameStateID.String(),
		UserID:     userID,
		UpdateType: sharedModels.UpdateTypeGameState,
		Status:     string(finalGs.PlayerStatus),
	}

	if finalGs.CurrentSceneID.Valid {
		sid := finalGs.CurrentSceneID.UUID.String()
		update.SceneID = &sid
	}

	if errPub := p.clientPub.PublishClientUpdate(ctx, update); errPub != nil {
		log.Error("Failed to publish client update after rollback", zap.Error(errPub))
	}

	log.Info("Game state rollback completed successfully")
}
