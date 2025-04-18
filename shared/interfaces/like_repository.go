package interfaces

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// Ошибки, специфичные для репозитория лайков
var (
	ErrLikeAlreadyExists = errors.New("like already exists")
	ErrLikeNotFound      = errors.New("like not found")
	// ErrStoryNotFound можно использовать из другого места или определить здесь, если нужно
	// ErrStoryNotFound = errors.New("published story not found")
)

// LikeRepository определяет методы для работы с лайками к опубликованным историям.
type LikeRepository interface {
	// AddLike добавляет запись о лайке.
	// Возвращает ErrLikeAlreadyExists, если пользователь уже лайкнул эту историю.
	// Может возвращать другие ошибки (например, связанные с FK на story_id).
	AddLike(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error

	// RemoveLike удаляет запись о лайке.
	// Возвращает ErrLikeNotFound, если лайка не было.
	// Может возвращать другие ошибки.
	RemoveLike(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error

	// CheckLike проверяет, лайкнул ли пользователь историю.
	// Возвращает true, если лайк существует, false иначе.
	CheckLike(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (bool, error)

	// CountLikes возвращает общее количество лайков для истории.
	// (Может быть полезно для периодической синхронизации, если используется кэш)
	CountLikes(ctx context.Context, publishedStoryID uuid.UUID) (int64, error)
}
