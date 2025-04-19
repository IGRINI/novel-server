package interfaces

import (
	"context"
	"encoding/json"
	"errors"
	"novel-server/shared/models"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrInvalidCursor сигнализирует о некорректном формате курсора пагинации.
// (Оставляем здесь или переносим в models/errors.go? Пока оставим здесь)
var ErrInvalidCursor = errors.New("invalid cursor format")

//go:generate mockery --name StoryConfigRepository --output ./mocks --outpkg mocks --case=underscore
type StoryConfigRepository interface {
	Create(ctx context.Context, config *models.StoryConfig) error
	GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*models.StoryConfig, error)
	GetByIDInternal(ctx context.Context, id uuid.UUID) (*models.StoryConfig, error)
	Update(ctx context.Context, config *models.StoryConfig) error
	CountActiveGenerations(ctx context.Context, userID uuid.UUID) (int, error)

	// Delete удаляет StoryConfig по ID, проверяя принадлежность пользователю.
	Delete(ctx context.Context, id uuid.UUID, userID uuid.UUID) error

	FindGeneratingConfigs(ctx context.Context) ([]*models.StoryConfig, error)

	DeleteTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, userID uuid.UUID) error

	ListByUserID(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]models.StoryConfig, string, error)

	// UpdateStatusAndConfig updates the status and config JSON of a story config.
	UpdateStatusAndConfig(ctx context.Context, id uuid.UUID, status models.StoryStatus, config json.RawMessage, title, description string) error

	// UpdateStatusAndError updates the status and error details of a story config.
	UpdateStatusAndError(ctx context.Context, id uuid.UUID, status models.StoryStatus, errorDetails string) error

	// FindGeneratingOlderThan finds drafts in 'generating' status older than a specified time.
	FindGeneratingOlderThan(ctx context.Context, threshold time.Time) ([]models.StoryConfig, error)

	// UpdateConfigAndInput updates the config and user input of a story config.
	UpdateConfigAndInput(ctx context.Context, id uuid.UUID, config, userInput []byte) error

	// UpdateConfigAndInputAndStatus updates the config, user input and status of a story config.
	UpdateConfigAndInputAndStatus(ctx context.Context, id uuid.UUID, configJSON, userInputJSON json.RawMessage, status models.StoryStatus) error
}
