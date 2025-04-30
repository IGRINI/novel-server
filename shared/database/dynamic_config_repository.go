package database

import (
	"context"
	"errors"
	"fmt"
	"novel-server/shared/models" // Импорт модели

	"github.com/georgysavva/scany/v2/pgxscan"
	pgxV5 "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type pgDynamicConfigRepository struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// NewPgDynamicConfigRepository создает новый экземпляр репозитория динамических настроек.
func NewPgDynamicConfigRepository(pool *pgxpool.Pool, logger *zap.Logger) *pgDynamicConfigRepository {
	return &pgDynamicConfigRepository{
		pool:   pool,
		logger: logger.Named("DynamicConfigRepo"),
	}
}

// GetByKey возвращает настройку по ее ключу.
func (r *pgDynamicConfigRepository) GetByKey(ctx context.Context, key string) (*models.DynamicConfig, error) {
	query := `SELECT key, value, created_at, updated_at FROM dynamic_configs WHERE key = $1`
	log := r.logger.With(zap.String("query_key", key))

	var config models.DynamicConfig
	err := pgxscan.Get(ctx, r.pool, &config, query, key)
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
func (r *pgDynamicConfigRepository) GetAll(ctx context.Context) ([]*models.DynamicConfig, error) {
	query := `SELECT key, value, created_at, updated_at FROM dynamic_configs ORDER BY key`
	log := r.logger

	var configs []*models.DynamicConfig
	err := pgxscan.Select(ctx, r.pool, &configs, query)
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
func (r *pgDynamicConfigRepository) Create(ctx context.Context, config *models.DynamicConfig) error {
	query := `
        INSERT INTO dynamic_configs (key, value)
        VALUES ($1, $2)
        ON CONFLICT (key) DO NOTHING
    `
	log := r.logger.With(zap.String("key", config.Key))

	commandTag, err := r.pool.Exec(ctx, query, config.Key, config.Value)
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
func (r *pgDynamicConfigRepository) Upsert(ctx context.Context, config *models.DynamicConfig) error {
	query := `
        INSERT INTO dynamic_configs (key, value)
        VALUES ($1, $2)
        ON CONFLICT (key) DO UPDATE SET
            value = EXCLUDED.value
            -- updated_at обновляется триггером
    `
	log := r.logger.With(zap.String("key", config.Key))

	_, err := r.pool.Exec(ctx, query, config.Key, config.Value)
	if err != nil {
		log.Error("Error upserting dynamic config", zap.Error(err))
		return fmt.Errorf("failed to upsert dynamic config with key %s: %w", config.Key, err)
	}
	log.Info("Dynamic config upserted successfully")
	return nil
}
