package interfaces

import (
	"context"
	"novel-server/shared/models"

	"github.com/google/uuid"
)

// PlayerProgressRepository defines the interface for interacting with player progress data.
type PlayerProgressRepository interface {
	// Get retrieves the current player progress for a specific story.
	// Returns models.ErrNotFound if no progress exists for the given user and story.
	// Get(ctx context.Context, userID uint64, publishedStoryID uuid.UUID) (*models.PlayerProgress, error)

	// Upsert creates a new player progress record or updates an existing one
	// based on the composite primary key (user_id, published_story_id).
	// Upsert(ctx context.Context, progress *models.PlayerProgress) error

	// TODO: Potentially add a Delete method if needed later?

	// GetByUserIDAndStoryID retrieves the player's progress for a specific story.
	// Returns models.ErrNotFound if no progress exists for the given user and story.
	GetByUserIDAndStoryID(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (*models.PlayerProgress, error)

	// CreateOrUpdate creates a new player progress record or updates an existing one based on UserID and PublishedStoryID.
	// It should update all relevant fields including stats, variables, flags, hash, and the current choice index.
	CreateOrUpdate(ctx context.Context, progress *models.PlayerProgress) error

	// Delete removes the player progress record for a specific user and story.
	// Returns nil if the record was deleted or did not exist.
	Delete(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error
}
