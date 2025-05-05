package interfaces

import (
	"context"

	"github.com/google/uuid"
)

// LikeRepository определяет методы для работы с лайками к опубликованным историям.
//
//go:generate mockery --name LikeRepository --output ./mocks --outpkg mocks --case=underscore
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

	// ListLikedStoryIDsByUserID возвращает список ID историй, лайкнутых пользователем, с пагинацией по курсору.
	// Возвращает срез ID, следующий курсор и ошибку.
	// Реализация должна поддерживать курсор (например, на основе времени лайка или ID лайка).
	ListLikedStoryIDsByUserID(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]uuid.UUID, string, error)
}
