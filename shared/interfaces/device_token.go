package interfaces

import (
	"context"
	"novel-server/shared/models"

	"github.com/google/uuid"
)

// UserDeviceTokenRepository определяет методы для работы с хранилищем токенов устройств.
type UserDeviceTokenRepository interface {
	// SaveDeviceToken сохраняет или обновляет токен устройства для пользователя.
	SaveDeviceToken(ctx context.Context, userID uuid.UUID, token, platform string) error
	// GetDeviceTokensForUser возвращает все активные токены для указанного пользователя.
	GetDeviceTokensForUser(ctx context.Context, userID uuid.UUID) ([]models.DeviceTokenInfo, error)
	// DeleteDeviceToken удаляет конкретный токен.
	DeleteDeviceToken(ctx context.Context, token string) error
	// DeleteDeviceTokensForUser удаляет все токены для указанного пользователя.
	DeleteDeviceTokensForUser(ctx context.Context, userID uuid.UUID) (int64, error)
}

// DeviceTokenService определяет методы для бизнес-логики управления токенами устройств.
type DeviceTokenService interface {
	// RegisterDeviceToken регистрирует новый токен устройства для пользователя.
	RegisterDeviceToken(ctx context.Context, userID uuid.UUID, data interface{}) error // Используем interface{} для DTO
	// UnregisterDeviceToken удаляет токен устройства.
	UnregisterDeviceToken(ctx context.Context, data interface{}) error // Используем interface{} для DTO
	// GetDeviceTokensForUser возвращает все активные токены для указанного пользователя.
	GetDeviceTokensForUser(ctx context.Context, userID uuid.UUID) ([]models.DeviceTokenInfo, error)
}
