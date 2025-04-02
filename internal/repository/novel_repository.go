package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"novel-server/internal/model"
)

// NovelRepository представляет репозиторий для работы с новеллами
type NovelRepository struct {
	pool *pgxpool.Pool
}

// NewNovelRepository создает новый экземпляр репозитория для работы с новеллами
func NewNovelRepository(pool *pgxpool.Pool) *NovelRepository {
	return &NovelRepository{
		pool: pool,
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

// GetByID получает новеллу по ID
func (r *NovelRepository) GetByID(ctx context.Context, id uuid.UUID) (model.Novel, error) {
	query := `
		SELECT id, title, description, author_id, is_public, cover_image, tags, created_at, updated_at, published_at
		FROM novels
		WHERE id = $1
	`

	row := r.pool.QueryRow(ctx, query, id)

	var novel model.Novel
	var tagsJSONStr string
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
		&novel.PublishedAt,
	)
	if err != nil {
		return model.Novel{}, err
	}

	// Разбор тегов из JSON
	err = json.Unmarshal([]byte(tagsJSONStr), &novel.Tags)
	if err != nil {
		return model.Novel{}, err
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

// CreateScene создает новую сцену для новеллы
func (r *NovelRepository) CreateScene(ctx context.Context, scene model.Scene) (model.Scene, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.Scene{}, err
	}
	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	// Сначала сохраняем сцену
	sceneQuery := `
		INSERT INTO scenes (id, novel_id, title, description, content, "order", created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		RETURNING id, novel_id, title, description, content, "order", created_at, updated_at
	`

	now := time.Now()

	// Если ID не указан, генерируем новый
	if scene.ID == uuid.Nil {
		scene.ID = uuid.New()
	}

	sceneRow := tx.QueryRow(ctx, sceneQuery,
		scene.ID,
		scene.NovelID,
		scene.Title,
		scene.Description,
		scene.Content,
		scene.Order,
		now,
	)

	var createdScene model.Scene
	err = sceneRow.Scan(
		&createdScene.ID,
		&createdScene.NovelID,
		&createdScene.Title,
		&createdScene.Description,
		&createdScene.Content,
		&createdScene.Order,
		&createdScene.CreatedAt,
		&createdScene.UpdatedAt,
	)
	if err != nil {
		return model.Scene{}, err
	}

	// Затем сохраняем все выборы для этой сцены
	createdScene.Choices = make([]model.Choice, 0, len(scene.Choices))
	for _, choice := range scene.Choices {
		// Если ID выбора не указан, генерируем новый
		if choice.ID == uuid.Nil {
			choice.ID = uuid.New()
		}

		choiceQuery := `
			INSERT INTO choices (id, scene_id, text, next_scene_id, requirements, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $6)
			RETURNING id, scene_id, text, next_scene_id, requirements, created_at, updated_at
		`

		choiceRow := tx.QueryRow(ctx, choiceQuery,
			choice.ID,
			createdScene.ID,
			choice.Text,
			choice.NextSceneID,
			choice.Requirements,
			now,
		)

		var createdChoice model.Choice
		err = choiceRow.Scan(
			&createdChoice.ID,
			&createdChoice.SceneID,
			&createdChoice.Text,
			&createdChoice.NextSceneID,
			&createdChoice.Requirements,
			&createdChoice.CreatedAt,
			&createdChoice.UpdatedAt,
		)
		if err != nil {
			return model.Scene{}, err
		}

		createdScene.Choices = append(createdScene.Choices, createdChoice)
	}

	// Фиксируем транзакцию
	err = tx.Commit(ctx)
	if err != nil {
		return model.Scene{}, err
	}

	return createdScene, nil
}

// GetScenesByNovelID получает все сцены для новеллы
func (r *NovelRepository) GetScenesByNovelID(ctx context.Context, novelID uuid.UUID) ([]model.Scene, error) {
	// Запрос для получения всех сцен для новеллы
	scenesQuery := `
		SELECT id, novel_id, title, description, content, "order", created_at, updated_at
		FROM scenes
		WHERE novel_id = $1
		ORDER BY "order"
	`

	rows, err := r.pool.Query(ctx, scenesQuery, novelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	scenes := []model.Scene{}
	sceneIDs := []uuid.UUID{}

	for rows.Next() {
		var scene model.Scene
		err := rows.Scan(
			&scene.ID,
			&scene.NovelID,
			&scene.Title,
			&scene.Description,
			&scene.Content,
			&scene.Order,
			&scene.CreatedAt,
			&scene.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		scenes = append(scenes, scene)
		sceneIDs = append(sceneIDs, scene.ID)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Если есть сцены, получаем для них выборы
	if len(scenes) > 0 {
		// Запрос для получения всех выборов для найденных сцен
		choicesQuery := `
			SELECT id, scene_id, text, next_scene_id, requirements, created_at, updated_at
			FROM choices
			WHERE scene_id = ANY($1)
		`

		choiceRows, err := r.pool.Query(ctx, choicesQuery, sceneIDs)
		if err != nil {
			return nil, err
		}
		defer choiceRows.Close()

		// Временная карта для связывания выборов со сценами
		choicesBySceneID := make(map[uuid.UUID][]model.Choice)

		for choiceRows.Next() {
			var choice model.Choice
			err := choiceRows.Scan(
				&choice.ID,
				&choice.SceneID,
				&choice.Text,
				&choice.NextSceneID,
				&choice.Requirements,
				&choice.CreatedAt,
				&choice.UpdatedAt,
			)
			if err != nil {
				return nil, err
			}

			// Добавляем выбор к соответствующей сцене
			choicesBySceneID[choice.SceneID] = append(choicesBySceneID[choice.SceneID], choice)
		}

		if err := choiceRows.Err(); err != nil {
			return nil, err
		}

		// Добавляем выборы к каждой сцене
		for i := range scenes {
			if choices, ok := choicesBySceneID[scenes[i].ID]; ok {
				scenes[i].Choices = choices
			} else {
				scenes[i].Choices = []model.Choice{}
			}
		}
	}

	return scenes, nil
}

// SaveNovelState сохраняет состояние новеллы для пользователя
func (r *NovelRepository) SaveNovelState(ctx context.Context, state model.NovelState) (model.NovelState, error) {
	// Преобразуем поля в JSON для хранения в базе
	variablesJSON, err := json.Marshal(state.Variables)
	if err != nil {
		return model.NovelState{}, err
	}

	historyJSON, err := json.Marshal(state.History)
	if err != nil {
		return model.NovelState{}, err
	}

	now := time.Now()

	// Проверяем, существует ли уже состояние для этого пользователя и новеллы
	var existingID uuid.UUID
	checkQuery := `
		SELECT id FROM novel_states
		WHERE user_id = $1 AND novel_id = $2
	`
	err = r.pool.QueryRow(ctx, checkQuery, state.UserID, state.NovelID).Scan(&existingID)

	// Если состояние уже существует, обновляем его
	if err == nil {
		updateQuery := `
			UPDATE novel_states
			SET current_scene_id = $3, variables = $4, history = $5, updated_at = $6
			WHERE id = $1
			RETURNING id, user_id, novel_id, current_scene_id, variables, history, created_at, updated_at
		`

		row := r.pool.QueryRow(ctx, updateQuery,
			existingID,
			state.CurrentSceneID,
			variablesJSON,
			historyJSON,
			now,
		)

		var updatedState model.NovelState
		var variablesJSONStr, historyJSONStr string
		err := row.Scan(
			&updatedState.ID,
			&updatedState.UserID,
			&updatedState.NovelID,
			&updatedState.CurrentSceneID,
			&variablesJSONStr,
			&historyJSONStr,
			&updatedState.CreatedAt,
			&updatedState.UpdatedAt,
		)
		if err != nil {
			return model.NovelState{}, err
		}

		// Разбор полей из JSON
		err = json.Unmarshal([]byte(variablesJSONStr), &updatedState.Variables)
		if err != nil {
			return model.NovelState{}, err
		}

		err = json.Unmarshal([]byte(historyJSONStr), &updatedState.History)
		if err != nil {
			return model.NovelState{}, err
		}

		return updatedState, nil
	} else if err != pgx.ErrNoRows {
		// Если произошла ошибка, отличная от "нет строк"
		return model.NovelState{}, err
	}

	// Если состояние не существует, создаем новое
	insertQuery := `
		INSERT INTO novel_states (id, user_id, novel_id, current_scene_id, variables, history, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		RETURNING id, user_id, novel_id, current_scene_id, variables, history, created_at, updated_at
	`

	// Если ID не указан, генерируем новый
	if state.ID == uuid.Nil {
		state.ID = uuid.New()
	}

	row := r.pool.QueryRow(ctx, insertQuery,
		state.ID,
		state.UserID,
		state.NovelID,
		state.CurrentSceneID,
		variablesJSON,
		historyJSON,
		now,
	)

	var createdState model.NovelState
	var variablesJSONStr, historyJSONStr string
	err = row.Scan(
		&createdState.ID,
		&createdState.UserID,
		&createdState.NovelID,
		&createdState.CurrentSceneID,
		&variablesJSONStr,
		&historyJSONStr,
		&createdState.CreatedAt,
		&createdState.UpdatedAt,
	)
	if err != nil {
		return model.NovelState{}, err
	}

	// Разбор полей из JSON
	err = json.Unmarshal([]byte(variablesJSONStr), &createdState.Variables)
	if err != nil {
		return model.NovelState{}, err
	}

	err = json.Unmarshal([]byte(historyJSONStr), &createdState.History)
	if err != nil {
		return model.NovelState{}, err
	}

	return createdState, nil
}

// GetNovelState получает состояние новеллы для пользователя
func (r *NovelRepository) GetNovelState(ctx context.Context, userID, novelID uuid.UUID) (model.NovelState, error) {
	query := `
		SELECT id, user_id, novel_id, current_scene_id, variables, history, created_at, updated_at
		FROM novel_states
		WHERE user_id = $1 AND novel_id = $2
	`

	row := r.pool.QueryRow(ctx, query, userID, novelID)

	var state model.NovelState
	var variablesJSONStr, historyJSONStr string
	err := row.Scan(
		&state.ID,
		&state.UserID,
		&state.NovelID,
		&state.CurrentSceneID,
		&variablesJSONStr,
		&historyJSONStr,
		&state.CreatedAt,
		&state.UpdatedAt,
	)
	if err != nil {
		return model.NovelState{}, err
	}

	// Разбор полей из JSON
	err = json.Unmarshal([]byte(variablesJSONStr), &state.Variables)
	if err != nil {
		return model.NovelState{}, err
	}

	err = json.Unmarshal([]byte(historyJSONStr), &state.History)
	if err != nil {
		return model.NovelState{}, err
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
