package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"novel-server/shared/models"
	"novel-server/shared/utils" // For cursor utils
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	// "github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// Константы для операций с прогрессом
const (
	findWithProgressByUserIDQueryBase = `
		SELECT ` + publishedStorySummaryWithProgressFields + `
		FROM published_stories ps
		JOIN player_game_states pgs ON ps.id = pgs.published_story_id
		LEFT JOIN story_likes sl ON ps.id = sl.published_story_id AND sl.user_id = $1
		LEFT JOIN users u ON ps.user_id = u.id
		WHERE pgs.player_id = $1
	`
	listUserSummariesWithProgressQueryBase = `
		SELECT ` + publishedStorySummaryWithProgressFields + `
		FROM published_stories ps
		LEFT JOIN player_game_states pgs ON ps.id = pgs.published_story_id AND pgs.player_id = $1
		LEFT JOIN story_likes sl ON ps.id = sl.published_story_id AND sl.user_id = $1
		LEFT JOIN users u ON ps.user_id = u.id
		WHERE ps.user_id = $1
	`
	listUserSummariesOnlyWithProgressQueryBase = `
		SELECT ` + publishedStorySummaryWithProgressFields + `, pgs.last_activity_at
		FROM published_stories ps
		JOIN player_game_states pgs ON ps.id = pgs.published_story_id AND pgs.player_id = $1
		LEFT JOIN story_likes sl ON ps.id = sl.published_story_id AND sl.user_id = $1
		LEFT JOIN users u ON ps.user_id = u.id
	` // User ID is $1
	getSummaryWithDetailsQueryBase = `
		SELECT ` + publishedStorySummaryWithProgressFields + `
		FROM published_stories ps
		LEFT JOIN player_game_states pgs ON ps.id = pgs.published_story_id AND pgs.player_id = $2 -- User ID is $2 here
		LEFT JOIN story_likes sl ON ps.id = sl.published_story_id AND sl.user_id = $2
		LEFT JOIN users u ON ps.user_id = u.id
		WHERE ps.id = $1
	`
	// publishedStorySummaryWithProgressFields needs to be defined/accessible
)

