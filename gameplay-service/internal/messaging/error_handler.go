package messaging

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"novel-server/shared/constants"
	interfaces "novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models"
)

// ErrorHandler централизованно обрабатывает ошибки в хендлерах
type ErrorHandler struct {
	publishedRepo       interfaces.PublishedStoryRepository
	playerGameStateRepo interfaces.PlayerGameStateRepository
	clientPub           ClientUpdatePublisher
	pushPub             PushNotificationPublisher
	logger              *zap.Logger
}

// NewErrorHandler создает новый обработчик ошибок
func NewErrorHandler(
	publishedRepo interfaces.PublishedStoryRepository,
	playerGameStateRepo interfaces.PlayerGameStateRepository,
	clientPub ClientUpdatePublisher,
	pushPub PushNotificationPublisher,
	logger *zap.Logger,
) *ErrorHandler {
	return &ErrorHandler{
		publishedRepo:       publishedRepo,
		playerGameStateRepo: playerGameStateRepo,
		clientPub:           clientPub,
		pushPub:             pushPub,
		logger:              logger,
	}
}

// HandleStoryError обрабатывает ошибки для PublishedStory (первая сцена)
func (h *ErrorHandler) HandleStoryError(
	ctx context.Context,
	tx interfaces.DBTX,
	storyID uuid.UUID,
	userID uuid.UUID,
	errorDetails string,
	eventType string,
) error {
	// Обновляем статус истории на Error
	err := h.publishedRepo.UpdateStatusAndError(ctx, tx, storyID,
		sharedModels.StatusError, &errorDetails)
	if err != nil {
		h.logger.Error("Failed to update story status to error",
			zap.String("story_id", storyID.String()),
			zap.Error(err))
		return fmt.Errorf("failed to update story status: %w", err)
	}

	// Отправляем уведомление клиенту
	h.publishClientStoryUpdateOnError(storyID, userID, errorDetails)

	// Отправляем push-уведомление
	h.publishPushOnError(ctx, storyID, userID, errorDetails, eventType)

	h.logger.Info("Story error handled",
		zap.String("story_id", storyID.String()),
		zap.String("user_id", userID.String()),
		zap.String("error", errorDetails))

	return nil
}

// HandleGameStateError обрабатывает ошибки для PlayerGameState (последующие сцены)
func (h *ErrorHandler) HandleGameStateError(
	ctx context.Context,
	tx interfaces.DBTX,
	gameStateID uuid.UUID,
	userID uuid.UUID,
	errorDetails string,
) error {
	// Получаем текущее состояние игры
	gameState, err := h.playerGameStateRepo.GetByID(ctx, tx, gameStateID)
	if err != nil {
		h.logger.Error("Failed to get game state for error update",
			zap.String("game_state_id", gameStateID.String()),
			zap.Error(err))
		return fmt.Errorf("failed to get game state: %w", err)
	}

	// Обновляем статус на Error
	gameState.PlayerStatus = sharedModels.PlayerStatusError
	gameState.ErrorDetails = &errorDetails

	// Сохраняем обновленное состояние
	_, err = h.playerGameStateRepo.Save(ctx, tx, gameState)
	if err != nil {
		h.logger.Error("Failed to update game state status to error",
			zap.String("game_state_id", gameStateID.String()),
			zap.Error(err))
		return fmt.Errorf("failed to update game state status: %w", err)
	}

	// Отправляем уведомление клиенту
	h.publishClientGameStateUpdateOnError(gameStateID, userID, errorDetails)

	// Отправляем push-уведомление
	h.publishPushOnGameStateError(ctx, gameStateID, userID, errorDetails)

	h.logger.Info("Game state error handled",
		zap.String("game_state_id", gameStateID.String()),
		zap.String("user_id", userID.String()),
		zap.String("error", errorDetails))

	return nil
}

