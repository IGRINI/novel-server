package repository

import (
	"context"

	"novel-server/story-generator/internal/model"
)

// ResultRepository определяет методы для работы с хранилищем результатов генерации.
type ResultRepository interface {
	Save(ctx context.Context, result *model.GenerationResult) error
	// В будущем можно добавить методы для чтения результатов:
	// GetByID(ctx context.Context, id string) (*model.GenerationResult, error)
	// ListByUserID(ctx context.Context, userID string) ([]*model.GenerationResult, error)
}
