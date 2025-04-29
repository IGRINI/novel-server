package interfaces

import (
	"context"

	"novel-server/shared/models"
)

// PromptRepository defines the interface for prompt data storage operations.
type PromptRepository interface {
	// ListKeys retrieves a list of unique prompt keys.
	ListKeys(ctx context.Context) ([]string, error)

	// FindByKey retrieves all language versions of a prompt by its key.
	FindByKey(ctx context.Context, key string) ([]*models.Prompt, error)

	// GetByKeyAndLanguage retrieves a specific language version of a prompt.
	GetByKeyAndLanguage(ctx context.Context, key, language string) (*models.Prompt, error)

	// Upsert creates a new prompt or updates an existing one based on key and language.
	Upsert(ctx context.Context, prompt *models.Prompt) error

	// CreateBatch inserts multiple prompts, typically used for initializing a new key with all languages.
	// It should handle potential conflicts (e.g., key+language already exists) appropriately,
	// possibly by skipping existing ones or returning a specific error.
	CreateBatch(ctx context.Context, prompts []*models.Prompt) error

	// DeleteByKey removes all language versions of a prompt by its key.
	DeleteByKey(ctx context.Context, key string) error

	// GetByID retrieves a prompt by its unique ID.
	GetByID(ctx context.Context, id int64) (*models.Prompt, error)

	// DeleteByID removes a prompt by its unique ID.
	DeleteByID(ctx context.Context, id int64) error

	// GetAll retrieves all prompts without filtering. (Kept for potential internal use)
	GetAll(ctx context.Context) ([]*models.Prompt, error)
}
