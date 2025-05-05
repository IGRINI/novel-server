package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/utils"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
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
            (id, user_id, config, setup, status, is_public, is_adult_content, title, description, language, created_at, updated_at)
        VALUES
            ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
    `
	logFields := []zap.Field{
		zap.String("publishedStoryID", story.ID.String()),
		zap.String("userID", story.UserID.String()),
		zap.String("language", story.Language),
	}
	r.logger.Debug("Creating published story", logFields...)

	_, err := r.db.Exec(ctx, query,
		story.ID, story.UserID, story.Config, story.Setup, story.Status,
		story.IsPublic, story.IsAdultContent, story.Title, story.Description,
		story.Language,
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
            title, description, error_details, likes_count, created_at, updated_at,
			is_first_scene_pending, are_images_pending
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
		&story.IsFirstScenePending, &story.AreImagesPending, // Добавляем сканирование новых флагов
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

// UpdateVisibility updates the visibility of a story.
func (r *pgPublishedStoryRepository) UpdateVisibility(ctx context.Context, storyID, userID uuid.UUID, isPublic bool, requiredStatus sharedModels.StoryStatus) error {
	query := `
		UPDATE published_stories
		SET is_public = $1, updated_at = NOW()
		WHERE id = $2 AND user_id = $3 AND status = $4
	`
	logFields := []zap.Field{
		zap.String("publishedStoryID", storyID.String()),
		zap.String("userID", userID.String()),
		zap.Bool("isPublic", isPublic),
		zap.String("requiredStatus", string(requiredStatus)),
	}
	r.logger.Debug("Updating story visibility with status check", logFields...)

	commandTag, err := r.db.Exec(ctx, query, isPublic, storyID, userID, requiredStatus)
	if err != nil {
		r.logger.Error("Failed to execute visibility update query", append(logFields, zap.Error(err))...)
		// Можно добавить проверку на конкретные ошибки БД, если нужно
		return fmt.Errorf("ошибка обновления видимости истории %s: %w", storyID, err)
	}

	if commandTag.RowsAffected() == 0 {
		// Update failed, determine the reason: Not Found, Forbidden (wrong owner), or Status Mismatch
		checkQuery := `SELECT user_id, status FROM published_stories WHERE id = $1`
		var ownerID uuid.UUID
		var currentStatus sharedModels.StoryStatus
		checkErr := r.db.QueryRow(ctx, checkQuery, storyID).Scan(&ownerID, &currentStatus)

		if checkErr != nil {
			if errors.Is(checkErr, pgx.ErrNoRows) {
				r.logger.Warn("Attempted to update visibility for non-existent story", logFields...)
				return models.ErrNotFound
			}
			// Unexpected error during check
			r.logger.Error("Failed to check story details after visibility update failed", append(logFields, zap.Error(checkErr))...)
			return fmt.Errorf("ошибка проверки истории после неудачного обновления видимости: %w", checkErr)
		}

		// Story exists, check owner and status
		if ownerID != userID {
			r.logger.Warn("Attempted to update visibility for story not owned by user", logFields...)
			return models.ErrForbidden
		}
		if currentStatus != requiredStatus {
			r.logger.Warn("Attempted to update visibility for story with incorrect status", append(logFields, zap.String("currentStatus", string(currentStatus)))...)
			return models.ErrStoryNotReadyForPublishing // Use the shared error
		}

		// If we reach here, something else prevented the update, which is unexpected
		r.logger.Error("Visibility update failed for unknown reason despite passing checks", logFields...)
		return fmt.Errorf("неизвестная ошибка при обновлении видимости истории %s", storyID)
	}

	r.logger.Info("Story visibility updated successfully", logFields...)
	return nil
}

// UpdateConfigAndSetupAndStatus updates config, setup and status for a published story.
func (r *pgPublishedStoryRepository) UpdateConfigAndSetupAndStatus(ctx context.Context, id uuid.UUID, config, setup json.RawMessage, status models.StoryStatus) error {
	query := `
        UPDATE published_stories
        SET config = $1, setup = $2, status = $3::story_status, updated_at = NOW()
        WHERE id = $4
    `
	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.String("newStatus", string(status)),
	}
	r.logger.Debug("Updating published story config/setup/status", logFields...)

	commandTag, err := r.db.Exec(ctx, query, config, setup, status, id)
	if err != nil {
		r.logger.Error("Failed to update published story config/setup/status", append(logFields, zap.Error(err))...)
		return fmt.Errorf("failed to update config/setup/status for story %s: %w", id, err)
	}
	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("Attempted to update config/setup/status for non-existent published story", logFields...)
		return models.ErrNotFound
	}
	r.logger.Info("Published story config/setup/status updated successfully", logFields...)
	return nil
}

// CountActiveGenerationsForUser counts the number of published stories with statuses
// indicating active generation for a specific user.
func (r *pgPublishedStoryRepository) CountActiveGenerationsForUser(ctx context.Context, userID uuid.UUID) (int, error) {
	activeStatuses := []string{
		string(models.StatusSetupPending),
		string(models.StatusFirstScenePending),
		string(models.StatusGenerating),
	}

	query := `SELECT COUNT(*) FROM published_stories WHERE user_id = $1 AND status = ANY($2::story_status[])`

	var count int
	logFields := []zap.Field{zap.String("userID", userID.String()), zap.Strings("activeStatuses", activeStatuses)}
	r.logger.Debug("Counting active generations for user in published_stories", logFields...)

	err := r.db.QueryRow(ctx, query, userID, pq.Array(activeStatuses)).Scan(&count)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Not an error, just means count is 0
			r.logger.Debug("No active generations found for user", logFields...)
			return 0, nil
		}
		r.logger.Error("Failed to count active generations in published_stories", append(logFields, zap.Error(err))...)
		return 0, fmt.Errorf("ошибка подсчета активных генераций для user %s: %w", userID.String(), err)
	}

	r.logger.Debug("Active generations count retrieved from published_stories", append(logFields, zap.Int("count", count))...)
	return count, nil
}

// ListLikedByUser получает пагинированный список историй, лайкнутых пользователем, используя курсор.
// Возвращает структуру PublishedStorySummaryWithProgress.
func (r *pgPublishedStoryRepository) ListLikedByUser(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]models.PublishedStorySummaryWithProgress, string, error) {
	if limit <= 0 {
		limit = 10 // Default limit
	}
	fetchLimit := limit + 1

	cursorTime, cursorID, err := utils.DecodeCursor(cursor) // Курсор основан на user_story_likes.created_at и published_story_id
	if err != nil {
		// Не возвращаем ошибку, если курсор просто пустой
		if cursor != "" {
			r.logger.Warn("Invalid cursor provided for ListLikedByUser", zap.String("cursor", cursor), zap.Stringer("userID", userID), zap.Error(err))
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		err = nil // Сбрасываем ошибку
		cursorTime = time.Time{}
		cursorID = uuid.Nil
	}

	// SQL Запрос: Выбираем поля для PublishedStorySummaryWithProgress
	query := `
        SELECT
            ps.id,                  -- PublishedStorySummary.ID
            ps.title,               -- PublishedStorySummary.Title
            ps.description,         -- PublishedStorySummary.ShortDescription (use NullString)
            ps.user_id,             -- PublishedStorySummary.AuthorID
            COALESCE(u.display_name, '[unknown]') AS author_name, -- PublishedStorySummary.AuthorName
            ps.created_at,          -- PublishedStorySummary.PublishedAt
            ps.is_adult_content,    -- PublishedStorySummary.IsAdultContent
            ps.likes_count,         -- PublishedStorySummary.LikesCount
            ps.status,              -- PublishedStorySummary.Status
            ir.image_url,           -- PublishedStorySummary.CoverImageURL (use NullString)
            TRUE AS is_liked,       -- PublishedStorySummary.IsLiked (always true here)
            (pp.user_id IS NOT NULL) AS has_player_progress, -- PublishedStorySummaryWithProgress.HasPlayerProgress
            ps.is_public,           -- PublishedStorySummaryWithProgress.IsPublic
            l.created_at AS like_created_at -- For cursor
        FROM published_stories ps
        JOIN story_likes l ON ps.id = l.published_story_id
        LEFT JOIN users u ON ps.user_id = u.id
        LEFT JOIN player_progress pp ON ps.id = pp.published_story_id AND pp.user_id = $1 -- Progress check for the requesting user
        LEFT JOIN image_references ir ON ir.reference = 'history_preview_' || ps.id::text
        WHERE l.user_id = $1 -- Filter by the user who liked the stories
    `

	args := []interface{}{userID}
	paramIndex := 2

	if !cursorTime.IsZero() && cursorID != uuid.Nil {
		// Фильтруем по времени лайка и ID истории
		query += fmt.Sprintf("AND (l.created_at, ps.id) < ($%d, $%d) ", paramIndex, paramIndex+1)
		args = append(args, cursorTime, cursorID)
		paramIndex += 2
	}

	// Упорядочиваем по времени лайка (сначала новые), затем по ID истории
	query += fmt.Sprintf("ORDER BY l.created_at DESC, ps.id DESC LIMIT $%d", paramIndex)
	args = append(args, fetchLimit)

	logFields := []zap.Field{
		zap.Stringer("userID", userID),
		zap.String("cursor", cursor),
		zap.Time("cursorTime", cursorTime),
		zap.Stringer("cursorID", cursorID),
		zap.Int("limit", limit),
		zap.Int("fetchLimit", fetchLimit),
	}
	r.logger.Debug("Listing liked stories by user with cursor", logFields...)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to query liked stories", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка получения списка лайкнутых историй для user %s: %w", userID, err)
	}
	defer rows.Close()

	// <<< ИЗМЕНЕНО: Ручное сканирование с NullString для description и cover_image_url >>>
	summaries := make([]models.PublishedStorySummaryWithProgress, 0, fetchLimit)
	likeTimes := make([]time.Time, 0, fetchLimit) // Храним время лайка отдельно

	for rows.Next() {
		var summary models.PublishedStorySummaryWithProgress
		// Временные переменные
		var description sql.NullString
		var authorName sql.NullString
		var publishedAt time.Time
		var coverImageUrl sql.NullString
		var isLiked bool
		var hasProgress bool
		var isPublic bool
		var likeCreatedAt time.Time

		err = rows.Scan( // Передаем 14 переменных
			&summary.ID,             // 1
			&summary.Title,          // 2
			&description,            // 3 -> NullString
			&summary.AuthorID,       // 4
			&authorName,             // 5 -> NullString
			&publishedAt,            // 6 -> time.Time (для PublishedAt)
			&summary.IsAdultContent, // 7
			&summary.LikesCount,     // 8
			&summary.Status,         // 9
			&coverImageUrl,          // 10 -> NullString
			&isLiked,                // 11 -> bool
			&hasProgress,            // 12 -> bool
			&isPublic,               // 13 -> bool
			&likeCreatedAt,          // 14 -> time.Time (для курсора)
		)
		if err != nil {
			r.logger.Error("Failed to scan liked story row", append(logFields, zap.Error(err))...)
			// Не прерываем весь процесс, просто пропускаем эту строку
			continue
		}
		// Присваивания из временных переменных
		if description.Valid {
			summary.ShortDescription = description.String
		}
		if authorName.Valid {
			summary.AuthorName = authorName.String
		} else {
			summary.AuthorName = "[unknown]"
		}
		summary.PublishedAt = publishedAt
		if coverImageUrl.Valid {
			urlStr := coverImageUrl.String
			summary.CoverImageURL = &urlStr
		} else {
			summary.CoverImageURL = nil
		}
		summary.IsLiked = isLiked
		summary.HasPlayerProgress = hasProgress
		summary.IsPublic = isPublic

		summaries = append(summaries, summary)
		likeTimes = append(likeTimes, likeCreatedAt)
	}
	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating liked story rows", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка итерации по результатам лайкнутых историй: %w", err)
	}
	// <<< КОНЕЦ ИЗМЕНЕНИЙ СКАНИРОВАНИЯ >>>

	var nextCursor string
	if len(summaries) == fetchLimit {
		lastSummary := summaries[limit]  // Нужен ID последнего элемента
		lastLikeTime := likeTimes[limit] // Используем время лайка последнего элемента
		nextCursor = utils.EncodeCursor(lastLikeTime, lastSummary.ID)
		summaries = summaries[:limit] // Возвращаем запрошенное количество
	} else {
		nextCursor = ""
	}

	// Логируем автор нейм перед возвратом
	for _, s := range summaries {
		r.logger.Debug("Returning liked story summary", append(logFields, zap.String("storyID", s.ID.String()), zap.String("authorName", s.AuthorName))...)
	}

	r.logger.Debug("Liked stories listed successfully", append(logFields, zap.Int("count", len(summaries)), zap.Bool("hasNext", nextCursor != ""))...)
	return summaries, nextCursor, nil
}

// MarkStoryAsLiked отмечает историю как лайкнутую пользователем.
// Выполняется в транзакции: добавляет запись в user_story_likes и инкрементирует счетчик.
func (r *pgPublishedStoryRepository) MarkStoryAsLiked(ctx context.Context, storyID uuid.UUID, userID uuid.UUID) error {
	logFields := []zap.Field{zap.String("storyID", storyID.String()), zap.String("userID", userID.String())}
	r.logger.Debug("Marking story as liked", logFields...)

	// <<< ИЗМЕНЕНО: Приведение типа r.db к *pgxpool.Pool для вызова Begin >>>
	pool, ok := r.db.(*pgxpool.Pool)
	if !ok {
		// Если r.db не *pgxpool.Pool, мы не можем начать транзакцию стандартным способом.
		// Возможно, нужно использовать другой подход или изменить тип r.db.
		// Пока возвращаем ошибку.
		r.logger.Error("r.db is not *pgxpool.Pool, cannot begin transaction for like", logFields...)
		return fmt.Errorf("внутренняя ошибка: невозможно начать транзакцию (неверный тип DBTX)")
	}
	tx, err := pool.Begin(ctx) // <<< Используем pool.Begin >>>
	if err != nil {
		r.logger.Error("Failed to begin transaction for marking like", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка начала транзакции для лайка: %w", err)
	}
	defer tx.Rollback(ctx) // Откат по умолчанию

	// 1. Вставить запись в user_story_likes
	// ON CONFLICT DO NOTHING - если лайк уже есть, ничего не делаем, но и ошибку не возвращаем
	insertLikeQuery := `INSERT INTO story_likes (user_id, published_story_id, created_at) VALUES ($1, $2, NOW()) ON CONFLICT (user_id, published_story_id) DO NOTHING`
	result, err := tx.Exec(ctx, insertLikeQuery, userID, storyID)
	if err != nil {
		r.logger.Error("Failed to insert into story_likes", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка добавления лайка в story_likes: %w", err)
	}

	// 2. Если запись была успешно вставлена (RowsAffected > 0), инкрементировать счетчик
	if result.RowsAffected() > 0 {
		incrementQuery := `UPDATE published_stories SET likes_count = likes_count + 1, updated_at = NOW() WHERE id = $1`
		incrementResult, err := tx.Exec(ctx, incrementQuery, storyID)
		if err != nil {
			r.logger.Error("Failed to increment likes count after inserting like", append(logFields, zap.Error(err))...)
			return fmt.Errorf("ошибка инкремента счетчика лайков: %w", err)
		}
		if incrementResult.RowsAffected() == 0 {
			// Это странная ситуация: лайк добавили, а историю не нашли для инкремента?
			r.logger.Error("Story not found for incrementing likes count after inserting like record", logFields...)
			// Возвращаем ошибку, т.к. данные могут быть несогласованными
			return models.ErrNotFound // Или более специфичная ошибка несогласованности
		}
		r.logger.Debug("Likes count incremented", logFields...)
	} else {
		r.logger.Debug("Like record already existed, likes count not incremented", logFields...)
	}

	if err := tx.Commit(ctx); err != nil {
		r.logger.Error("Failed to commit transaction for marking like", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка коммита транзакции для лайка: %w", err)
	}

	r.logger.Info("Story marked as liked successfully (or already liked)", logFields...)
	return nil
}

// MarkStoryAsUnliked отмечает историю как не лайкнутую пользователем.
// Выполняется в транзакции: удаляет запись из user_story_likes и декрементирует счетчик.
func (r *pgPublishedStoryRepository) MarkStoryAsUnliked(ctx context.Context, storyID uuid.UUID, userID uuid.UUID) error {
	logFields := []zap.Field{zap.String("storyID", storyID.String()), zap.String("userID", userID.String())}
	r.logger.Debug("Marking story as unliked", logFields...)

	// <<< ИЗМЕНЕНО: Приведение типа r.db к *pgxpool.Pool для вызова Begin >>>
	pool, ok := r.db.(*pgxpool.Pool)
	if !ok {
		r.logger.Error("r.db is not *pgxpool.Pool, cannot begin transaction for unlike", logFields...)
		return fmt.Errorf("внутренняя ошибка: невозможно начать транзакцию (неверный тип DBTX)")
	}
	tx, err := pool.Begin(ctx) // <<< Используем pool.Begin >>>
	if err != nil {
		r.logger.Error("Failed to begin transaction for unmarking like", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка начала транзакции для снятия лайка: %w", err)
	}
	defer tx.Rollback(ctx) // Откат по умолчанию

	// 1. Удалить запись из user_story_likes
	deleteLikeQuery := `DELETE FROM story_likes WHERE user_id = $1 AND published_story_id = $2`
	result, err := tx.Exec(ctx, deleteLikeQuery, userID, storyID)
	if err != nil {
		r.logger.Error("Failed to delete from story_likes", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка удаления лайка из story_likes: %w", err)
	}

	// 2. Если запись была успешно удалена (RowsAffected > 0), декрементировать счетчик
	if result.RowsAffected() > 0 {
		// Используем уже существующий DecrementLikesCount, но передаем ему транзакцию tx
		// Для этого нужен способ передать tx в DecrementLikesCount или продублировать логику.
		// Проще продублировать логику декремента здесь.
		decrementQuery := `UPDATE published_stories SET likes_count = GREATEST(0, likes_count - 1), updated_at = NOW() WHERE id = $1`
		decrementResult, err := tx.Exec(ctx, decrementQuery, storyID)
		if err != nil {
			r.logger.Error("Failed to decrement likes count after deleting like", append(logFields, zap.Error(err))...)
			return fmt.Errorf("ошибка декремента счетчика лайков: %w", err)
		}
		if decrementResult.RowsAffected() == 0 {
			// Это тоже странно: лайк удалили, а историю не нашли для декремента?
			r.logger.Error("Story not found for decrementing likes count after deleting like record", logFields...)
			return models.ErrNotFound // Ошибка несогласованности
		}
		r.logger.Debug("Likes count decremented", logFields...)
	} else {
		r.logger.Debug("Like record did not exist, likes count not decremented", logFields...)
	}

	if err := tx.Commit(ctx); err != nil {
		r.logger.Error("Failed to commit transaction for unmarking like", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка коммита транзакции для снятия лайка: %w", err)
	}

	r.logger.Info("Story marked as unliked successfully (or was not liked)", logFields...)
	return nil
}

// Ensure pgPublishedStoryRepository implements PublishedStoryRepository
var _ interfaces.PublishedStoryRepository = (*pgPublishedStoryRepository)(nil)

// Delete удаляет опубликованную историю и все связанные с ней данные.
func (r *pgPublishedStoryRepository) Delete(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.String("userID", userID.String()),
	}
	r.logger.Info("Attempting to delete published story and related data", logFields...)

	// Используем транзакцию для атомарности
	pool, ok := r.db.(*pgxpool.Pool)
	if !ok {
		r.logger.Error("r.db is not *pgxpool.Pool, cannot begin transaction for delete", logFields...)
		return fmt.Errorf("внутренняя ошибка: невозможно начать транзакцию (неверный тип DBTX)")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		r.logger.Error("Failed to begin transaction for deleting story", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка начала транзакции для удаления истории: %w", err)
	}
	defer tx.Rollback(ctx) // Откат по умолчанию

	// 1. Проверка владения и существования истории
	var ownerID uuid.UUID
	checkQuery := `SELECT user_id FROM published_stories WHERE id = $1`
	err = tx.QueryRow(ctx, checkQuery, id).Scan(&ownerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Warn("Published story not found for deletion", logFields...)
			return models.ErrNotFound // История не найдена
		}
		r.logger.Error("Failed to check story ownership before deletion", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка проверки владения историей %s: %w", id, err)
	}
	if ownerID != userID {
		r.logger.Warn("User does not own the story attempted for deletion", logFields...)
		return models.ErrForbidden // Пользователь не владелец
	}

	// 2. Удаление связанных данных (можно объединить в CTE или выполнять последовательно)
	// Порядок: лайки -> прогресс -> сцены -> сама история (из-за внешних ключей)

	// 2a. Удаление лайков
	deleteLikesQuery := `DELETE FROM story_likes WHERE published_story_id = $1`
	_, err = tx.Exec(ctx, deleteLikesQuery, id)
	if err != nil {
		r.logger.Error("Failed to delete story_likes during story deletion", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка удаления лайков для истории %s: %w", id, err)
	}
	r.logger.Debug("Deleted related likes", logFields...)

	// 2b. Удаление прогресса игроков
	deleteProgressQuery := `DELETE FROM player_progress WHERE published_story_id = $1`
	_, err = tx.Exec(ctx, deleteProgressQuery, id)
	if err != nil {
		r.logger.Error("Failed to delete player_progress during story deletion", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка удаления прогресса игроков для истории %s: %w", id, err)
	}
	r.logger.Debug("Deleted related player progress", logFields...)

	// 2c. Удаление сцен
	deleteScenesQuery := `DELETE FROM story_scenes WHERE published_story_id = $1`
	_, err = tx.Exec(ctx, deleteScenesQuery, id)
	if err != nil {
		r.logger.Error("Failed to delete story_scenes during story deletion", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка удаления сцен для истории %s: %w", id, err)
	}
	r.logger.Debug("Deleted related scenes", logFields...)

	// 3. Удаление самой истории
	deleteStoryQuery := `DELETE FROM published_stories WHERE id = $1` // Условие `AND user_id = $2` уже не нужно, проверили выше
	commandTag, err := tx.Exec(ctx, deleteStoryQuery, id)
	if err != nil {
		r.logger.Error("Failed to delete published_stories record", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка удаления основной записи истории %s: %w", id, err)
	}
	if commandTag.RowsAffected() == 0 {
		// Это не должно произойти, т.к. мы проверили существование ранее
		r.logger.Error("Published story disappeared during deletion transaction", logFields...)
		return models.ErrNotFound // Или другая ошибка несогласованности
	}
	r.logger.Debug("Deleted published_stories record", logFields...)

	// Коммит транзакции
	if err := tx.Commit(ctx); err != nil {
		r.logger.Error("Failed to commit transaction for deleting story", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка коммита транзакции при удалении истории %s: %w", id, err)
	}

	r.logger.Info("Published story and related data deleted successfully", logFields...)
	return nil
}

// FindWithProgressByUserID retrieves a paginated list of stories with progress for a specific user using cursor pagination.
func (r *pgPublishedStoryRepository) FindWithProgressByUserID(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]models.PublishedStorySummaryWithProgress, string, error) {
	r.logger.Debug("Finding stories with progress", zap.String("userID", userID.String()), zap.Int("limit", limit), zap.String("cursor", cursor))

	if limit <= 0 {
		limit = 20 // Default limit
	}
	fetchLimit := limit + 1

	cursorTime, cursorID, err := utils.DecodeCursor(cursor) // Используем DecodeCursor
	if err != nil {
		r.logger.Warn("Invalid cursor provided for FindWithProgressByUserID", zap.String("cursor", cursor), zap.Error(err))
		return nil, "", fmt.Errorf("invalid cursor: %w", err)
	}

	// Query to join published_stories and player_progress, filter by user_id, handle cursor pagination, and check likes.
	query := `
        SELECT
            ps.id,
            ps.title,
            ps.description,
            ps.user_id,
            ps.created_at,
            ps.is_adult_content,
            ps.likes_count,
            pp.updated_at AS progress_updated_at,
            (sl.user_id IS NOT NULL) AS is_liked -- Check if a like exists for the current user
        FROM published_stories ps
        JOIN player_progress pp ON ps.id = pp.published_story_id
        LEFT JOIN story_likes sl ON ps.id = sl.published_story_id AND sl.user_id = $1 -- LEFT JOIN to check like for the specific user
        WHERE pp.user_id = $1
    `
	args := []interface{}{userID}
	paramIndex := 2

	if !cursorTime.IsZero() && cursorID != uuid.Nil {
		// Use composite key (progress_updated_at, story_id) for cursor pagination
		query += fmt.Sprintf(" AND (pp.updated_at, ps.id) < ($%d, $%d) ", paramIndex, paramIndex+1)
		args = append(args, cursorTime, cursorID)
		paramIndex += 2
	}

	// Order by progress timestamp first, then by story ID for stability
	query += fmt.Sprintf(" ORDER BY pp.updated_at DESC, ps.id DESC LIMIT $%d", paramIndex)
	args = append(args, fetchLimit)

	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("cursor", cursor),
		zap.Time("cursorTime", cursorTime),
		zap.Stringer("cursorID", cursorID),
		zap.Int("limit", limit),
		zap.Int("fetchLimit", fetchLimit),
	}
	r.logger.Debug("Executing query for stories with progress (with like check)", append(logFields, zap.String("query", query))...)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Error querying stories with progress (with like check)", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("database query failed: %w", err)
	}
	defer rows.Close()

	stories := make([]models.PublishedStorySummaryWithProgress, 0, limit)
	var lastProgressUpdate time.Time
	var lastStoryID uuid.UUID // Needed for the cursor

	for rows.Next() {
		var summary models.PublishedStorySummaryWithProgress
		var progressUpdatedAt time.Time
		var isLiked bool // Variable to scan the like status

		if err := rows.Scan(
			&summary.ID,
			&summary.Title,
			&summary.PublishedStorySummary.ShortDescription,
			&summary.AuthorID,
			&summary.PublishedAt,
			&summary.IsAdultContent,
			&summary.LikesCount,
			&progressUpdatedAt, // Scan pp.updated_at
			&isLiked,           // Scan the calculated is_liked
		); err != nil {
			r.logger.Error("Error scanning story with progress row (with like check)", append(logFields, zap.Error(err))...)
			continue // Skip problematic row
		}
		summary.HasPlayerProgress = true
		summary.IsLiked = isLiked // Assign the scanned like status
		summary.AuthorName = ""   // Placeholder - to be filled by service layer

		stories = append(stories, summary)
		lastProgressUpdate = progressUpdatedAt
		lastStoryID = summary.ID // Keep track of the last ID for the cursor
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating story with progress rows (with like check)", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("database iteration failed: %w", err)
	}

	var nextCursor string
	if len(stories) == fetchLimit {
		stories = stories[:fetchLimit-1] // Correctly remove the extra item
		// Create composite cursor using the last item's progress update time and ID
		nextCursor = utils.EncodeCursor(lastProgressUpdate, lastStoryID) // Use EncodeCursor
	}

	r.logger.Debug("Found stories with progress (with like check)", append(logFields, zap.Int("count", len(stories)), zap.Bool("hasNext", nextCursor != ""))...)
	return stories, nextCursor, nil
}

// CheckLike checks if a user has liked a story.
func (r *pgPublishedStoryRepository) CheckLike(ctx context.Context, userID, storyID uuid.UUID) (bool, error) {
	r.logger.Debug("Checking like status", zap.String("userID", userID.String()), zap.String("storyID", storyID.String()))
	// Assuming the table name is 'story_likes' and columns are 'user_id' and 'published_story_id'
	query := `SELECT EXISTS (SELECT 1 FROM story_likes WHERE user_id = $1 AND published_story_id = $2)`
	var exists bool
	err := r.db.QueryRow(ctx, query, userID, storyID).Scan(&exists)
	if err != nil {
		r.logger.Error("Error checking like status", zap.String("userID", userID.String()), zap.String("storyID", storyID.String()), zap.Error(err))
		// Do not return ErrNotFound, return the actual DB error
		return false, fmt.Errorf("database error checking like: %w", err)
	}
	r.logger.Debug("Like status checked", zap.String("userID", userID.String()), zap.String("storyID", storyID.String()), zap.Bool("liked", exists))
	return exists, nil
}

// CountByStatus подсчитывает количество опубликованных историй по заданному статусу.
func (r *pgPublishedStoryRepository) CountByStatus(ctx context.Context, status models.StoryStatus) (int, error) {
	query := `SELECT COUNT(*) FROM published_stories WHERE status = $1`
	log := r.logger.With(zap.String("status", string(status)))

	var count int
	err := r.db.QueryRow(ctx, query, status).Scan(&count)
	if err != nil {
		log.Error("Failed to count published stories by status", zap.Error(err))
		// Не возвращаем ErrNotFound, т.к. COUNT(*) всегда вернет строку (даже если 0)
		return 0, fmt.Errorf("failed to query story count by status: %w", err)
	}

	log.Debug("Successfully counted published stories by status", zap.Int("count", count))
	return count, nil
}

const findAndMarkStaleGeneratingQueryBase = `
UPDATE published_stories
SET status = $1, -- StatusError
    error_details = $2, -- Сообщение об ошибке
    updated_at = NOW()
