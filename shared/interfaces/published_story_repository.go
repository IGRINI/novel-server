package interfaces

import (
	"context"
	"encoding/json"
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
	UpdateStatusDetails(ctx context.Context, id uuid.UUID, status models.StoryStatus, setup json.RawMessage, title, description, errorDetails *string) error

	// SetPublic updates the is_public flag for a story.
	// Requires userID for ownership check.
	SetPublic(ctx context.Context, id uuid.UUID, userID uuid.UUID, isPublic bool) error

	// ListPublic retrieves a paginated list of public, non-adult stories using cursor pagination.
	ListPublic(ctx context.Context, cursor string, limit int) ([]*models.PublishedStory, string, error)

	// ListByUserID retrieves a paginated list of stories created by a specific user using cursor pagination.
	ListByUserID(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*models.PublishedStory, string, error)

	// IncrementLikesCount атомарно увеличивает счетчик лайков для истории.
	IncrementLikesCount(ctx context.Context, id uuid.UUID) error

	// DecrementLikesCount атомарно уменьшает счетчик лайков для истории.
	// Реализация должна убедиться, что счетчик не уходит ниже нуля.
	DecrementLikesCount(ctx context.Context, id uuid.UUID) error

	// UpdateVisibility updates the visibility of a story.
	UpdateVisibility(ctx context.Context, storyID, userID uuid.UUID, isPublic bool) error

	// ListByIDs retrieves a list of published stories based on their IDs.
	// The order of returned stories is not guaranteed to match the input ID order.
	ListByIDs(ctx context.Context, ids []uuid.UUID) ([]*models.PublishedStory, error)

	// UpdateConfigAndSetup updates the config and setup of a story.
	UpdateConfigAndSetup(ctx context.Context, id uuid.UUID, config, setup []byte) error

	// UpdateConfigAndSetupAndStatus updates config, setup and status for a published story.
	UpdateConfigAndSetupAndStatus(ctx context.Context, id uuid.UUID, config, setup json.RawMessage, status models.StoryStatus) error
}