// DetermineErrorHandler определяет, какой тип ошибки использовать на основе контекста
func (h *ErrorHandler) DetermineErrorHandler(
	stateHash string,
	gameStateID string,
) (isStoryError bool) {
	// Если это первая сцена (initial state hash) или нет game state ID,
	// то это ошибка истории
	return stateHash == sharedModels.InitialStateHash || gameStateID == ""
}

// HandleError универсальный метод для обработки ошибок
func (h *ErrorHandler) HandleError(
	ctx context.Context,
	tx interfaces.DBTX,
	storyID uuid.UUID,
	gameStateID string,
	userID uuid.UUID,
	stateHash string,
	errorDetails string,
	eventType string,
) error {
	if h.DetermineErrorHandler(stateHash, gameStateID) {
		return h.HandleStoryError(ctx, tx, storyID, userID, errorDetails, eventType)
	} else {
		gameStateUUID, err := uuid.Parse(gameStateID)
		if err != nil {
			return fmt.Errorf("invalid game state ID: %w", err)
		}
		return h.HandleGameStateError(ctx, tx, gameStateUUID, userID, errorDetails)
	}
}

// publishClientStoryUpdateOnError отправляет обновление клиенту об ошибке истории
func (h *ErrorHandler) publishClientStoryUpdateOnError(storyID uuid.UUID, userID uuid.UUID, errorDetails string) {
	if h.clientPub != nil {
		update := sharedModels.ClientStoryUpdate{
			ID:           storyID.String(),
			UserID:       userID.String(),
			UpdateType:   sharedModels.UpdateTypeDraft,
			Status:       string(sharedModels.StatusError),
			ErrorDetails: &errorDetails,
		}
		if err := h.clientPub.PublishClientUpdate(context.Background(), update); err != nil {
			h.logger.Error("Failed to publish client story update", zap.Error(err))
		}
	}
}

// publishClientGameStateUpdateOnError отправляет обновление клиенту об ошибке игрового состояния
func (h *ErrorHandler) publishClientGameStateUpdateOnError(gameStateID uuid.UUID, userID uuid.UUID, errorDetails string) {
	if h.clientPub != nil {
		update := sharedModels.ClientStoryUpdate{
			ID:           gameStateID.String(),
			UserID:       userID.String(),
			UpdateType:   sharedModels.UpdateTypeGameState,
			Status:       string(sharedModels.PlayerStatusError),
			ErrorDetails: &errorDetails,
		}
		if err := h.clientPub.PublishClientUpdate(context.Background(), update); err != nil {
			h.logger.Error("Failed to publish client game state update", zap.Error(err))
		}
	}
}

// publishPushOnError отправляет push-уведомление об ошибке истории
func (h *ErrorHandler) publishPushOnError(ctx context.Context, storyID uuid.UUID, userID uuid.UUID, errorDetails, eventType string) {
	if h.pushPub != nil {
		notification := sharedModels.PushNotificationPayload{
			UserID: userID,
			Notification: sharedModels.PushNotification{
				Title: "Ошибка генерации истории",
				Body:  fmt.Sprintf("Произошла ошибка при генерации: %s", errorDetails),
			},
			Data: map[string]string{
				"story_id":   storyID.String(),
				"error":      errorDetails,
				"event_type": eventType,
			},
		}
		h.pushPub.PublishPushNotification(ctx, notification)
	}
}

// publishPushOnGameStateError отправляет push-уведомление об ошибке игрового состояния
func (h *ErrorHandler) publishPushOnGameStateError(ctx context.Context, gameStateID uuid.UUID, userID uuid.UUID, errorDetails string) {
	if h.pushPub != nil {
		notification := sharedModels.PushNotificationPayload{
			UserID: userID,
			Notification: sharedModels.PushNotification{
				Title: "Ошибка в игре",
				Body:  fmt.Sprintf("Произошла ошибка в игре: %s", errorDetails),
			},
			Data: map[string]string{
				"game_state_id": gameStateID.String(),
				"error":         errorDetails,
				"event_type":    constants.WSEventSceneError,
			},
		}
		h.pushPub.PublishPushNotification(ctx, notification)
	}
}
