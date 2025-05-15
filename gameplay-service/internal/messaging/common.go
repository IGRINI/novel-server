package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models"
)

// withTransaction выполняет fn в рамках транзакции, коммитит при успехе или откатывает при ошибке.
func (p *NotificationProcessor) withTransaction(ctx context.Context, fn func(tx interfaces.DBTX) error) error {
	tx, err := p.db.Begin(ctx)
	if err != nil {
		p.logger.Error("Failed to begin DB transaction", zap.Error(err))
		return err
	}
	// Восстановление при панике и откат
	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback(context.Background())
			panic(r)
		}
	}()

	// Выполняем пользовательский код
	err = fn(tx)
	if err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	// Коммитим
	if commitErr := tx.Commit(ctx); commitErr != nil {
		p.logger.Error("Failed to commit DB transaction", zap.Error(commitErr))
		return commitErr
	}
	return nil
}

// decodeStrict декодирует JSON-строку в структуру T с строгой проверкой синтаксиса.
func decodeStrict[T any](raw string) (T, error) {
	var v T
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return v, err
	}
	return v, nil
}

// publishTaskWithTimeout отправляет задачу генерации с таймаутом в 15 секунд.
func (p *NotificationProcessor) publishTaskWithTimeout(ctx context.Context, publishFunc func(context.Context) error) {
	taskCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := publishFunc(taskCtx); err != nil {
		p.logger.Error("Failed to publish generation task with timeout", zap.Error(err))
	}
}

// notifyClient отправляет клиенту обновление по WebSocket.
func (p *NotificationProcessor) notifyClient(id uuid.UUID, userID uuid.UUID, updateType sharedModels.UpdateType, status string, errorDetails *string) {
	payload := sharedModels.ClientStoryUpdate{
		ID:           id.String(),
		UserID:       userID.String(),
		UpdateType:   updateType,
		Status:       status,
		ErrorDetails: errorDetails,
	}
	if err := p.clientPub.PublishClientUpdate(context.Background(), payload); err != nil {
		p.logger.Error("Failed to publish client update", zap.String("id", id.String()), zap.Error(err))
	}
}

// mergeSetup объединяет существующий JSON setup и новые поля из updates.
func mergeSetup(existing json.RawMessage, updates map[string]interface{}) (json.RawMessage, error) {
	var current map[string]interface{}
	if len(existing) > 0 && string(existing) != "null" && string(existing) != "{}" {
		if err := json.Unmarshal(existing, &current); err != nil {
			current = make(map[string]interface{})
		}
	} else {
		current = make(map[string]interface{})
	}
	for k, v := range updates {
		if v == nil {
			delete(current, k)
		} else {
			current[k] = v
		}
	}
	newBytes, err := json.Marshal(current)
	return newBytes, err
}

