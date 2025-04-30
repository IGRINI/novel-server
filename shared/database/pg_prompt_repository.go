package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"novel-server/shared/models"
)

var (
	ErrPromptNotFound         = errors.New("prompt not found")
	ErrPromptKeyAlreadyExists = errors.New("prompt key already exists")
	ErrPromptAlreadyExists    = errors.New("prompt with this key and language already exists")
	ErrNotFound               = errors.New("record not found")
)

const promptFields = `id, key, language, content, created_at, updated_at`

type PgPromptRepository struct {
	db *pgxpool.Pool
}

func NewPgPromptRepository(db *pgxpool.Pool) *PgPromptRepository {
	if db == nil {
		log.Fatal().Msg("Database pool is nil for PgPromptRepository")
	}
	return &PgPromptRepository{db: db}
}

func (r *PgPromptRepository) Create(ctx context.Context, prompt *models.Prompt) error {
	query := `INSERT INTO prompts (key, language, content) VALUES ($1, $2, $3) RETURNING id, created_at, updated_at`
	err := r.db.QueryRow(ctx, query, prompt.Key, prompt.Language, prompt.Content).Scan(
		&prompt.ID, &prompt.CreatedAt, &prompt.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return ErrPromptAlreadyExists
		}
		log.Error().Err(err).Str("key", prompt.Key).Str("language", prompt.Language).Msg("Failed to create prompt")
		return fmt.Errorf("failed to create prompt: %w", err)
	}
	log.Info().Str("key", prompt.Key).Str("language", prompt.Language).Int64("id", prompt.ID).Msg("Prompt created")
	return nil
}

func (r *PgPromptRepository) GetByKeyAndLanguage(ctx context.Context, key, language string) (*models.Prompt, error) {
	query := fmt.Sprintf(`SELECT %s FROM prompts WHERE key = $1 AND language = $2`, promptFields)
	var prompt models.Prompt
	err := r.db.QueryRow(ctx, query, key, language).Scan(
		&prompt.ID, &prompt.Key, &prompt.Language, &prompt.Content, &prompt.CreatedAt, &prompt.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPromptNotFound
		}
		log.Error().Err(err).Str("key", key).Str("language", language).Msg("Failed to get prompt by key and language")
		return nil, fmt.Errorf("failed to get prompt by key and language: %w", err)
	}
	return &prompt, nil
}

func (r *PgPromptRepository) Update(ctx context.Context, prompt *models.Prompt) error {
	query := `UPDATE prompts SET content = $1, updated_at = NOW() WHERE key = $2 AND language = $3 RETURNING updated_at`
	var updatedAt time.Time
	err := r.db.QueryRow(ctx, query, prompt.Content, prompt.Key, prompt.Language).Scan(&updatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPromptNotFound // If no rows were updated, it means the prompt wasn't found
		}
		log.Error().Err(err).Str("key", prompt.Key).Str("language", prompt.Language).Msg("Failed to update prompt")
		return fmt.Errorf("failed to update prompt: %w", err)
	}
	prompt.UpdatedAt = updatedAt
	log.Info().Str("key", prompt.Key).Str("language", prompt.Language).Int64("id", prompt.ID).Msg("Prompt updated")
	return nil
}

