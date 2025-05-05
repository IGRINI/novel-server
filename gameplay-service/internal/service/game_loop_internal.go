package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"novel-server/shared/models"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// UpdateSceneInternal updates the content of a scene.
func (s *gameLoopServiceImpl) UpdateSceneInternal(ctx context.Context, sceneID uuid.UUID, contentJSON string) error {
	log := s.logger.With(zap.String("sceneID", sceneID.String()))
	log.Info("UpdateSceneInternal called")

	var contentBytes []byte
	if contentJSON != "" {
		if !json.Valid([]byte(contentJSON)) {
			log.Warn("Invalid JSON received for scene content")
			return fmt.Errorf("%w: invalid scene content JSON format", models.ErrBadRequest)
		}
		contentBytes = []byte(contentJSON)
	} else {
		log.Warn("Attempted to set empty content for scene")
		return fmt.Errorf("%w: scene content cannot be empty", models.ErrBadRequest)
	}

	err := s.sceneRepo.UpdateContent(ctx, sceneID, contentBytes)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			log.Warn("Scene not found for update")
			return models.ErrNotFound
		}
		log.Error("Failed to update scene content in repository", zap.Error(err))
		return models.ErrInternalServer
	}

	log.Info("Scene content updated successfully by internal request")
	return nil
}

// DeleteSceneInternal deletes a scene.
func (s *gameLoopServiceImpl) DeleteSceneInternal(ctx context.Context, sceneID uuid.UUID) error {
	log := s.logger.With(zap.String("sceneID", sceneID.String()))
	log.Info("DeleteSceneInternal called")

	err := s.sceneRepo.Delete(ctx, sceneID)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			log.Warn("Scene not found for deletion")
			return models.ErrNotFound
		}
		log.Error("Failed to delete scene from repository", zap.Error(err))
		return models.ErrInternalServer
	}

	log.Info("Scene deleted successfully")
	return nil
}

// UpdatePlayerProgressInternal updates the player's progress internally.
func (s *gameLoopServiceImpl) UpdatePlayerProgressInternal(ctx context.Context, progressID uuid.UUID, progressData map[string]interface{}) error {
	log := s.logger.With(zap.String("progressID", progressID.String()))
	log.Info("Attempting to update player progress internally")

	currentProgress, err := s.playerProgressRepo.GetByID(ctx, progressID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("Player progress not found for internal update")
			return fmt.Errorf("%w: player progress with ID %s not found", models.ErrNotFound, progressID)
		}
		log.Error("Failed to get player progress by ID for internal update", zap.Error(err))
		return fmt.Errorf("failed to retrieve player progress: %w", err)
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

	err = s.playerProgressRepo.UpdateFields(ctx, currentProgress.ID, updates)
	if err != nil {
		log.Error("Failed to update player progress internally in repository", zap.Error(err))
		return fmt.Errorf("failed to update player progress in repository: %w", err)
	}

	log.Info("Player progress updated successfully internally")
	return nil
}