// ensureStoryStatus получает историю по ID и проверяет, что её статус равен expected
// или, если текущий статус "generating", то InternalGenerationStep соответствует ожидаемому шагу.
// Возвращает nil, nil если статус не совпал (пропуск дальнейшей логики). Ошибку если проблема доступа.
func (p *NotificationProcessor) ensureStoryStatus(ctx context.Context, storyID uuid.UUID, expectedStatus sharedModels.StoryStatus) (*sharedModels.PublishedStory, error) {
	story, err := p.publishedRepo.GetByID(ctx, p.db, storyID)
	if err != nil {
		p.logger.Error("Failed to load story for status check", zap.Error(err), zap.Stringer("storyID", storyID))
		return nil, err
	}

	if story.Status == expectedStatus {
		return story, nil // Статус точно совпадает
	}

	// Если текущий статус 'generating', проверяем, не ретрай ли это для ожидаемого шага
	if story.Status == sharedModels.StatusGenerating {
		var correspondingStep sharedModels.InternalGenerationStep
		switch expectedStatus {
		case sharedModels.StatusModerationPending:
			correspondingStep = sharedModels.StepModeration
		case sharedModels.StatusProtagonistGoalPending:
			correspondingStep = sharedModels.StepProtagonistGoal
		case sharedModels.StatusScenePlannerPending:
			correspondingStep = sharedModels.StepScenePlanner
		case sharedModels.StatusSubTasksPending: // Этот статус более общий, может покрывать несколько шагов
			// Если SubTasksPending, это обычно CharacterGeneration или ImageGeneration этапы.
			// Для CharacterGeneration, InternalGenerationStep будет StepCharacterGeneration.
			// Для ImageGeneration (карт), InternalGenerationStep будет StepCardImageGeneration.
			// Для ImageGeneration (персонажей), InternalGenerationStep будет StepCharacterImageGeneration.
			// Мы можем проверить, является ли InternalGenerationStep одним из этих.
			if story.InternalGenerationStep != nil &&
				(*story.InternalGenerationStep == sharedModels.StepCharacterGeneration ||
					*story.InternalGenerationStep == sharedModels.StepCardImageGeneration ||
					*story.InternalGenerationStep == sharedModels.StepCharacterImageGeneration) {
				p.logger.Info("Story status is 'generating', but InternalGenerationStep matches a sub_task type for expected 'sub_tasks_pending'. Proceeding.",
					zap.Stringer("storyID", storyID),
					zap.String("current_internal_step", string(*story.InternalGenerationStep)),
					zap.String("expected_status", string(expectedStatus))) // Log expected status instead of specific step here
				return story, nil
			}
		case sharedModels.StatusSetupPending:
			correspondingStep = sharedModels.StepSetupGeneration
		case sharedModels.StatusFirstScenePending: // Обычно это StepInitialSceneJSON
			correspondingStep = sharedModels.StepInitialSceneJSON
		case sharedModels.StatusImageGenerationPending: // Может быть StepCoverImageGeneration, StepCardImageGeneration, StepCharacterImageGeneration
			if story.InternalGenerationStep != nil &&
				(*story.InternalGenerationStep == sharedModels.StepCoverImageGeneration ||
					*story.InternalGenerationStep == sharedModels.StepCardImageGeneration ||
					*story.InternalGenerationStep == sharedModels.StepCharacterImageGeneration) {
				p.logger.Info("Story status is 'generating', but InternalGenerationStep matches an image generation type for expected 'image_generation_pending'. Proceeding.",
					zap.Stringer("storyID", storyID),
					zap.String("current_internal_step", string(*story.InternalGenerationStep)),
					zap.String("expected_status", string(expectedStatus)))
				return story, nil
			}
		case sharedModels.StatusJsonGenerationPending: // Обычно это StepInitialSceneJSON
			correspondingStep = sharedModels.StepInitialSceneJSON
		// StatusInitialGeneration не обрабатывается здесь т.к. он сам по себе generating
		// StatusReady и StatusError не должны быть expected здесь для генерационных шагов.
		default:
			// Для других expectedStatus, если текущий 'generating', это, вероятно, несоответствие.
			p.logger.Warn("Story status is 'generating', but expected status does not have a direct retry step mapping for this check.",
				zap.Stringer("storyID", storyID),
				zap.String("current_status", string(story.Status)),
				zap.String("expected_status", string(expectedStatus)),
				zap.Any("internal_step", story.InternalGenerationStep))
			return nil, nil
		}

		if correspondingStep != "" && story.InternalGenerationStep != nil && *story.InternalGenerationStep == correspondingStep {
			p.logger.Info("Story status is 'generating', but InternalGenerationStep matches the expected step. Proceeding.",
				zap.Stringer("storyID", storyID),
				zap.String("current_status", string(story.Status)),
				zap.String("expected_status", string(expectedStatus)),
				zap.String("internal_step", string(*story.InternalGenerationStep)))
			return story, nil
		}
	}

	// Если ни одно из условий не выполнено, это несоответствие статуса
	p.logger.Warn("Story status mismatch, skipping handler",
		zap.Stringer("storyID", storyID),
		zap.String("current_status", string(story.Status)),
		zap.String("expected_status", string(expectedStatus)),
		zap.Any("internal_step", story.InternalGenerationStep))
	return nil, nil
}

// ensureGameStateStatus получает PlayerGameState по ID и проверяет ожидаемый статус.
func (p *NotificationProcessor) ensureGameStateStatus(ctx context.Context, stateID uuid.UUID, expected sharedModels.PlayerStatus) (*sharedModels.PlayerGameState, error) {
	gs, err := p.playerGameStateRepo.GetByID(ctx, p.db, stateID)
	if err != nil {
		p.logger.Error("Failed to load game state for status check", zap.Error(err))
		return nil, err
	}
	if gs.PlayerStatus != expected {
		p.logger.Warn("GameState status mismatch, skipping handler", zap.String("current", string(gs.PlayerStatus)), zap.String("expected", string(expected)))
		return nil, nil
	}
	return gs, nil
}