// FindWithProgressByUserID возвращает список историй, в которых у пользователя есть прогресс.
// !!! Проблема с типами пагинации/сортировки, временно используем placeholders !!!
func (r *pgPublishedStoryRepository) FindWithProgressByUserID(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]models.PublishedStorySummaryWithProgress, string, error) {
	logFields := []zap.Field{zap.String("userID", userID.String()), zap.Int("limit", limit), zap.String("cursor", cursor)}
	r.logger.Debug("Finding stories with progress", logFields...)

	if limit <= 0 {
		limit = 20 // Default limit
	}
	fetchLimit := limit + 1

	// Decode cursor (assuming progress is sorted by player_game_states.last_activity_at)
	cursorTime, cursorID, err := utils.DecodeCursor(cursor)
	if err != nil && cursor != "" { // Only error if cursor is non-empty and invalid
		r.logger.Warn("Invalid cursor provided for FindWithProgressByUserID", zap.String("cursor", cursor), zap.Error(err))
		return nil, "", fmt.Errorf("invalid cursor: %w", err)
	}

	args := []interface{}{userID}
	paramIndex := 2
	queryBuilder := strings.Builder{}
	queryBuilder.WriteString(findWithProgressByUserIDQueryBase)

	// Add cursor condition
	if cursor != "" {
		queryBuilder.WriteString(fmt.Sprintf(" AND (pgs.last_activity_at, ps.id) < ($%d, $%d)", paramIndex, paramIndex+1))
		args = append(args, cursorTime, cursorID)
		paramIndex += 2
	}

	// Add order and limit
	queryBuilder.WriteString(fmt.Sprintf(" ORDER BY pgs.last_activity_at DESC, ps.id DESC LIMIT $%d", paramIndex))
	args = append(args, fetchLimit)

	finalQuery := queryBuilder.String()
	r.logger.Debug("Executing FindWithProgressByUserID query", append(logFields, zap.String("query", finalQuery))...)

	rows, err := r.db.Query(ctx, finalQuery, args...)
	if err != nil {
		r.logger.Error("Error querying stories with progress", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("database query failed: %w", err)
	}
	defer rows.Close()

	stories := make([]models.PublishedStorySummaryWithProgress, 0, limit)
	var lastActivityTime time.Time
	var lastStoryID uuid.UUID

	for rows.Next() {
		summary, err := scanPublishedStorySummaryWithProgress(rows) // Use helper
		if err != nil {
			r.logger.Error("Error scanning story with progress row", append(logFields, zap.Error(err))...)
			// Decide: return error or skip row? Returning error for now.
			return nil, "", fmt.Errorf("ошибка сканирования строки истории с прогрессом: %w", err)
		}
		stories = append(stories, *summary) // Dereference summary before appending
		// Need to get last_activity_at for cursor, but it's not in the summary struct
		// This approach requires scanning last_activity_at separately or adding it to the DTO.
		// For now, cursor logic might be broken here.
		// TODO: Fix cursor logic by scanning relevant field.
		lastStoryID = summary.ID
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating story with progress rows", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("database iteration failed: %w", err)
	}

	var nextCursor string
	if len(stories) == fetchLimit {
		stories = stories[:limit] // Return only the requested number
		// TODO: Fix cursor generation - needs lastActivityTime from the *last item*
		if lastStoryID != uuid.Nil { // Check if we actually scanned any rows
			nextCursor = utils.EncodeCursor(lastActivityTime, lastStoryID) // This lastActivityTime is likely incorrect
		}
	}

	r.logger.Debug("Found stories with progress", append(logFields, zap.Int("count", len(stories)), zap.Bool("hasNext", nextCursor != ""))...)
	return stories, nextCursor, nil
}

// ListUserSummariesWithProgress возвращает список историй пользователя, включая информацию о прогрессе.
// !!! Проблема с типами пагинации/сортировки, временно используем placeholders !!!
func (r *pgPublishedStoryRepository) ListUserSummariesWithProgress(ctx context.Context, userID uuid.UUID, cursor string, limit int, filterAdult bool) ([]models.PublishedStorySummaryWithProgress, string, error) {
	logFields := []zap.Field{zap.String("userID", userID.String()), zap.Int("limit", limit), zap.String("cursor", cursor), zap.Bool("filterAdult", filterAdult)}
	r.logger.Debug("Listing user summaries with progress", logFields...)

	if limit <= 0 {
		limit = 10 // Default limit
	}
	fetchLimit := limit + 1

	// Decode cursor (assuming sorting by ps.created_at)
	cursorTime, cursorID, err := utils.DecodeCursor(cursor)
	if err != nil && cursor != "" {
		r.logger.Warn("Invalid cursor provided for ListUserSummariesWithProgress", zap.String("cursor", cursor), zap.Error(err))
		return nil, "", fmt.Errorf("invalid cursor: %w", err)
	}

	args := []interface{}{userID}
	paramIndex := 2
	queryBuilder := strings.Builder{}
	queryBuilder.WriteString(listUserSummariesWithProgressQueryBase)

	// Add filters
	if filterAdult {
		queryBuilder.WriteString(fmt.Sprintf(" AND ps.is_adult_content = $%d", paramIndex))
		args = append(args, false)
		paramIndex++
	}

	// Add cursor condition
	if cursor != "" {
		queryBuilder.WriteString(fmt.Sprintf(" AND (ps.created_at, ps.id) < ($%d, $%d)", paramIndex, paramIndex+1))
		args = append(args, cursorTime, cursorID)
		paramIndex += 2
	}

	// Add order and limit
	queryBuilder.WriteString(fmt.Sprintf(" ORDER BY ps.created_at DESC, ps.id DESC LIMIT $%d", paramIndex))
	args = append(args, fetchLimit)

	finalQuery := queryBuilder.String()
	r.logger.Debug("Executing ListUserSummariesWithProgress query", append(logFields, zap.String("query", finalQuery))...)

	rows, err := r.db.Query(ctx, finalQuery, args...)
	if err != nil {
		r.logger.Error("Error querying user summaries with progress", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("database query failed: %w", err)
	}
	defer rows.Close()

	summaries := make([]models.PublishedStorySummaryWithProgress, 0, limit)
	var lastCreatedAt time.Time
	var lastStoryID uuid.UUID

	for rows.Next() {
		summary, err := scanPublishedStorySummaryWithProgress(rows) // Use helper
		if err != nil {
			r.logger.Error("Error scanning user summary with progress row", append(logFields, zap.Error(err))...)
			return nil, "", fmt.Errorf("ошибка сканирования строки сводки истории: %w", err)
		}
		summaries = append(summaries, *summary)
		// Need CreatedAt for cursor (which is PublishedAt in the summary struct)
		if !summary.PublishedAt.IsZero() { // Check if time is set
			lastCreatedAt = summary.PublishedAt // Assign directly
		} else {
			lastCreatedAt = time.Time{} // Use zero time if not set
		}
		lastStoryID = summary.ID
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating user summary with progress rows", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("database iteration failed: %w", err)
	}

	var nextCursor string
	if len(summaries) == fetchLimit {
		summaries = summaries[:limit]
		if lastStoryID != uuid.Nil { // Check if we actually scanned any rows
			nextCursor = utils.EncodeCursor(lastCreatedAt, lastStoryID)
		}
	}

	r.logger.Debug("Listed user summaries with progress", append(logFields, zap.Int("count", len(summaries)), zap.Bool("hasNext", nextCursor != ""))...)
	return summaries, nextCursor, nil
}

// ListUserSummariesOnlyWithProgress возвращает список историй, в которых у пользователя есть прогресс.
// !!! Проблема с типами пагинации/сортировки, временно используем placeholders !!!
func (r *pgPublishedStoryRepository) ListUserSummariesOnlyWithProgress(ctx context.Context, userID uuid.UUID, cursor string, limit int, filterAdult bool) ([]models.PublishedStorySummaryWithProgress, string, error) {
	logFields := []zap.Field{zap.String("userID", userID.String()), zap.Int("limit", limit), zap.String("cursor", cursor), zap.Bool("filterAdult", filterAdult)}
	r.logger.Debug("Listing user summaries ONLY with progress", logFields...)

	if limit <= 0 {
		limit = 10 // Default limit
	}
	fetchLimit := limit + 1

	// Decode cursor (assuming sorting by pgs.last_activity_at)
	cursorTime, cursorID, err := utils.DecodeCursor(cursor)
	if err != nil && cursor != "" {
		r.logger.Warn("Invalid cursor provided for ListUserSummariesOnlyWithProgress", zap.String("cursor", cursor), zap.Error(err))
		return nil, "", fmt.Errorf("invalid cursor: %w", err)
	}

	args := []interface{}{userID}
	paramIndex := 2
	queryBuilder := strings.Builder{}
	queryBuilder.WriteString(listUserSummariesOnlyWithProgressQueryBase)

	// Add filters
	if filterAdult {
		queryBuilder.WriteString(fmt.Sprintf(" AND ps.is_adult_content = $%d", paramIndex))
		args = append(args, false)
		paramIndex++
	}

	// Add cursor condition
	if cursor != "" {
		// Sort by pgs.last_activity_at DESC, ps.id DESC
		queryBuilder.WriteString(fmt.Sprintf(" AND (pgs.last_activity_at, ps.id) < ($%d, $%d)", paramIndex, paramIndex+1))
		args = append(args, cursorTime, cursorID)
		paramIndex += 2
	}

	// Add order and limit
	queryBuilder.WriteString(fmt.Sprintf(" ORDER BY pgs.last_activity_at DESC, ps.id DESC LIMIT $%d", paramIndex))
	args = append(args, fetchLimit)

	finalQuery := queryBuilder.String()
	r.logger.Debug("Executing ListUserSummariesOnlyWithProgress query", append(logFields, zap.String("query", finalQuery))...)

	rows, err := r.db.Query(ctx, finalQuery, args...)
	if err != nil {
		r.logger.Error("Error querying user summaries only with progress", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("database query failed: %w", err)
	}
	defer rows.Close()

	summaries := make([]models.PublishedStorySummaryWithProgress, 0, limit)
	var lastActivityTime time.Time // Variable to store the last activity time for cursor
	var lastStoryID uuid.UUID

	for rows.Next() {
		// <<< RESTORED: Call the helper function >>>
		summary, err := scanPublishedStorySummaryWithProgressAndActivity(rows, &lastActivityTime)
		if err != nil {
			r.logger.Error("Error scanning user summary only with progress row", append(logFields, zap.Error(err))...)
			return nil, "", fmt.Errorf("ошибка сканирования строки сводки истории: %w", err)
		}
		summaries = append(summaries, *summary) // Append value
		lastStoryID = summary.ID
		// lastActivityTime is updated by the helper via pointer
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating user summary only with progress rows", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("database iteration failed: %w", err)
	}

	var nextCursor string
	if len(summaries) == fetchLimit {
		summaries = summaries[:limit]
		if lastStoryID != uuid.Nil {
			nextCursor = utils.EncodeCursor(lastActivityTime, lastStoryID)
		}
	}

	r.logger.Debug("Listed user summaries ONLY with progress", append(logFields, zap.Int("count", len(summaries)), zap.Bool("hasNext", nextCursor != ""))...)
	return summaries, nextCursor, nil
}

// GetSummaryWithDetails получает детали истории для конкретного пользователя.
func (r *pgPublishedStoryRepository) GetSummaryWithDetails(ctx context.Context, storyID, userID uuid.UUID) (*models.PublishedStorySummaryWithProgress, error) {
	logFields := []zap.Field{zap.String("storyID", storyID.String()), zap.String("userID", userID.String())}
	r.logger.Debug("Getting summary with details", logFields...)

	row := r.db.QueryRow(ctx, getSummaryWithDetailsQueryBase, storyID, userID)
	summary, err := scanPublishedStorySummaryWithProgress(row) // Use standard helper

	if err != nil {
		if err == models.ErrNotFound {
			r.logger.Warn("Story not found for GetSummaryWithDetails", logFields...)
			return nil, models.ErrNotFound
		}
		r.logger.Error("Failed to get summary with details", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения сводки истории: %w", err)
	}

	r.logger.Debug("Retrieved summary with details successfully", logFields...)
	return summary, nil
}

// --- Helper for scanning summary + activity time ---
// scanPublishedStorySummaryWithProgressAndActivity scans a summary and the last activity time.
func scanPublishedStorySummaryWithProgressAndActivity(row pgx.Row, lastActivityAt *time.Time) (*models.PublishedStorySummaryWithProgress, error) {
	var summary models.PublishedStorySummaryWithProgress
	var playerGameStatus sql.NullString
	var publishedAt time.Time // published_at from stories table (mapped to summary.PublishedAt)

	// Need to know the exact order from the query using this helper
	// Assuming: summaryFields..., has_player_progress, is_public, player_status, last_activity_at
	err := row.Scan( // Now expecting 14 destinations
		&summary.ID,                // 1
		&summary.Title,             // 2
		&summary.ShortDescription,  // 3
		&summary.AuthorID,          // 4
		&summary.AuthorName,        // 5
		&publishedAt,               // 6
		&summary.IsAdultContent,    // 7
		&summary.LikesCount,        // 8
		&summary.IsLiked,           // 9
		&summary.Status,            // 10
		&summary.HasPlayerProgress, // 11 (was 12)
		&summary.IsPublic,          // 12 (was 13)
		&playerGameStatus,          // 13 (was 14)
		lastActivityAt,             // 14 (was 15)
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("ошибка сканирования строки PublishedStorySummaryWithProgressAndActivity: %w", err)
	}

	summary.PublishedAt = publishedAt // Assign scanned time
	if playerGameStatus.Valid {
		summary.PlayerGameStatus = playerGameStatus.String
	}

	return &summary, nil
}
