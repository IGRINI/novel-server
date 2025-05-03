package interfaces

import (
	"context"
	"novel-server/shared/models"

	"github.com/google/uuid"
)

// UserRepository defines the interface for user data persistence (e.g., PostgreSQL).
// This interface is defined in shared so implementations and consumers can use it.
type UserRepository interface {
	// CreateUser inserts a new user into the database.
	// It should handle potential errors like duplicate usernames.
	CreateUser(ctx context.Context, user *models.User) error

	// GetUserByUsername retrieves a user by their username.
	// Returns models.ErrUserNotFound if the user does not exist.
	GetUserByUsername(ctx context.Context, username string) (*models.User, error)

	// GetUserByID retrieves a user by their ID.
	// Returns models.ErrUserNotFound if the user does not exist.
	GetUserByID(ctx context.Context, id uuid.UUID) (*models.User, error)

	// GetUserByEmail retrieves a user by their email address.
	// Returns models.ErrUserNotFound if the user does not exist.
	GetUserByEmail(ctx context.Context, email string) (*models.User, error)

	// GetUserCount retrieves the total number of users.
	GetUserCount(ctx context.Context) (int64, error)

	// ListUsers retrieves a list of users (add pagination parameters later).
	ListUsers(ctx context.Context, cursor string, limit int) ([]models.User, string, error)

	// SetUserBanStatus sets the ban status of a user.
	SetUserBanStatus(ctx context.Context, userID uuid.UUID, isBanned bool) error

	// UpdateUserFields обновляет только указанные поля пользователя (email, роли, статус бана).
	// Поля со значением nil не обновляются.
	UpdateUserFields(ctx context.Context, userID uuid.UUID, email *string, displayName *string, roles []string, isBanned *bool) error

	// UpdatePasswordHash обновляет хеш пароля пользователя.
	UpdatePasswordHash(ctx context.Context, userID uuid.UUID, newPasswordHash string) error

	// GetUsersByIDs retrieves multiple users by their IDs.
	// Returns an empty slice and nil error if no users are found for the given IDs.
	GetUsersByIDs(ctx context.Context, ids []uuid.UUID) ([]models.User, error)
}
