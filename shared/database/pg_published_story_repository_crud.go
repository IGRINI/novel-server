package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype" // Import pgtype for NullUUID equivalent
	"go.uber.org/zap"
)

// Определение поля для сканирования полной структуры PublishedStory - ПЕРЕНЕСЕНО В ОСНОВНОЙ ФАЙЛ
/*
const publishedStoryFields = `
		ps.id, ps.user_id, ps.config, ps.setup, ps.status, ps.language, ps.is_public, ps.is_adult_content,
		ps.title, ps.description, ps.cover_image_url, ps.error_details, ps.likes_count, ps.created_at, ps.updated_at,
		ps.is_first_scene_pending, ps.are_images_pending
	`
*/

// Константы, необходимые для CRUD операций
const (
	createPublishedStoryQuery = `
		INSERT INTO published_stories (
			id, user_id, title, description, status, language, -- Include id in insert
			is_public, is_adult_content, /* cover_image_url REMOVED */ config, setup,
			created_at, updated_at -- likes_count default to 0
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, /* $9 REMOVED */ $9, $10, $11, $12 -- 12 args now
		)
	`
	// publishedStoryFields используется в GetByID, предполагается, что он доступен
	getPublishedStoryByIDQuery = `SELECT ` + publishedStoryFields + ` FROM published_stories ps WHERE ps.id = $1`

	updatePublishedStoryQuery = `
		UPDATE published_stories SET
			title = $2,
			description = $3,
			status = $4,
			language = $5,
			is_public = $6,
			is_adult_content = $7,
			-- cover_image_url = $8, -- REMOVED
			config = $8, -- Index shifted
			setup = $9,  -- Index shifted
			updated_at = NOW()
		WHERE id = $1
		RETURNING updated_at
	`
	// Константа, используемая в GetByID, должна быть доступна (определена в основном файле)
	// publishedStoryFields = `...`

	getConfigAndSetupQuery = `SELECT config, setup FROM published_stories WHERE id = $1`
)

