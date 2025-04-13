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
INSERT INTO story_scenes (id, published_story_id, state_hash, content, created_at)
VALUES ($1, $2, $3, $4, $5)`

const findStorySceneByHashQuery = `
SELECT id, published_story_id, state_hash, content, created_at
FROM story_scenes
WHERE published_story_id = $1 AND state_hash = $2`

// Добавляем константу для запроса GetByID
const getStorySceneByIDQuery = `
SELECT id, published_story_id, state_hash, content, created_at
FROM story_scenes
WHERE id = $1`

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
		scene.Content, // Передаем json.RawMessage напрямую
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
		&scene.Content, // Сканируем напрямую в json.RawMessage
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
		&scene.Content, // Сканируем напрямую в json.RawMessage
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
