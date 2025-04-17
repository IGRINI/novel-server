package interfaces

import (
	"context"
	"time"

	"novel-server/shared/models"
)

// TokenVerifier defines the interface for verifying JWT tokens.
type TokenVerifier interface {
	// VerifyToken verifies a standard user JWT token and returns its claims.
	VerifyToken(ctx context.Context, tokenString string) (*models.Claims, error)
	// VerifyInterServiceToken verifies an inter-service JWT token.
	// It checks signature, expiry, issuer, and subject, but NOT UserID.
	VerifyInterServiceToken(ctx context.Context, tokenString string) (*models.Claims, error)
}

// TokenGenerator defines the interface for generating JWT tokens.
type TokenGenerator interface {
	// GenerateToken generates a standard user JWT token.
	GenerateToken(userID uint64, roles []string, ttl time.Duration) (string, error)
	// GenerateInterServiceToken generates an inter-service JWT token.
	GenerateInterServiceToken(issuer string, subject string, ttl time.Duration) (string, error)
}
