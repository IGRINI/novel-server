package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"
	"novel-server/shared/utils"
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
			&story.ErrorDetails,
			&story.LikesCount,
			&story.CreatedAt,
			&story.UpdatedAt,
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

// UpdateStatusDetails обновляет статус, детали ошибки или setup опубликованной истории.
func (r *pgPublishedStoryRepository) UpdateStatusDetails(ctx context.Context, id uuid.UUID, status models.StoryStatus, setup json.RawMessage, title, description, errorDetails *string) error {
	query := baseUpdateStatusDetailsQuery
	args := []interface{}{id, status, time.Now().UTC()} // Используем UTC
	paramIndex := 4                                     // Start after id, status, updated_at

	if setup != nil {
		query += fmt.Sprintf(", setup = $%d", paramIndex)
		args = append(args, setup)
		paramIndex++
	}
	if title != nil {
		query += fmt.Sprintf(", title = $%d", paramIndex)
		args = append(args, *title)
		paramIndex++
	}
	if description != nil {
		query += fmt.Sprintf(", description = $%d", paramIndex)
		args = append(args, *description)
		paramIndex++
	}
	// Добавляем обновление error_details
	if errorDetails != nil {
		query += fmt.Sprintf(", error_details = $%d", paramIndex)
		args = append(args, *errorDetails)
		paramIndex++
	}

	query += " WHERE id = $1"

	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.String("newStatus", string(status)),
	}
	if setup != nil {
		logFields = append(logFields, zap.Int("setupSize", len(setup))) // Логируем размер setup
	}
	r.logger.Debug("Updating published story status/details", append(logFields, zap.String("query", query))...)

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

// ListByUserID retrieves a paginated list of stories created by a specific user using cursor pagination.
func (r *pgPublishedStoryRepository) ListByUserID(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*models.PublishedStory, string, error) {
	if limit <= 0 {
		limit = 10 // Default limit
	}
	// Fetch one extra item to determine if there's a next page
	fetchLimit := limit + 1

	cursorTime, cursorID, err := utils.DecodeCursor(cursor)
	if err != nil {
		r.logger.Warn("Invalid cursor provided for ListByUserID", zap.String("cursor", cursor), zap.Error(err))
		// Consider returning a specific error like models.ErrInvalidInput or interfaces.ErrInvalidCursor
		return nil, "", fmt.Errorf("invalid cursor: %w", err)
	}

	query := `
        SELECT
            id, user_id, config, setup, status, is_public, is_adult_content,
            title, description, error_details, likes_count, created_at, updated_at
        FROM published_stories
        WHERE user_id = $1 ` // Note the space before appending WHERE clause

	args := []interface{}{userID}
	paramIndex := 2

	// Add cursor condition if cursor is provided
	if !cursorTime.IsZero() && cursorID != uuid.Nil {
		query += fmt.Sprintf("AND (created_at, id) < ($%d, $%d) ", paramIndex, paramIndex+1)
		args = append(args, cursorTime, cursorID)
		paramIndex += 2
	}

	query += fmt.Sprintf("ORDER BY created_at DESC, id DESC LIMIT $%d", paramIndex)
	args = append(args, fetchLimit)

	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("cursor", cursor),
		zap.Time("cursorTime", cursorTime),
		zap.Stringer("cursorID", cursorID),
		zap.Int("limit", limit),
		zap.Int("fetchLimit", fetchLimit),
	}
	r.logger.Debug("Listing published stories by user with cursor", logFields...)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to query published stories by user with cursor", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка получения списка опубликованных историй пользователя %s (курсор): %w", userID.String(), err)
	}
	defer rows.Close()

	stories, err := scanStories(rows) // scanStories remains the same
	if err != nil {
		// scanStories already logs row iteration errors
		r.logger.Error("Failed to scan stories in ListByUserID", append(logFields, zap.Error(err))...)
		return nil, "", err // Return the scan error
	}

	var nextCursor string
	if len(stories) == fetchLimit {
		// There is a next page
		lastStory := stories[limit] // The last story to generate cursor from
		nextCursor = utils.EncodeCursor(lastStory.CreatedAt, lastStory.ID)
		stories = stories[:limit] // Return only the requested number of stories
	} else {
		// This is the last page
		nextCursor = ""
	}

	r.logger.Debug("Published stories listed successfully by user with cursor", append(logFields, zap.Int("count", len(stories)), zap.Bool("hasNext", nextCursor != ""))...)
	return stories, nextCursor, nil
}

