package database

import (
	"context"
	"errors"
	"fmt"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"

	"time"

	"novel-server/shared/utils"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

const (
	addLikeQuery    = `INSERT INTO story_likes (user_id, published_story_id) VALUES ($1, $2)`
	removeLikeQuery = `DELETE FROM story_likes WHERE user_id = $1 AND published_story_id = $2`
	checkLikeQuery  = `SELECT EXISTS (SELECT 1 FROM story_likes WHERE user_id = $1 AND published_story_id = $2)`
	countLikesQuery = `SELECT COUNT(*) FROM story_likes WHERE published_story_id = $1`
	// Base query for listing likes, cursor logic is appended dynamically
	listLikedStoryIDsBaseQuery = `SELECT published_story_id, created_at FROM story_likes WHERE user_id = $1 `
)

// pgLikeRepository реализует интерфейс LikeRepository для PostgreSQL.
type pgLikeRepository struct {
	db     interfaces.DBTX
	logger *zap.Logger
}

// Compile-time check
var _ interfaces.LikeRepository = (*pgLikeRepository)(nil)

// NewPgLikeRepository создает новый экземпляр репозитория лайков.
func NewPgLikeRepository(db interfaces.DBTX, logger *zap.Logger) interfaces.LikeRepository {
	return &pgLikeRepository{
		db:     db,
		logger: logger.Named("PgLikeRepo"),
	}
}

// AddLike добавляет запись о лайке.
func (r *pgLikeRepository) AddLike(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error {
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	r.logger.Debug("Adding like record", logFields...)

	_, err := r.db.Exec(ctx, addLikeQuery, userID, publishedStoryID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505": // unique_violation (означает, что лайк уже существует)
				r.logger.Warn("Like already exists (unique constraint violation)", logFields...)
				return models.ErrLikeAlreadyExists
			case "23503": // foreign_key_violation (означает, что published_story_id не найден)
				r.logger.Warn("Published story not found (foreign key violation)", logFields...)
				return models.ErrNotFound // Возвращаем стандартную ошибку
			}
		}
		// Другая ошибка БД
		r.logger.Error("Failed to add like record", append(logFields, zap.Error(err))...)
		return fmt.Errorf("failed to add like: %w", err)
	}

	r.logger.Info("Like record added successfully", logFields...)
	return nil
}

// RemoveLike удаляет запись о лайке.
func (r *pgLikeRepository) RemoveLike(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error {
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	r.logger.Debug("Removing like record", logFields...)

	commandTag, err := r.db.Exec(ctx, removeLikeQuery, userID, publishedStoryID)
	if err != nil {
		r.logger.Error("Failed to remove like record", append(logFields, zap.Error(err))...)
		return fmt.Errorf("failed to remove like: %w", err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("Like not found to remove", logFields...)
		return models.ErrLikeNotFound // Лайка не было
	}

	r.logger.Info("Like record removed successfully", logFields...)
	return nil
}

// CheckLike проверяет, лайкнул ли пользователь историю.
func (r *pgLikeRepository) CheckLike(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (bool, error) {
	var exists bool
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	r.logger.Debug("Checking if like exists", logFields...)

	err := r.db.QueryRow(ctx, checkLikeQuery, userID, publishedStoryID).Scan(&exists)
	if err != nil {
		r.logger.Error("Failed to check like existence", append(logFields, zap.Error(err))...)
		return false, fmt.Errorf("failed to check like existence: %w", err)
	}

	r.logger.Debug("Like existence check completed", append(logFields, zap.Bool("exists", exists))...)
	return exists, nil
}

// CountLikes возвращает общее количество лайков для истории.
func (r *pgLikeRepository) CountLikes(ctx context.Context, publishedStoryID uuid.UUID) (int64, error) {
	var count int64
	logFields := []zap.Field{zap.String("publishedStoryID", publishedStoryID.String())}
	r.logger.Debug("Counting likes for story", logFields...)

	err := r.db.QueryRow(ctx, countLikesQuery, publishedStoryID).Scan(&count)
	if err != nil {
		// Если история не найдена, COUNT вернет 0, ошибки не будет (если FK не строгий или история удалена каскадно)
		// Поэтому проверяем на pgx.ErrNoRows здесь не нужно.
		r.logger.Error("Failed to count likes for story", append(logFields, zap.Error(err))...)
		return 0, fmt.Errorf("failed to count likes: %w", err)
	}

	r.logger.Debug("Likes counted successfully", append(logFields, zap.Int64("count", count))...)
	return count, nil
}

// ListLikedStoryIDsByUserID возвращает список ID историй, лайкнутых пользователем, с пагинацией по курсору.
func (r *pgLikeRepository) ListLikedStoryIDsByUserID(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]uuid.UUID, string, error) {
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("cursor", cursor),
		zap.Int("limit", limit),
	}
	r.logger.Debug("Listing liked story IDs by user ID", logFields...)

	if limit <= 0 {
		limit = 10 // Default limit
	}

	var args []interface{}
	args = append(args, userID, limit+1) // Fetch one extra to check for next page

	query := listLikedStoryIDsBaseQuery // Start with the base query

	// --- Cursor Logic ---
	var cursorTime time.Time
	var cursorStoryID uuid.UUID
	var cursorErr error

	if cursor != "" {
		cursorTime, cursorStoryID, cursorErr = utils.DecodeCursor(cursor)
		if cursorErr != nil {
			r.logger.Warn("Invalid cursor format", append(logFields, zap.Error(cursorErr))...)
			return nil, "", interfaces.ErrInvalidCursor
		}
		// Add WHERE clause for cursor pagination
		// We assume descending order (newest first)
		query += `AND (created_at, published_story_id) < ($3, $4) `
		args = append(args, cursorTime, cursorStoryID)
	}
	// --- End Cursor Logic ---

	query += `ORDER BY created_at DESC, published_story_id DESC LIMIT $2`
	r.logger.Debug("Executing query to list liked stories", append(logFields, zap.String("query", query), zap.Any("args", args))...)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to query liked stories", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("failed to query liked stories: %w", err)
	}
	defer rows.Close()

	likedIDs := make([]uuid.UUID, 0, limit)
	var lastTime time.Time
	var lastStoryID uuid.UUID
	count := 0

	for rows.Next() {
		count++
		var storyID uuid.UUID
		var createdAt time.Time
		if err := rows.Scan(&storyID, &createdAt); err != nil {
			r.logger.Error("Failed to scan liked story row", append(logFields, zap.Error(err))...)
			return nil, "", fmt.Errorf("failed to scan liked story: %w", err)
		}

		if count <= limit {
			likedIDs = append(likedIDs, storyID)
		}
		// Keep track of the last item for the next cursor
		lastTime = createdAt
		lastStoryID = storyID
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating liked story rows", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("failed to iterate liked stories: %w", err)
	}

	var nextCursor string
	if count > limit {
		// We fetched one extra row, so there is a next page
		nextCursor = utils.EncodeCursor(lastTime, lastStoryID)
	} // else: no next page, nextCursor remains ""

	r.logger.Debug("Successfully listed liked story IDs", append(logFields, zap.Int("count_returned", len(likedIDs)), zap.Bool("has_next_page", nextCursor != ""))...)
	return likedIDs, nextCursor, nil
}
