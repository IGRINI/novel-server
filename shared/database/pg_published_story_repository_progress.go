package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	interfaces "novel-server/shared/interfaces"
	"novel-server/shared/models"
	"novel-server/shared/utils" // For cursor utils
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

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

// FindWithProgressByUserID retrieves a paginated list of stories with progress for a specific user using cursor pagination.
// Includes author name directly via JOIN.
func (r *pgPublishedStoryRepository) FindWithProgressByUserID(ctx context.Context, querier interfaces.DBTX, userID uuid.UUID, limit int, cursor string) ([]models.PublishedStorySummary, string, error) {
	// Note: The SELECT list now includes fields assumed to be in PublishedStorySummary
	const baseQuery = `
		SELECT
			ps.id, ps.title, ps.description, ps.user_id, u.display_name AS author_name,
			ps.created_at, ps.is_adult_content, ps.likes_count,
			TRUE AS has_player_progress,
			ps.is_public,
			pgs.player_status, pgs.id as player_game_state_id,
			pgs.last_accessed_at -- For cursor pagination and LastPlayedAt field
		FROM published_stories ps
		JOIN player_game_states pgs ON pgs.published_story_id = ps.id AND pgs.user_id = $1
		JOIN users u ON u.id = ps.user_id
		WHERE pgs.user_id = $1
	`
	const orderBy = " ORDER BY pgs.last_accessed_at DESC, ps.id DESC"

	log := r.logger.With(
		zap.String("method", "FindWithProgressByUserID"),
		zap.Stringer("userID", userID),
		zap.Int("limit", limit),
		zap.String("cursor", cursor),
	)
	log.Debug("Finding stories with progress by user ID")

	var queryArgs []interface{}
	queryArgs = append(queryArgs, userID)

	finalQuery := baseQuery
	// Cursor Logic using placeholder utils.DecodeCursor
	if cursor != "" {
		lastAccessedAt, id, err := utils.DecodeCursor(cursor) // Placeholder
		if err != nil {
			log.Warn("Invalid cursor format", zap.Error(err))
			return nil, "", fmt.Errorf("%w: invalid cursor: %v", models.ErrBadRequest, err)
		}
		finalQuery += " AND (pgs.last_accessed_at, ps.id) < ($2, $3)"
		queryArgs = append(queryArgs, lastAccessedAt, id)
	}

	finalQuery += orderBy + fmt.Sprintf(" LIMIT $%d", len(queryArgs)+1)
	queryArgs = append(queryArgs, limit+1)

	rows, err := querier.Query(ctx, finalQuery, queryArgs...)
	if err != nil {
		log.Error("Failed to query stories with progress", zap.Error(err))
		return nil, "", fmt.Errorf("database query error: %w", err)
	}
	defer rows.Close()

	stories := make([]models.PublishedStorySummary, 0, limit)
	var lastAccessedAtCursor time.Time
	var storyIDCursor uuid.UUID

	for rows.Next() {
		var story models.PublishedStorySummary
		var lastAccessedAtNullable pgtype.Timestamptz

		// Scan fields into PublishedStorySummary
		err := rows.Scan(
			&story.ID, &story.Title, &story.ShortDescription, // Use ShortDescription
			&story.AuthorID, &story.AuthorName,
			&story.PublishedAt, // Use PublishedAt for ps.created_at
			&story.IsAdultContent, &story.LikesCount,
			&story.HasPlayerProgress,
			&story.IsPublic,
			&story.PlayerGameStatus, // Use PlayerGameStatus for pgs.player_status
			&story.PlayerGameStateID,
			&lastAccessedAtNullable,
		)
		if err != nil {
			log.Error("Failed to scan story summary with progress", zap.Error(err))
			return nil, "", fmt.Errorf("database scan error: %w", err)
		}
		if lastAccessedAtNullable.Valid {
			story.LastPlayedAt = &lastAccessedAtNullable.Time // Assign LastPlayedAt
		}

		stories = append(stories, story)

		if lastAccessedAtNullable.Valid {
			lastAccessedAtCursor = lastAccessedAtNullable.Time
			storyIDCursor = story.ID
		} else {
			log.Warn("last_accessed_at is unexpectedly NULL, cursor might be incorrect", zap.Stringer("storyID", story.ID))
		}
	}

	if err = rows.Err(); err != nil {
		log.Error("Error iterating story rows", zap.Error(err))
		return nil, "", fmt.Errorf("database iteration error: %w", err)
	}

	var nextCursor string
	if len(stories) > limit {
		// Generate next cursor using placeholder utils.EncodeCursor
		nextCursor = utils.EncodeCursor(lastAccessedAtCursor, storyIDCursor) // Placeholder
		stories = stories[:limit]
	}

	log.Info("Successfully found stories with progress", zap.Int("count", len(stories)), zap.Bool("hasNext", nextCursor != ""))
	return stories, nextCursor, nil
}

// ListUserSummariesWithProgress возвращает список историй пользователя, включая информацию о прогрессе.
// !!! Проблема с типами пагинации/сортировки, временно используем placeholders !!!
func (r *pgPublishedStoryRepository) ListUserSummariesWithProgress(ctx context.Context, querier interfaces.DBTX, userID uuid.UUID, cursor string, limit int, filterAdult bool) ([]models.PublishedStorySummary, string, error) {
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

	rows, err := querier.Query(ctx, finalQuery, args...)
	if err != nil {
		r.logger.Error("Error querying user summaries with progress", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("database query failed: %w", err)
	}
	defer rows.Close()

	summaries := make([]models.PublishedStorySummary, 0, limit)
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
func (r *pgPublishedStoryRepository) ListUserSummariesOnlyWithProgress(ctx context.Context, querier interfaces.DBTX, userID uuid.UUID, cursor string, limit int, filterAdult bool) ([]models.PublishedStorySummary, string, error) {
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

	rows, err := querier.Query(ctx, finalQuery, args...)
	if err != nil {
		r.logger.Error("Error querying user summaries only with progress", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("database query failed: %w", err)
	}
	defer rows.Close()

	summaries := make([]models.PublishedStorySummary, 0, limit)
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
func (r *pgPublishedStoryRepository) GetSummaryWithDetails(ctx context.Context, querier interfaces.DBTX, storyID, userID uuid.UUID) (*models.PublishedStorySummary, error) {
	logFields := []zap.Field{zap.String("storyID", storyID.String()), zap.String("userID", userID.String())}
	r.logger.Debug("Getting summary with details", logFields...)

	row := querier.QueryRow(ctx, getSummaryWithDetailsQueryBase, storyID, userID)
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
func scanPublishedStorySummaryWithProgressAndActivity(row pgx.Row, lastActivityAt *time.Time) (*models.PublishedStorySummary, error) {
	var summary models.PublishedStorySummary
	var playerGameStatus sql.NullString
	var publishedAt time.Time // published_at from stories table (mapped to summary.PublishedAt)

	// Need to know the exact order from the query using this helper
	// Assuming: summaryFields..., has_player_progress, is_public, player_status, player_game_state_id, last_activity_at
	err := row.Scan( // Now expecting 15 destinations
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
		&summary.HasPlayerProgress, // 11
		&summary.IsPublic,          // 12
		&playerGameStatus,          // 13
		&summary.PlayerGameStateID, // 14 (pgs.id)
		lastActivityAt,             // 15 (pgs.last_activity_at)
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