// ListPublic retrieves a paginated list of public stories using cursor pagination.
func (r *pgPublishedStoryRepository) ListPublic(ctx context.Context, cursor string, limit int) ([]*models.PublishedStory, string, error) {
	if limit <= 0 {
		limit = 10 // Default limit
	}
	// Fetch one extra item to determine if there's a next page
	fetchLimit := limit + 1

	cursorTime, cursorID, err := utils.DecodeCursor(cursor)
	if err != nil {
		r.logger.Warn("Invalid cursor provided for ListPublic", zap.String("cursor", cursor), zap.Error(err))
		return nil, "", fmt.Errorf("invalid cursor: %w", err)
	}

	// Note: Query SELECT list should match scanStories exactly!
	// Corrected to include all fields expected by scanStories.
	query := `
        SELECT
            id, user_id, config, setup, status, is_public, is_adult_content,
            title, description, error_details, likes_count, created_at, updated_at
        FROM published_stories
        WHERE is_public = TRUE ` // Note the space

	args := []interface{}{}
	paramIndex := 1

	// Add cursor condition if cursor is provided
	if !cursorTime.IsZero() && cursorID != uuid.Nil {
		query += fmt.Sprintf("AND (created_at, id) < ($%d, $%d) ", paramIndex, paramIndex+1)
		args = append(args, cursorTime, cursorID)
		paramIndex += 2
	}

	query += fmt.Sprintf("ORDER BY created_at DESC, id DESC LIMIT $%d", paramIndex)
	args = append(args, fetchLimit)

	logFields := []zap.Field{
		zap.String("cursor", cursor),
		zap.Time("cursorTime", cursorTime),
		zap.Stringer("cursorID", cursorID),
		zap.Int("limit", limit),
		zap.Int("fetchLimit", fetchLimit),
	}
	r.logger.Debug("Listing public published stories with cursor", logFields...)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to query public published stories with cursor", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка получения списка публичных историй (курсор): %w", err)
	}
	defer rows.Close()

	stories, err := scanStories(rows)
	if err != nil {
		r.logger.Error("Failed to scan public published stories", append(logFields, zap.Error(err))...)
		return nil, "", err
	}

	var nextCursor string
	if len(stories) == fetchLimit {
		lastStory := stories[limit]
		nextCursor = utils.EncodeCursor(lastStory.CreatedAt, lastStory.ID)
		stories = stories[:limit]
	} else {
		nextCursor = ""
	}

	r.logger.Debug("Public published stories listed successfully with cursor", append(logFields, zap.Int("count", len(stories)), zap.Bool("hasNext", nextCursor != ""))...)
	return stories, nextCursor, nil
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

// UpdateVisibility updates the visibility of a story.
func (r *pgPublishedStoryRepository) UpdateVisibility(ctx context.Context, storyID, userID uuid.UUID, isPublic bool) error {
	query := `
		UPDATE published_stories
		SET is_public = $1, updated_at = NOW()
		WHERE id = $2 AND user_id = $3
	`
	logFields := []zap.Field{
		zap.String("publishedStoryID", storyID.String()),
		zap.String("userID", userID.String()),
		zap.Bool("isPublic", isPublic),
	}
	r.logger.Debug("Updating story visibility", logFields...)

	commandTag, err := r.db.Exec(ctx, query, isPublic, storyID, userID)
	if err != nil {
		r.logger.Error("Failed to update story visibility", append(logFields, zap.Error(err))...)
		// Можно добавить проверку на конкретные ошибки БД, если нужно
		return fmt.Errorf("ошибка обновления видимости истории %s: %w", storyID, err)
	}

	if commandTag.RowsAffected() == 0 {
		// Важно: Сначала проверяем, существует ли вообще история с таким ID,
		// чтобы отличить 'не найдено' от 'не принадлежит пользователю'.
		existsQuery := `SELECT EXISTS(SELECT 1 FROM published_stories WHERE id = $1)`
		var exists bool
		if existsErr := r.db.QueryRow(ctx, existsQuery, storyID).Scan(&exists); existsErr != nil {
			r.logger.Error("Failed to check story existence after visibility update failed", append(logFields, zap.Error(existsErr))...)
			// Возвращаем исходную ошибку 'не найдено', т.к. не смогли проверить причину
			return models.ErrNotFound
		}
		if !exists {
			r.logger.Warn("Attempted to update visibility for non-existent story", logFields...)
			return models.ErrNotFound
		} else {
			// История существует, но не принадлежит пользователю
			r.logger.Warn("Attempted to update visibility for story not owned by user", logFields...)
			// Возвращаем Forbidden, т.к. пользователь не автор
			return models.ErrForbidden
		}
	}

	r.logger.Info("Story visibility updated successfully", logFields...)
	return nil
}

// ListByIDs retrieves a list of published stories based on their IDs.
func (r *pgPublishedStoryRepository) ListByIDs(ctx context.Context, ids []uuid.UUID) ([]*models.PublishedStory, error) {
	if len(ids) == 0 {
		return []*models.PublishedStory{}, nil // Return empty slice if no IDs provided
	}

	query := `
        SELECT
            id, user_id, config, setup, status, is_public, is_adult_content,
            title, description, error_details, likes_count, created_at, updated_at
        FROM published_stories
        WHERE id = ANY($1::uuid[]) -- Use PostgreSQL array operator
    `
	logFields := []zap.Field{zap.Int("id_count", len(ids))}
	r.logger.Debug("Listing published stories by IDs", logFields...)

	rows, err := r.db.Query(ctx, query, ids)
	if err != nil {
		r.logger.Error("Failed to query published stories by IDs", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения опубликованных историй по IDs: %w", err)
	}
	defer rows.Close()

	stories, err := scanStories(rows) // Используем scanStories
	if err != nil {
		// Ошибка уже залогирована в scanStories
		return nil, err
	}

	// Note: The order might not match the input `ids` slice order depending on DB execution.
	// If order is critical, you might need to reorder the results in the application code.
	r.logger.Debug("Published stories listed successfully by IDs", append(logFields, zap.Int("found_count", len(stories)))...)
	return stories, nil
}
