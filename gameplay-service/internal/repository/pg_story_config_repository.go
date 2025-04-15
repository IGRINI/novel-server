package repository

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	models "novel-server/gameplay-service/internal/models"

	// service "novel-server/gameplay-service/internal/service" // <<< Убираем импорт сервиса
	"strconv"
	"strings"
	"time"

	interfaces "novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// Compile-time check
var _ StoryConfigRepository = (*pgStoryConfigRepository)(nil)

type pgStoryConfigRepository struct {
	db     interfaces.DBTX
	logger *zap.Logger
}

// Конструктор возвращает локальный интерфейс
func NewPgStoryConfigRepository(db interfaces.DBTX, logger *zap.Logger) StoryConfigRepository {
	return &pgStoryConfigRepository{
		db:     db,
		logger: logger.Named("PgStoryConfigRepo"),
	}
}

// GetAll - метод для получения всех конфигов пользователя (если нужен)
// func (r *pgStoryConfigRepository) GetAll(ctx context.Context, userID uint64) ([]models.StoryConfig, error) { ... }

// Create - Реализация метода Create
func (r *pgStoryConfigRepository) Create(ctx context.Context, config *models.StoryConfig) error {
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
func (r *pgStoryConfigRepository) GetByID(ctx context.Context, id uuid.UUID, userID uint64) (*models.StoryConfig, error) {
	query := `
        SELECT id, user_id, title, description, user_input, config, status, created_at, updated_at
        FROM story_configs
        WHERE id = $1 AND user_id = $2
    `
	config := &models.StoryConfig{}
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
func (r *pgStoryConfigRepository) GetByIDInternal(ctx context.Context, id uuid.UUID) (*models.StoryConfig, error) {
	query := `
        SELECT id, user_id, title, description, user_input, config, status, created_at, updated_at
        FROM story_configs
        WHERE id = $1
    `
	config := &models.StoryConfig{}
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
func (r *pgStoryConfigRepository) Update(ctx context.Context, config *models.StoryConfig) error {
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
	query := `SELECT COUNT(*) FROM story_configs WHERE user_id = $1 AND status = 'generating'`
	var count int
	logFields := []zap.Field{zap.Uint64("userID", userID)}
	r.logger.Debug("Counting active generations for user", logFields...)

	err := r.db.QueryRow(ctx, query, userID).Scan(&count)
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

// --- Пагинация --- //

const cursorSeparator = "_"

// encodeCursor создает строку курсора из времени и UUID.
func encodeCursor(t time.Time, id uuid.UUID) string {
	key := fmt.Sprintf("%d%s%s", t.UnixNano(), cursorSeparator, id.String())
	return base64.URLEncoding.EncodeToString([]byte(key))
}

// decodeCursor разбирает строку курсора на время и UUID.
func decodeCursor(cursor string) (time.Time, uuid.UUID, error) {
	if cursor == "" {
		return time.Time{}, uuid.Nil, nil // Нет курсора - нет ошибки
	}
	decodedBytes, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("некорректный формат курсора (base64 decode): %w", err)
	}
	key := string(decodedBytes)
	parts := strings.SplitN(key, cursorSeparator, 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, fmt.Errorf("некорректный формат курсора (separator)")
	}

	timestampNano, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("некорректный формат курсора (timestamp): %w", err)
	}
	t := time.Unix(0, timestampNano).UTC() // Важно использовать UTC

	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("некорректный формат курсора (uuid): %w", err)
	}

	return t, id, nil
}

// ListByUser возвращает список черновиков пользователя с курсорной пагинацией.
func (r *pgStoryConfigRepository) ListByUser(ctx context.Context, userID uint64, limit int, cursor string) ([]models.StoryConfig, string, error) {
	if limit <= 0 {
		limit = 10 // Значение по умолчанию
	}
	// +1 для проверки наличия следующей страницы
	fetchLimit := limit + 1

	cursorTime, cursorID, err := decodeCursor(cursor)
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

	configs := make([]models.StoryConfig, 0, limit)
	for rows.Next() {
		var config models.StoryConfig
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
		nextCursor = encodeCursor(lastConfig.CreatedAt, lastConfig.ID)
		// Обрезаем результат до запрошенного лимита
		configs = configs[:limit]
	}

	r.logger.Debug("User story configs listed successfully", append(logFields, zap.Int("count", len(configs)), zap.String("nextCursor", nextCursor))...)
	return configs, nextCursor, nil
}
