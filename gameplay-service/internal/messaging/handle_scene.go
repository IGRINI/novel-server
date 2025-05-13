package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/utils"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// stringRef возвращает указатель на строку.
func stringRef(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// handleSceneGenerationNotification обрабатывает уведомления об успешной или неуспешной генерации сцены/концовки.
// Теперь он извлекает текст и запускает задачу PromptTypeJsonGeneration.
func (p *NotificationProcessor) handleSceneGenerationNotification(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	// Увеличиваем общий таймаут операции
	operationCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	gameStateID, errParseStateID := uuid.Parse(notification.GameStateID)
	if errParseStateID != nil && notification.GameStateID != "" {
		p.logger.Error("ERROR: Failed to parse GameStateID from scene notification", zap.Error(errParseStateID), zap.String("gameStateID", notification.GameStateID))
		// Не фатально для самого обработчика, но логируем
		// return fmt.Errorf("invalid GameStateID: %w", errParseStateID)
	} else if notification.GameStateID == "" {
		p.logger.Error("CRITICAL: Received scene generation notification without GameStateID", zap.String("taskID", taskID))
		return fmt.Errorf("missing GameStateID in scene notification for task %s", taskID) // NACK
	}

	logFields := []zap.Field{
		zap.String("task_id", taskID),
		zap.String("published_story_id", publishedStoryID.String()),
		zap.String("state_hash", notification.StateHash),
		zap.String("prompt_type", string(notification.PromptType)),
		zap.String("game_state_id", gameStateID.String()),
	}
	logWithState := p.logger.With(logFields...)

	logWithState.Info("Processing Scene/GameOver notification (to trigger JsonGeneration)")

	// Получаем GameState для проверки статуса
	gameStateCheckCtx, gameStateCheckCancel := context.WithTimeout(operationCtx, 5*time.Second)
	gameState, errGetState := p.playerGameStateRepo.GetByID(gameStateCheckCtx, p.db, gameStateID)
	gameStateCheckCancel()
	if errGetState != nil {
		logWithState.Error("Failed to get PlayerGameState for status check", zap.Error(errGetState))
		// Если не найдено, это странно, но может быть обработано дальше как ошибка. Вернем ошибку.
		return fmt.Errorf("failed to get PlayerGameState %s for status check: %w", gameStateID, errGetState) // NACK
	}
	if gameState.PlayerStatus != sharedModels.PlayerStatusGeneratingScene {
		logWithState.Warn("PlayerGameState not in GeneratingScene status, skipping scene result processing.", zap.String("current_status", string(gameState.PlayerStatus)))
		return nil // Ack, так как это дубликат или устаревшее сообщение
	}

	if notification.Status == sharedMessaging.NotificationStatusSuccess {
		logWithState.Info("Handling Scene/GameOver SUCCESS notification")

		var rawNarrativeText string // Переименовано для ясности
		var fetchErr error
		var genResultError string
		var currentStoryState *sharedModels.PublishedStory // Для получения языка

		genResultCtx, genResultCancel := context.WithTimeout(operationCtx, 10*time.Second)
		genResult, genErr := p.genResultRepo.GetByTaskID(genResultCtx, taskID)
		genResultCancel()

		if genErr != nil {
			logWithState.Error("DB ERROR: Could not get GenerationResult by TaskID", zap.Error(genErr))
			fetchErr = fmt.Errorf("failed to fetch generation result: %w", genErr)
			genResultError = fetchErr.Error() // Используем как ошибку для handleSceneGenerationError
		} else if genResult.Error != "" {
			logWithState.Error("TASK ERROR: GenerationResult indicates an error", zap.String("gen_error", genResult.Error))
			fetchErr = errors.New(genResult.Error)
			genResultError = genResult.Error // Используем как ошибку для handleSceneGenerationError
		} else {
			// Ожидаем JSON вида {"result": "..."}
			var resultPayload struct {
				Result string `json:"result"`
			}
			if errUnmarshal := json.Unmarshal([]byte(genResult.GeneratedText), &resultPayload); errUnmarshal != nil {
				logWithState.Error("UNMARSHAL ERROR: Failed to unmarshal {\"result\": \"...\"} from genResult.GeneratedText",
					zap.Error(errUnmarshal),
					zap.String("json_snippet", utils.StringShort(genResult.GeneratedText, 200)),
				)
				fetchErr = fmt.Errorf("failed to unmarshal scene/gameover result JSON: %w", errUnmarshal)
				genResultError = fetchErr.Error()
			} else {
				rawNarrativeText = resultPayload.Result // Извлекаем текст
				logWithState.Debug("Successfully fetched and extracted NarrativeText from DB result")
			}
		}

		if fetchErr != nil {
			logWithState.Error("Error during data fetching/parsing, handling as generation error", zap.Error(fetchErr))
			// Передаем извлеченную или сформированную строку ошибки
			return p.handleSceneGenerationError(operationCtx, notification, publishedStoryID, genResultError, logWithState)
		}

		logWithState.Info("Successfully processed scene generation result, preparing JsonGeneration task.")

		// Получаем язык истории для передачи в следующую задачу
		storyStateCtx, storyStateCancel := context.WithTimeout(operationCtx, 5*time.Second)
		currentStoryState, errStory := p.publishedRepo.GetByID(storyStateCtx, p.db, publishedStoryID)
		storyStateCancel()
		if errStory != nil {
			logWithState.Error("Failed to get PublishedStory to retrieve language for JsonGeneration task", zap.Error(errStory))
			// Обрабатываем как ошибку, так как язык важен
			return p.handleSceneGenerationError(operationCtx, notification, publishedStoryID, fmt.Sprintf("failed to get story for language: %v", errStory), logWithState)
		}

		// Публикуем задачу JsonGeneration
		jsonGenTaskID := uuid.New().String()
		jsonGenPayload := sharedMessaging.GenerationTaskPayload{
			TaskID:           jsonGenTaskID,
			UserID:           notification.UserID, // Берем из уведомления
			PromptType:       sharedModels.PromptTypeJsonGeneration,
			UserInput:        rawNarrativeText, // Передаем извлеченный текст
			PublishedStoryID: publishedStoryID.String(),
			StateHash:        notification.StateHash,
			GameStateID:      notification.GameStateID,
			Language:         currentStoryState.Language, // Используем язык истории
		}

		// Используем внутренний хелпер publishTask для публикации с retry логикой
		if errPub := p.publishTask(jsonGenPayload); errPub != nil {
			logWithState.Error("CRITICAL: Failed to publish JsonGeneration task", zap.Error(errPub))
			// Обрабатываем как ошибку генерации, т.к. следующий шаг не запущен
			return p.handleSceneGenerationError(operationCtx, notification, publishedStoryID, fmt.Sprintf("failed to publish JsonGeneration task: %v", errPub), logWithState)
		}

		logWithState.Info("JsonGeneration task published successfully.")

	} else {
		logWithState.Warn("Handling Scene/GameOver ERROR notification",
			zap.String("error_details", notification.ErrorDetails),
		)
		// Обработка ошибки остается прежней, т.к. она обновляет статусы и шлет WS/Push об ошибке
		_ = p.handleSceneGenerationError(operationCtx, notification, publishedStoryID, notification.ErrorDetails, logWithState)
	}

	return nil
}

// handleSceneGenerationError остается в основном без изменений,
// т.к. его задача - обновить статусы на Error и уведомить клиента.
func (p *NotificationProcessor) handleSceneGenerationError(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID, errorDetails string, logger *zap.Logger) error {
	logger.Warn("Entering handleSceneGenerationError", zap.String("reason", errorDetails))

	dbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var storyUserID uuid.UUID
	var updateErr error
	var parsedGameStateID uuid.UUID
	var gameStateUpdatedSuccessfully bool

	if notification.GameStateID != "" {
		gameStateID, errParse := uuid.Parse(notification.GameStateID)
		if errParse != nil {
			logger.Error("ERROR: Failed to parse GameStateID from error notification", zap.Error(errParse), zap.String("gameStateID", notification.GameStateID))
		} else {
			parsedGameStateID = gameStateID
			gameState, errGetState := p.playerGameStateRepo.GetByID(dbCtx, p.db, gameStateID)
			if errGetState != nil {
				if errors.Is(errGetState, sharedModels.ErrNotFound) {
					logger.Warn("PlayerGameState not found, cannot update status to Error", zap.String("game_state_id", gameStateID.String()))
				} else {
					logger.Error("DB ERROR: Failed to get PlayerGameState by ID to update status to Error", zap.Error(errGetState))
					updateErr = fmt.Errorf("failed to get game state %s during error handling: %w", gameStateID.String(), errGetState)
				}
			} else {
				storyUserID = gameState.PlayerID
				if gameState.PlayerStatus != sharedModels.PlayerStatusError {
					gameState.PlayerStatus = sharedModels.PlayerStatusError
					gameState.ErrorDetails = stringRef(errorDetails)
					gameState.LastActivityAt = time.Now().UTC()
					_, errSaveState := p.playerGameStateRepo.Save(dbCtx, p.db, gameState)
					if errSaveState != nil {
						logger.Error("DB ERROR: Failed to save PlayerGameState with Error status", zap.Error(errSaveState))
						updateErr = fmt.Errorf("failed to save game state %s during error handling: %w", gameStateID.String(), errSaveState)
					} else {
						logger.Info("PlayerGameState updated to Error status successfully")
						gameStateUpdatedSuccessfully = true
					}
				} else {
					logger.Info("PlayerGameState already in Error status, skipping update.")
					gameStateUpdatedSuccessfully = true
					storyUserID = gameState.PlayerID // Убедимся, что UserID установлен
				}
			}
		}
	}

	if !gameStateUpdatedSuccessfully {
		logger.Info("Updating PublishedStory status to Error (either no GameStateID or GameState update failed/skipped)")
		errUpdateStory := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtx, p.db, publishedStoryID, sharedModels.StatusError, false, false, stringRef(errorDetails), nil)
		if errUpdateStory != nil {
			logger.Error("CRITICAL DB ERROR: Failed to update PublishedStory status to Error", zap.Error(errUpdateStory))
			if updateErr == nil { // Сохраняем первую ошибку
				updateErr = fmt.Errorf("failed to update story %s status to Error: %w", publishedStoryID.String(), errUpdateStory)
			}
		} else {
			logger.Info("PublishedStory status updated to Error successfully")
		}
	}

	// Получаем UserID, если еще не получили
	if storyUserID == uuid.Nil {
		story, errGet := p.publishedRepo.GetByID(dbCtx, p.db, publishedStoryID)
		if errGet != nil {
			logger.Error("Failed to get story details for WS update after setting Error status", zap.Error(errGet))
		} else {
			storyUserID = story.UserID
		}
	}

	// Отправка WS уведомления об ошибке
	if storyUserID != uuid.Nil {
		clientUpdateError := sharedModels.ClientStoryUpdate{
			UserID:       storyUserID.String(),
			ErrorDetails: stringRef(errorDetails),
			StateHash:    notification.StateHash,
		}
		logMessage := ""
		wsLogFields := []zap.Field{
			zap.Stringer("userID", storyUserID),
			zap.Stringer("publishedStoryID", publishedStoryID),
			zap.String("stateHash", notification.StateHash),
		}

		if parsedGameStateID != uuid.Nil && gameStateUpdatedSuccessfully {
			clientUpdateError.ID = parsedGameStateID.String()
			clientUpdateError.UpdateType = sharedModels.UpdateTypeGameState
			clientUpdateError.Status = string(sharedModels.PlayerStatusError)
			logMessage = "ClientStoryUpdate sent (GameState Error)"
			wsLogFields = append(wsLogFields, zap.Stringer("gameStateID", parsedGameStateID))
		} else {
			clientUpdateError.ID = publishedStoryID.String()
			clientUpdateError.UpdateType = sharedModels.UpdateTypeStory
			clientUpdateError.Status = string(sharedModels.StatusError)
			logMessage = "ClientStoryUpdate sent (Story Error)"
		}

		wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdateError); errWs != nil {
			logger.Error("Error sending ClientStoryUpdate on error", append(wsLogFields, zap.Error(errWs))...)
		} else {
			logger.Info(logMessage, wsLogFields...)
		}
		wsCancel()
	} else {
		logger.Warn("Cannot send WebSocket error update: UserID is unknown",
			zap.Stringer("publishedStoryID", publishedStoryID),
			zap.Stringer("parsedGameStateID", parsedGameStateID),
			zap.Bool("gameStateUpdatedSuccessfully", gameStateUpdatedSuccessfully),
		)
	}

	return updateErr // Возвращаем первую возникшую ошибку
}
