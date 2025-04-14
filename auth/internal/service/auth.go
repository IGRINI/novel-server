package service

import (
	"context"
	"novel-server/auth/internal/domain"
	"novel-server/shared/models"
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
	BanUser(ctx context.Context, userID uint64) error   // Бан пользователя
	UnbanUser(ctx context.Context, userID uint64) error // Разбан пользователя
	ValidateAndGetClaims(ctx context.Context, tokenString string) (*domain.Claims, error) // ValidateAndGetClaims method
	UpdateUser(ctx context.Context, userID uint64, email *string, roles []string, isBanned *bool) error // UpdateUser method
	UpdatePassword(ctx context.Context, userID uint64, newPassword string) error // Смена пароля (для админа)
}
