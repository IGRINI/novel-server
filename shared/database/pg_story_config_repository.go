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
            (id, user_id, title, description, user_input, config, status, language, created_at, updated_at)
        VALUES
            ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
    `
	logFields := []zap.Field{zap.String("storyConfigID", config.ID.String()), zap.String("userID", config.UserID.String())}
	r.logger.Debug("Creating story config", logFields...)

	_, err := r.db.Exec(ctx, query,
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
func (r *pgStoryConfigRepository) GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*sharedModels.StoryConfig, error) { // <<< Возвращаем sharedModels.StoryConfig
	query := `
        SELECT id, user_id, title, description, user_input, config, status, created_at, updated_at
        FROM story_configs
        WHERE id = $1 AND user_id = $2
    `
	config := &sharedModels.StoryConfig{} // <<< Используем sharedModels.StoryConfig
	logFields := []zap.Field{zap.String("storyConfigID", id.String()), zap.String("userID", userID.String())}
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
	logFields := []zap.Field{zap.String("storyConfigID", config.ID.String()), zap.String("userID", config.UserID.String())}
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
func (r *pgStoryConfigRepository) CountActiveGenerations(ctx context.Context, userID uuid.UUID) (int, error) {
	query := `SELECT COUNT(*) FROM story_configs WHERE user_id = $1 AND status = $2` // Используем $2 для статуса
	var count int
	logFields := []zap.Field{zap.String("userID", userID.String())}
	r.logger.Debug("Counting active generations for user", logFields...)

	err := r.db.QueryRow(ctx, query, userID, "generating").Scan(&count) // <<< Используем "generating"
	if err != nil {
		r.logger.Error("Failed to count active generations", append(logFields, zap.Error(err))...)
		return 0, fmt.Errorf("ошибка подсчета активных генераций для user %s: %w", userID.String(), err)
	}
	r.logger.Debug("Active generations count retrieved", append(logFields, zap.Int("count", count))...)
	return count, nil
}

// Delete
func (r *pgStoryConfigRepository) Delete(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	query := `DELETE FROM story_configs WHERE id = $1 AND user_id = $2`
	logFields := []zap.Field{
		zap.String("storyConfigID", id.String()),
		zap.String("userID", userID.String()),
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

// DeleteTx удаляет StoryConfig по ID в рамках транзакции, проверяя принадлежность пользователю.
func (r *pgStoryConfigRepository) DeleteTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, userID uuid.UUID) error {
	query := `DELETE FROM story_configs WHERE id = $1 AND user_id = $2`
	logFields := []zap.Field{
		zap.String("storyConfigID", id.String()),
		zap.String("userID", userID.String()),
		zap.String("tx", "active"), // Indicate transaction context
	}
	r.logger.Debug("Deleting story config within transaction", logFields...)

	commandTag, err := tx.Exec(ctx, query, id, userID)
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
	query := `
        SELECT id, user_id, title, description, user_input, config, status, created_at, updated_at
        FROM story_configs
        WHERE status = $1 OR status = $2
        ORDER BY updated_at ASC
    ` // Find generating or revising
	logFields := []zap.Field{zap.String("status1", "generating"), zap.String("status2", "revising")}
	r.logger.Debug("Finding story configs with generating or revising status", logFields...)

	rows, err := r.db.Query(ctx, query, "generating", "revising") // Используем строки
	if err != nil {
		r.logger.Error("Failed to query generating/revising story configs", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка БД при поиске generating/revising story configs: %w", err)
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

// <<< ДОБАВЛЕНО: Реализация FindGeneratingOlderThan >>>
const findGeneratingOlderThanQuery = `
SELECT id, user_id, title, description, user_input, config, status, created_at, updated_at, language
FROM story_configs
WHERE status = 'generating' AND created_at < $1
ORDER BY created_at ASC`

// <<< ДОБАВЛЕНО: Реализация UpdateConfigAndInput >>>
const updateConfigAndInputQuery = `
UPDATE story_configs
SET config = $2, user_input = $3, updated_at = NOW()
WHERE id = $1`

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
	for rows.Next() { // <<< Ручное сканирование >>>
		var config sharedModels.StoryConfig
		if err := rows.Scan(
			&config.ID,
			&config.UserID,
			&config.Title,
			&config.Description,
			&config.UserInput,
			&config.Config,
			&config.Status,
			&config.CreatedAt,
			&config.UpdatedAt,
			&config.Language,
		); err != nil {
			r.logger.Error("Failed to scan story config row in FindGeneratingOlderThan", append(logFields, zap.Error(err))...)
			// Не прерываем весь процесс, просто пропускаем эту строку?
			// Или возвращаем ошибку? Пока вернем ошибку.
			return nil, fmt.Errorf("ошибка сканирования строки черновика: %w", err)
		}
		configs = append(configs, config)
	} // <<< Конец ручного сканирования >>>

	if err := rows.Err(); err != nil { // Проверка ошибок после цикла
		r.logger.Error("Error during row iteration in FindGeneratingOlderThan", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка итерации по строкам черновиков: %w", err)
	}

	r.logger.Debug("Found old generating story configs", append(logFields, zap.Int("count", len(configs)))...)
	return configs, nil
}

// <<< ДОБАВЛЕНО: Реализация UpdateConfigAndInput >>>
func (r *pgStoryConfigRepository) UpdateConfigAndInput(ctx context.Context, id uuid.UUID, config, userInput []byte) error {
	query := `
        UPDATE story_configs SET
            config = $1, user_input = $2, updated_at = $3
        WHERE id = $4
    `
	logFields := []zap.Field{zap.String("storyConfigID", id.String())}
	r.logger.Debug("Updating story config config/userInput", logFields...)

	commandTag, err := r.db.Exec(ctx, query, config, userInput, time.Now().UTC(), id)
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

// <<< НАЧАЛО НОВОГО МЕТОДА >>>
// UpdateConfigAndInputAndStatus обновляет поля config, user_input и status.
func (r *pgStoryConfigRepository) UpdateConfigAndInputAndStatus(ctx context.Context, id uuid.UUID, configJSON, userInputJSON json.RawMessage, status sharedModels.StoryStatus) error {
	query := `
        UPDATE story_configs SET
            config = $1, user_input = $2, status = $3, updated_at = $4
        WHERE id = $5
    `
	logFields := []zap.Field{
		zap.String("storyConfigID", id.String()),
		zap.String("newStatus", string(status)),
	}
	r.logger.Debug("Updating story config config/userInput/status", logFields...)

	commandTag, err := r.db.Exec(ctx, query, configJSON, userInputJSON, status, time.Now().UTC(), id)
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

// <<< КОНЕЦ НОВОГО МЕТОДА >>>

// UpdateStatusAndConfig обновляет статус, конфиг, заголовок и описание черновика.
func (r *pgStoryConfigRepository) UpdateStatusAndConfig(ctx context.Context, id uuid.UUID, status models.StoryStatus, config json.RawMessage, title, description string) error {
	query := `
        UPDATE story_configs SET
            status = $1, config = $2, title = $3, description = $4, updated_at = $5
        WHERE id = $6
    `
	logFields := []zap.Field{
		zap.String("storyConfigID", id.String()),
		zap.String("newStatus", string(status)),
		zap.Int("configSize", len(config)),
		zap.String("title", title),
		zap.String("description", description),
	}
	r.logger.Debug("Updating story config status and config data", logFields...)

	commandTag, err := r.db.Exec(ctx, query, status, config, title, description, time.Now().UTC(), id)
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

// <<< ДОБАВЛЕНО: Реализация UpdateStatusAndError >>>
const updateStatusAndErrorQuery = `
UPDATE story_configs
SET status = $2, error_details = $3, updated_at = NOW()
WHERE id = $1`

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