WHERE status = ANY($3::story_status[]) -- Массив зависших статусов
`

// FindAndMarkStaleGeneratingAsError находит "зависшие" истории в процессе генерации и устанавливает им статус Error.
func (r *pgPublishedStoryRepository) FindAndMarkStaleGeneratingAsError(ctx context.Context, staleThreshold time.Duration) (int64, error) {
	// Статусы, которые считаются "зависшими"
	// Используем строки, так как StoryStatus - это string alias
	staleStatuses := []string{
		string(models.StatusSetupPending),      // Используем константу, где она есть
		string(models.StatusFirstScenePending), // <<< ВОЗВРАЩАЕМ ПРОВЕРКУ ЭТОГО СТАТУСА
		string(models.StatusInitialGeneration), // Используем константу, где она есть
		"image_generation_pending",             // <<< Используем строковый литерал, т.к. константы нет
	}

	logFields := []zap.Field{
		zap.Duration("staleThreshold", staleThreshold),
		zap.Strings("staleStatuses", staleStatuses), // Логируем используемые статусы
	}
	r.logger.Info("Finding and marking stale generating published stories as Error", logFields...)

	// Базовый запрос
	// Используем pq.Array для передачи среза строк в ANY($3)
	query := `UPDATE published_stories SET status = $1, error_details = $2, updated_at = NOW() WHERE status::text = ANY($3)`
	args := []interface{}{models.StatusError, "Generation timed out or failed (marked as stale)", pq.Array(staleStatuses)}

	// Добавляем условие по времени, если staleThreshold > 0
	if staleThreshold > 0 {
		query += " AND updated_at < $4"
		args = append(args, time.Now().UTC().Add(-staleThreshold))
	} else {
		// Если threshold == 0, проверяем ВСЕ записи с указанными статусами независимо от времени
		r.logger.Info("Stale threshold is zero, checking all specified stale statuses regardless of time.", logFields...)
	}

	commandTag, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		// Логируем ошибку
		r.logger.Error("Failed to execute update query for stale published stories", append(logFields, zap.Error(err), zap.String("query", query))...)
		return 0, fmt.Errorf("ошибка обновления статуса зависших опубликованных историй: %w", err)
	}

	updatedCount := commandTag.RowsAffected()
	r.logger.Info("FindAndMarkStaleGeneratingAsError completed", append(logFields, zap.Int64("updated_count", updatedCount))...)

	return updatedCount, nil
}

// CheckInitialGenerationStatus проверяет, готовы ли Setup и Первая сцена (проверяя статус).
func (r *pgPublishedStoryRepository) CheckInitialGenerationStatus(ctx context.Context, id uuid.UUID) (bool, error) {
	query := `SELECT status FROM published_stories WHERE id = $1`
	var status models.StoryStatus
	logFields := []zap.Field{zap.String("publishedStoryID", id.String())}
	r.logger.Debug("Checking initial generation status for published story", logFields...)

	err := r.db.QueryRow(ctx, query, id).Scan(&status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Warn("Published story not found for initial generation status check", logFields...)
			return false, models.ErrNotFound
		}
		r.logger.Error("Failed to query status for initial generation check", append(logFields, zap.Error(err))...)
		return false, fmt.Errorf("ошибка получения статуса истории %s: %w", id, err)
	}

	isReady := (status == models.StatusReady)
	r.logger.Debug("Initial generation status check complete", append(logFields, zap.Bool("isReady", isReady))...)
	return isReady, nil
}

// GetConfigAndSetup получает Config и Setup по ID истории.
func (r *pgPublishedStoryRepository) GetConfigAndSetup(ctx context.Context, id uuid.UUID) (json.RawMessage, json.RawMessage, error) {
	query := `SELECT config, setup FROM published_stories WHERE id = $1`
	var config, setup json.RawMessage
	logFields := []zap.Field{zap.String("publishedStoryID", id.String())}
	r.logger.Debug("Getting config and setup for published story", logFields...)

	err := r.db.QueryRow(ctx, query, id).Scan(&config, &setup)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Warn("Published story not found for config/setup retrieval", logFields...)
			return nil, nil, models.ErrNotFound
		}
		r.logger.Error("Failed to query config/setup", append(logFields, zap.Error(err))...)
		return nil, nil, fmt.Errorf("ошибка получения config/setup для истории %s: %w", id, err)
	}

	r.logger.Debug("Config and setup retrieved successfully", append(logFields, zap.Int("configSize", len(config)), zap.Int("setupSize", len(setup)))...)
	return config, setup, nil
}

// ListPublicSummaries получает список публичных историй с пагинацией.
func (r *pgPublishedStoryRepository) ListPublicSummaries(ctx context.Context, userID *uuid.UUID, cursor string, limit int, sortBy string, filterAdult bool) ([]models.PublishedStorySummary, string, error) {
	if limit <= 0 {
		limit = 10 // Default limit
	}
	fetchLimit := limit + 1

	// Определяем поле и направление сортировки
	orderByField := "created_at"
	orderByDir := "DESC"
	isLikesSort := false // <<< ДОБАВЛЕНО: Флаг для типа сортировки
	sortBy = strings.ToLower(sortBy)
	if sortBy == "likes" {
		orderByField = "likes_count"
		isLikesSort = true // <<< ДОБАВЛЕНО
	} else if sortBy == "newest" {
		orderByField = "created_at"
	} // Добавить другие варианты сортировки при необходимости

	// Декодируем курсор в зависимости от поля сортировки
	var cursorValue interface{}
	var cursorID uuid.UUID
	var err error
	if isLikesSort { // <<< ИЗМЕНЕНО: Проверка флага
		var likes int64
		likes, cursorID, err = utils.DecodeIntCursor(cursor) // <<< ИЗМЕНЕНО: Используем DecodeIntCursor
		cursorValue = likes
	} else { // По умолчанию сортировка по created_at
		var createdTime time.Time
		createdTime, cursorID, err = utils.DecodeCursor(cursor)
		cursorValue = createdTime
	}
	if err != nil {
		// Не возвращаем ошибку, если курсор просто пустой
		if cursor != "" {
			r.logger.Warn("Invalid cursor provided for ListPublicSummaries", zap.String("cursor", cursor), zap.String("sortBy", sortBy), zap.Error(err))
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		// Если курсор пустой, ошибки нет, продолжаем без курсора
		err = nil // Сбрасываем ошибку
		if isLikesSort {
			cursorValue = int64(0) // Устанавливаем значение по умолчанию для пустого курсора
		} else {
			cursorValue = time.Time{}
		}
		cursorID = uuid.Nil
	}

	// Строим запрос
	var queryBuilder strings.Builder
	args := []interface{}{}
	paramIndex := 1

	queryBuilder.WriteString(`
        SELECT DISTINCT ON (ps.id)
            ps.id,
            ps.title,
            ps.description, -- Используем полное описание, если short нет
            ps.user_id,     -- author_id
            COALESCE(u.display_name, '[unknown]') AS author_name, -- <<< ИЗМЕНЕНО: Получаем имя из users >>>
            ps.created_at,
            ps.is_adult_content,
            ps.likes_count,
            ps.status,
            ps.cover_image_url,
            (CASE WHEN $`)
	queryBuilder.WriteString(fmt.Sprintf("%d", paramIndex)) // Динамический индекс для userID в CASE
	args = append(args, userID)                             // Добавляем userID в аргументы ($1)
	paramIndex++
	queryBuilder.WriteString(`::uuid IS NOT NULL THEN EXISTS (
                SELECT 1 FROM story_likes sl WHERE sl.published_story_id = ps.id AND sl.user_id = $1 -- Используем $1 снова
            ) ELSE FALSE END) AS is_liked
        FROM published_stories ps
        LEFT JOIN users u ON ps.user_id = u.id -- <<< ДОБАВЛЕНО: JOIN с users >>>
        WHERE ps.is_public = TRUE AND ps.status = $`)
	queryBuilder.WriteString(fmt.Sprintf("%d", paramIndex)) // ($2)
	args = append(args, models.StatusReady)
	paramIndex++

	if filterAdult {
		queryBuilder.WriteString(" AND ps.is_adult_content = FALSE")
	}

	// Добавляем условие курсора, если он был предоставлен
	if cursor != "" {
		comparisonOperator := "<" // Для DESC сортировки
		// NOTE: Для ASC сортировки нужно будет поменять оператор
		queryBuilder.WriteString(fmt.Sprintf(" AND (ps.%s, ps.id) %s ($%d, $%d)", orderByField, comparisonOperator, paramIndex, paramIndex+1))
		args = append(args, cursorValue, cursorID)
		paramIndex += 2
	}

	// Добавляем сортировку и лимит
	// <<< ИЗМЕНЕНО: Сначала сортируем по ps.id для DISTINCT ON, затем по основному полю >>>
	queryBuilder.WriteString(fmt.Sprintf(" ORDER BY ps.id, ps.%s %s, ps.id %s LIMIT $%d", orderByField, orderByDir, orderByDir, paramIndex))
	args = append(args, fetchLimit)

	query := queryBuilder.String()
	logFields := []zap.Field{
		zap.Stringer("userID", userID),
		zap.String("cursor", cursor),
		zap.Int("limit", limit),
		zap.Bool("filterAdult", filterAdult),
		// zap.String("query", query), // Debug
	}
	r.logger.Debug("Listing public story summaries", logFields...)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to query public story summaries", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка получения списка публичных историй: %w", err)
	}
	defer rows.Close()

	summaries := make([]models.PublishedStorySummary, 0, limit)
	var lastCreatedAt time.Time // <<< ДОБАВЛЕНО: Переменная для created_at курсора
	var lastID uuid.UUID        // <<< ДОБАВЛЕНО: Переменная для ID курсора

	for rows.Next() {
		var s models.PublishedStorySummary
		var description sql.NullString
		var coverImageUrl sql.NullString
		var currentLastActivity time.Time

		if err := rows.Scan(
			&s.ID,
			&s.Title,
			&description,
			&s.AuthorID,
			&s.AuthorName,
			&currentLastActivity,
			&s.LikesCount,
			&s.Status,
			&coverImageUrl,
			&s.IsLiked,
		); err != nil {
			r.logger.Error("Failed to scan public story summary row", append(logFields, zap.Error(err))...)
			continue
		}

		if description.Valid {
			s.ShortDescription = description.String
		}
		if coverImageUrl.Valid {
			urlStr := coverImageUrl.String
			s.CoverImageURL = &urlStr
		} else {
			s.CoverImageURL = nil
		}

		summaries = append(summaries, s)
		lastCreatedAt = currentLastActivity
		lastID = s.ID
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating public story summary rows", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка итерации по результатам публичных историй: %w", err)
	}

	var nextCursor string
	if len(summaries) == fetchLimit {
		summaries = summaries[:limit]
		// Курсор теперь основан на времени создания истории (published_at) и ID истории
		nextCursor = utils.EncodeCursor(lastCreatedAt, lastID) // <<< ИЗМЕНЕНО: Используем правильные переменные
	}

	r.logger.Debug("Public story summaries listed successfully", append(logFields, zap.Int("count", len(summaries)), zap.Bool("hasNext", nextCursor != ""))...)
	return summaries, nextCursor, nil
}

// ListUserSummariesWithProgress получает список историй пользователя с прогрессом.
func (r *pgPublishedStoryRepository) ListUserSummariesWithProgress(ctx context.Context, userID uuid.UUID, cursor string, limit int, filterAdult bool) ([]models.PublishedStorySummaryWithProgress, string, error) {
	if limit <= 0 {
		limit = 10 // Default limit
	}
	fetchLimit := limit + 1

	// Сортируем по дате создания истории DESC
	orderByField := "created_at"
	orderByDir := "DESC"

	createdTime, cursorID, err := utils.DecodeCursor(cursor)
	if err != nil {
		if cursor != "" {
			r.logger.Warn("Invalid cursor provided for ListUserSummariesWithProgress", zap.String("cursor", cursor), zap.Stringer("userID", userID), zap.Error(err))
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		err = nil
		createdTime = time.Time{}
		cursorID = uuid.Nil
	}

	// Строим запрос
	var queryBuilder strings.Builder
	args := []interface{}{userID} // $1
	paramIndex := 2

	queryBuilder.WriteString(`
        SELECT DISTINCT ON (ps.id) -- <<< ДОБАВЛЕНО DISTINCT ON >>>
            ps.id,
            ps.title,
            ps.description,
            ps.user_id,     -- author_id
            COALESCE(u.display_name, '[unknown]') AS author_name, -- <<< ИЗМЕНЕНО: Получаем имя из users >>>
            ps.created_at,
            ps.is_adult_content,
            ps.likes_count,
            ps.status,
            ir.image_url AS cover_image_url, -- <<< ИЗМЕНЕНО: Получаем из image_references >>>
            ps.is_public, -- <<< ДОБАВЛЕНО: is_public >>>
            EXISTS (SELECT 1 FROM story_likes sl WHERE sl.published_story_id = ps.id AND sl.user_id = $1) AS is_liked,
            -- ИЗМЕНЕНО: Проверяем наличие game_state с player_progress_id
            EXISTS (SELECT 1 FROM player_game_states pgs WHERE pgs.published_story_id = ps.id AND pgs.player_id = $1 AND pgs.player_progress_id IS NOT NULL) AS has_player_progress
        FROM published_stories ps
        LEFT JOIN users u ON u.id = ps.user_id -- <<< ДОБАВЛЕНО: JOIN с users >>>
        LEFT JOIN image_references ir ON ir.reference = 'history_preview_' || ps.id::text -- <<< ИЗМЕНЕНО: JOIN с image_references >>>
        WHERE ps.user_id = $1
    `)

	if filterAdult {
		queryBuilder.WriteString(" AND ps.is_adult_content = FALSE")
	}

	// Добавляем условие курсора
	if cursor != "" {
		// Курсор основан на поле сортировки (created_at) и ID
		queryBuilder.WriteString(fmt.Sprintf(" AND (ps.%s, ps.id) < ($%d, $%d)", orderByField, paramIndex, paramIndex+1))
		args = append(args, createdTime, cursorID)
		paramIndex += 2
	}

	// Добавляем сортировку и лимит
	// <<< ИЗМЕНЕНО: Сначала сортируем по ps.id для DISTINCT ON, затем по основному полю >>>
	queryBuilder.WriteString(fmt.Sprintf(" ORDER BY ps.id, ps.%s %s, ps.id %s LIMIT $%d", orderByField, orderByDir, orderByDir, paramIndex))
	args = append(args, fetchLimit)

	query := queryBuilder.String()
	logFields := []zap.Field{
		zap.Stringer("userID", userID),
		zap.String("cursor", cursor),
		zap.Int("limit", limit),
		zap.Bool("filterAdult", filterAdult),
		// zap.String("query", query), // Debug
	}
	r.logger.Debug("Listing user story summaries with progress", logFields...)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to query user story summaries with progress", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка получения списка историй пользователя с прогрессом: %w", err)
	}
	defer rows.Close()

	summaries := make([]models.PublishedStorySummaryWithProgress, 0, limit)
	var lastCreatedAt time.Time // <<< ВОССТАНОВЛЕНО
	var lastIDCursor uuid.UUID  // <<< ВОССТАНОВЛЕНО

	for rows.Next() {
		var s models.PublishedStorySummaryWithProgress
		var description sql.NullString
		var coverImageUrl sql.NullString
		var authorName sql.NullString // <<< Используем NullString для автора
		var publishedAt time.Time     // <<< Переименовано для ясности
		var isLiked bool
		var hasProgress bool

		if err := rows.Scan(
			&s.ID,             // 1: ps.id
			&s.Title,          // 2: ps.title
			&description,      // 3: ps.description
			&s.AuthorID,       // 4: ps.user_id
			&authorName,       // 5: author_name
			&publishedAt,      // 6: ps.created_at
			&s.IsAdultContent, // 7: ps.is_adult_content <<< ДОБАВЛЕНО
			&s.LikesCount,     // 8: ps.likes_count
			&s.Status,         // 9: ps.status
			&coverImageUrl,    // 10: ir.image_url
			&s.IsPublic,       // 11: ps.is_public
			&isLiked,          // 12: calculated is_liked
			&hasProgress,      // 13: calculated has_player_progress
		); err != nil {
			r.logger.Error("Failed to scan user story summary with progress row", append(logFields, zap.Error(err))...)
			continue
		}

		// Assign scanned values
		if description.Valid {
			s.ShortDescription = description.String
		}
		if authorName.Valid {
			s.AuthorName = authorName.String
		} else {
			s.AuthorName = "[unknown]" // Default if COALESCE returns null (shouldn't happen with COALESCE)
		}
		s.PublishedAt = publishedAt // <<< Используем publishedAt
		if coverImageUrl.Valid {
			urlStr := coverImageUrl.String
			s.CoverImageURL = &urlStr
		} else {
			s.CoverImageURL = nil
		}
		s.IsLiked = isLiked
		s.HasPlayerProgress = hasProgress

		summaries = append(summaries, s)
		lastCreatedAt = publishedAt // <<< Используем publishedAt для курсора
		lastIDCursor = s.ID
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating user story summary with progress rows", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка итерации по результатам историй пользователя с прогрессом: %w", err)
	}

	var nextCursor string
	if len(summaries) == fetchLimit {
		summaries = summaries[:limit]
		nextCursor = utils.EncodeCursor(lastCreatedAt, lastIDCursor)
	}

	r.logger.Debug("User story summaries with progress listed successfully", append(logFields, zap.Int("count", len(summaries)), zap.Bool("hasNext", nextCursor != ""))...)
	return summaries, nextCursor, nil
}

// GetSummaryWithDetails получает детали истории, имя автора, флаг лайка и прогресса для указанного пользователя.
func (r *pgPublishedStoryRepository) GetSummaryWithDetails(ctx context.Context, storyID, userID uuid.UUID) (*models.PublishedStoryDetailWithProgressAndLike, error) {
	query := `
        SELECT
            ps.id,
            ps.title,
            ps.description,
            ps.user_id,     -- author_id
            COALESCE(u.display_name, '[unknown]') AS author_name,
            ps.created_at,  -- published_at
            ps.is_adult_content,
            ps.likes_count,
            ps.status,
            ir.image_url AS cover_image_url,
            ps.is_public,
            EXISTS (SELECT 1 FROM story_likes sl WHERE sl.published_story_id = ps.id AND sl.user_id = $2) AS is_liked,
            EXISTS (SELECT 1 FROM player_game_states pgs WHERE pgs.published_story_id = ps.id AND pgs.player_id = $2 AND pgs.player_progress_id IS NOT NULL) AS has_player_progress -- Corrected progress check
        FROM published_stories ps
        LEFT JOIN users u ON u.id = ps.user_id
        LEFT JOIN image_references ir ON ir.reference = 'history_preview_' || ps.id::text
        WHERE ps.id = $1
    `
	result := &models.PublishedStoryDetailWithProgressAndLike{}
	var title, description sql.NullString
	var coverImageUrl sql.NullString
	var hasProgress bool // Variable to scan has_player_progress

	logFields := []zap.Field{zap.String("storyID", storyID.String()), zap.Stringer("userID", userID)}
	r.logger.Debug("Getting story summary with details", logFields...)

	err := r.db.QueryRow(ctx, query, storyID, userID).Scan(
		&result.ID,
		&title,
		&description,
		&result.AuthorID,
		&result.AuthorName,
		&result.PublishedAt,
		&result.IsAdultContent,
		&result.LikesCount,
		&result.Status,
		&coverImageUrl,
		&result.IsPublic,
		&hasProgress, // Scan into the dedicated variable
	)

	r.logger.Debug("Scanned author name (details)", append(logFields, zap.String("scannedAuthorName", result.AuthorName))...) // <<< DEBUG LOG

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Warn("Story not found for GetSummaryWithDetails", logFields...)
			return nil, models.ErrNotFound
		}
		r.logger.Error("Failed to get story summary with details", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения деталей истории %s для пользователя %s: %w", storyID, userID, err)
	}

	if title.Valid {
		result.Title = title.String
	}
	if description.Valid {
		result.ShortDescription = description.String
	}
	if coverImageUrl.Valid {
		urlStr := coverImageUrl.String
		result.CoverImageURL = &urlStr
	} else {
		result.CoverImageURL = nil
	}
	result.HasPlayerProgress = hasProgress // Assign the scanned value

	r.logger.Debug("Story summary with details retrieved successfully", logFields...)
	return result, nil
}

// ListUserSummariesOnlyWithProgress получает список историй, в которых у пользователя есть прогресс,
// сортируя их по времени последней активности (сначала новые).
func (r *pgPublishedStoryRepository) ListUserSummariesOnlyWithProgress(ctx context.Context, userID uuid.UUID, cursor string, limit int, filterAdult bool) ([]models.PublishedStorySummaryWithProgress, string, error) {
	if limit <= 0 {
		limit = 10 // Default limit
	}
	fetchLimit := limit + 1

	// Сортируем по last_activity_at DESC, затем по ps.id DESC
	lastActivityTime, cursorID, err := utils.DecodeCursor(cursor) // <<< ИЗМЕНЕНО: Используем DecodeCursor
	if err != nil {
		if cursor != "" {
			r.logger.Warn("Invalid cursor provided for ListUserSummariesOnlyWithProgress", zap.String("cursor", cursor), zap.Stringer("userID", userID), zap.Error(err))
		}
		// Proceed without cursor if it's invalid or empty
		cursor = ""                    // Ensure cursor is empty if invalid
		lastActivityTime = time.Time{} // Zero time
		cursorID = uuid.Nil
	}

	logFields := []zap.Field{
		zap.Stringer("userID", userID),
		zap.String("cursor", cursor),
		zap.Int("limit", limit),
	}
	r.logger.Debug("Listing user story summaries ONLY with progress", logFields...)

	args := make([]interface{}, 0, 5) // Estimate initial capacity
	args = append(args, userID)       // $1 = userID (used in CTE, CASE, maybe WHERE)

	paramIndex := 2 // Start param indexing from $2

	var queryBuilder strings.Builder

	// <<< ИЗМЕНЕНО: Используем CTE для корректной сортировки и DISTINCT >>>
	queryBuilder.WriteString(`
    WITH LatestPlayerStates AS (
        SELECT DISTINCT ON (published_story_id)
            published_story_id,
            last_activity_at
        FROM player_game_states
        WHERE player_id = $1 -- User ID parameter
        ORDER BY published_story_id, last_activity_at DESC
    )
    SELECT
        ps.id,                  -- PublishedStorySummary.ID
        ps.title,               -- PublishedStorySummary.Title
        ps.description,         -- PublishedStorySummary.ShortDescription (use NullString)
        ps.user_id,             -- PublishedStorySummary.AuthorID
        COALESCE(u.display_name, '[unknown]') AS author_name, -- PublishedStorySummary.AuthorName
        ps.created_at,          -- PublishedStorySummary.PublishedAt
        ps.is_adult_content,    -- PublishedStorySummary.IsAdultContent
        ps.likes_count,         -- PublishedStorySummary.LikesCount
        ps.status,              -- PublishedStorySummary.Status
        ir.image_url AS cover_image_url, -- <<< ИЗМЕНЕНО: Получаем из image_references >>>
        ps.is_public,           -- PublishedStorySummaryWithProgress.IsPublic
        lps.last_activity_at,   -- For sorting and cursor (from CTE)
        (CASE WHEN $1::uuid IS NOT NULL THEN EXISTS (
            SELECT 1 FROM story_likes sl WHERE sl.published_story_id = ps.id AND sl.user_id = $1
        ) ELSE FALSE END) AS is_liked -- is_liked requires userID ($1)
    FROM
        published_stories ps
    JOIN
        LatestPlayerStates lps ON ps.id = lps.published_story_id -- Join with CTE
    LEFT JOIN
        users u ON ps.user_id = u.id -- Join to get author name
    LEFT JOIN -- <<< ДОБАВЛЕНО: JOIN для получения обложки >>>
        image_references ir ON ir.reference = 'history_preview_' || ps.id::text
    WHERE ps.status = $`)
	queryBuilder.WriteString(fmt.Sprintf("%d", paramIndex)) // Status param index ($2)
	args = append(args, models.StatusReady)
	paramIndex++

	if filterAdult {
		queryBuilder.WriteString(fmt.Sprintf(" AND ps.is_adult_content = $%d", paramIndex))
		args = append(args, false)
		paramIndex++
	}

	if cursor != "" {
		// Курсор теперь основан на времени последней активности (из CTE) и ID истории
		queryBuilder.WriteString(fmt.Sprintf(" AND (lps.last_activity_at, ps.id) < ($%d, $%d)", paramIndex, paramIndex+1))
		args = append(args, lastActivityTime, cursorID)
		paramIndex += 2
	}

	// Сортировка по last_activity_at из CTE
	queryBuilder.WriteString(fmt.Sprintf(" ORDER BY lps.last_activity_at DESC, ps.id DESC LIMIT $%d", paramIndex))
	args = append(args, fetchLimit)

	query := queryBuilder.String()

	r.logger.Debug("Executing SQL for ListUserSummariesOnlyWithProgress", append(logFields, zap.String("query", query), zap.Any("args", args))...)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to query user story summaries only with progress", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка получения списка историй пользователя с прогрессом: %w", err)
	}
	defer rows.Close()

	summaries := make([]models.PublishedStorySummaryWithProgress, 0, limit)
	var lastActivityCursor time.Time // Для сохранения последнего значения для курсора
	var lastIDCursor uuid.UUID       // Для сохранения последнего значения для курсора

	for rows.Next() {
		var s models.PublishedStorySummaryWithProgress
		var description sql.NullString
		var coverImageUrl sql.NullString
		var currentLastActivity time.Time // <<< Переменная для сканирования lps.last_activity_at

		if err := rows.Scan(
			&s.PublishedStorySummary.ID,
			&s.PublishedStorySummary.Title,
			&description,
			&s.PublishedStorySummary.AuthorID,
			&s.PublishedStorySummary.AuthorName,
			&s.PublishedStorySummary.PublishedAt,
			&s.PublishedStorySummary.IsAdultContent,
			&s.PublishedStorySummary.LikesCount,
			&s.PublishedStorySummary.Status,
			&coverImageUrl,
			&s.IsPublic,          // Сканируем IsPublic
			&currentLastActivity, // <<< Сканируем lps.last_activity_at
			&s.PublishedStorySummary.IsLiked,
		); err != nil {
			r.logger.Error("Failed to scan user story summary only with progress row", append(logFields, zap.Error(err))...)
			// Важно не прерывать весь процесс, если одна строка битая
			continue
		}

		// Обработка NullString
		s.PublishedStorySummary.ShortDescription = description.String
		if coverImageUrl.Valid {
			s.PublishedStorySummary.CoverImageURL = &coverImageUrl.String // <<< ИЗМЕНЕНО: Присваиваем указатель
		} else {
			s.PublishedStorySummary.CoverImageURL = nil
		}

		// Устанавливаем флаг прогресса (всегда true из-за INNER JOIN с LatestPlayerStates)
		s.HasPlayerProgress = true

		summaries = append(summaries, s)

		// Обновляем переменные для следующего курсора
		lastActivityCursor = currentLastActivity
		lastIDCursor = s.PublishedStorySummary.ID
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating over user story summaries only with progress rows", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка чтения строк историй пользователя с прогрессом: %w", err)
	}

	var nextCursor string
	if len(summaries) == fetchLimit {
		summaries = summaries[:limit] // Убираем лишний элемент
		// Генерируем курсор на основе последнего элемента
		nextCursor = utils.EncodeCursor(lastActivityCursor, lastIDCursor) // <<< ИЗМЕНЕНО: Используем EncodeCursor
	}

	r.logger.Debug("User story summaries ONLY with progress listed successfully", append(logFields, zap.Int("count", len(summaries)), zap.Bool("hasNext", nextCursor != ""))...)
	return summaries, nextCursor, nil
}

// UpdateStatusFlagsAndSetup обновляет статус, флаги и setup для опубликованной истории.
func (r *pgPublishedStoryRepository) UpdateStatusFlagsAndSetup(ctx context.Context, id uuid.UUID, status models.StoryStatus, setup json.RawMessage, isFirstScenePending bool, areImagesPending bool) error {
	query := `
        UPDATE published_stories
        SET
            status = $2::story_status,
            setup = $3,
            is_first_scene_pending = $4,
            are_images_pending = $5,
            updated_at = NOW(),
			error_details = NULL -- Сбрасываем ошибку при успешном обновлении Setup
        WHERE id = $1
    `
	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.String("newStatus", string(status)),
		zap.Int("setupSize", len(setup)),
		zap.Bool("isFirstScenePending", isFirstScenePending),
		zap.Bool("areImagesPending", areImagesPending),
	}
	r.logger.Debug("Updating published story status, flags, and setup", logFields...)

	commandTag, err := r.db.Exec(ctx, query, id, status, setup, isFirstScenePending, areImagesPending)
	if err != nil {
		r.logger.Error("Failed to update published story status, flags, and setup", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления статуса/флагов/Setup для истории %s: %w", id, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("No rows affected when updating published story status, flags, and setup (story not found?)", logFields...)
		return models.ErrNotFound // Возвращаем ErrNotFound, если история не найдена
	}

	r.logger.Info("Published story status, flags, and setup updated successfully", logFields...)
	return nil
}

// UpdateStatusFlagsAndDetails обновляет статус, флаги и детали ошибки для опубликованной истории.
func (r *pgPublishedStoryRepository) UpdateStatusFlagsAndDetails(ctx context.Context, id uuid.UUID, status models.StoryStatus, isFirstScenePending bool, areImagesPending bool, errorDetails *string) error {
	query := `
        UPDATE published_stories
        SET
            status = $2::story_status,
            is_first_scene_pending = $3,
            are_images_pending = $4,
            error_details = $5,
            updated_at = NOW()
        WHERE id = $1
    `
	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.String("newStatus", string(status)),
		zap.Bool("isFirstScenePending", isFirstScenePending),
		zap.Bool("areImagesPending", areImagesPending),
	}
	if errorDetails != nil {
		logFields = append(logFields, zap.Stringp("errorDetails", errorDetails))
	} else {
		logFields = append(logFields, zap.Bool("clearErrorDetails", true))
	}
	r.logger.Debug("Updating published story status, flags, and error details", logFields...)

	commandTag, err := r.db.Exec(ctx, query, id, status, isFirstScenePending, areImagesPending, errorDetails)
	if err != nil {
		r.logger.Error("Failed to update published story status, flags, and error details", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления статуса/флагов/деталей ошибки для истории %s: %w", id, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("No rows affected when updating published story status, flags, and error details (story not found?)", logFields...)
		return models.ErrNotFound // Возвращаем ErrNotFound, если история не найдена
	}

	r.logger.Info("Published story status, flags, and error details updated successfully", logFields...)
	return nil
}
