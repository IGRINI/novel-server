package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

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
	gameStateID, err := uuid.Parse(notification.GameStateID)
	if err != nil {
		log.Error("Invalid GameStateID in JsonGenerationResult", zap.Error(err))
		// Если ID невалиден, мы не можем проверить статус. Лучше вернуть ошибку.
		return fmt.Errorf("invalid GameStateID: %w", err)
	} else if notification.GameStateID == "" {
		log.Error("CRITICAL: Received JSON generation notification without GameStateID", zap.String("taskID", taskID))
		return fmt.Errorf("missing GameStateID in JSON notification for task %s", taskID) // NACK
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
		log.Warn("GenerationResult indicates an error in JsonGeneration", zap.String("gen_error", genResult.Error))
		// Обновляем PlayerGameState до статуса Error
		if gs, errGS := p.playerGameStateRepo.GetByID(ctx, p.db, gameStateID); errGS != nil {
			log.Error("Failed to get PlayerGameState for error update", zap.Error(errGS))
		} else {
			gs.PlayerStatus = sharedModels.PlayerStatusError
			errStr := fmt.Sprintf("generation result error for JsonGeneration: %s", genResult.Error)
			gs.ErrorDetails = &errStr
			gs.LastActivityAt = time.Now().UTC()
			if _, errSave := p.playerGameStateRepo.Save(ctx, p.db, gs); errSave != nil {
				log.Error("Failed to save PlayerGameState with Error status", zap.Error(errSave))
			} else {
				// Уведомляем клиента об ошибке GameState
				update := sharedModels.ClientStoryUpdate{
					ID:           gameStateID.String(),
					UserID:       notification.UserID,
					UpdateType:   sharedModels.UpdateTypeGameState,
					Status:       string(sharedModels.PlayerStatusError),
					ErrorDetails: &errStr,
					StateHash:    notification.StateHash,
				}
				if errPub := p.clientPub.PublishClientUpdate(ctx, update); errPub != nil {
					log.Error("Failed to publish ClientStoryUpdate for GameState error", zap.Error(errPub))
				}
			}
		}
		return nil
	}
	rawJSON := genResult.GeneratedText

	// Валидируем JSON
	var structured interface{}
	if errUnm := json.Unmarshal([]byte(rawJSON), &structured); errUnm != nil {
		errMsg := fmt.Sprintf("failed to unmarshal structured JSON: %v", errUnm)
		log.Error(errMsg, zap.Error(errUnm))

		// <<< НАЧАЛО: Исправление обработки ошибок для начальной сцены >>>
		if notification.StateHash == sharedModels.InitialStateHash {
			// Обновляем PublishedStory на Error для начальной сцены
			if errUpdateStory := p.publishedRepo.UpdateStatusAndError(ctx, p.db, publishedStoryID, sharedModels.StatusError, &errMsg); errUpdateStory != nil {
				log.Error("Failed to update PublishedStory status to Error after initial scene JSON unmarshal failure", zap.Error(errUpdateStory))
			}
			// TODO: Уведомить клиента об ошибке PublishedStory
			p.publishClientStoryUpdateOnError(publishedStoryID, uuid.MustParse(notification.UserID), errMsg)
		} else {
			// Обновляем PlayerGameState до статуса Error (для не-начальных сцен)
			if gs, errGS := p.playerGameStateRepo.GetByID(ctx, p.db, gameStateID); errGS != nil {
				log.Error("Failed to get PlayerGameState for JSON error update", zap.Error(errGS))
			} else {
				gs.PlayerStatus = sharedModels.PlayerStatusError
				gs.ErrorDetails = &errMsg
				gs.LastActivityAt = time.Now().UTC()
				if _, errSave := p.playerGameStateRepo.Save(ctx, p.db, gs); errSave != nil {
					log.Error("Failed to save PlayerGameState with JSON error status", zap.Error(errSave))
				} else {
					// Уведомляем клиента об ошибке GameState
					update := sharedModels.ClientStoryUpdate{
						ID:           gameStateID.String(),
						UserID:       notification.UserID,
						UpdateType:   sharedModels.UpdateTypeGameState,
						Status:       string(sharedModels.PlayerStatusError),
						ErrorDetails: &errMsg,
						StateHash:    notification.StateHash,
					}
					if errPub := p.clientPub.PublishClientUpdate(ctx, update); errPub != nil {
						log.Error("Failed to publish ClientStoryUpdate for GameState JSON error", zap.Error(errPub))
					}
				}
			}
		}
		// <<< КОНЕЦ: Исправление обработки ошибок для начальной сцены >>>
		return nil
	}

	// <<< НАЧАЛО: Транзакционная обработка >>>
	tx, errTx := p.db.Begin(ctx)
	if errTx != nil {
		log.Error("Failed to begin transaction for JSON result processing", zap.Error(errTx))
		// Обработка ошибки для начальной сцены или GameState
		// TODO: Вынести обработку ошибок в отдельную функцию, чтобы избежать дублирования
		errMsg := fmt.Sprintf("internal server error (db transaction): %v", errTx)
		if notification.StateHash == sharedModels.InitialStateHash {
			if errUpdateStory := p.publishedRepo.UpdateStatusAndError(ctx, p.db, publishedStoryID, sharedModels.StatusError, &errMsg); errUpdateStory != nil {
				log.Error("Failed to update PublishedStory status to Error after transaction begin failure", zap.Error(errUpdateStory))
			}
			p.publishClientStoryUpdateOnError(publishedStoryID, uuid.MustParse(notification.UserID), errMsg)
		} else {
			if gs, errGS := p.playerGameStateRepo.GetByID(ctx, p.db, gameStateID); errGS == nil {
				gs.PlayerStatus = sharedModels.PlayerStatusError
				gs.ErrorDetails = &errMsg
				gs.LastActivityAt = time.Now().UTC()
				if _, errSave := p.playerGameStateRepo.Save(ctx, p.db, gs); errSave == nil {
					p.publishClientStoryUpdateOnError(gameStateID, gs.PlayerID, errMsg) // Используем ID GameState и PlayerID
				}
			}
		}
		return fmt.Errorf("failed to begin transaction: %w", errTx) // NACK
	}
	// Гарантируем Rollback в случае ошибки или паники
	defer func() {
		if r := recover(); r != nil {
			log.Error("Panic recovered during JSON result transaction", zap.Any("panic", r))
			_ = tx.Rollback(context.Background())
			// TODO: Определить, нужно ли NACK или устанавливать статус Error при панике
		} else if errTx != nil { // Если errTx установлен внутри блока doTx
			log.Warn("Rolling back transaction due to error during JSON result processing", zap.Error(errTx))
			_ = tx.Rollback(ctx)
		}
	}()

	// Выполняем операции внутри транзакции
	errTx = func(tx pgx.Tx) error {
		// Обеспечиваем запись PlayerProgress
		progress := &sharedModels.PlayerProgress{
			PublishedStoryID: publishedStoryID,
			CurrentStateHash: notification.StateHash,
		}
		_, errUp := p.playerProgressRepo.UpsertByHash(ctx, tx, progress)
		if errUp != nil {
			log.Error("Failed to upsert PlayerProgress for JsonGeneration within transaction", zap.Error(errUp))
			return errUp
		}

		// Обновляем содержимое сцены в StoryScene
		scene, errScene := p.sceneRepo.FindByStoryAndHash(ctx, tx, publishedStoryID, notification.StateHash)
		if errScene != nil {
			// Если сцена не найдена, это может быть проблемой, но попробуем продолжить обновление GameState
			log.Warn("Failed to find StoryScene for JsonGeneration update within transaction", zap.Error(errScene))
			// return fmt.Errorf("failed to find scene: %w", errScene) // Раскомментировать, если сцена обязательна
		} else {
			if errContent := p.sceneRepo.UpdateContent(ctx, tx, scene.ID, []byte(rawJSON)); errContent != nil {
				log.Error("Failed to update StoryScene content within transaction", zap.Error(errContent))
				return errContent
			}
		}

		// Обновляем PlayerGameState: ставим текущую сцену и статус Playing
		gs, errGS := p.playerGameStateRepo.GetByID(ctx, tx, gameStateID)
		if errGS != nil {
			log.Error("Failed to get PlayerGameState by ID within transaction", zap.Error(errGS))
			return errGS // Ошибка получения GameState критична
		}

		// Перепроверяем статус на случай конкурентного обновления
		if gs.PlayerStatus != sharedModels.PlayerStatusGeneratingScene {
			log.Warn("PlayerGameState status changed during transaction, skipping update", zap.String("current_status", string(gs.PlayerStatus)))
			return nil // Не ошибка, просто пропускаем обновление
		}

		if scene != nil { // Обновляем SceneID только если сцена найдена
			gs.CurrentSceneID = uuid.NullUUID{UUID: scene.ID, Valid: true}
		}
		gs.PlayerStatus = sharedModels.PlayerStatusPlaying
		gs.ErrorDetails = nil // Сбрасываем предыдущие ошибки
		gs.LastActivityAt = time.Now().UTC()
		if _, errSave := p.playerGameStateRepo.Save(ctx, tx, gs); errSave != nil {
			log.Error("Failed to save updated PlayerGameState within transaction", zap.Error(errSave))
			return errSave
		}

		// <<< Обновление PublishedStory для начальной сцены тоже должно быть в транзакции >>>
		if notification.StateHash == sharedModels.InitialStateHash {
			log.Info("Initial scene JSON processed, updating PublishedStory to Ready/Complete within transaction")
			stepComplete := sharedModels.StepComplete
			updateCtx, cancelUpdate := context.WithTimeout(ctx, 15*time.Second) // Используем ctx транзакции
			defer cancelUpdate()
			if errUpdateStory := p.publishedRepo.UpdateStatusFlagsAndDetails(updateCtx, tx, publishedStoryID, sharedModels.StatusReady, false, false, nil, &stepComplete); errUpdateStory != nil {
				log.Error("Failed to update PublishedStory status to Ready after initial scene JSON generation within transaction", zap.Error(errUpdateStory))
				return errUpdateStory // Ошибка обновления PublishedStory критична
			} else {
				log.Info("PublishedStory status updated to Ready and step to Complete within transaction.")
			}
		}

		return nil // Успех транзакции
	}(tx)

	if errTx != nil {
		// Ошибка произошла внутри блока func(tx pgx.Tx), defer tx.Rollback сработает
		log.Error("Transaction failed during JSON result processing", zap.Error(errTx))
		// Обработка ошибки для начальной сцены или GameState
		// TODO: Вынести обработку ошибок в отдельную функцию
		errMsg := fmt.Sprintf("internal server error (db operation): %v", errTx)
		if notification.StateHash == sharedModels.InitialStateHash {
			if errUpdateStory := p.publishedRepo.UpdateStatusAndError(context.Background(), p.db, publishedStoryID, sharedModels.StatusError, &errMsg); errUpdateStory != nil {
				log.Error("Failed to update PublishedStory status to Error after transaction failure", zap.Error(errUpdateStory))
			}
			p.publishClientStoryUpdateOnError(publishedStoryID, uuid.MustParse(notification.UserID), errMsg)
		} else {
			if gs, errGS := p.playerGameStateRepo.GetByID(context.Background(), p.db, gameStateID); errGS == nil {
				gs.PlayerStatus = sharedModels.PlayerStatusError
				gs.ErrorDetails = &errMsg
				gs.LastActivityAt = time.Now().UTC()
				if _, errSave := p.playerGameStateRepo.Save(context.Background(), p.db, gs); errSave == nil {
					p.publishClientStoryUpdateOnError(gameStateID, gs.PlayerID, errMsg)
				}
			}
		}
		return errTx // NACK
	}

	// Если дошли сюда без ошибок, коммитим транзакцию
	if errCommit := tx.Commit(ctx); errCommit != nil {
		log.Error("Failed to commit transaction for JSON result processing", zap.Error(errCommit))
		// Обработка ошибки для начальной сцены или GameState
		// TODO: Вынести обработку ошибок в отдельную функцию
		errMsg := fmt.Sprintf("internal server error (db commit): %v", errCommit)
		if notification.StateHash == sharedModels.InitialStateHash {
			if errUpdateStory := p.publishedRepo.UpdateStatusAndError(context.Background(), p.db, publishedStoryID, sharedModels.StatusError, &errMsg); errUpdateStory != nil {
				log.Error("Failed to update PublishedStory status to Error after commit failure", zap.Error(errUpdateStory))
			}
			p.publishClientStoryUpdateOnError(publishedStoryID, uuid.MustParse(notification.UserID), errMsg)
		} else {
			if gs, errGS := p.playerGameStateRepo.GetByID(context.Background(), p.db, gameStateID); errGS == nil {
				gs.PlayerStatus = sharedModels.PlayerStatusError
				gs.ErrorDetails = &errMsg
				gs.LastActivityAt = time.Now().UTC()
				if _, errSave := p.playerGameStateRepo.Save(context.Background(), p.db, gs); errSave == nil {
					p.publishClientStoryUpdateOnError(gameStateID, gs.PlayerID, errMsg)
				}
			}
		}
		return fmt.Errorf("failed to commit transaction: %w", errCommit) // NACK
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
