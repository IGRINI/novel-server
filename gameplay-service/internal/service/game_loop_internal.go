package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"novel-server/shared/models"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// UpdateSceneInternal updates the content of a scene.
func (s *gameLoopServiceImpl) UpdateSceneInternal(ctx context.Context, sceneID uuid.UUID, contentJSON string) error {
	log := s.logger.With(zap.String("sceneID", sceneID.String()))
	log.Info("UpdateSceneInternal called")

	var contentBytes []byte
	if contentJSON == "" {
		log.Warn("Attempted to set empty content for scene")
		return fmt.Errorf("%w: scene content cannot be empty", models.ErrBadRequest)
	}
	var raw json.RawMessage
	if err := DecodeStrictJSON([]byte(contentJSON), &raw); err != nil {
		log.Warn("Invalid JSON received for scene content", zap.Error(err))
		return err
	}
	contentBytes = []byte(contentJSON)

	err := s.sceneRepo.UpdateContent(ctx, s.pool, sceneID, contentBytes)
	if wrapErr := WrapRepoError(s.logger, err, "StoryScene"); wrapErr != nil {
		return wrapErr
	}

	log.Info("Scene content updated successfully by internal request")
	return nil
}

// DeleteSceneInternal deletes a scene.
func (s *gameLoopServiceImpl) DeleteSceneInternal(ctx context.Context, sceneID uuid.UUID) error {
	log := s.logger.With(zap.String("sceneID", sceneID.String()))
	log.Info("DeleteSceneInternal called")

	err := s.sceneRepo.Delete(ctx, s.pool, sceneID)
	if wrapErr := WrapRepoError(s.logger, err, "StoryScene"); wrapErr != nil {
		return wrapErr
	}

	log.Info("Scene deleted successfully")
	return nil
}

// UpdatePlayerProgressInternal updates the player's progress internally.
func (s *gameLoopServiceImpl) UpdatePlayerProgressInternal(ctx context.Context, progressID uuid.UUID, progressData map[string]interface{}) error {
	log := s.logger.With(zap.String("progressID", progressID.String()))
	log.Info("Attempting to update player progress internally")

	currentProgress, err := s.playerProgressRepo.GetByID(ctx, s.pool, progressID)
	if wrapErr := WrapRepoError(s.logger, err, "PlayerProgress"); wrapErr != nil {
		if errors.Is(wrapErr, models.ErrNotFound) {
			log.Warn("Player progress not found for internal update")
		}
		return wrapErr
	}

	progressDataBytes, err := json.Marshal(progressData)
	if err != nil {
		log.Error("Failed to marshal progress data for internal update", zap.Error(err))
		return fmt.Errorf("%w: failed to marshal progress data", models.ErrBadRequest)
	}

	updates := map[string]interface{}{
		"progress_data_json": json.RawMessage(progressDataBytes),
		"updated_at":         time.Now().UTC(),
	}

	err = s.playerProgressRepo.UpdateFields(ctx, s.pool, currentProgress.ID, updates)
	if err != nil {
		log.Error("Failed to update player progress internally in repository", zap.Error(err))
		return fmt.Errorf("failed to update player progress in repository: %w", err)
	}

	log.Info("Player progress updated successfully internally")
	return nil
}