// Create создает новую запись опубликованной истории.
// Возвращает только error в соответствии с интерфейсом.
func (r *pgPublishedStoryRepository) Create(ctx context.Context, querier interfaces.DBTX, story *models.PublishedStory) error { // Add querier param
	// Генерируем UUID, если он еще не установлен
	if story.ID == uuid.Nil {
		story.ID = uuid.New()
	}
	now := time.Now().UTC()
	story.CreatedAt = now
	story.UpdatedAt = now

	logFields := []zap.Field{
		zap.String("userID", story.UserID.String()),
		zap.Stringp("title", story.Title),
		zap.String("status", string(story.Status)),
		zap.String("newStoryID", story.ID.String()), // Log the new ID
	}
	r.logger.Debug("Creating new published story", logFields...)

	// Используем Exec, так как RETURNING id не нужен согласно интерфейсу
	_, err := querier.Exec(ctx, createPublishedStoryQuery, // Use querier
		story.ID,             // $1 (now generated)
		story.UserID,         // $2
		story.Title,          // $3 (*string)
		story.Description,    // $4 (*string)
		story.Status,         // $5
		story.Language,       // $6
		story.IsPublic,       // $7
		story.IsAdultContent, // $8
		story.Config,         // $9 (was $10)
		story.Setup,          // $10 (was $11)
		story.CreatedAt,      // $11 (was $12)
		story.UpdatedAt,      // $12 (was $13)
	)

	if err != nil {
		r.logger.Error("Failed to create published story", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка создания опубликованной истории: %w", err)
	}

	r.logger.Info("Published story created successfully", logFields...)
	return nil
}

// GetByID получает опубликованную историю по ее ID.
func (r *pgPublishedStoryRepository) GetByID(ctx context.Context, querier interfaces.DBTX, id uuid.UUID) (*models.PublishedStory, error) {
	logFields := []zap.Field{zap.String("storyID", id.String())}
	r.logger.Debug("Getting published story by ID", logFields...)

	row := querier.QueryRow(ctx, getPublishedStoryByIDQuery, id)
	story, err := scanPublishedStory(row) // USE HELPER

	if err != nil {
		if err == models.ErrNotFound { // Use error from helper
			r.logger.Warn("Published story not found by ID", logFields...)
			return nil, models.ErrNotFound
		}
		r.logger.Error("Failed to get published story by ID", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения опубликованной истории по ID %s: %w", id, err)
	}

	r.logger.Debug("Published story retrieved successfully by ID", logFields...)
	return story, nil
}

// GetWithLikeStatus retrieves a published story by its unique ID and checks if the specified user has liked it.
// If userID is uuid.Nil, isLiked will always be false.
func (r *pgPublishedStoryRepository) GetWithLikeStatus(ctx context.Context, querier interfaces.DBTX, storyID, userID uuid.UUID) (*models.PublishedStory, bool, error) {
	// Assume publishedStoryFields is defined in the main pg_published_story_repository.go file and accessible
	// It should include all fields of models.PublishedStory
	const query = `
		SELECT
			` + publishedStoryFields + `,
			CASE WHEN $2::UUID IS NOT NULL THEN EXISTS (
				SELECT 1 FROM story_likes usl WHERE usl.published_story_id = ps.id AND usl.user_id = $2
			) ELSE FALSE END AS is_liked
		FROM published_stories ps
		WHERE ps.id = $1;
	`
	log := r.logger.With(zap.String("method", "GetWithLikeStatus"), zap.Stringer("storyID", storyID), zap.Stringer("userID", userID))

	var isLiked bool
	pgUserID := pgtype.UUID{}
	if userID != uuid.Nil {
		pgUserID = pgtype.UUID{Bytes: userID, Valid: true}
	}
	row := querier.QueryRow(ctx, query, storyID, pgUserID) // Use pgtype.UUID

	var story models.PublishedStory
	// Ensure all fields from publishedStoryFields are scanned here
	err := row.Scan(
		&story.ID, &story.UserID, &story.Config, &story.Setup, &story.Status, &story.Language, &story.IsPublic, &story.IsAdultContent,
		&story.Title, &story.Description, &story.ErrorDetails, &story.LikesCount, &story.CreatedAt, &story.UpdatedAt,
		&story.IsFirstScenePending, &story.AreImagesPending, // Add any other fields included in publishedStoryFields
		&isLiked,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("Story not found")
			return nil, false, models.ErrNotFound
		}
		log.Error("Failed to scan story with like status", zap.Error(err))
		return nil, false, fmt.Errorf("ошибка сканирования истории %s со статусом лайка: %w", storyID, err)
	}

	log.Debug("Story with like status retrieved successfully")
	return &story, isLiked, nil
}

// Update обновляет данные опубликованной истории.
func (r *pgPublishedStoryRepository) Update(ctx context.Context, story *models.PublishedStory) error {
	logFields := []zap.Field{zap.String("storyID", story.ID.String())}
	r.logger.Debug("Updating published story data", logFields...)

	var updatedAt time.Time
	err := r.db.QueryRow(ctx, updatePublishedStoryQuery, // Use constant
		story.ID,             // $1
		story.Title,          // $2 (*string)
		story.Description,    // $3 (*string)
		story.Status,         // $4
		story.Language,       // $5
		story.IsPublic,       // $6
		story.IsAdultContent, // $7
		story.Config,         // $8 (was $9)
		story.Setup,          // $9 (was $10)
	).Scan(&updatedAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Warn("Published story not found for update", logFields...)
			return models.ErrNotFound
		}
		r.logger.Error("Failed to update published story", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления опубликованной истории: %w", err)
	}

	story.UpdatedAt = updatedAt // Update the timestamp in the passed struct
	r.logger.Info("Published story updated successfully", logFields...)
	return nil
}

// GetConfigAndSetup получает Config и Setup по ID истории.
func (r *pgPublishedStoryRepository) GetConfigAndSetup(ctx context.Context, querier interfaces.DBTX, id uuid.UUID) (json.RawMessage, json.RawMessage, error) {
	var config, setup []byte // Scan into byte slices
	logFields := []zap.Field{zap.String("publishedStoryID", id.String())}
	r.logger.Debug("Getting config and setup for published story", logFields...)

	err := querier.QueryRow(ctx, getConfigAndSetupQuery, id).Scan(&config, &setup)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Warn("Published story not found for config/setup retrieval", logFields...)
			return nil, nil, models.ErrNotFound
		}
		r.logger.Error("Failed to query config/setup", append(logFields, zap.Error(err))...)
		return nil, nil, fmt.Errorf("ошибка получения config/setup для истории %s: %w", id, err)
	}

	// Return as json.RawMessage, handle potential NULL (empty slice)
	var configRaw, setupRaw json.RawMessage
	if len(config) > 0 {
		configRaw = json.RawMessage(config)
	} // else remains nil
	if len(setup) > 0 {
		setupRaw = json.RawMessage(setup)
	} // else remains nil

	r.logger.Debug("Config and setup retrieved successfully", append(logFields, zap.Int("configSize", len(configRaw)), zap.Int("setupSize", len(setupRaw)))...)
	return configRaw, setupRaw, nil
}
