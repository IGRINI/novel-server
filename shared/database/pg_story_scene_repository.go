package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	interfaces "novel-server/shared/interfaces"
	"novel-server/shared/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"go.uber.org/zap"
)

// Compile-time check to ensure implementation satisfies the interface.
var _ interfaces.StorySceneRepository = (*pgStorySceneRepository)(nil)

type pgStorySceneRepository struct {
	db     interfaces.DBTX
	logger *zap.Logger
}

func NewPgStorySceneRepository(db interfaces.DBTX, logger *zap.Logger) interfaces.StorySceneRepository {
	return &pgStorySceneRepository{
		db:     db,
		logger: logger.Named("PgStorySceneRepo"),
	}
}

const createStorySceneQuery = `
INSERT INTO story_scenes (id, published_story_id, state_hash, scene_content, created_at)
VALUES ($1, $2, $3, $4, $5)`

const findStorySceneByHashQuery = `
SELECT id, published_story_id, state_hash, scene_content, created_at
FROM story_scenes
WHERE published_story_id = $1 AND state_hash = $2`

// Добавляем константу для запроса GetByID
const getStorySceneByIDQuery = `
SELECT id, published_story_id, state_hash, scene_content, created_at
FROM story_scenes
WHERE id = $1`

// <<< ДОБАВЛЕНО: Константа для запроса ListByStoryID >>>
const listStoryScenesByStoryIDQuery = `
SELECT id, published_story_id, state_hash, scene_content, created_at
FROM story_scenes
WHERE published_story_id = $1
ORDER BY created_at DESC` // Сортируем по убыванию даты создания

// Create inserts a new story scene record.
func (r *pgStorySceneRepository) Create(ctx context.Context, scene *models.StoryScene) error {
	if scene.ID == uuid.Nil {
		scene.ID = uuid.New()
	}
	if scene.CreatedAt.IsZero() {
		scene.CreatedAt = time.Now()
	}

	_, err := r.db.Exec(ctx, createStorySceneQuery,
		scene.ID,
		scene.PublishedStoryID,
		scene.StateHash,
		scene.Content, // Передаем json.RawMessage напрямую, имя колонки в SQL уже обновлено
		scene.CreatedAt,
	)
	if err != nil {
		r.logger.Error("Failed to create story scene", zap.Error(err), zap.String("storyID", scene.PublishedStoryID.String()), zap.String("stateHash", scene.StateHash))
		return fmt.Errorf("ошибка создания сцены: %w", err)
	}
	r.logger.Info("Story scene created", zap.String("sceneID", scene.ID.String()))
	return nil
}

// FindByStoryAndHash attempts to find an existing scene for a given story and state hash.
func (r *pgStorySceneRepository) FindByStoryAndHash(ctx context.Context, publishedStoryID uuid.UUID, stateHash string) (*models.StoryScene, error) {
	scene := &models.StoryScene{}
	logFields := []zap.Field{
		zap.String("publishedStoryID", publishedStoryID.String()),
		zap.String("stateHash", stateHash),
	}
	err := r.db.QueryRow(ctx, findStorySceneByHashQuery, publishedStoryID, stateHash).Scan(
		&scene.ID,
		&scene.PublishedStoryID,
		&scene.StateHash,
		&scene.Content, // Сканируем напрямую в json.RawMessage, имя колонки в SQL уже обновлено
		&scene.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Debug("Story scene not found by hash", logFields...)
			return nil, models.ErrNotFound
		}
		r.logger.Error("Failed to find story scene by hash", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка поиска сцены по хэшу: %w", err)
	}
	r.logger.Debug("Story scene found by hash", append(logFields, zap.String("sceneID", scene.ID.String()))...)
	return scene, nil
}

// GetByID retrieves a story scene by its unique ID.
func (r *pgStorySceneRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.StoryScene, error) {
	scene := &models.StoryScene{}
	logFields := []zap.Field{zap.String("sceneID", id.String())}

	err := r.db.QueryRow(ctx, getStorySceneByIDQuery, id).Scan(
		&scene.ID,
		&scene.PublishedStoryID,
		&scene.StateHash,
		&scene.Content, // Сканируем напрямую в json.RawMessage, имя колонки в SQL уже обновлено
		&scene.CreatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Warn("Story scene not found by ID", logFields...)
			return nil, models.ErrNotFound
		}
		r.logger.Error("Failed to get story scene by ID", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения сцены по ID %s: %w", id, err)
	}

	r.logger.Debug("Story scene retrieved successfully by ID", logFields...)
	return scene, nil
}

