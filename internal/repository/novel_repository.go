package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq" // Импортируем драйвер PostgreSQL для sqlx
	"github.com/rs/zerolog/log"

	"novel-server/internal/model"
)

// NovelRepository предоставляет доступ к данным новелл
type NovelRepository struct {
	pool *pgxpool.Pool
	db   *sqlx.DB
}

// NewNovelRepository создает новый экземпляр репозитория для работы с новеллами
func NewNovelRepository(pool *pgxpool.Pool, db *sqlx.DB) *NovelRepository {
	return &NovelRepository{
		pool: pool,
		db:   db,
	}
}

// Create создает новую новеллу в базе данных
func (r *NovelRepository) Create(ctx context.Context, novel model.Novel) (model.Novel, error) {
	query := `
		INSERT INTO novels (id, title, description, author_id, is_public, cover_image, tags, config, setup, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
		RETURNING id, title, description, author_id, is_public, cover_image, tags, config, setup, created_at, updated_at
	`

	// Преобразование в JSON
	tagsJSON, err := json.Marshal(novel.Tags)
	if err != nil {
		return model.Novel{}, fmt.Errorf("ошибка маршалинга tags: %w", err)
	}
	configJSON, err := json.Marshal(novel.Config)
	if err != nil {
		return model.Novel{}, fmt.Errorf("ошибка маршалинга config: %w", err)
	}
	setupJSON, err := json.Marshal(novel.Setup)
	if err != nil {
		return model.Novel{}, fmt.Errorf("ошибка маршалинга setup: %w", err)
	}

	var createdNovel model.Novel
	now := time.Now()

	// Если ID не указан, генерируем новый
	if novel.ID == uuid.Nil {
		novel.ID = uuid.New()
	}

	row := r.pool.QueryRow(ctx, query,
		novel.ID,
		novel.Title,
		novel.Description,
		novel.AuthorID,
		novel.IsPublic,
		novel.CoverImage,
		tagsJSON,
		configJSON,
		setupJSON,
		now,
	)

	var tagsJSONStr, configJSONStr, setupJSONStr string
	err = row.Scan(
		&createdNovel.ID,
		&createdNovel.Title,
		&createdNovel.Description,
		&createdNovel.AuthorID,
		&createdNovel.IsPublic,
		&createdNovel.CoverImage,
		&tagsJSONStr,
		&configJSONStr,
		&setupJSONStr,
		&createdNovel.CreatedAt,
		&createdNovel.UpdatedAt,
	)
	if err != nil {
		return model.Novel{}, fmt.Errorf("ошибка сканирования созданной новеллы: %w", err)
	}

	// Разбор JSON
	err = json.Unmarshal([]byte(tagsJSONStr), &createdNovel.Tags)
	if err != nil {
		return model.Novel{}, fmt.Errorf("ошибка разбора tags: %w", err)
	}
	err = json.Unmarshal([]byte(configJSONStr), &createdNovel.Config)
	if err != nil {
		return model.Novel{}, fmt.Errorf("ошибка разбора config: %w", err)
	}
	err = json.Unmarshal([]byte(setupJSONStr), &createdNovel.Setup)
	if err != nil {
		return model.Novel{}, fmt.Errorf("ошибка разбора setup: %w", err)
	}

	return createdNovel, nil
}

