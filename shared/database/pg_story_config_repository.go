package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	// models "novel-server/gameplay-service/internal/models" // <<< Удаляем старый импорт

	// service "novel-server/gameplay-service/internal/service" // <<< Убираем импорт сервиса

	"strings"
	"time"

	interfaces "novel-server/shared/interfaces" // <<< Используем shared интерфейсы
	sharedModels "novel-server/shared/models"   // <<< Используем shared модели
	"novel-server/shared/utils"                 // <<< Добавляем импорт utils

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// Compile-time check
var _ interfaces.StoryConfigRepository = (*pgStoryConfigRepository)(nil) // <<< Проверяем реализацию shared интерфейса

type pgStoryConfigRepository struct {
	db     interfaces.DBTX
	logger *zap.Logger
}

// Оставляем здесь, так как используется в коде ниже
var ErrInvalidCursor = errors.New("invalid cursor format")

// Конструктор возвращает ОБЩИЙ интерфейс
func NewPgStoryConfigRepository(db interfaces.DBTX, logger *zap.Logger) interfaces.StoryConfigRepository {
	return &pgStoryConfigRepository{
		db:     db,
		logger: logger.Named("PgStoryConfigRepo"),
	}
}

// Create - Реализация метода Create
func (r *pgStoryConfigRepository) Create(ctx context.Context, config *sharedModels.StoryConfig) error { // <<< Используем sharedModels.StoryConfig
	query := `
        INSERT INTO story_configs
            (id, user_id, title, description, user_input, config, status, created_at, updated_at)
        VALUES
            ($1, $2, $3, $4, $5, $6, $7, $8, $9)
    `
	logFields := []zap.Field{zap.String("storyConfigID", config.ID.String()), zap.Uint64("userID", config.UserID)}
	r.logger.Debug("Creating story config", logFields...)

	_, err := r.db.Exec(ctx, query,
		config.ID,
		config.UserID,
		config.Title,
		config.Description,
		config.UserInput,
		config.Config,
		config.Status,
		config.CreatedAt,
		config.UpdatedAt,
	)
	if err != nil {
		r.logger.Error("Failed to create story config", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка создания story config: %w", err)
	}
	r.logger.Info("Story config created successfully", logFields...)
	return nil
}

// GetByID - Реализация метода GetByID
func (r *pgStoryConfigRepository) GetByID(ctx context.Context, id uuid.UUID, userID uint64) (*sharedModels.StoryConfig, error) { // <<< Возвращаем sharedModels.StoryConfig
	query := `
        SELECT id, user_id, title, description, user_input, config, status, created_at, updated_at
        FROM story_configs
        WHERE id = $1 AND user_id = $2
    `
	config := &sharedModels.StoryConfig{} // <<< Используем sharedModels.StoryConfig
	logFields := []zap.Field{zap.String("storyConfigID", id.String()), zap.Uint64("userID", userID)}
	r.logger.Debug("Getting story config by ID", logFields...)

	err := r.db.QueryRow(ctx, query, id, userID).Scan(
		&config.ID, &config.UserID, &config.Title, &config.Description,
		&config.UserInput, &config.Config, &config.Status, &config.CreatedAt, &config.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Warn("Story config not found by ID for user", logFields...)
			return nil, sharedModels.ErrNotFound
		}
		r.logger.Error("Failed to get story config by ID", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения story config %s: %w", id, err)
	}
	r.logger.Debug("Story config retrieved successfully", logFields...)
	return config, nil
}

// GetByIDInternal
func (r *pgStoryConfigRepository) GetByIDInternal(ctx context.Context, id uuid.UUID) (*sharedModels.StoryConfig, error) { // <<< Возвращаем sharedModels.StoryConfig
	query := `
        SELECT id, user_id, title, description, user_input, config, status, created_at, updated_at
        FROM story_configs
        WHERE id = $1
    `
	config := &sharedModels.StoryConfig{} // <<< Используем sharedModels.StoryConfig
	logFields := []zap.Field{zap.String("storyConfigID", id.String())}
	r.logger.Debug("Getting story config by ID (internal)", logFields...)

	err := r.db.QueryRow(ctx, query, id).Scan(
		&config.ID, &config.UserID, &config.Title, &config.Description,
		&config.UserInput, &config.Config, &config.Status, &config.CreatedAt, &config.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Warn("Story config not found by ID (internal)", logFields...)
			return nil, sharedModels.ErrNotFound
		}
		r.logger.Error("Failed to get story config by ID (internal)", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения story config %s (internal): %w", id, err)
	}
	r.logger.Debug("Story config retrieved successfully (internal)", logFields...)
	return config, nil
}

// Update
func (r *pgStoryConfigRepository) Update(ctx context.Context, config *sharedModels.StoryConfig) error { // <<< Используем sharedModels.StoryConfig
	query := `
        UPDATE story_configs SET
            title = $1, description = $2, user_input = $3, config = $4, status = $5, updated_at = $6
        WHERE id = $7 AND user_id = $8
    `
	logFields := []zap.Field{zap.String("storyConfigID", config.ID.String()), zap.Uint64("userID", config.UserID)}
	r.logger.Debug("Updating story config", logFields...)

	commandTag, err := r.db.Exec(ctx, query,
		config.Title, config.Description, config.UserInput, config.Config, config.Status, time.Now().UTC(), config.ID, config.UserID,
	)
	if err != nil {
		r.logger.Error("Failed to update story config", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления story config %s: %w", config.ID, err)
	}
	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("Attempted to update non-existent or unauthorized story config", logFields...)
		return sharedModels.ErrNotFound
	}
	r.logger.Info("Story config updated successfully", logFields...)
	return nil
}

// CountActiveGenerations
func (r *pgStoryConfigRepository) CountActiveGenerations(ctx context.Context, userID uint64) (int, error) {
	query := `SELECT COUNT(*) FROM story_configs WHERE user_id = $1 AND status = $2` // Используем $2 для статуса
	var count int
	logFields := []zap.Field{zap.Uint64("userID", userID)}
	r.logger.Debug("Counting active generations for user", logFields...)

	err := r.db.QueryRow(ctx, query, userID, sharedModels.StatusGenerating).Scan(&count) // <<< Используем sharedModels.StatusGenerating
	if err != nil {
		r.logger.Error("Failed to count active generations", append(logFields, zap.Error(err))...)
		return 0, fmt.Errorf("ошибка подсчета активных генераций для user %d: %w", userID, err)
	}
	r.logger.Debug("Active generations count retrieved", append(logFields, zap.Int("count", count))...)
	return count, nil
}

// Delete
func (r *pgStoryConfigRepository) Delete(ctx context.Context, id uuid.UUID, userID uint64) error {
	query := `DELETE FROM story_configs WHERE id = $1 AND user_id = $2`
	logFields := []zap.Field{
		zap.String("storyConfigID", id.String()),
		zap.Uint64("userID", userID),
	}
	r.logger.Debug("Deleting story config", logFields...)

	commandTag, err := r.db.Exec(ctx, query, id, userID)
	if err != nil {
		r.logger.Error("Failed to delete story config", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка удаления story config %s: %w", id, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("Attempted to delete non-existent or unauthorized story config", logFields...)
		return sharedModels.ErrNotFound
	}

	r.logger.Info("Story config deleted successfully", logFields...)
	return nil
}

// ListByUser возвращает список черновиков пользователя с курсорной пагинацией.
func (r *pgStoryConfigRepository) ListByUser(ctx context.Context, userID uint64, limit int, cursor string) ([]sharedModels.StoryConfig, string, error) { // <<< Возвращаем sharedModels.StoryConfig
	if limit <= 0 {
		limit = 10 // Значение по умолчанию
	}
	// +1 для проверки наличия следующей страницы
	fetchLimit := limit + 1

	cursorTime, cursorID, err := utils.DecodeCursor(cursor) // <<< Используем utils.DecodeCursor
	if err != nil {
		r.logger.Warn("Failed to decode cursor", zap.Uint64("userID", userID), zap.String("cursor", cursor), zap.Error(err))
		return nil, "", ErrInvalidCursor
	}

	var queryBuilder strings.Builder
	args := []interface{}{userID}
	paramIndex := 2 // Начинаем с $2

	queryBuilder.WriteString(`
        SELECT id, user_id, title, description, user_input, config, status, created_at, updated_at
        FROM story_configs
        WHERE user_id = $1
    `)

	if !cursorTime.IsZero() {
		queryBuilder.WriteString(fmt.Sprintf(" AND (created_at, id) < ($%d, $%d)", paramIndex, paramIndex+1))
		args = append(args, cursorTime, cursorID)
		paramIndex += 2
	}

	queryBuilder.WriteString(fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", paramIndex))
	args = append(args, fetchLimit)

	query := queryBuilder.String()
	logFields := []zap.Field{
		zap.Uint64("userID", userID),
		zap.Int("limit", limit),
		zap.String("cursor", cursor),
		zap.Time("cursorTime", cursorTime),
		zap.Stringer("cursorID", cursorID),
	}
	r.logger.Debug("Listing user story configs", append(logFields, zap.String("query", query))...)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to query user story configs", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка получения списка черновиков из БД: %w", err)
	}
	defer rows.Close()

	configs := make([]sharedModels.StoryConfig, 0, limit) // <<< Используем sharedModels.StoryConfig
	for rows.Next() {
		var config sharedModels.StoryConfig // <<< Используем sharedModels.StoryConfig
		err := rows.Scan(
			&config.ID, &config.UserID, &config.Title, &config.Description,
			&config.UserInput, &config.Config, &config.Status, &config.CreatedAt, &config.UpdatedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan story config row", append(logFields, zap.Error(err))...)
			return nil, "", fmt.Errorf("ошибка чтения данных из БД: %w", err)
		}
		configs = append(configs, config)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating story config rows", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка после чтения данных из БД: %w", err)
	}

	var nextCursor string
	if len(configs) > limit {
		// Есть следующая страница, формируем курсор из последнего *возвращаемого* элемента
		lastConfig := configs[limit-1]
		nextCursor = utils.EncodeCursor(lastConfig.CreatedAt, lastConfig.ID) // <<< Используем utils.EncodeCursor
		// Обрезаем результат до запрошенного лимита
		configs = configs[:limit]
	}

	r.logger.Debug("User story configs listed successfully", append(logFields, zap.Int("count", len(configs)), zap.String("nextCursor", nextCursor))...)
	return configs, nextCursor, nil
}

