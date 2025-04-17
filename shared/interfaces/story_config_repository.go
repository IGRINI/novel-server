package interfaces

import (
	"context"
	"errors"

	models "novel-server/shared/models" // <<< Используем shared models

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
}