// <<< ДОБАВЛЕНО: Реализация метода ListByStoryID >>>
func (r *pgStorySceneRepository) ListByStoryID(ctx context.Context, publishedStoryID uuid.UUID) ([]models.StoryScene, error) {
	rows, err := r.db.Query(ctx, listStoryScenesByStoryIDQuery, publishedStoryID)
	if err != nil {
		r.logger.Error("Failed to query story scenes by story ID", zap.String("publishedStoryID", publishedStoryID.String()), zap.Error(err))
		return nil, fmt.Errorf("ошибка запроса сцен истории: %w", err)
	}
	defer rows.Close()

	scenes := make([]models.StoryScene, 0)
	for rows.Next() {
		var scene models.StoryScene
		err := rows.Scan(
			&scene.ID,
			&scene.PublishedStoryID,
			&scene.StateHash,
			&scene.Content, // Сканируем напрямую в json.RawMessage, имя колонки в SQL уже обновлено
			&scene.CreatedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan story scene row", zap.String("publishedStoryID", publishedStoryID.String()), zap.Error(err))
			return nil, fmt.Errorf("ошибка сканирования строки сцены: %w", err)
		}
		scenes = append(scenes, scene)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating over story scene rows", zap.String("publishedStoryID", publishedStoryID.String()), zap.Error(err))
		return nil, fmt.Errorf("ошибка итерации по строкам сцен: %w", err)
	}

	// Не возвращаем ErrNotFound, если список пуст, просто пустой слайс.
	r.logger.Debug("Successfully listed story scenes by story ID", zap.String("publishedStoryID", publishedStoryID.String()), zap.Int("count", len(scenes)))
	return scenes, nil
}

// <<< ДОБАВЛЕНО: Реализация UpdateContent >>>
const updateSceneContentQuery = `
UPDATE story_scenes
SET scene_content = $2, updated_at = NOW()
WHERE id = $1`

func (r *pgStorySceneRepository) UpdateContent(ctx context.Context, id uuid.UUID, content []byte) error {
	logFields := []zap.Field{
		zap.String("sceneID", id.String()),
		zap.Int("contentSize", len(content)),
	}
	r.logger.Debug("Updating story scene content", logFields...)

	// Используем NOW() для updated_at, если такой колонки нет, надо будет добавить миграцией
	commandTag, err := r.db.Exec(ctx, updateSceneContentQuery, id, content)
	if err != nil {
		r.logger.Error("Failed to update story scene content", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления контента сцены %s: %w", id, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("Story scene not found for content update", logFields...)
		return models.ErrNotFound // Используем стандартную ошибку
	}

	r.logger.Info("Story scene content updated successfully", logFields...)
	return nil
}

// <<< ДОБАВЛЕНО: Запрос и реализация Upsert >>>
const upsertStorySceneQuery = `
INSERT INTO story_scenes (id, published_story_id, state_hash, scene_content, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, NOW())
ON CONFLICT (published_story_id, state_hash) DO UPDATE SET
    scene_content = EXCLUDED.scene_content,
    updated_at = NOW()
WHERE trim(story_scenes.scene_content) = '{}' OR trim(story_scenes.scene_content) = '[]';`

// Upsert creates a new scene or updates an existing one based on storyID and stateHash.
func (r *pgStorySceneRepository) Upsert(ctx context.Context, scene *models.StoryScene) error {
	if scene.ID == uuid.Nil {
		scene.ID = uuid.New() // Генерируем ID для новой записи
	}
	if scene.CreatedAt.IsZero() {
		scene.CreatedAt = time.Now() // Устанавливаем время создания только для новой записи
	}
	// updated_at обрабатывается через NOW() в самом запросе

	logFields := []zap.Field{
		zap.String("publishedStoryID", scene.PublishedStoryID.String()),
		zap.String("stateHash", scene.StateHash),
		zap.String("sceneIDHint", scene.ID.String()), // ID может измениться при конфликте
	}

	_, err := r.db.Exec(ctx, upsertStorySceneQuery,
		scene.ID,
		scene.PublishedStoryID,
		scene.StateHash,
		scene.Content,
		scene.CreatedAt,
	)

	if err != nil {
		r.logger.Error("Failed to upsert story scene", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка upsert сцены: %w", err)
	}
	r.logger.Info("Story scene upserted successfully", logFields...)
	return nil
}

// Delete удаляет сцену по ID.
func (r *pgStorySceneRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM story_scenes WHERE id = $1`

	result, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("error executing delete query for story scene %s: %w", id, err)
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		// Если ничего не удалено, значит, сцена с таким ID не найдена.
		return models.ErrNotFound
	}

	if rowsAffected > 1 {
		// Этого не должно происходить при удалении по PK, но добавим проверку.
		r.logger.Warn("Multiple rows affected by delete query for single story scene ID",
			zap.String("sceneID", id.String()),
			zap.Int64("rowsAffected", rowsAffected),
		)
	}

	return nil
}

// GetByStoryIDAndStateHash получает сцену по ID истории и хешу состояния.
func (r *pgStorySceneRepository) GetByStoryIDAndStateHash(ctx context.Context, storyID uuid.UUID, stateHash string) (*models.StoryScene, error) {
	query := `
		SELECT id, published_story_id, state_hash, scene_content, created_at
		FROM story_scenes
		WHERE published_story_id = $1 AND state_hash = $2
	`

	row := r.db.QueryRow(ctx, query, storyID, stateHash)
	scene := &models.StoryScene{}

	err := row.Scan(
		&scene.ID,
		&scene.PublishedStoryID,
		&scene.StateHash,
		&scene.Content,
		&scene.CreatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound // Используем стандартную ошибку
		}
		return nil, fmt.Errorf("error scanning story scene row for story %s, hash %s: %w", storyID, stateHash, err)
	}

	return scene, nil
}
