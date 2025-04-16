package client

import (
	"context"

	"novel-server/shared/models"
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
	BanUser(ctx context.Context, userID uint64) error
	UnbanUser(ctx context.Context, userID uint64) error
	UpdateUser(ctx context.Context, userID uint64, payload UserUpdatePayload) error
	ResetPassword(ctx context.Context, userID uint64) (string, error)
	// RefreshAdminToken обновляет Access и Refresh токены, используя предоставленный Refresh Token.
	// Возвращает новые TokenDetails, Claims и ошибку.
	RefreshAdminToken(ctx context.Context, refreshToken string) (*models.TokenDetails, *models.Claims, error)
}

// UserUpdatePayload определяет структуру данных для запроса на обновление пользователя.
// Используем указатели, чтобы можно было передать только изменяемые поля.
type UserUpdatePayload struct {
	Email    *string  `json:"email,omitempty"`
	Roles    []string `json:"roles,omitempty"` // Передаем весь слайс, если роли меняются
	IsBanned *bool    `json:"is_banned,omitempty"`
}

// --- Структура ответа от auth-service ---
// (остальные структуры)
