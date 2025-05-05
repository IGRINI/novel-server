package interfaces

import (
	"context"
	"novel-server/shared/models"

	"github.com/google/uuid"
)

// TokenRepository defines the interface for token persistence (e.g., Redis).
// This interface is defined in shared so that implementations (like in shared/database)
// and consumers (like the auth service) can depend on it without circular dependencies
// or using internal packages.
type TokenRepository interface {
	// SetToken stores the token details (Access & Refresh UUIDs mapped to UserID)
	// with appropriate TTLs.
	SetToken(ctx context.Context, userID uuid.UUID, td *models.TokenDetails) error

	// DeleteTokens removes the specified token UUIDs from the store.
	// Returns the number of keys deleted.
	DeleteTokens(ctx context.Context, userID uuid.UUID, accessUUID, refreshUUID string) (int64, error)

	// GetUserIDByAccessUUID checks if the Access UUID exists in the store and returns the associated UserID.
	// Returns models.ErrTokenNotFound if the token is not found (or expired).
	GetUserIDByAccessUUID(ctx context.Context, accessUUID string) (uuid.UUID, error)

	// GetUserIDByRefreshUUID checks if the Refresh UUID exists in the store and returns the associated UserID.
	// Returns models.ErrTokenNotFound if the token is not found (or expired).
	GetUserIDByRefreshUUID(ctx context.Context, refreshUUID string) (uuid.UUID, error)

	// DeleteRefreshUUID removes only the refresh token UUID from the store.
	// Useful for testing scenarios or specific logout logic.
	DeleteRefreshUUID(ctx context.Context, userID uuid.UUID, refreshUUID string) error

	// DeleteTokensByUserID removes all tokens (access and refresh) associated with a user ID.
	// Returns the number of tokens deleted.
	DeleteTokensByUserID(ctx context.Context, userID uuid.UUID) (int64, error)
}
