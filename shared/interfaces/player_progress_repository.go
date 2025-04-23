package interfaces

import (
	"context"
	"novel-server/shared/models"

	"github.com/google/uuid"
)

// PlayerProgressRepository defines the interface for interacting with player progress data.
//
//go:generate mockery --name PlayerProgressRepository --output ./mocks --outpkg mocks --case=underscore
type PlayerProgressRepository interface {
	// GetByID retrieves a specific progress node by its unique ID.
	// Returns models.ErrNotFound if not found.
	GetByID(ctx context.Context, progressID uuid.UUID) (*models.PlayerProgress, error)

	// GetByStoryIDAndHash retrieves a specific progress node by story ID and state hash.
	// This is used to find existing nodes and promote state reuse.
	// Returns models.ErrNotFound if not found.
	GetByStoryIDAndHash(ctx context.Context, publishedStoryID uuid.UUID, stateHash string) (*models.PlayerProgress, error)

	// Save creates a new player progress node if progress.ID is zero UUID,
	// or updates an existing one based on progress.ID.
	// Returns the ID of the created/updated record.
	Save(ctx context.Context, progress *models.PlayerProgress) (uuid.UUID, error)

	// GetByUserIDAndStoryID retrieves the player's progress for a specific story.
	// DEPRECATED? Might still be useful for specific lookups, but primary access is via PlayerGameState.
	// Returns models.ErrNotFound if no progress exists for the given user and story.
	GetByUserIDAndStoryID(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (*models.PlayerProgress, error)

	// Delete removes a specific progress node by its unique ID.
	// Returns models.ErrNotFound if the node with the given ID does not exist.
	Delete(ctx context.Context, progressID uuid.UUID) error

	// UpdateFields updates specific fields of a progress node by its unique ID.
	UpdateFields(ctx context.Context, progressID uuid.UUID, updates map[string]interface{}) error
}