func (r *PgPromptRepository) DeleteByKeyAndLanguage(ctx context.Context, key, language string) error {
	query := `DELETE FROM prompts WHERE key = $1 AND language = $2`
	commandTag, err := r.db.Exec(ctx, query, key, language)
	if err != nil {
		log.Error().Err(err).Str("key", key).Str("language", language).Msg("Failed to delete prompt")
		return fmt.Errorf("failed to delete prompt: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrPromptNotFound // Nothing deleted means it wasn't found
	}
	log.Info().Str("key", key).Str("language", language).Msg("Prompt deleted")
	return nil
}

func (r *PgPromptRepository) ListAll(ctx context.Context, language *string, key *string) ([]*models.Prompt, error) {
	var args []interface{}
	var conditions []string
	paramCount := 1

	queryBuilder := strings.Builder{}
	queryBuilder.WriteString(fmt.Sprintf(`SELECT %s FROM prompts`, promptFields))

	if language != nil && *language != "" {
		conditions = append(conditions, fmt.Sprintf("language = $%d", paramCount))
		args = append(args, *language)
		paramCount++
	}
	if key != nil && *key != "" {
		conditions = append(conditions, fmt.Sprintf("key = $%d", paramCount))
		args = append(args, *key)
		paramCount++
	}

	if len(conditions) > 0 {
		queryBuilder.WriteString(" WHERE ")
		queryBuilder.WriteString(strings.Join(conditions, " AND "))
	}
	queryBuilder.WriteString(" ORDER BY key, language") // Consistent ordering

	rows, err := r.db.Query(ctx, queryBuilder.String(), args...)
	if err != nil {
		log.Error().Err(err).Interface("args", args).Msg("Failed to list prompts")
		return nil, fmt.Errorf("failed to list prompts: %w", err)
	}
	defer rows.Close()

	prompts := make([]*models.Prompt, 0)
	for rows.Next() {
		var prompt models.Prompt
		err := rows.Scan(
			&prompt.ID, &prompt.Key, &prompt.Language, &prompt.Content, &prompt.CreatedAt, &prompt.UpdatedAt,
		)
		if err != nil {
			log.Error().Err(err).Msg("Failed to scan prompt row")
			return nil, fmt.Errorf("failed to scan prompt row: %w", err)
		}
		prompts = append(prompts, &prompt)
	}

	if err = rows.Err(); err != nil {
		log.Error().Err(err).Msg("Error during rows iteration for list prompts")
		return nil, fmt.Errorf("error during rows iteration: %w", err)
	}

	return prompts, nil
}

func (r *PgPromptRepository) GetAll(ctx context.Context) ([]*models.Prompt, error) {
	return r.ListAll(ctx, nil, nil) // Use ListAll without filters
}

// ListKeys retrieves a list of unique prompt keys.
func (r *PgPromptRepository) ListKeys(ctx context.Context) ([]string, error) {
	query := `SELECT DISTINCT key FROM prompts ORDER BY key`
	rows, err := r.db.Query(ctx, query)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list prompt keys")
		return nil, fmt.Errorf("failed to list prompt keys: %w", err)
	}
	defer rows.Close()

	keys := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			log.Error().Err(err).Msg("Failed to scan prompt key row")
			return nil, fmt.Errorf("failed to scan prompt key row: %w", err)
		}
		keys = append(keys, key)
	}

	if err = rows.Err(); err != nil {
		log.Error().Err(err).Msg("Error during rows iteration for list keys")
		return nil, fmt.Errorf("error during rows iteration for list keys: %w", err)
	}

	return keys, nil
}

// GetAllPromptsByKey retrieves all language versions of a prompt by its key.
// This is functionally the same as FindByKey in this implementation.
func (r *PgPromptRepository) GetAllPromptsByKey(ctx context.Context, key string) ([]*models.Prompt, error) {
	query := fmt.Sprintf(`SELECT %s FROM prompts WHERE key = $1 ORDER BY language`, promptFields)
	rows, err := r.db.Query(ctx, query, key)
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("Failed to get all prompts by key")
		return nil, fmt.Errorf("failed to get all prompts by key %s: %w", key, err)
	}
	defer rows.Close()

	prompts := make([]*models.Prompt, 0)
	for rows.Next() {
		var prompt models.Prompt
		err := rows.Scan(
			&prompt.ID, &prompt.Key, &prompt.Language, &prompt.Content, &prompt.CreatedAt, &prompt.UpdatedAt,
		)
		if err != nil {
			log.Error().Err(err).Str("key", key).Msg("Failed to scan prompt row in GetAllPromptsByKey")
			return nil, fmt.Errorf("failed to scan prompt row for key %s: %w", key, err)
		}
		prompts = append(prompts, &prompt)
	}

	if err = rows.Err(); err != nil {
		log.Error().Err(err).Str("key", key).Msg("Error during rows iteration for get all prompts by key")
		return nil, fmt.Errorf("error during rows iteration for get all prompts by key %s: %w", key, err)
	}
	return prompts, nil
}

// Upsert creates a new prompt or updates an existing one based on key and language.
func (r *PgPromptRepository) Upsert(ctx context.Context, prompt *models.Prompt) error {
	// Используем INSERT ... ON CONFLICT для атомарного создания/обновления
	query := `
        INSERT INTO prompts (key, language, content, created_at, updated_at)
        VALUES ($1, $2, $3, NOW(), NOW())
        ON CONFLICT (key, language) DO UPDATE SET
            content = EXCLUDED.content,
            updated_at = NOW()
        RETURNING id, created_at, updated_at
    `
	err := r.db.QueryRow(ctx, query, prompt.Key, prompt.Language, prompt.Content).Scan(
		&prompt.ID, &prompt.CreatedAt, &prompt.UpdatedAt, // Обновляем ID и временные метки в переданной структуре
	)
	if err != nil {
		log.Error().Err(err).Str("key", prompt.Key).Str("language", prompt.Language).Msg("Failed to upsert prompt")
		return fmt.Errorf("failed to upsert prompt: %w", err)
	}
	log.Info().Str("key", prompt.Key).Str("language", prompt.Language).Int64("id", prompt.ID).Msg("Prompt upserted")
	return nil
}

