package service

import (
	"auth/internal/domain"
	"context"
	"shared/models"
)

// AuthService defines the interface for authentication and authorization logic.
type AuthService interface {
	Register(ctx context.Context, username, email, password string) (*models.User, error)
	Login(ctx context.Context, username, password string) (*models.TokenDetails, error)
	Logout(ctx context.Context, accessUUID, refreshUUID string) error
	Refresh(ctx context.Context, refreshToken string) (*models.TokenDetails, error)
	VerifyAccessToken(ctx context.Context, tokenString string) (*domain.Claims, error)
	GenerateInterServiceToken(ctx context.Context, serviceName string) (string, error) // Межсервисная авторизация
	VerifyInterServiceToken(ctx context.Context, tokenString string) (string, error)   // Межсервисная авторизация
}
