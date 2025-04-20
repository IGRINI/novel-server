package interfaces

import (
	"context"

	"github.com/google/uuid"
)

// UserInfo defines the basic user information needed by other services.
// Adjust fields as necessary.
type UserInfo struct {
	ID          uuid.UUID
	Username    string
	DisplayName string
	// Add other fields if needed, e.g., Email, Roles
}

// AuthServiceClient defines the interface for communicating with the auth service
// from other internal services.
type AuthServiceClient interface {
	// GetUsersInfo fetches basic information for a list of user IDs.
	// Returns a map of UserID to UserInfo and an error.
	GetUsersInfo(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]UserInfo, error)

	// TODO: Add other methods needed for inter-service communication with auth,
	// e.g., ValidateToken, VerifyInterServiceToken etc.
}
