package interfaces

import (
	"context"
	"novel-server/shared/models"
)

// DynamicConfigRepository определяет методы для доступа к динамическим настройкам.
type DynamicConfigRepository interface {
	// GetByKey возвращает настройку по ее ключу.
	GetByKey(ctx context.Context, key string) (*models.DynamicConfig, error)
	// GetAll возвращает все динамические настройки.
	GetAll(ctx context.Context) ([]*models.DynamicConfig, error)
	// Upsert создает или обновляет настройку.
	// Обновляет только поля Value и Description. Key и UpdatedAt не трогает (UpdatedAt обновляется триггером).
	Upsert(ctx context.Context, config *models.DynamicConfig) error
}