// GetByID получает новеллу по ID (без config и setup)
func (r *NovelRepository) GetByID(ctx context.Context, id uuid.UUID) (model.Novel, error) {
	query := `
		SELECT id, title, description, author_id, is_public, cover_image, tags, created_at, updated_at, published_at
		FROM novels
		WHERE id = $1
	`

	row := r.pool.QueryRow(ctx, query, id)

	var novel model.Novel
	var tagsJSONStr sql.NullString // Используем sql.NullString для tags
	var publishedAt sql.NullTime   // Используем sql.NullTime для published_at

	err := row.Scan(
		&novel.ID,
		&novel.Title,
		&novel.Description,
		&novel.AuthorID,
		&novel.IsPublic,
		&novel.CoverImage,
		&tagsJSONStr,
		&novel.CreatedAt,
		&novel.UpdatedAt,
		&publishedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.Novel{}, model.ErrNotFound
		}
		return model.Novel{}, fmt.Errorf("ошибка сканирования новеллы по ID: %w", err)
	}

	// Разбор тегов из JSON, если они есть
	if tagsJSONStr.Valid {
		err = json.Unmarshal([]byte(tagsJSONStr.String), &novel.Tags)
		if err != nil {
			return model.Novel{}, fmt.Errorf("ошибка разбора tags JSON: %w", err)
		}
	}
	// Присваиваем published_at, если оно есть
	if publishedAt.Valid {
		novel.PublishedAt = &publishedAt.Time
	}

	return novel, nil
}

