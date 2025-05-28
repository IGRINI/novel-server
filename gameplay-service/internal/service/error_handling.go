package service

import (
	"context"
	"fmt"
	"novel-server/gameplay-service/internal/messaging"
	"novel-server/shared/models"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ErrorContext содержит контекст для обработки ошибок
type ErrorContext struct {
	UserID       uuid.UUID
	StoryID      *uuid.UUID
	GameStateID  *uuid.UUID
	Operation    string
	ErrorType    ErrorType
	OriginalErr  error
	ErrorMessage string
}

// ErrorType определяет тип ошибки для централизованной обработки
type ErrorType string

const (
	ErrorTypeValidation  ErrorType = "validation"
	ErrorTypeRepository  ErrorType = "repository"
	ErrorTypeGeneration  ErrorType = "generation"
	ErrorTypeRetry       ErrorType = "retry"
	ErrorTypeWebSocket   ErrorType = "websocket"
	ErrorTypeTransaction ErrorType = "transaction"
	ErrorTypeInternal    ErrorType = "internal"
)

// CentralizedErrorHandler централизованно обрабатывает ошибки в геймфлоу
type CentralizedErrorHandler struct {
	logger    *zap.Logger
	clientPub messaging.ClientUpdatePublisher
}

// NewCentralizedErrorHandler создает новый централизованный обработчик ошибок
func NewCentralizedErrorHandler(logger *zap.Logger, clientPub messaging.ClientUpdatePublisher) *CentralizedErrorHandler {
	return &CentralizedErrorHandler{
		logger:    logger,
		clientPub: clientPub,
	}
}

// HandleError централизованно обрабатывает ошибку с логированием и уведомлениями
func (h *CentralizedErrorHandler) HandleError(ctx context.Context, errCtx ErrorContext) error {
	// Создаем структурированный лог
	logFields := []zap.Field{
		zap.String("operation", errCtx.Operation),
		zap.String("error_type", string(errCtx.ErrorType)),
		zap.String("user_id", errCtx.UserID.String()),
	}

	if errCtx.StoryID != nil {
		logFields = append(logFields, zap.String("story_id", errCtx.StoryID.String()))
	}

	if errCtx.GameStateID != nil {
		logFields = append(logFields, zap.String("game_state_id", errCtx.GameStateID.String()))
	}

	if errCtx.OriginalErr != nil {
		logFields = append(logFields, zap.Error(errCtx.OriginalErr))
	}

	// Логируем ошибку с соответствующим уровнем
	switch errCtx.ErrorType {
	case ErrorTypeValidation:
		h.logger.Warn("Validation error in gameflow", logFields...)
	case ErrorTypeRepository:
		h.logger.Error("Repository error in gameflow", logFields...)
	case ErrorTypeGeneration:
		h.logger.Error("Generation error in gameflow", logFields...)
	case ErrorTypeRetry:
		h.logger.Error("Retry error in gameflow", logFields...)
	case ErrorTypeWebSocket:
		h.logger.Warn("WebSocket error in gameflow", logFields...)
	case ErrorTypeTransaction:
		h.logger.Error("Transaction error in gameflow", logFields...)
	case ErrorTypeInternal:
		h.logger.Error("Internal error in gameflow", logFields...)
	default:
		h.logger.Error("Unknown error in gameflow", logFields...)
	}

	// Отправляем WebSocket уведомление если возможно
	if h.clientPub != nil {
		h.sendErrorNotification(ctx, errCtx)
	}

	// Возвращаем обработанную ошибку
	return h.wrapError(errCtx)
}

// sendErrorNotification отправляет WebSocket уведомление об ошибке
func (h *CentralizedErrorHandler) sendErrorNotification(ctx context.Context, errCtx ErrorContext) {
	var clientUpdate models.ClientStoryUpdate
	errorMsg := errCtx.ErrorMessage
	if errorMsg == "" && errCtx.OriginalErr != nil {
		errorMsg = errCtx.OriginalErr.Error()
	}

	if errCtx.GameStateID != nil {
		// Ошибка игрового состояния
		clientUpdate = models.ClientStoryUpdate{
			ID:           errCtx.GameStateID.String(),
			UserID:       errCtx.UserID.String(),
			UpdateType:   models.UpdateTypeGameState,
			Status:       "error",
			ErrorDetails: &errorMsg,
		}
	} else if errCtx.StoryID != nil {
		// Ошибка истории
		clientUpdate = models.ClientStoryUpdate{
			ID:           errCtx.StoryID.String(),
			UserID:       errCtx.UserID.String(),
			UpdateType:   models.UpdateTypeStory,
			Status:       "error",
			ErrorDetails: &errorMsg,
		}
	} else {
		// Общая ошибка пользователя
		clientUpdate = models.ClientStoryUpdate{
			ID:           errCtx.UserID.String(),
			UserID:       errCtx.UserID.String(),
			UpdateType:   models.UpdateTypeStory,
			Status:       "error",
			ErrorDetails: &errorMsg,
		}
	}

	// Отправляем уведомление с таймаутом
	notifyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := h.clientPub.PublishClientUpdate(notifyCtx, clientUpdate); err != nil {
		h.logger.Warn("Failed to send error notification via WebSocket",
			zap.String("operation", errCtx.Operation),
			zap.String("user_id", errCtx.UserID.String()),
			zap.Error(err))
	}
}

// wrapError оборачивает ошибку в соответствующий тип
func (h *CentralizedErrorHandler) wrapError(errCtx ErrorContext) error {
	switch errCtx.ErrorType {
	case ErrorTypeValidation:
		if errCtx.OriginalErr != nil {
			return fmt.Errorf("%w: %s: %v", models.ErrBadRequest, errCtx.Operation, errCtx.OriginalErr)
		}
		return fmt.Errorf("%w: %s: %s", models.ErrBadRequest, errCtx.Operation, errCtx.ErrorMessage)

	case ErrorTypeRepository:
		// Проверяем специфичные ошибки репозитория
		if errCtx.OriginalErr != nil {
			if wrappedErr := WrapRepoError(h.logger, errCtx.OriginalErr, errCtx.Operation); wrappedErr != nil {
				return wrappedErr
			}
		}
		return fmt.Errorf("repository error in %s: %v", errCtx.Operation, errCtx.OriginalErr)

	case ErrorTypeGeneration:
		return fmt.Errorf("generation error in %s: %v", errCtx.Operation, errCtx.OriginalErr)

	case ErrorTypeRetry:
		return fmt.Errorf("retry error in %s: %v", errCtx.Operation, errCtx.OriginalErr)

	case ErrorTypeWebSocket:
		// WebSocket ошибки обычно не критичны для основного флоу
		h.logger.Debug("WebSocket error handled",
			zap.String("operation", errCtx.Operation),
			zap.Error(errCtx.OriginalErr))
		return nil

	case ErrorTypeTransaction:
		return fmt.Errorf("transaction error in %s: %v", errCtx.Operation, errCtx.OriginalErr)

	case ErrorTypeInternal:
		return fmt.Errorf("%w: %s: %v", models.ErrInternalServer, errCtx.Operation, errCtx.OriginalErr)

	default:
		return fmt.Errorf("unknown error in %s: %v", errCtx.Operation, errCtx.OriginalErr)
	}
}

// HandleRepositoryError специализированный обработчик для ошибок репозитория
func (h *CentralizedErrorHandler) HandleRepositoryError(ctx context.Context, err error, operation string, userID uuid.UUID, entityID *uuid.UUID) error {
	errCtx := ErrorContext{
		UserID:      userID,
		Operation:   operation,
		ErrorType:   ErrorTypeRepository,
		OriginalErr: err,
	}

	if entityID != nil {
		errCtx.StoryID = entityID
	}

	return h.HandleError(ctx, errCtx)
}

// HandleValidationError специализированный обработчик для ошибок валидации
func (h *CentralizedErrorHandler) HandleValidationError(ctx context.Context, message string, operation string, userID uuid.UUID, entityID *uuid.UUID) error {
	errCtx := ErrorContext{
		UserID:       userID,
		Operation:    operation,
		ErrorType:    ErrorTypeValidation,
		ErrorMessage: message,
	}

	if entityID != nil {
		errCtx.StoryID = entityID
	}

	return h.HandleError(ctx, errCtx)
}

// HandleGenerationError специализированный обработчик для ошибок генерации
func (h *CentralizedErrorHandler) HandleGenerationError(ctx context.Context, err error, operation string, userID uuid.UUID, storyID *uuid.UUID, gameStateID *uuid.UUID) error {
	errCtx := ErrorContext{
		UserID:      userID,
		StoryID:     storyID,
		GameStateID: gameStateID,
		Operation:   operation,
		ErrorType:   ErrorTypeGeneration,
		OriginalErr: err,
	}

	return h.HandleError(ctx, errCtx)
}

// HandleRetryError специализированный обработчик для ошибок retry
func (h *CentralizedErrorHandler) HandleRetryError(ctx context.Context, err error, operation string, userID uuid.UUID, storyID *uuid.UUID, gameStateID *uuid.UUID) error {
	errCtx := ErrorContext{
		UserID:      userID,
		StoryID:     storyID,
		GameStateID: gameStateID,
		Operation:   operation,
		ErrorType:   ErrorTypeRetry,
		OriginalErr: err,
	}

	return h.HandleError(ctx, errCtx)
}
