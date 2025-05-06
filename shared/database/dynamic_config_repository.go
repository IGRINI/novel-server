package database

import (
	"context"
	"errors"
	"fmt"
	"novel-server/shared/interfaces"
	"novel-server/shared/models" // Импорт модели

	"github.com/georgysavva/scany/v2/pgxscan"
	pgxV5 "github.com/jackc/pgx/v5"

	// "github.com/jackc/pgx/v5/pgxpool" // Пул больше не используется напрямую
	"go.uber.org/zap"
)

const (
	getDynamicConfigByKeyQuery = `SELECT key, value, created_at, updated_at FROM dynamic_configs WHERE key = $1`
	getAllDynamicConfigsQuery  = `SELECT key, value, created_at, updated_at FROM dynamic_configs ORDER BY key`
	createDynamicConfigQuery   = `
        INSERT INTO dynamic_configs (key, value)
        VALUES ($1, $2)
        ON CONFLICT (key) DO NOTHING
    `
	upsertDynamicConfigQuery = `
        INSERT INTO dynamic_configs (key, value)
        VALUES ($1, $2)
        ON CONFLICT (key) DO UPDATE SET
            value = EXCLUDED.value
            -- updated_at обновляется триггером
    `
)

type pgDynamicConfigRepository struct {
	db     interfaces.DBTX // <<< ИЗМЕНЕНО: Используем DBTX
	logger *zap.Logger
}

// NewPgDynamicConfigRepository создает новый экземпляр репозитория динамических настроек.
// <<< ИЗМЕНЕНО: Принимает interfaces.DBTX >>>
func NewPgDynamicConfigRepository(querier interfaces.DBTX, logger *zap.Logger) *pgDynamicConfigRepository { // Возвращаемый тип оставляем конкретным
	return &pgDynamicConfigRepository{
		db:     querier,
		logger: logger.Named("DynamicConfigRepo"),
	}
}

// GetByKey возвращает настройку по ее ключу.
// <<< ИЗМЕНЕНО: Добавлен querier >>>
func (r *pgDynamicConfigRepository) GetByKey(ctx context.Context, querier interfaces.DBTX, key string) (*models.DynamicConfig, error) {
	log := r.logger.With(zap.String("query_key", key))

	var config models.DynamicConfig
	// <<< ИЗМЕНЕНО: Используем querier >>>
	err := pgxscan.Get(ctx, querier, &config, getDynamicConfigByKeyQuery, key)
	if err != nil {
		if errors.Is(err, pgxV5.ErrNoRows) {
			log.Warn("Dynamic config not found by key")
			return nil, models.ErrNotFound // Используем стандартную ошибку
		}
		log.Error("Error getting dynamic config by key", zap.Error(err))
		return nil, fmt.Errorf("failed to get dynamic config by key %s: %w", key, err)
	}
	return &config, nil
}

// GetAll возвращает все динамические настройки.
// <<< ИЗМЕНЕНО: Добавлен querier >>>
func (r *pgDynamicConfigRepository) GetAll(ctx context.Context, querier interfaces.DBTX) ([]*models.DynamicConfig, error) {
	log := r.logger

	var configs []*models.DynamicConfig
	// <<< ИЗМЕНЕНО: Используем querier >>>
	err := pgxscan.Select(ctx, querier, &configs, getAllDynamicConfigsQuery)
	if err != nil {
		// Ошибка pgx.ErrNoRows здесь не страшна, просто вернем пустой срез
		if errors.Is(err, pgxV5.ErrNoRows) {
			log.Info("No dynamic configs found, returning empty list.")
			return []*models.DynamicConfig{}, nil // Пустой срез, не ошибка
		}
		log.Error("Error getting all dynamic configs", zap.Error(err))
		return nil, fmt.Errorf("failed to get all dynamic configs: %w", err)
	}
	return configs, nil
}

// Create создает новую настройку. Если настройка с таким ключом уже существует, возвращает ошибку.
// <<< ИЗМЕНЕНО: Добавлен querier >>>
func (r *pgDynamicConfigRepository) Create(ctx context.Context, querier interfaces.DBTX, config *models.DynamicConfig) error {
	log := r.logger.With(zap.String("key", config.Key))

	// <<< ИЗМЕНЕНО: Используем querier >>>
	commandTag, err := querier.Exec(ctx, createDynamicConfigQuery, config.Key, config.Value)
	if err != nil {
		log.Error("Error creating dynamic config", zap.Error(err))
		return fmt.Errorf("failed to create dynamic config with key %s: %w", config.Key, err)
	}

	if commandTag.RowsAffected() == 0 {
		log.Warn("Dynamic config with this key already exists", zap.String("key", config.Key))
		// Возвращаем специфичную ошибку, если запись уже существует
		// TODO: Определить и использовать стандартную ошибку для конфликта/дубликата
		return fmt.Errorf("dynamic config with key '%s' already exists", config.Key) // Можно заменить на models.ErrAlreadyExists, если он есть
	}

	log.Info("Dynamic config created successfully")
	return nil
}

// Upsert создает или обновляет настройку.
// <<< ИЗМЕНЕНО: Добавлен querier >>>
func (r *pgDynamicConfigRepository) Upsert(ctx context.Context, querier interfaces.DBTX, config *models.DynamicConfig) error {
	log := r.logger.With(zap.String("key", config.Key))

	// <<< ИЗМЕНЕНО: Используем querier >>>
	_, err := querier.Exec(ctx, upsertDynamicConfigQuery, config.Key, config.Value)
	if err != nil {
		log.Error("Error upserting dynamic config", zap.Error(err))
		return fmt.Errorf("failed to upsert dynamic config with key %s: %w", config.Key, err)
	}
	log.Info("Dynamic config upserted successfully")
	return nil
}
