package client

import (
	"context"

	"novel-server/shared/models"

	"github.com/google/uuid"
)

type AuthServiceHttpClient interface {
	Login(ctx context.Context, username, password string) (*models.TokenDetails, error)
	GenerateInterServiceToken(ctx context.Context, serviceName string) (string, error)
	SetInterServiceToken(token string)
	ValidateAdminToken(ctx context.Context, token string) (*models.Claims, error)
	GetUserCount(ctx context.Context) (int, error)
	// ListUsers получает список пользователей с пагинацией.
	// afterCursor - это идентификатор (или другой курсор), после которого нужно начать выборку.
	// Возвращает список пользователей, следующий курсор (nextCursor) и ошибку.
	ListUsers(ctx context.Context, limit int, afterCursor string) ([]models.User, string, error)
	BanUser(ctx context.Context, userID uuid.UUID, adminAccessToken string) error
	UnbanUser(ctx context.Context, userID uuid.UUID, adminAccessToken string) error
	UpdateUser(ctx context.Context, userID uuid.UUID, payload UserUpdatePayload, adminAccessToken string) error
	ResetPassword(ctx context.Context, userID uuid.UUID, adminAccessToken string) (string, error)
	// RefreshAdminToken обновляет Access и Refresh токены, используя предоставленный Refresh Token.
	// Возвращает новые TokenDetails, Claims и ошибку.
	RefreshAdminToken(ctx context.Context, refreshToken string) (*models.TokenDetails, *models.Claims, error)
	GetUserInfo(ctx context.Context, userID uuid.UUID) (*models.User, error)
}

// UserUpdatePayload defines the structure for updating user data via the auth service.
type UserUpdatePayload struct {
	Email       *string  `json:"email,omitempty"`
	DisplayName *string  `json:"display_name,omitempty"`
	Roles       []string `json:"roles,omitempty"`
	IsBanned    *bool    `json:"is_banned,omitempty"`
}

// --- Структура ответа от auth-service ---
// (остальные структуры)