// GetByAuthorID получает все новеллы автора
func (r *NovelRepository) GetByAuthorID(ctx context.Context, authorID uuid.UUID) ([]model.Novel, error) {
	query := `
		SELECT id, title, description, author_id, is_public, cover_image, tags, created_at, updated_at, published_at
		FROM novels
		WHERE author_id = $1
		ORDER BY created_at DESC
	`

	rows, err := r.pool.Query(ctx, query, authorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	novels := []model.Novel{}
	for rows.Next() {
		var novel model.Novel
		var tagsJSONStr string
		err := rows.Scan(
			&novel.ID,
			&novel.Title,
			&novel.Description,
			&novel.AuthorID,
			&novel.IsPublic,
			&novel.CoverImage,
			&tagsJSONStr,
			&novel.CreatedAt,
			&novel.UpdatedAt,
			&novel.PublishedAt,
		)
		if err != nil {
			return nil, err
		}

		// Разбор тегов из JSON
		err = json.Unmarshal([]byte(tagsJSONStr), &novel.Tags)
		if err != nil {
			return nil, err
		}

		novels = append(novels, novel)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return novels, nil
}

// Update обновляет новеллу в базе данных
func (r *NovelRepository) Update(ctx context.Context, novel model.Novel) (model.Novel, error) {
	query := `
		UPDATE novels
		SET title = $2, description = $3, is_public = $4, cover_image = $5, tags = $6, updated_at = $7, published_at = $8
		WHERE id = $1
		RETURNING id, title, description, author_id, is_public, cover_image, tags, created_at, updated_at, published_at
	`

	// Преобразование массива тегов в JSON
	tagsJSON, err := json.Marshal(novel.Tags)
	if err != nil {
		return model.Novel{}, err
	}

	now := time.Now()
	novel.UpdatedAt = now

	row := r.pool.QueryRow(ctx, query,
		novel.ID,
		novel.Title,
		novel.Description,
		novel.IsPublic,
		novel.CoverImage,
		tagsJSON,
		now,
		novel.PublishedAt,
	)

	var updatedNovel model.Novel
	var tagsJSONStr string
	err = row.Scan(
		&updatedNovel.ID,
		&updatedNovel.Title,
		&updatedNovel.Description,
		&updatedNovel.AuthorID,
		&updatedNovel.IsPublic,
		&updatedNovel.CoverImage,
		&tagsJSONStr,
		&updatedNovel.CreatedAt,
		&updatedNovel.UpdatedAt,
		&updatedNovel.PublishedAt,
	)
	if err != nil {
		return model.Novel{}, err
	}

	// Разбор тегов из JSON
	err = json.Unmarshal([]byte(tagsJSONStr), &updatedNovel.Tags)
	if err != nil {
		return model.Novel{}, err
	}

	return updatedNovel, nil
}

// Delete удаляет новеллу из базы данных
func (r *NovelRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM novels WHERE id = $1`
	_, err := r.pool.Exec(ctx, query, id)
	return err
}

// ListPublic получает список публичных новелл с пагинацией
func (r *NovelRepository) ListPublic(ctx context.Context, limit, offset int) ([]model.Novel, error) {
	query := `
		SELECT id, title, description, author_id, is_public, cover_image, tags, created_at, updated_at, published_at
		FROM novels
		WHERE is_public = true
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`

	rows, err := r.pool.Query(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	novels := []model.Novel{}
	for rows.Next() {
		var novel model.Novel
		var tagsJSONStr string
		err := rows.Scan(
			&novel.ID,
			&novel.Title,
			&novel.Description,
			&novel.AuthorID,
			&novel.IsPublic,
			&novel.CoverImage,
			&tagsJSONStr,
			&novel.CreatedAt,
			&novel.UpdatedAt,
			&novel.PublishedAt,
		)
		if err != nil {
			return nil, err
		}

		// Разбор тегов из JSON
		err = json.Unmarshal([]byte(tagsJSONStr), &novel.Tags)
		if err != nil {
			return nil, err
		}

		novels = append(novels, novel)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return novels, nil
}

// GetNovelState получает состояние новеллы для конкретного пользователя и новеллы
func (r *NovelRepository) GetNovelState(ctx context.Context, userID, novelID uuid.UUID) (model.NovelState, error) {
	query := `
		SELECT id, user_id, novel_id, current_batch_number, story_summary_so_far, future_direction, story_variables, history, created_at, updated_at
		FROM novel_states
		WHERE user_id = $1 AND novel_id = $2
	`

	row := r.pool.QueryRow(ctx, query, userID, novelID)

	var state model.NovelState
	var variablesJSON, historyJSON []byte

	err := row.Scan(
		&state.ID,
		&state.UserID,
		&state.NovelID,
		&state.CurrentBatchNumber,
		&state.StorySummarySoFar,
		&state.FutureDirection,
		&variablesJSON,
		&historyJSON,
		&state.CreatedAt,
		&state.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.NovelState{}, model.ErrNotFound
		}
		return model.NovelState{}, fmt.Errorf("ошибка при получении состояния новеллы: %w", err)
	}

	// Разбираем JSON
	if err := json.Unmarshal(variablesJSON, &state.StoryVariables); err != nil {
		return model.NovelState{}, fmt.Errorf("ошибка разбора JSON story_variables: %w", err)
	}
	if err := json.Unmarshal(historyJSON, &state.History); err != nil {
		return model.NovelState{}, fmt.Errorf("ошибка разбора JSON history: %w", err)
	}

	return state, nil
}

// SaveNovelState сохраняет или обновляет состояние новеллы
func (r *NovelRepository) SaveNovelState(ctx context.Context, state model.NovelState) (model.NovelState, error) {
	// Преобразуем поля в JSON для хранения в базе
	variablesJSON, err := json.Marshal(state.StoryVariables)
	if err != nil {
		return model.NovelState{}, err
	}

	historyJSON, err := json.Marshal(state.History)
	if err != nil {
		return model.NovelState{}, err
	}

	now := time.Now()
	state.UpdatedAt = now // Устанавливаем время обновления

	// Запрос для вставки или обновления (UPSERT)
	query := `
		INSERT INTO novel_states (id, user_id, novel_id, current_batch_number, story_summary_so_far, future_direction, story_variables, history, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (user_id, novel_id) DO UPDATE SET
			current_batch_number = EXCLUDED.current_batch_number,
			story_summary_so_far = EXCLUDED.story_summary_so_far,
			future_direction = EXCLUDED.future_direction,
			story_variables = EXCLUDED.story_variables,
			history = EXCLUDED.history,
			updated_at = EXCLUDED.updated_at
		RETURNING id, created_at, updated_at
	`

	// Если ID не установлен (новая запись), генерируем его
	if state.ID == uuid.Nil {
		state.ID = uuid.New()
		state.CreatedAt = now // Устанавливаем время создания только для новых записей
	}

	// Выполняем запрос
	err = r.pool.QueryRow(ctx, query,
		state.ID,
		state.UserID,
		state.NovelID,
		state.CurrentBatchNumber,
		state.StorySummarySoFar,
		state.FutureDirection,
		variablesJSON,
		historyJSON,
		state.CreatedAt,
		state.UpdatedAt,
	).Scan(&state.ID, &state.CreatedAt, &state.UpdatedAt)

	if err != nil {
		return model.NovelState{}, fmt.Errorf("ошибка при сохранении состояния новеллы: %w", err)
	}

	return state, nil
}

// DeleteNovelState удаляет состояние новеллы для пользователя
func (r *NovelRepository) DeleteNovelState(ctx context.Context, userID, novelID uuid.UUID) error {
	query := `
		DELETE FROM novel_states
		WHERE user_id = $1 AND novel_id = $2
	`

	_, err := r.pool.Exec(ctx, query, userID, novelID)
	if err != nil {
		return fmt.Errorf("ошибка при удалении состояния новеллы: %w", err)
	}

	return nil
}

// SaveSceneBatch сохраняет или обновляет кешированный батч сцены
// Использует sqlx для удобства работы со структурами и UPSERT.
func (r *NovelRepository) SaveSceneBatch(ctx context.Context, batch model.SceneBatch) (model.SceneBatch, error) {
	// Гарантируем, что ID установлен
	if batch.ID == uuid.Nil {
		batch.ID = uuid.New()
	}
	batch.CreatedAt = time.Now().UTC() // Используем UTC для консистентности

	query := `
	INSERT INTO scene_batches (id, novel_id, state_hash, story_summary_so_far, future_direction, choices, ending_text, created_at)
	VALUES (:id, :novel_id, :state_hash, :story_summary_so_far, :future_direction, :choices, :ending_text, :created_at)
	ON CONFLICT (novel_id, state_hash)
	DO UPDATE SET
		story_summary_so_far = EXCLUDED.story_summary_so_far,
		future_direction = EXCLUDED.future_direction,
		choices = EXCLUDED.choices,
		ending_text = EXCLUDED.ending_text
	RETURNING id, novel_id, state_hash, story_summary_so_far, future_direction, choices, ending_text, created_at
	`

	// Используем NamedExecContext для удобства передачи структуры
	rows, err := r.db.NamedQueryContext(ctx, query, batch)
	if err != nil {
		return model.SceneBatch{}, fmt.Errorf("ошибка сохранения scene batch: %w", err)
	}
	defer rows.Close()

	// Сканируем возвращенную строку обратно в структуру
	var savedBatch model.SceneBatch
	if rows.Next() {
		err = rows.StructScan(&savedBatch)
		if err != nil {
			return model.SceneBatch{}, fmt.Errorf("ошибка сканирования сохраненного scene batch: %w", err)
		}
	} else {
		// Если RETURNING ничего не вернул (что странно для UPSERT), возвращаем исходный батч
		// Или можно вернуть ошибку, если это считается невозможным состоянием
		log.Warn().Msg("UPSERT для scene_batches не вернул строку")
		return batch, nil // Возвращаем исходный батч с присвоенным ID и CreatedAt
	}

	if err := rows.Err(); err != nil { // Проверяем ошибки после итерации
		return model.SceneBatch{}, fmt.Errorf("ошибка итерации после сохранения scene batch: %w", err)
	}

	return savedBatch, nil
}

// GetSceneBatchByHash ищет кешированный батч сцены по хешу состояния
func (r *NovelRepository) GetSceneBatchByHash(ctx context.Context, novelID uuid.UUID, stateHash string) (model.SceneBatch, error) {
	query := `
	SELECT id, novel_id, state_hash, story_summary_so_far, future_direction, choices, ending_text, created_at
	FROM scene_batches
	WHERE novel_id = $1 AND state_hash = $2
	`

	var batch model.SceneBatch
	err := r.db.GetContext(ctx, &batch, query, novelID, stateHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Используем стандартную ошибку "не найдено"
			return model.SceneBatch{}, model.ErrNotFound
		}
		return model.SceneBatch{}, fmt.Errorf("ошибка получения scene batch по хешу: %w", err)
	}

	return batch, nil
}

// SaveNovelDraft сохраняет черновик новеллы в базу данных
func (r *NovelRepository) SaveNovelDraft(ctx context.Context, draft model.NovelDraft) (model.NovelDraft, error) {
	draft.ID = uuid.New() // Генерируем новый ID
	draft.CreatedAt = time.Now()
	draft.UpdatedAt = draft.CreatedAt

	// Сериализуем Config в JSON для хранения в БД (предполагается тип JSONB)
	configJSON, err := json.Marshal(draft.Config)
	if err != nil {
		return model.NovelDraft{}, fmt.Errorf("ошибка сериализации Config в JSON: %w", err)
	}

	query := `
		INSERT INTO novel_drafts (id, user_id, config, user_prompt, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET
			config = EXCLUDED.config,
			user_prompt = EXCLUDED.user_prompt,
			updated_at = EXCLUDED.updated_at
		RETURNING id, created_at, updated_at
	`

	err = r.pool.QueryRow(ctx, query,
		draft.ID,
		draft.UserID,
		configJSON,
		draft.UserPrompt,
		draft.CreatedAt,
		draft.UpdatedAt,
	).Scan(&draft.ID, &draft.CreatedAt, &draft.UpdatedAt)

	if err != nil {
		return model.NovelDraft{}, fmt.Errorf("ошибка при сохранении черновика новеллы: %w", err)
	}

	return draft, nil
}

// DeleteDraft удаляет черновик новеллы по ID
func (r *NovelRepository) DeleteDraft(ctx context.Context, draftID uuid.UUID) error {
	query := `DELETE FROM novel_drafts WHERE id = $1`
	_, err := r.pool.Exec(ctx, query, draftID)
	return err
}

// GetDraftByID получает черновик новеллы по ID
func (r *NovelRepository) GetDraftByID(ctx context.Context, draftID uuid.UUID) (model.NovelDraft, error) {
	query := `
		SELECT id, user_id, config, user_prompt, created_at, updated_at
		FROM novel_drafts
		WHERE id = $1
	`

	row := r.pool.QueryRow(ctx, query, draftID)

	var draft model.NovelDraft
	var configJSON []byte // Сканируем JSONB как []byte

	err := row.Scan(
		&draft.ID,
		&draft.UserID,
		&configJSON, // Сканируем как []byte
		&draft.UserPrompt,
		&draft.CreatedAt,
		&draft.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return model.NovelDraft{}, fmt.Errorf("черновик с ID %s не найден", draftID)
		}
		return model.NovelDraft{}, fmt.Errorf("ошибка при получении черновика: %w", err)
	}

	// Разбираем JSON конфига
	if err := json.Unmarshal(configJSON, &draft.Config); err != nil {
		return model.NovelDraft{}, fmt.Errorf("ошибка разбора JSON конфига черновика: %w", err)
	}

	return draft, nil
}

// UpdateNovelDraft обновляет существующий черновик новеллы
func (r *NovelRepository) UpdateNovelDraft(ctx context.Context, draft model.NovelDraft) (model.NovelDraft, error) {
	now := time.Now()
	draft.UpdatedAt = now

	// Сериализуем Config в JSON
	configJSON, err := json.Marshal(draft.Config)
	if err != nil {
		return model.NovelDraft{}, fmt.Errorf("ошибка сериализации Config в JSON при обновлении: %w", err)
	}

	query := `
		UPDATE novel_drafts
		SET config = $1, user_prompt = $2, updated_at = $3
		WHERE id = $4
		RETURNING id, user_id, config, user_prompt, created_at, updated_at
	`

	row := r.pool.QueryRow(ctx, query,
		configJSON,
		draft.UserPrompt,
		draft.UpdatedAt,
		draft.ID,
	)

	var updatedDraft model.NovelDraft
	var updatedConfigJSON []byte

	err = row.Scan(
		&updatedDraft.ID,
		&updatedDraft.UserID,
		&updatedConfigJSON,
		&updatedDraft.UserPrompt,
		&updatedDraft.CreatedAt,
		&updatedDraft.UpdatedAt,
	)
	if err != nil {
		return model.NovelDraft{}, fmt.Errorf("ошибка при обновлении черновика: %w", err)
	}

	// Разбираем JSON обновленного конфига
	if err := json.Unmarshal(updatedConfigJSON, &updatedDraft.Config); err != nil {
		return model.NovelDraft{}, fmt.Errorf("ошибка разбора JSON обновленного конфига черновика: %w", err)
	}

	return updatedDraft, nil
}

// GetDraftsByUserID получает все черновики пользователя по его ID
func (r *NovelRepository) GetDraftsByUserID(ctx context.Context, userID uuid.UUID) ([]model.NovelDraft, error) {
	query := `
		SELECT id, user_id, config, user_prompt, created_at, updated_at
		FROM novel_drafts
		WHERE user_id = $1
		ORDER BY updated_at DESC
	`

	rows, err := r.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("ошибка при запросе черновиков пользователя: %w", err)
	}
	defer rows.Close()

	drafts := []model.NovelDraft{}
	for rows.Next() {
		var draft model.NovelDraft
		var configJSON []byte

		err := rows.Scan(
			&draft.ID,
			&draft.UserID,
			&configJSON,
			&draft.UserPrompt,
			&draft.CreatedAt,
			&draft.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("ошибка при сканировании строки черновика: %w", err)
		}

		// Разбираем JSON конфига
		if err := json.Unmarshal(configJSON, &draft.Config); err != nil {
			return nil, fmt.Errorf("ошибка разбора JSON конфига черновика: %w", err)
		}

		drafts = append(drafts, draft)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка при итерации по результатам запроса: %w", err)
	}

	return drafts, nil
}

// GetNovelWithSetup получает новеллу по ID вместе с её setup
func (r *NovelRepository) GetNovelWithSetup(ctx context.Context, id uuid.UUID) (model.Novel, error) {
	query := `
		SELECT id, title, description, author_id, is_public, cover_image, tags, setup, created_at, updated_at, published_at
		FROM novels
		WHERE id = $1
	`

	row := r.pool.QueryRow(ctx, query, id)

	var novel model.Novel
	var tagsJSONStr, setupJSONStr sql.NullString // Используем sql.NullString
	var publishedAt sql.NullTime

	err := row.Scan(
		&novel.ID,
		&novel.Title,
		&novel.Description,
		&novel.AuthorID,
		&novel.IsPublic,
		&novel.CoverImage,
		&tagsJSONStr,
		&setupJSONStr, // Сканируем setup
		&novel.CreatedAt,
		&novel.UpdatedAt,
		&publishedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.Novel{}, model.ErrNotFound
		}
		return model.Novel{}, fmt.Errorf("ошибка сканирования новеллы с setup по ID: %w", err)
	}

	// Разбор JSON полей, если они не NULL
	if tagsJSONStr.Valid {
		if err := json.Unmarshal([]byte(tagsJSONStr.String), &novel.Tags); err != nil {
			return model.Novel{}, fmt.Errorf("ошибка разбора tags JSON: %w", err)
		}
	}
	if setupJSONStr.Valid {
		if err := json.Unmarshal([]byte(setupJSONStr.String), &novel.Setup); err != nil {
			return model.Novel{}, fmt.Errorf("ошибка разбора setup JSON: %w", err)
		}
	} else {
		// Если setup NULL в базе, инициализируем пустым значением
		novel.Setup = model.NovelSetup{}
	}

	if publishedAt.Valid {
		novel.PublishedAt = &publishedAt.Time
	}

	return novel, nil
}
