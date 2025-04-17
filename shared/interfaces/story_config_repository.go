package interfaces

import (
	"context"
	"errors"

	models "novel-server/shared/models" // <<< Используем shared models

	"github.com/google/uuid"
)

// ErrInvalidCursor сигнализирует о некорректном формате курсора пагинации.
// (Оставляем здесь или переносим в models/errors.go? Пока оставим здесь)
var ErrInvalidCursor = errors.New("invalid cursor format")

//go:generate mockery --name StoryConfigRepository --output ./mocks --outpkg mocks --case=underscore
type StoryConfigRepository interface {
	Create(ctx context.Context, config *models.StoryConfig) error
	GetByID(ctx context.Context, id uuid.UUID, userID uint64) (*models.StoryConfig, error)
	GetByIDInternal(ctx context.Context, id uuid.UUID) (*models.StoryConfig, error)
	Update(ctx context.Context, config *models.StoryConfig) error
	CountActiveGenerations(ctx context.Context, userID uint64) (int, error)

	// Delete удаляет StoryConfig по ID, проверяя принадлежность пользователю.
	Delete(ctx context.Context, id uuid.UUID, userID uint64) error

	// ListByUser возвращает список черновиков пользователя с курсорной пагинацией.
	// cursor - непрозрачная строка, полученная из предыдущего вызова.
	// Возвращает список конфигов, следующий курсор (пустой, если больше нет) и ошибку.
	ListByUser(ctx context.Context, userID uint64, limit int, cursor string) ([]models.StoryConfig, string, error)

	FindGeneratingConfigs(ctx context.Context) ([]*models.StoryConfig, error)
}
