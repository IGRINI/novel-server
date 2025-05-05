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
	"novel-server/shared/models"
	sharedModels "novel-server/shared/models" // <<< Используем shared модели
	"novel-server/shared/utils"               // <<< Добавляем импорт utils

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// Compile-time check
var _ interfaces.StoryConfigRepository = (*pgStoryConfigRepository)(nil) // <<< Проверяем реализацию shared интерфейса

// --- Constants for SQL Queries ---
const (
	createStoryConfigQuery = `
        INSERT INTO story_configs
            (id, user_id, title, description, user_input, config, status, language, created_at, updated_at)
        VALUES
            ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
    `
	getStoryConfigByIDQuery = `
        SELECT id, user_id, title, description, user_input, config, status, language, created_at, updated_at
        FROM story_configs
        WHERE id = $1 AND user_id = $2
    `
	getStoryConfigByIDInternalQuery = `
        SELECT id, user_id, title, description, user_input, config, status, language, created_at, updated_at
        FROM story_configs
        WHERE id = $1
    `
	updateStoryConfigQuery = `
        UPDATE story_configs SET
            title = $1, description = $2, user_input = $3, config = $4, status = $5, updated_at = $6
        WHERE id = $7 AND user_id = $8
    `
	countStoryConfigActiveGenerationsQuery = `SELECT COUNT(*) FROM story_configs WHERE user_id = $1 AND status = $2`
	deleteStoryConfigQuery                 = `DELETE FROM story_configs WHERE id = $1 AND user_id = $2`
	listStoryConfigsByUserIDBaseQuery      = `
        SELECT id, user_id, title, description, user_input, config, status, language, created_at, updated_at
        FROM story_configs
        WHERE user_id = $1
    `
	findGeneratingConfigsQuery = `
        SELECT id, user_id, title, description, user_input, config, status, language, created_at, updated_at
        FROM story_configs
        WHERE status = $1 OR status = $2
        ORDER BY updated_at ASC
    `
	findGeneratingOlderThanQuery = `
        SELECT id, user_id, title, description, user_input, config, status, language, created_at, updated_at
        FROM story_configs
        WHERE status = 'generating' AND created_at < $1
        ORDER BY created_at ASC
    `
	updateConfigAndInputQuery = `
        UPDATE story_configs SET
            config = $1, user_input = $2, updated_at = $3
        WHERE id = $4
    `
	updateConfigAndInputAndStatusQuery = `
        UPDATE story_configs SET
            config = $1, user_input = $2, status = $3, updated_at = $4
        WHERE id = $5
    `
	updateStatusAndConfigQuery = `
        UPDATE story_configs SET
            status = $1, config = $2, title = $3, description = $4, updated_at = $5
        WHERE id = $6
    `
	updateStatusAndErrorQuery = `
        UPDATE story_configs
        SET status = $2, error_details = $3, updated_at = NOW()
        WHERE id = $1
    `
	findAndMarkStaleGeneratingDraftsQueryBase = `
        UPDATE story_configs
        SET status = $1, error_details = $2, updated_at = NOW()
        WHERE status = $3
    `
	updateStoryConfigStatusQuery = `
        UPDATE story_configs SET
            status = $1, updated_at = $2
        WHERE id = $3
    `
	selectUserInputForUpdateQuery = `SELECT user_input FROM story_configs WHERE id = $1 FOR UPDATE`
	updateUserInputQuery          = `UPDATE story_configs SET user_input = $1, updated_at = NOW() WHERE id = $2`
)

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

// scanStoryConfig scans a row into a StoryConfig struct.
// It handles json unmarshalling and potential ErrNoRows.
func scanStoryConfig(row pgx.Row) (*sharedModels.StoryConfig, error) {
	var config sharedModels.StoryConfig
	var userInputJSON, configJSON []byte

	err := row.Scan(
		&config.ID,
		&config.UserID,
		&config.Title,
		&config.Description,
		&userInputJSON, // Scan JSON as bytes
		&configJSON,    // Scan JSON as bytes
		&config.Status,
		&config.Language,
		&config.CreatedAt,
		&config.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, sharedModels.ErrNotFound
		}
		// Log the error in the calling function with more context
		return nil, fmt.Errorf("error scanning story config row: %w", err)
	}

	// Unmarshal UserInput
	if len(userInputJSON) > 0 {
		if err := json.Unmarshal(userInputJSON, &config.UserInput); err != nil {
			// Log but don't fail the whole scan, just leave UserInput as nil
			// Consider logging the error in the caller for ID context?
			// r.logger.Warn("Failed to unmarshal user_input", zap.Error(err))
			config.UserInput = nil
		}
	}

	// Unmarshal Config
	if len(configJSON) > 0 {
		if err := json.Unmarshal(configJSON, &config.Config); err != nil {
			// Log but don't fail the whole scan, just leave Config as nil
			// r.logger.Warn("Failed to unmarshal config", zap.Error(err))
			config.Config = nil
		}
	}

	return &config, nil
}

