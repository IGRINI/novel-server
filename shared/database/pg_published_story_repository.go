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

// scanPublishedStory сканирует строку в структуру PublishedStory
func scanPublishedStory(row pgx.Row) (*models.PublishedStory, error) {
	var story models.PublishedStory
	err := row.Scan(
		&story.ID,
		&story.UserID,
		&story.Config,
		&story.Setup,
		&story.Status,
		&story.IsPublic,
		&story.IsAdultContent,
		&story.Title,
		&story.Description,
		&story.CreatedAt, // Убедиться, что поле CreatedAt есть
		&story.UpdatedAt, // Убедиться, что поле UpdatedAt есть
	)
	if err != nil {
		return nil, err
	}
	return &story, nil
}

// scanPublishedStories сканирует несколько строк
func scanPublishedStories(rows pgx.Rows) ([]models.PublishedStory, error) {
	stories := make([]models.PublishedStory, 0)
	var err error
	for rows.Next() {
		var story models.PublishedStory
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
		)
		if err != nil {
			continue
		}
		stories = append(stories, story)
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
		zap.Uint64("userID", story.UserID),
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
            title, description, error_details, created_at, updated_at
        FROM published_stories
        WHERE id = $1
    `
	story := &models.PublishedStory{}
	logFields := []zap.Field{zap.String("publishedStoryID", id.String())}
	r.logger.Debug("Getting published story by ID", logFields...)

	err := r.db.QueryRow(ctx, query, id).Scan(
		&story.ID, &story.UserID, &story.Config, &story.Setup, &story.Status,
		&story.IsPublic, &story.IsAdultContent, &story.Title, &story.Description,
		&story.ErrorDetails, &story.CreatedAt, &story.UpdatedAt,
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
func (r *pgPublishedStoryRepository) SetPublic(ctx context.Context, id uuid.UUID, userID uint64, isPublic bool) error {
	query := `
        UPDATE published_stories
        SET is_public = $2, updated_at = NOW()
        WHERE id = $1 AND user_id = $3
    `
	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.Uint64("userID", userID),
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
		// We return ErrNotFound, assuming either the story doesn't exist or doesn't belong to the user.
		// More specific checks could be done with a preliminary SELECT if needed.
		return models.ErrNotFound
	}

	r.logger.Info("Published story public status updated successfully", logFields...)
	return nil
}

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
			// Убедитесь, что error_details тоже сканируется, если оно есть в запросе
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

// ListByUser возвращает список опубликованных историй пользователя с offset/limit пагинацией.
// Сигнатура соответствует старому интерфейсу.
func (r *pgPublishedStoryRepository) ListByUser(ctx context.Context, userID uint64, limit, offset int) ([]*models.PublishedStory, error) {
	if limit <= 0 {
		limit = 10 // Default limit
	}
	if offset < 0 {
		offset = 0 // Offset cannot be negative
	}

	query := `
        SELECT id, user_id, config, setup, status, is_public, is_adult_content, title, description, created_at, updated_at /*, error_details */
        FROM published_stories
        WHERE user_id = $1
        ORDER BY updated_at DESC -- Сортировка по обновлению
        LIMIT $2 OFFSET $3
    `
	logFields := []zap.Field{zap.Uint64("userID", userID), zap.Int("limit", limit), zap.Int("offset", offset)}
	r.logger.Debug("Listing published stories by user (offset/limit)", logFields...)

	rows, err := r.db.Query(ctx, query, userID, limit, offset)
	if err != nil {
		r.logger.Error("Failed to query published stories by user", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения списка историй пользователя %d: %w", userID, err)
	}
	defer rows.Close()

	stories, err := scanStories(rows) // Используем scanStories, возвращающий []*...
	if err != nil {
		r.logger.Error("Failed to scan published stories for user", append(logFields, zap.Error(err))...)
		return nil, err
	}

	r.logger.Debug("Published stories for user listed successfully", append(logFields, zap.Int("count", len(stories)))...)
	return stories, nil
}

// ListPublic возвращает список публичных опубликованных историй с offset/limit пагинацией.
// Сигнатура соответствует старому интерфейсу.
func (r *pgPublishedStoryRepository) ListPublic(ctx context.Context, limit, offset int) ([]*models.PublishedStory, error) {
	if limit <= 0 {
		limit = 10 // Default limit
	}
	if offset < 0 {
		offset = 0 // Offset cannot be negative
	}

	// Убираем is_adult_content = FALSE из запроса, если интерфейс этого не требует
	query := `
        SELECT id, user_id, config, setup, status, is_public, is_adult_content, title, description, created_at, updated_at /*, error_details */
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