// validateJSON возвращает ошибку, если входная строка невалидна как JSON.
func validateJSON(raw string) error {
	if !json.Valid([]byte(raw)) {
		return fmt.Errorf("invalid JSON format: %s", raw)
	}
	return nil
}

// decodeStrictJSON декодирует JSON-строку в структуру T после проверки на валидность JSON.
func decodeStrictJSON[T any](raw string) (T, error) {
	var v T
	if err := validateJSON(raw); err != nil {
		return v, err
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return v, err
	}
	return v, nil
}

// parseUUIDField парсит строку как UUID и возвращает подробную ошибку.
func parseUUIDField(raw string, fieldName string) (uuid.UUID, error) {
	if raw == "" {
		return uuid.Nil, fmt.Errorf("missing %s", fieldName)
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid %s %q: %w", fieldName, raw, err)
	}
	return id, nil
}

// handleStoryError обновляет статус истории на Error, сохраняет в БД, отправляет WS и Push.
func (p *NotificationProcessor) handleStoryError(ctx context.Context, storyID uuid.UUID, userIDStr string, errorDetails string, eventType string) error {
	uid, err := parseUUIDField(userIDStr, "UserID")
	if err != nil {
		p.logger.Warn("Invalid UserID for story error", zap.Error(err))
		return nil
	}
	if updErr := p.publishedRepo.UpdateStatusAndError(ctx, p.db, storyID, sharedModels.StatusError, &errorDetails); updErr != nil {
		p.logger.Error("Failed to update story status to Error", zap.Error(updErr))
		return updErr
	}
	p.publishClientStoryUpdateOnError(storyID, uid, errorDetails)
	p.publishPushOnError(ctx, storyID, uid, errorDetails, eventType)
	return nil
}

// handleGameStateError обновляет статус GameState на Error, сохраняет в БД и отправляет WS-уведомление.
func (p *NotificationProcessor) handleGameStateError(ctx context.Context, gameStateID uuid.UUID, userIDStr string, errorDetails string) error {
	uid, err := parseUUIDField(userIDStr, "UserID")
	if err != nil {
		p.logger.Warn("Invalid UserID for game state error", zap.Error(err))
		return nil
	}
	gs, getErr := p.playerGameStateRepo.GetByID(ctx, p.db, gameStateID)
	if getErr != nil {
		p.logger.Error("Failed to get PlayerGameState for error handling", zap.Error(getErr))
		return getErr
	}
	gs.PlayerStatus = sharedModels.PlayerStatusError
	gs.ErrorDetails = &errorDetails
	gs.LastActivityAt = time.Now().UTC()
	if _, saveErr := p.playerGameStateRepo.Save(ctx, p.db, gs); saveErr != nil {
		p.logger.Error("Failed to save PlayerGameState with Error status", zap.Error(saveErr))
		return saveErr
	}
	payload := sharedModels.ClientStoryUpdate{
		ID:           gs.ID.String(),
		UserID:       uid.String(),
		UpdateType:   sharedModels.UpdateTypeGameState,
		Status:       string(gs.PlayerStatus),
		ErrorDetails: &errorDetails,
	}
	if errPub := p.clientPub.PublishClientUpdate(context.Background(), payload); errPub != nil {
		p.logger.Error("Failed to publish client game state update on error", zap.Error(errPub))
	}
	return nil
}

// handleDraftError обновляет статус StoryConfig на Error, сохраняет в БД и отправляет WS-уведомление.
func (p *NotificationProcessor) handleDraftError(ctx context.Context, configID uuid.UUID, errorDetails string) error {
	cfg, err := p.repo.GetByIDInternal(ctx, configID)
	if err != nil {
		p.logger.Warn("Invalid StoryConfigID for draft error", zap.Error(err))
		return nil
	}
	if updErr := p.repo.UpdateStatusAndError(ctx, configID, sharedModels.StatusError, errorDetails); updErr != nil {
		p.logger.Error("Failed to update StoryConfig status to Error", zap.Error(updErr))
		return updErr
	}
	p.publishClientDraftUpdate(ctx, cfg, &errorDetails)
	return nil
}
