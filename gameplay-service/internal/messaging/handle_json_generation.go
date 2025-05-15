package messaging

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"novel-server/shared/constants"
	"novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
)

// handleJsonGenerationResult обрабатывает результат задачи структурирования сцены в JSON.
func (p *NotificationProcessor) handleJsonGenerationResult(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	log := p.logger.With(
		zap.String("task_id", taskID),
		zap.String("published_story_id", publishedStoryID.String()),
		zap.String("prompt_type", string(notification.PromptType)),
		zap.String("state_hash", notification.StateHash),
		zap.String("game_state_id", notification.GameStateID),
	)
	log.Info("Processing JsonGeneration result")

	// Parse GameStateID
	gameStateID, err := parseUUIDField(notification.GameStateID, "GameStateID")
	if err != nil {
		log.Error("Invalid GameStateID in JsonGenerationResult", zap.Error(err))
		return fmt.Errorf("invalid GameStateID: %w", err)
	}

	// <<< НАЧАЛО: Проверка статуса GameState >>>
	dbCheckCtx, cancelCheck := context.WithTimeout(ctx, 5*time.Second)
	gameState, errGetState := p.playerGameStateRepo.GetByID(dbCheckCtx, p.db, gameStateID)
	cancelCheck()
	if errGetState != nil {
		log.Error("Failed to get PlayerGameState for JSON generation status check", zap.Error(errGetState))
		return fmt.Errorf("failed to get PlayerGameState %s for status check: %w", gameStateID, errGetState) // NACK
	}
	// Ожидаем, что GameState все еще в GeneratingScene, так как JsonGeneration - это подшаг
	if gameState.PlayerStatus != sharedModels.PlayerStatusGeneratingScene {
		log.Warn("PlayerGameState not in GeneratingScene status for JsonGeneration result, skipping.", zap.String("current_status", string(gameState.PlayerStatus)))
		return nil // Ack, дубликат или устаревшее
	}
	// <<< КОНЕЦ: Проверка статуса GameState >>>

	// Получаем генерационный результат из БД
	genResult, errGet := p.genResultRepo.GetByTaskID(ctx, taskID)
	if errGet != nil {
		log.Error("Failed to get GenerationResult for JsonGeneration", zap.Error(errGet))
		return fmt.Errorf("failed to get GenerationResult for JsonGeneration: %w", errGet)
	}
	if genResult.Error != "" {
		// Ошибка генерации JSON — корректная обработка в зависимости от stateHash
		errDetails := fmt.Sprintf("generation result error for JsonGeneration: %s", genResult.Error)
		if notification.StateHash == sharedModels.InitialStateHash {
			p.handleStoryError(ctx, publishedStoryID, notification.UserID, errDetails, constants.WSEventStoryError)
		} else {
			p.handleGameStateError(ctx, gameStateID, notification.UserID, errDetails)
		}
		return nil
	}
	rawJSON := genResult.GeneratedText

	// Строгая проверка и парсинг JSON структуры сцены
	_, err = decodeStrictJSON[interface{}](rawJSON)
	if err != nil {
		errMsg := fmt.Sprintf("failed to parse structured JSON: %v", err)
		if notification.StateHash == sharedModels.InitialStateHash {
			p.handleStoryError(ctx, publishedStoryID, notification.UserID, errMsg, constants.WSEventStoryError)
		} else {
			p.handleGameStateError(ctx, gameStateID, notification.UserID, errMsg)
		}
		return nil
	}

	// <<< НАЧАЛО: Транзакционная обработка >>>
	errTx := p.withTransaction(ctx, func(tx interfaces.DBTX) error {
		progress := &sharedModels.PlayerProgress{
			PublishedStoryID: publishedStoryID,
			CurrentStateHash: notification.StateHash,
		}
		if _, upErr := p.playerProgressRepo.UpsertByHash(ctx, tx, progress); upErr != nil {
			p.logger.Error("Failed to upsert PlayerProgress for JsonGeneration", zap.Error(upErr))
			return upErr
		}
		scene, findErr := p.sceneRepo.FindByStoryAndHash(ctx, tx, publishedStoryID, notification.StateHash)
		if findErr == nil {
			if contentErr := p.sceneRepo.UpdateContent(ctx, tx, scene.ID, []byte(rawJSON)); contentErr != nil {
				p.logger.Error("Failed to update StoryScene content for JsonGeneration", zap.Error(contentErr))
				return contentErr
			}
		}
		gs, gsErr := p.playerGameStateRepo.GetByID(ctx, tx, gameStateID)
		if gsErr != nil {
			p.logger.Error("Failed to get PlayerGameState by ID for JsonGeneration", zap.Error(gsErr))
			return gsErr
		}
		if gs.PlayerStatus == sharedModels.PlayerStatusGeneratingScene {
			if scene != nil {
				gs.CurrentSceneID = uuid.NullUUID{UUID: scene.ID, Valid: true}
			}
			gs.PlayerStatus = sharedModels.PlayerStatusPlaying
			gs.ErrorDetails = nil
			gs.LastActivityAt = time.Now().UTC()
			if _, saveErr := p.playerGameStateRepo.Save(ctx, tx, gs); saveErr != nil {
				p.logger.Error("Failed to save PlayerGameState for JsonGeneration", zap.Error(saveErr))
				return saveErr
			}
		}
		if notification.StateHash == sharedModels.InitialStateHash {
			stepComplete := sharedModels.StepComplete
			if updateErr := p.publishedRepo.UpdateStatusFlagsAndDetails(ctx, tx, publishedStoryID, sharedModels.StatusReady, false, false, 0, 0, 0, nil, &stepComplete); updateErr != nil {
				p.logger.Error("Failed to update PublishedStory status to Ready after initial scene JSON generation", zap.Error(updateErr))
				return updateErr
			}
		}
		return nil
	})
	if errTx != nil {
		errMsg := fmt.Sprintf("internal server error (db operation): %v", errTx)
		if notification.StateHash == sharedModels.InitialStateHash {
			p.handleStoryError(ctx, publishedStoryID, notification.UserID, errMsg, constants.WSEventStoryError)
		} else {
			p.handleGameStateError(ctx, gameStateID, notification.UserID, errMsg)
		}
		return fmt.Errorf("transaction block failed: %w", errTx)
	}
	// <<< КОНЕЦ: Транзакционная обработка >>>

	// <<< Уведомления ВНЕ транзакции >>>
	// Запускаем уведомления о готовности истории (асинхронно), если это была начальная сцена
	if notification.StateHash == sharedModels.InitialStateHash {
		go func() {
			notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer notifyCancel()
			// Получаем актуальное состояние истории для уведомлений
			storyForNotify, errGetNotify := p.publishedRepo.GetByID(notifyCtx, p.db, publishedStoryID)
			if errGetNotify != nil {
				log.Error("Failed to get story for Ready notifications after initial scene JSON commit", zap.Error(errGetNotify))
				return
			}
			p.publishClientStoryUpdateOnReady(storyForNotify)                 // Уведомление через WebSocket
			p.publishPushNotificationForStoryReady(notifyCtx, storyForNotify) // Push уведомление
		}()
	}

	// Уведомляем клиента через WebSocket об обновлении GameState (если не начальная сцена)
	if notification.StateHash != sharedModels.InitialStateHash {
		// Получаем свежие данные сцены и статуса ИЗ БД, так как они могли измениться
		finalGs, errGetFinalGs := p.playerGameStateRepo.GetByID(context.Background(), p.db, gameStateID)
		if errGetFinalGs != nil {
			log.Error("Failed to get final GameState for client notification", zap.Error(errGetFinalGs))
		} else {
			update := sharedModels.ClientStoryUpdate{
				ID:         gameStateID.String(),
				UserID:     notification.UserID,
				UpdateType: sharedModels.UpdateTypeGameState,
				Status:     string(finalGs.PlayerStatus), // Используем актуальный статус
				StateHash:  notification.StateHash,
			}
			if finalGs.CurrentSceneID.Valid {
				sid := finalGs.CurrentSceneID.UUID.String()
				update.SceneID = &sid
			}
			if errPub := p.clientPub.PublishClientUpdate(context.Background(), update); errPub != nil {
				log.Error("Failed to publish ClientStoryUpdate for JsonGeneration (non-initial)", zap.Error(errPub))
			}
		}
	}

	log.Info("JsonGeneration result processed and notified to client")
	return nil // ACK
}
