package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"novel-server/shared/constants"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/utils"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// handleSceneGenerationNotification обрабатывает уведомления об успешной или неуспешной генерации сцены/концовки.
// Теперь он извлекает текст и запускает задачу PromptTypeJsonGeneration.
func (p *NotificationProcessor) handleSceneGenerationNotification(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	// Увеличиваем общий таймаут операции
	operationCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	gameStateID, err := parseUUIDField(notification.GameStateID, "GameStateID")
	if err != nil {
		p.logger.Error("Invalid GameStateID in scene notification", zap.Error(err))
		// Уведомляем клиента об ошибке сцены
		p.handleStoryError(ctx, publishedStoryID, notification.UserID, fmt.Sprintf("invalid GameStateID: %v", err), constants.WSEventSceneError)
		return fmt.Errorf("invalid GameStateID: %w", err)
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
		// Уведомляем клиента об ошибке игрового состояния
		p.handleGameStateError(ctx, gameStateID, notification.UserID, fmt.Sprintf("failed to get PlayerGameState: %v", errGetState))
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
			// Строгая проверка и парсинг JSON
			var payload struct {
				Res string `json:"res"`
			}
			payload, err = decodeStrictJSON[struct {
				Res string `json:"res"`
			}](genResult.GeneratedText)
			if err != nil {
				logWithState.Error("UNMARSHAL ERROR: Failed to unmarshal {\"res\": \"...\"} from genResult.GeneratedText",
					zap.Error(err),
					zap.String("json_snippet", utils.StringShort(genResult.GeneratedText, 200)),
				)
				fetchErr = fmt.Errorf("failed to unmarshal scene/gameover result JSON: %w", err)
				genResultError = fetchErr.Error()
			} else {
				rawNarrativeText = payload.Res // Извлекаем текст
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
	if notification.GameStateID != "" {
		gsID, err := parseUUIDField(notification.GameStateID, "GameStateID")
		if err != nil {
			logger.Error("Invalid GameStateID for error handling", zap.Error(err))
			return nil
		}
		return p.handleGameStateError(ctx, gsID, notification.UserID, errorDetails)
	}
	return p.handleStoryError(ctx, publishedStoryID, notification.UserID, errorDetails, constants.WSEventSceneError)
}
