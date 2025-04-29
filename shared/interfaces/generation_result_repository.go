package interfaces

import (
	"context"
	"novel-server/shared/models" // Предполагаем, что GenerationResult здесь
)

// GenerationResultRepository defines the interface for interacting with generation result data.
// Generation results are typically stored temporarily or for logging/debugging purposes.
//
//go:generate mockery --name GenerationResultRepository --output ./mocks --outpkg mocks --case=underscore
type GenerationResultRepository interface {
	// GetByTaskID retrieves a generation result by its unique task ID.
	// Returns models.ErrNotFound if the result for the given task ID is not found.
	GetByTaskID(ctx context.Context, taskID string) (*models.GenerationResult, error)

	// Save stores a generation result. This might be used by the generator service.
	// Implementations might decide whether to create or update based on TaskID uniqueness.
	Save(ctx context.Context, result *models.GenerationResult) error
}
