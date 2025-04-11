package interfaces

import (
	"context"
	"novel-server/shared/models"
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
	GetUserByID(ctx context.Context, id uint64) (*models.User, error)

	// GetUserByEmail retrieves a user by their email address.
	// Returns models.ErrUserNotFound if the user does not exist.
	GetUserByEmail(ctx context.Context, email string) (*models.User, error)
}