// CreateBatch inserts multiple prompts, typically used for initializing a new key with all languages.
func (r *PgPromptRepository) CreateBatch(ctx context.Context, prompts []*models.Prompt) error {
	if len(prompts) == 0 {
		return nil // Ничего не делаем, если список пуст
	}

	// Используем pgx.Batch для эффективности
	batch := &pgx.Batch{}
	insertQuery := `INSERT INTO prompts (key, language, content) VALUES ($1, $2, $3)`
	for _, p := range prompts {
		batch.Queue(insertQuery, p.Key, p.Language, p.Content)
	}

	br := r.db.SendBatch(ctx, batch)
	defer br.Close() // Важно закрыть batch results

	var firstError error
	// Проверяем результат каждой операции в батче
	for i := 0; i < len(prompts); i++ {
		_, err := br.Exec()
		if err != nil {
			var pgErr *pgconn.PgError
			// Если ошибка - дубликат ключа, это может быть ожидаемо при создании ключа,
			// который уже существует (например, при параллельном запросе). Возвращаем специальную ошибку.
			if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
				if firstError == nil { // Запоминаем только первую ошибку
					// Возможно, стоит вернуть ошибку, специфичную для CreateBatch?
					// Пока используем общую ErrPromptKeyAlreadyExists, но это относится ко всему ключу,
					// а не к конкретной языковой паре, что может быть не совсем точно.
					firstError = ErrPromptKeyAlreadyExists
				}
				log.Warn().Err(err).Str("key", prompts[i].Key).Str("language", prompts[i].Language).Msg("Prompt already exists during batch create")
			} else if firstError == nil { // Запоминаем только первую ошибку другого типа
				log.Error().Err(err).Str("key", prompts[i].Key).Str("language", prompts[i].Language).Msg("Failed to create prompt in batch")
				firstError = fmt.Errorf("failed to create prompt %s/%s in batch: %w", prompts[i].Key, prompts[i].Language, err)
			}
		}
	}

	// Возвращаем первую встретившуюся ошибку
	if firstError != nil {
		return firstError
	}

	log.Info().Int("count", len(prompts)).Str("key", prompts[0].Key).Msg("Prompt batch created/inserted")
	return nil
}

// DeleteByKey removes all language versions of a prompt by its key.
func (r *PgPromptRepository) DeleteByKey(ctx context.Context, key string) error {
	query := `DELETE FROM prompts WHERE key = $1`
	commandTag, err := r.db.Exec(ctx, query, key)
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("Failed to delete prompts by key")
		return fmt.Errorf("failed to delete prompts by key %s: %w", key, err)
	}
	// Если ничего не удалено, это не обязательно ошибка - ключа могло и не быть.
	// ErrPromptNotFound здесь не возвращаем.
	log.Info().Str("key", key).Int64("rowsAffected", commandTag.RowsAffected()).Msg("Prompts deleted by key")
	return nil
}

// GetByID retrieves a prompt by its unique ID.
func (r *PgPromptRepository) GetByID(ctx context.Context, id int64) (*models.Prompt, error) {
	query := fmt.Sprintf(`SELECT %s FROM prompts WHERE id = $1`, promptFields)
	var prompt models.Prompt
	err := r.db.QueryRow(ctx, query, id).Scan(
		&prompt.ID, &prompt.Key, &prompt.Language, &prompt.Content, &prompt.CreatedAt, &prompt.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Используем общую ошибку ErrNotFound, определенную в этом пакете
			return nil, ErrNotFound
		}
		log.Error().Err(err).Int64("id", id).Msg("Failed to get prompt by ID")
		return nil, fmt.Errorf("failed to get prompt by ID %d: %w", id, err)
	}
	return &prompt, nil
}

// DeleteByID removes a prompt by its unique ID.
func (r *PgPromptRepository) DeleteByID(ctx context.Context, id int64) error {
	query := `DELETE FROM prompts WHERE id = $1`
	commandTag, err := r.db.Exec(ctx, query, id)
	if err != nil {
		log.Error().Err(err).Int64("id", id).Msg("Failed to delete prompt by ID")
		return fmt.Errorf("failed to delete prompt by ID %d: %w", id, err)
	}
	if commandTag.RowsAffected() == 0 {
		// Если ничего не удалено, значит запись не найдена
		return ErrNotFound
	}
	log.Info().Int64("id", id).Msg("Prompt deleted by ID")
	return nil
}
