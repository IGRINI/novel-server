package interfaces

import (
	"context"
	"novel-server/shared/models"

	"github.com/google/uuid"
)

// PublishedStoryRepository defines the interface for interacting with published story data.
type PublishedStoryRepository interface {
	// Create inserts a new published story record. Used internally during publishing.
	Create(ctx context.Context, story *models.PublishedStory) error

	// GetByID retrieves a published story by its unique ID.
	GetByID(ctx context.Context, id uuid.UUID) (*models.PublishedStory, error)

	// UpdateStatusDetails updates the status, setup, error details, and potentially ending text of a published story.
	// Use this method for various state transitions after generation tasks.
	// Set setup, errorDetails, or endingText to nil if they shouldn't be updated.
	UpdateStatusDetails(ctx context.Context, id uuid.UUID, status models.StoryStatus, setup []byte, errorDetails *string, endingText *string) error

	// SetPublic updates the is_public flag for a story.
	// Requires userID for ownership check.
	SetPublic(ctx context.Context, id uuid.UUID, userID uint64, isPublic bool) error

	// ListPublic retrieves a paginated list of public, non-adult stories.
	ListPublic(ctx context.Context, limit, offset int) ([]*models.PublishedStory, error)

	// ListByUser retrieves a paginated list of stories created by a specific user.
	ListByUser(ctx context.Context, userID uint64, limit, offset int) ([]*models.PublishedStory, error)
}
