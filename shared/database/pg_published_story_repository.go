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
	"go.uber.org/zap"
)

// Compile-time check
var _ interfaces.PublishedStoryRepository = (*pgPublishedStoryRepository)(nil)

// scanStories сканирует несколько строк (возвращает слайс указателей)
func scanStories(rows pgx.Rows) ([]*models.PublishedStory, error) {
	stories := make([]*models.PublishedStory, 0)
	var err error
	for rows.Next() {
		var story models.PublishedStory // Сканируем в значение
		err = rows.Scan(
			&story.ID,
			&story.UserID,
			&story.Config,
			&story.Setup,
			&story.Status,
			&story.IsPublic,
			&story.IsAdultContent,
			&story.Title,
			&story.Description,
			&story.CreatedAt,
			&story.UpdatedAt,
			&story.LikesCount,
		)
		if err != nil {
			continue
		}
		stories = append(stories, &story) // Добавляем указатель
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка итерации по результатам published_stories: %w", err)
	}
	return stories, nil
}

// pgPublishedStoryRepository реализует интерфейс PublishedStoryRepository для PostgreSQL.
type pgPublishedStoryRepository struct {
	db     interfaces.DBTX
	logger *zap.Logger
}

// NewPgPublishedStoryRepository создает новый экземпляр репозитория.
func NewPgPublishedStoryRepository(db interfaces.DBTX, logger *zap.Logger) interfaces.PublishedStoryRepository {
	return &pgPublishedStoryRepository{
		db:     db,
		logger: logger.Named("PgPublishedStoryRepo"),
	}
}

// Create создает новую запись опубликованной истории.
func (r *pgPublishedStoryRepository) Create(ctx context.Context, story *models.PublishedStory) error {
	// Генерируем UUID, если он еще не установлен
	if story.ID == uuid.Nil {
		story.ID = uuid.New()
	}
	now := time.Now().UTC()
	story.CreatedAt = now
	story.UpdatedAt = now

	query := `
        INSERT INTO published_stories
            (id, user_id, config, setup, status, is_public, is_adult_content, title, description, created_at, updated_at)
        VALUES
            ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
    `
	logFields := []zap.Field{
		zap.String("publishedStoryID", story.ID.String()),
		zap.String("userID", story.UserID.String()),
	}
	r.logger.Debug("Creating published story", logFields...)

	_, err := r.db.Exec(ctx, query,
		story.ID, story.UserID, story.Config, story.Setup, story.Status,
		story.IsPublic, story.IsAdultContent, story.Title, story.Description,
		story.CreatedAt, story.UpdatedAt,
	)
	if err != nil {
		r.logger.Error("Failed to create published story", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка создания published story: %w", err)
	}
	r.logger.Info("Published story created successfully", logFields...)
	return nil
}

// GetByID retrieves a published story by its unique ID.
func (r *pgPublishedStoryRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.PublishedStory, error) {
	query := `
        SELECT
            id, user_id, config, setup, status, is_public, is_adult_content,
            title, description, error_details, likes_count, created_at, updated_at
        FROM published_stories
        WHERE id = $1
    `
	story := &models.PublishedStory{}
	logFields := []zap.Field{zap.String("publishedStoryID", id.String())}
	r.logger.Debug("Getting published story by ID", logFields...)

	err := r.db.QueryRow(ctx, query, id).Scan(
		&story.ID, &story.UserID, &story.Config, &story.Setup, &story.Status,
		&story.IsPublic, &story.IsAdultContent, &story.Title, &story.Description,
		&story.ErrorDetails, &story.LikesCount, &story.CreatedAt, &story.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Warn("Published story not found by ID", logFields...)
			return nil, models.ErrNotFound // Use shared error
		}
		r.logger.Error("Failed to get published story by ID", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("failed to get published story by ID %s: %w", id, err)
	}

	r.logger.Debug("Published story retrieved successfully", logFields...)
	return story, nil
}

// updateStatusDetailsQuery builds the SET clause dynamically.
const baseUpdateStatusDetailsQuery = `UPDATE published_stories SET status = $2, updated_at = $3`

// UpdateStatusDetails updates various fields based on non-nil arguments.
func (r *pgPublishedStoryRepository) UpdateStatusDetails(ctx context.Context, id uuid.UUID, status models.StoryStatus, setup []byte, errorDetails *string, endingText *string) error {
	query := baseUpdateStatusDetailsQuery
	args := []interface{}{id, status, time.Now()}
	paramIndex := 4 // Start after id, status, updated_at

	if setup != nil {
		query += fmt.Sprintf(", setup = $%d", paramIndex)
		args = append(args, json.RawMessage(setup)) // Ensure it's json.RawMessage
		paramIndex++
	}
	if errorDetails != nil {
		query += fmt.Sprintf(", error_details = $%d", paramIndex)
		args = append(args, errorDetails)
		paramIndex++
	}
	if endingText != nil {
		query += fmt.Sprintf(", ending_text = $%d", paramIndex)
		args = append(args, endingText)
		paramIndex++
	}

	query += " WHERE id = $1"

	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.String("newStatus", string(status)),
		zap.Bool("setupUpdated", setup != nil),
		zap.Bool("errorUpdated", errorDetails != nil),
		zap.Bool("endingTextUpdated", endingText != nil),
	}
	r.logger.Debug("Updating published story status and details", logFields...)

	tag, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to update published story status/details", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления статуса/деталей опубликованной истории %s: %w", id, err)
	}

	if tag.RowsAffected() == 0 {
		r.logger.Warn("No rows affected when updating published story status/details", logFields...)
		return models.ErrNotFound // Or a more specific error?
	}

	r.logger.Info("Published story status and details updated successfully", logFields...)
	return nil
}