// FindGeneratingConfigs находит все StoryConfig со статусом 'generating'
func (r *pgStoryConfigRepository) FindGeneratingConfigs(ctx context.Context) ([]*sharedModels.StoryConfig, error) { // <<< Возвращаем sharedModels.StoryConfig
	sql := `SELECT id, user_id, title, description, user_input, config, status, created_at, updated_at
	        FROM story_configs
	        WHERE status = $1`

	rows, err := r.db.Query(ctx, sql, sharedModels.StatusGenerating) // <<< Используем sharedModels.StatusGenerating
	if err != nil {
		r.logger.Error("Ошибка при запросе генерирующихся конфигов", zap.Error(err))
		return nil, fmt.Errorf("ошибка БД при поиске generating configs: %w", err)
	}
	defer rows.Close()

	configs := make([]*sharedModels.StoryConfig, 0) // <<< Используем sharedModels.StoryConfig
	for rows.Next() {
		var cfg sharedModels.StoryConfig // <<< Используем sharedModels.StoryConfig
		var userInputJSON []byte
		var configJSON []byte // Для необработанного JSON из БД

		if err := rows.Scan(
			&cfg.ID,
			&cfg.UserID,
			&cfg.Title,
			&cfg.Description,
			&userInputJSON,
			&configJSON, // Читаем как []byte
			&cfg.Status,
			&cfg.CreatedAt,
			&cfg.UpdatedAt,
		); err != nil {
			r.logger.Error("Ошибка при сканировании строки генерирующегося конфига", zap.Error(err))
			return nil, fmt.Errorf("ошибка сканирования строки: %w", err)
		}

		// Десериализуем user_input
		if err := json.Unmarshal(userInputJSON, &cfg.UserInput); err != nil {
			r.logger.Warn("Не удалось десериализовать user_input для StoryConfig", zap.String("storyConfigID", cfg.ID.String()), zap.Error(err))
			// Не возвращаем ошибку, просто оставляем UserInput пустым (nil)
			cfg.UserInput = nil
		}

		// Десериализуем config (если не NULL)
		if len(configJSON) > 0 {
			if err := json.Unmarshal(configJSON, &cfg.Config); err != nil {
				r.logger.Warn("Не удалось десериализовать config для StoryConfig", zap.String("storyConfigID", cfg.ID.String()), zap.Error(err))
				// Не возвращаем ошибку, просто оставляем Config nil
				cfg.Config = nil
			}
		} else {
			cfg.Config = nil // Устанавливаем в nil, если в БД был NULL
		}

		configs = append(configs, &cfg)
	}

	if rows.Err() != nil {
		r.logger.Error("Ошибка после итерации по строкам генерирующихся конфигов", zap.Error(rows.Err()))
		return nil, fmt.Errorf("ошибка итерации: %w", rows.Err())
	}

	r.logger.Info("Найдено генерирующихся конфигов", zap.Int("count", len(configs)))
	return configs, nil
}