// Create - Реализация метода Create
func (r *pgStoryConfigRepository) Create(ctx context.Context, config *sharedModels.StoryConfig) error {
	logFields := []zap.Field{zap.String("storyConfigID", config.ID.String()), zap.String("userID", config.UserID.String())}
	r.logger.Debug("Creating story config", logFields...)

	_, err := r.db.Exec(ctx, createStoryConfigQuery,
		config.ID,
		config.UserID,
		config.Title,
		config.Description,
		config.UserInput,
		config.Config,
		config.Status,
		config.Language,
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
func (r *pgStoryConfigRepository) GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*sharedModels.StoryConfig, error) {
	logFields := []zap.Field{zap.String("storyConfigID", id.String()), zap.String("userID", userID.String())}
	r.logger.Debug("Getting story config by ID", logFields...)

	row := r.db.QueryRow(ctx, getStoryConfigByIDQuery, id, userID)
	config, err := scanStoryConfig(row)

	if err != nil {
		if err == sharedModels.ErrNotFound {
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
func (r *pgStoryConfigRepository) GetByIDInternal(ctx context.Context, id uuid.UUID) (*sharedModels.StoryConfig, error) {
	logFields := []zap.Field{zap.String("storyConfigID", id.String())}
	r.logger.Debug("Getting story config by ID (internal)", logFields...)

	row := r.db.QueryRow(ctx, getStoryConfigByIDInternalQuery, id)
	config, err := scanStoryConfig(row)

	if err != nil {
		if err == sharedModels.ErrNotFound {
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
func (r *pgStoryConfigRepository) Update(ctx context.Context, config *sharedModels.StoryConfig) error {
	logFields := []zap.Field{zap.String("storyConfigID", config.ID.String()), zap.String("userID", config.UserID.String())}
	r.logger.Debug("Updating story config", logFields...)

	commandTag, err := r.db.Exec(ctx, updateStoryConfigQuery,
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
func (r *pgStoryConfigRepository) CountActiveGenerations(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	logFields := []zap.Field{zap.String("userID", userID.String())}
	r.logger.Debug("Counting active generations for user", logFields...)

	err := r.db.QueryRow(ctx, countStoryConfigActiveGenerationsQuery, userID, "generating").Scan(&count)
	if err != nil {
		r.logger.Error("Failed to count active generations", append(logFields, zap.Error(err))...)
		return 0, fmt.Errorf("ошибка подсчета активных генераций для user %s: %w", userID.String(), err)
	}
	r.logger.Debug("Active generations count retrieved", append(logFields, zap.Int("count", count))...)
	return count, nil
}

// Delete
func (r *pgStoryConfigRepository) Delete(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	logFields := []zap.Field{
		zap.String("storyConfigID", id.String()),
		zap.String("userID", userID.String()),
	}
	r.logger.Debug("Deleting story config", logFields...)

	commandTag, err := r.db.Exec(ctx, deleteStoryConfigQuery, id, userID)
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

// DeleteTx удаляет StoryConfig по ID в рамках транзакции, проверяя принадлежность пользователю.
func (r *pgStoryConfigRepository) DeleteTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, userID uuid.UUID) error {
	logFields := []zap.Field{
		zap.String("storyConfigID", id.String()),
		zap.String("userID", userID.String()),
		zap.String("tx", "active"), // Indicate transaction context
	}
	r.logger.Debug("Deleting story config within transaction", logFields...)

	commandTag, err := tx.Exec(ctx, deleteStoryConfigQuery, id, userID)
	if err != nil {
		r.logger.Error("Failed to delete story config within transaction", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка удаления story config %s в транзакции: %w", id, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("Attempted to delete non-existent or unauthorized story config within transaction", logFields...)
		return sharedModels.ErrNotFound
	}

	r.logger.Info("Story config deleted successfully within transaction", logFields...)
	return nil
}

// ListByUserID возвращает список черновиков пользователя с курсорной пагинацией.
func (r *pgStoryConfigRepository) ListByUserID(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.StoryConfig, string, error) {
	if limit <= 0 {
		limit = 10 // Значение по умолчанию
	}
	fetchLimit := limit + 1

	cursorTime, cursorID, err := utils.DecodeCursor(cursor)
	if err != nil {
		return nil, "", fmt.Errorf("ошибка декодирования курсора: %w", err)
	}

	var queryBuilder strings.Builder
	args := []interface{}{userID}
	paramIndex := 2 // Начинаем с $2

	queryBuilder.WriteString(listStoryConfigsByUserIDBaseQuery)

	if !cursorTime.IsZero() {
		queryBuilder.WriteString(fmt.Sprintf(" AND (created_at, id) < ($%d, $%d)", paramIndex, paramIndex+1))
		args = append(args, cursorTime, cursorID)
		paramIndex += 2
	}

	queryBuilder.WriteString(fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", paramIndex))
	args = append(args, fetchLimit)

	query := queryBuilder.String()
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
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

	configs := make([]sharedModels.StoryConfig, 0, limit)
	for rows.Next() {
		config, err := scanStoryConfig(rows)
		if err != nil {
			r.logger.Error("Failed to scan story config row", append(logFields, zap.Error(err))...)
			return nil, "", fmt.Errorf("ошибка чтения данных из БД: %w", err)
		}
		configs = append(configs, *config)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating story config rows", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка после чтения данных из БД: %w", err)
	}

	var nextCursor string
	if len(configs) > limit {
		// Есть следующая страница, формируем курсор из последнего *возвращаемого* элемента
		lastConfig := configs[limit-1]
		nextCursor = utils.EncodeCursor(lastConfig.CreatedAt, lastConfig.ID)
		// Обрезаем результат до запрошенного лимита
		configs = configs[:limit]
	}

	r.logger.Debug("User story configs listed successfully", append(logFields, zap.Int("count", len(configs)), zap.String("nextCursor", nextCursor))...)
	return configs, nextCursor, nil
}

// FindGeneratingConfigs находит все StoryConfig со статусом 'generating'
func (r *pgStoryConfigRepository) FindGeneratingConfigs(ctx context.Context) ([]*sharedModels.StoryConfig, error) {
	logFields := []zap.Field{zap.String("status1", "generating"), zap.String("status2", "revising")}
	r.logger.Debug("Finding story configs with generating or revising status", logFields...)

	rows, err := r.db.Query(ctx, findGeneratingConfigsQuery, "generating", "revising")
	if err != nil {
		r.logger.Error("Failed to query generating/revising story configs", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка БД при поиске generating/revising story configs: %w", err)
	}
	defer rows.Close()

	configs := make([]*sharedModels.StoryConfig, 0)
	for rows.Next() {
		config, err := scanStoryConfig(rows)
		if err != nil {
			r.logger.Error("Ошибка при сканировании строки генерирующегося конфига", zap.Error(err))
			return nil, fmt.Errorf("ошибка сканирования строки: %w", err)
		}
		configs = append(configs, config)
	}

	if rows.Err() != nil {
		r.logger.Error("Ошибка после итерации по строкам генерирующихся конфигов", zap.Error(rows.Err()))
		return nil, fmt.Errorf("ошибка итерации: %w", rows.Err())
	}

	r.logger.Info("Найдено генерирующихся конфигов", zap.Int("count", len(configs)))
	return configs, nil
}

func (r *pgStoryConfigRepository) FindGeneratingOlderThan(ctx context.Context, threshold time.Time) ([]sharedModels.StoryConfig, error) {
	logFields := []zap.Field{zap.Time("threshold", threshold)}
	r.logger.Debug("Finding generating story configs older than threshold", logFields...)

	rows, err := r.db.Query(ctx, findGeneratingOlderThanQuery, threshold)
	if err != nil {
		r.logger.Error("Failed to query generating story configs older than threshold", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка поиска старых генерирующихся черновиков: %w", err)
	}
	defer rows.Close()

	configs := make([]sharedModels.StoryConfig, 0)
	for rows.Next() {
		config, err := scanStoryConfig(rows)
		if err != nil {
			r.logger.Error("Failed to scan story config row in FindGeneratingOlderThan", append(logFields, zap.Error(err))...)
			return nil, fmt.Errorf("ошибка сканирования строки черновика: %w", err)
		}
		configs = append(configs, *config)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error during row iteration in FindGeneratingOlderThan", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка итерации по строкам черновиков: %w", err)
	}

	r.logger.Debug("Found old generating story configs", append(logFields, zap.Int("count", len(configs)))...)
	return configs, nil
}

// UpdateConfigAndInput обновляет поля config и user_input для StoryConfig.
func (r *pgStoryConfigRepository) UpdateConfigAndInput(ctx context.Context, id uuid.UUID, config, userInput []byte) error {
	logFields := []zap.Field{zap.String("storyConfigID", id.String())}
	r.logger.Debug("Updating story config config/userInput", logFields...)

	commandTag, err := r.db.Exec(ctx, updateConfigAndInputQuery, config, userInput, time.Now().UTC(), id)
	if err != nil {
		r.logger.Error("Failed to update story config config/userInput", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления config/input для story config %s: %w", id, err)
	}
	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("Attempted to update config/userInput for non-existent story config", logFields...)
		return sharedModels.ErrNotFound
	}
	r.logger.Info("Story config config/userInput updated successfully", logFields...)
	return nil
}

// UpdateConfigAndInputAndStatus обновляет поля config, user_input и status.
func (r *pgStoryConfigRepository) UpdateConfigAndInputAndStatus(ctx context.Context, id uuid.UUID, configJSON, userInputJSON json.RawMessage, status sharedModels.StoryStatus) error {
	logFields := []zap.Field{
		zap.String("storyConfigID", id.String()),
		zap.String("newStatus", string(status)),
	}
	r.logger.Debug("Updating story config config/userInput/status", logFields...)

	commandTag, err := r.db.Exec(ctx, updateConfigAndInputAndStatusQuery, configJSON, userInputJSON, status, time.Now().UTC(), id)
	if err != nil {
		r.logger.Error("Failed to update story config config/userInput/status", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления config/input/status для story config %s: %w", id, err)
	}
	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("Attempted to update config/userInput/status for non-existent story config", logFields...)
		return sharedModels.ErrNotFound
	}
	r.logger.Info("Story config config/userInput/status updated successfully", logFields...)
	return nil
}

// UpdateStatusAndConfig обновляет статус, конфиг, заголовок и описание черновика.
func (r *pgStoryConfigRepository) UpdateStatusAndConfig(ctx context.Context, id uuid.UUID, status models.StoryStatus, config json.RawMessage, title, description string) error {
	logFields := []zap.Field{
		zap.String("storyConfigID", id.String()),
		zap.String("newStatus", string(status)),
		zap.Int("configSize", len(config)),
		zap.String("title", title),
		zap.String("description", description),
	}
	r.logger.Debug("Updating story config status and config data", logFields...)

	commandTag, err := r.db.Exec(ctx, updateStatusAndConfigQuery, status, config, title, description, time.Now().UTC(), id)
	if err != nil {
		r.logger.Error("Failed to update story config status and config data", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления статуса и конфига черновика %s: %w", id, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("Story config not found for status/config update", logFields...)
		return models.ErrNotFound
	}

	r.logger.Info("Story config status and config data updated successfully", logFields...)
	return nil
}

// UpdateStatusAndError обновляет статус и ошибку черновика.
func (r *pgStoryConfigRepository) UpdateStatusAndError(ctx context.Context, id uuid.UUID, status models.StoryStatus, errorDetails string) error {
	logFields := []zap.Field{
		zap.String("storyConfigID", id.String()),
		zap.String("newStatus", string(status)),
		zap.String("errorDetails", errorDetails),
	}
	r.logger.Debug("Updating story config status and error details", logFields...)

	commandTag, err := r.db.Exec(ctx, updateStatusAndErrorQuery, id, status, errorDetails)
	if err != nil {
		r.logger.Error("Failed to update story config status and error details", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления статуса и ошибки черновика %s: %w", id, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("Story config not found for status/error update", logFields...)
		return models.ErrNotFound
	}

	r.logger.Info("Story config status and error details updated successfully", logFields...)
	return nil
}

// FindAndMarkStaleGeneratingDraftsAsError находит черновики со статусом 'generating',
// чье время последнего обновления старше указанного порога, или все такие черновики, если порог 0,
// и устанавливает им статус 'Error'.
// Возвращает количество обновленных записей и ошибку.
func (r *pgStoryConfigRepository) FindAndMarkStaleGeneratingDraftsAsError(ctx context.Context, staleThreshold time.Duration) (int64, error) {
	errorDetails := "Task timed out or got stuck during generation."
	args := []interface{}{
		models.StatusError,      // $1
		errorDetails,            // $2
		models.StatusGenerating, // $3
	}
	query := findAndMarkStaleGeneratingDraftsQueryBase
	staleTime := time.Now().Add(-staleThreshold)

	logFields := []zap.Field{
		zap.String("status_to_set", string(models.StatusError)),
		zap.String("required_status", string(models.StatusGenerating)),
		zap.Duration("stale_threshold_duration", staleThreshold),
	}

	// Добавляем условие времени только если staleThreshold > 0
	if staleThreshold > 0 {
		query += " AND updated_at < $4" // $4 будет staleTime
		args = append(args, staleTime)
		logFields = append(logFields, zap.Time("stale_time_threshold", staleTime))
	} else {
		r.logger.Info("Stale threshold is zero, checking all generating drafts regardless of time.", logFields...)
	}

	r.logger.Debug("Executing FindAndMarkStaleGeneratingDraftsAsError", logFields...)

	cmdTag, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		r.logger.Error("Error executing FindAndMarkStaleGeneratingDraftsAsError", zap.Error(err))
		return 0, fmt.Errorf("ошибка при обновлении статуса зависших черновиков: %w", err)
	}

	updatedCount := cmdTag.RowsAffected()
	r.logger.Info("FindAndMarkStaleGeneratingDraftsAsError completed", zap.Int64("updated_count", updatedCount))

	return updatedCount, nil
}

// UpdateStatus обновляет статус конфигурации истории
func (r *pgStoryConfigRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status models.StoryStatus) error {
	logFields := []zap.Field{
		zap.String("storyConfigID", id.String()),
		zap.String("newStatus", string(status)),
	}
	r.logger.Debug("Updating story config status", logFields...)

	commandTag, err := r.db.Exec(ctx, updateStoryConfigStatusQuery, status, time.Now().UTC(), id)
	if err != nil {
		r.logger.Error("Failed to update story config status", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления статуса story config %s: %w", id, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("Story config not found for status update", logFields...)
		return models.ErrNotFound
	}

	r.logger.Info("Story config status updated successfully", logFields...)
	return nil
}

// AppendUserInput добавляет новый пользовательский ввод к существующему списку.
// ВНИМАНИЕ: Этот метод больше НЕ управляет транзакцией. Вызывающий код ДОЛЖЕН обернуть его в транзакцию.
// Запрос SELECT ... FOR UPDATE обеспечит блокировку строки в рамках внешней транзакции.
func (r *pgStoryConfigRepository) AppendUserInput(ctx context.Context, id uuid.UUID, userInput string) error {
	logFields := []zap.Field{
		zap.String("storyConfigID", id.String()),
	}
	r.logger.Debug("Appending user input to story config (transaction managed externally)", logFields...)

	// Транзакция управляется извне.

	// 1. Получаем текущий user_input С БЛОКИРОВКОЙ
	// Используем r.db напрямую.
	var currentInputBytes []byte
	err := r.db.QueryRow(ctx, selectUserInputForUpdateQuery, id).Scan(&currentInputBytes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Warn("Story config not found for appending user input", logFields...)
			return models.ErrNotFound
		}
		r.logger.Error("Failed to get user_input for update", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка получения user_input: %w", err)
	}

	// 2. Десериализуем, добавляем новый ввод, сериализуем обратно
	var currentInput []string
	if len(currentInputBytes) > 0 { // Проверяем, что JSON не пустой/NULL перед десериализацией
		if errUnmarshal := json.Unmarshal(currentInputBytes, &currentInput); errUnmarshal != nil {
			r.logger.Error("Failed to unmarshal existing user_input", append(logFields, zap.Error(errUnmarshal))...)
			return fmt.Errorf("ошибка десериализации user_input: %w", errUnmarshal)
		}
	}
	// Если currentInput все еще nil (был NULL или пустой JSON массив), инициализируем
	if currentInput == nil {
		currentInput = make([]string, 0)
	}

	currentInput = append(currentInput, userInput)
	newInputBytes, errMarshal := json.Marshal(currentInput)
	if errMarshal != nil {
		r.logger.Error("Failed to marshal new user_input", append(logFields, zap.Error(errMarshal))...)
		return fmt.Errorf("ошибка сериализации user_input: %w", errMarshal)
	}

	// 3. Обновляем запись
	// Используем r.db напрямую.
	commandTag, err := r.db.Exec(ctx, updateUserInputQuery, newInputBytes, id)
	if err != nil {
		r.logger.Error("Failed to update user_input", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления user_input: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		// Это не должно произойти, т.к. строка была заблокирована
		r.logger.Error("Story config disappeared during AppendUserInput operation", logFields...)
		return models.ErrNotFound // Или другая ошибка несогласованности
	}

	// Коммит или откат выполняется вызывающим кодом.
	r.logger.Info("User input append operation completed within external transaction context", logFields...)
	return nil
}