// SetPublic updates the is_public flag for a story.
func (r *pgPublishedStoryRepository) SetPublic(ctx context.Context, id uuid.UUID, userID uuid.UUID, isPublic bool) error {
	query := `
        UPDATE published_stories
        SET is_public = $2, updated_at = NOW()
        WHERE id = $1 AND user_id = $3
    `
	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.String("userID", userID.String()),
		zap.Bool("isPublic", isPublic),
	}
	r.logger.Debug("Setting published story public status", logFields...)

	commandTag, err := r.db.Exec(ctx, query, id, isPublic, userID)

	if err != nil {
		r.logger.Error("Failed to set public status for published story", append(logFields, zap.Error(err))...)
		return fmt.Errorf("failed to set public status for story %s: %w", id, err)
	}
	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("Attempted to set public status for non-existent or unauthorized story", logFields...)
		return models.ErrNotFound
	}

	r.logger.Info("Published story public status updated successfully", logFields...)
	return nil
}

// ListByUserID retrieves a paginated list of stories created by a specific user.
func (r *pgPublishedStoryRepository) ListByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*models.PublishedStory, error) {
	query := `
        SELECT
            id, user_id, config, setup, status, is_public, is_adult_content,
            title, description, error_details, likes_count, created_at, updated_at
        FROM published_stories
        WHERE user_id = $1
        ORDER BY created_at DESC
        LIMIT $2 OFFSET $3
    `
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.Int("limit", limit),
		zap.Int("offset", offset),
	}
	r.logger.Debug("Listing published stories by user", logFields...)

	rows, err := r.db.Query(ctx, query, userID, limit, offset)
	if err != nil {
		r.logger.Error("Failed to query published stories by user", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения списка опубликованных историй пользователя %s: %w", userID.String(), err)
	}
	defer rows.Close()

	stories, err := scanStories(rows) // Используем scanStories
	if err != nil {
		// Ошибка уже залогирована в scanStories, если rows.Err() != nil
		return nil, err // Просто возвращаем ошибку сканирования
	}

	r.logger.Debug("Published stories listed successfully by user", append(logFields, zap.Int("count", len(stories)))...)
	return stories, nil
}

// ListPublic retrieves a paginated list of public stories.
func (r *pgPublishedStoryRepository) ListPublic(ctx context.Context, limit, offset int) ([]*models.PublishedStory, error) {
	if limit <= 0 {
		limit = 10 // Default limit
	}
	if offset < 0 {
		offset = 0 // Offset cannot be negative
	}

	// Убираем is_adult_content = FALSE из запроса, если интерфейс этого не требует
	query := `
        SELECT id, user_id, config, setup, status, is_public, is_adult_content, title, description, created_at, updated_at, likes_count /*, error_details */
        FROM published_stories
        WHERE is_public = TRUE
        ORDER BY created_at DESC -- Сортировка по созданию
        LIMIT $1 OFFSET $2
    `
	logFields := []zap.Field{zap.Int("limit", limit), zap.Int("offset", offset)}
	r.logger.Debug("Listing public published stories (offset/limit)", logFields...)

	rows, err := r.db.Query(ctx, query, limit, offset)
	if err != nil {
		r.logger.Error("Failed to query public published stories", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения списка публичных историй: %w", err)
	}
	defer rows.Close()

	stories, err := scanStories(rows) // Используем scanStories, возвращающий []*...
	if err != nil {
		r.logger.Error("Failed to scan public published stories", append(logFields, zap.Error(err))...)
		return nil, err
	}

	r.logger.Debug("Public published stories listed successfully", append(logFields, zap.Int("count", len(stories)))...)
	return stories, nil
}

// IncrementLikesCount атомарно увеличивает счетчик лайков для истории.
func (r *pgPublishedStoryRepository) IncrementLikesCount(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE published_stories SET likes_count = likes_count + 1, updated_at = NOW() WHERE id = $1`
	logFields := []zap.Field{zap.String("publishedStoryID", id.String())}
	r.logger.Debug("Incrementing likes count", logFields...)

	commandTag, err := r.db.Exec(ctx, query, id)
	if err != nil {
		r.logger.Error("Failed to increment likes count", append(logFields, zap.Error(err))...)
		return fmt.Errorf("failed to increment likes count for story %s: %w", id, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("Attempted to increment likes count for non-existent story", logFields...)
		return models.ErrNotFound // История не найдена
	}

	r.logger.Info("Likes count incremented successfully", logFields...)
	return nil
}

// DecrementLikesCount атомарно уменьшает счетчик лайков для истории, не давая уйти ниже нуля.
func (r *pgPublishedStoryRepository) DecrementLikesCount(ctx context.Context, id uuid.UUID) error {
	// Обновляем только если likes_count > 0
	query := `UPDATE published_stories SET likes_count = likes_count - 1, updated_at = NOW() WHERE id = $1 AND likes_count > 0`
	logFields := []zap.Field{zap.String("publishedStoryID", id.String())}
	r.logger.Debug("Decrementing likes count", logFields...)

	_, err := r.db.Exec(ctx, query, id)
	if err != nil {
		r.logger.Error("Failed to decrement likes count", append(logFields, zap.Error(err))...)
		return fmt.Errorf("failed to decrement likes count for story %s: %w", id, err)
	}

	r.logger.Info("Likes count decremented (or was already zero) successfully", logFields...)
	return nil
}
