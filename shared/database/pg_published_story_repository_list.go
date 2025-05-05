package database

import (
	"context"
	"database/sql"
	"fmt"

	// "novel-server/shared/interfaces" // Not used here
	"novel-server/shared/models" // Import for pagination utils
	// For pagination utils AND PaginationInfo
	// For pagination calculation
	"strings"

	// "time" // Not directly used here

	"github.com/google/uuid"
	// "github.com/jackc/pgx/v5" // Not directly used here
	// "github.com/lib/pq" // Not directly used here
	"novel-server/shared/utils"
	"time"

	"go.uber.org/zap"
)

// Поля для разных уровней детализации - ПЕРЕНЕСЕНЫ В ОСНОВНОЙ ФАЙЛ
/*
const (
	// publishedStoryFields defined in _crud.go
	publishedStorySummaryFields = `
		ps.id, ps.title, ps.description, ps.user_id, u.display_name as author_name, ps.created_at, ps.is_adult_content,
		ps.likes_count, (sl.user_id IS NOT NULL) as is_liked, ps.status, ir.image_url as cover_image_url
	`
	publishedStorySummaryWithProgressFields = publishedStorySummaryFields + `,
		(pgs.player_progress_id IS NOT NULL) as has_player_progress, ps.is_public, pgs.player_status
	`
)
*/

// Константы, необходимые для List/Search операций
const (
	// publishedStoryFields должен быть доступен из основного файла
	listPublishedStoriesByUserIDQueryBase = `SELECT ` + publishedStoryFields + ` FROM published_stories ps WHERE ps.user_id = $1`

	listLikedStoriesByUserQueryBase = `
		SELECT ` + publishedStorySummaryWithProgressFields + `
		FROM published_stories ps
		JOIN story_likes sl ON ps.id = sl.published_story_id
		LEFT JOIN player_game_states pgs ON ps.id = pgs.published_story_id AND pgs.player_id = $1 -- Need player_id for progress
		-- LEFT JOIN story_likes already joined (for is_liked calculation in fields)
		WHERE sl.user_id = $1 AND ps.status = 'published'
	` // Only published liked stories

	// publishedStorySummaryFields должен быть доступен из основного файла
	listPublicSummariesQueryBase = `
		SELECT ` + publishedStorySummaryWithProgressFields + `
		FROM published_stories ps
		LEFT JOIN player_game_states pgs ON ps.id = pgs.published_story_id AND pgs.player_id = $1 -- User ID for progress
		LEFT JOIN story_likes sl ON ps.id = sl.published_story_id AND sl.user_id = $1       -- User ID for like status
		WHERE ps.status = 'published'
	`

	// publishedStorySummaryWithProgressFields должен быть доступен из основного файла
	searchPublicQueryBase = `
		SELECT ` + publishedStorySummaryWithProgressFields + `, ts_rank_cd(search_vector, query) as rank
		FROM published_stories ps
		LEFT JOIN player_game_states pgs ON ps.id = pgs.published_story_id AND pgs.player_id = $1 -- User checking likes/progress
		LEFT JOIN story_likes sl ON ps.id = sl.published_story_id AND sl.user_id = $1
		, websearch_to_tsquery('simple', $2) query
		WHERE ps.status = 'published' AND search_vector @@ query
	`
	countPublicQueryBase = `SELECT COUNT(*) FROM published_stories ps WHERE ps.status = 'published'`

	searchPublicCountQueryBase = `
		SELECT COUNT(*)
		FROM published_stories ps, websearch_to_tsquery('simple', $1) query
		WHERE ps.status = 'published' AND search_vector @@ query
	`
)

// ListByUserID возвращает список опубликованных историй для указанного пользователя.
// !!! Проблема с типами пагинации/сортировки, временно используем placeholders !!!
// TODO: Implement proper cursor pagination instead of offset/limit
func (r *pgPublishedStoryRepository) ListByUserID(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*models.PublishedStory, string, error) {
	logFields := []zap.Field{zap.String("userID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit)}
	r.logger.Debug("Listing published stories by user ID", logFields...)

	stories := make([]*models.PublishedStory, 0)
	var args []interface{}
	args = append(args, userID) // $1 = userID

	queryBuilder := strings.Builder{}
	queryBuilder.WriteString(listPublishedStoriesByUserIDQueryBase)

	// Count Total
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM (%s) AS sub", strings.ReplaceAll(queryBuilder.String(), publishedStoryFields, "1")) // Replace fields with 1 for count
	var totalItems int64
	err := r.db.QueryRow(ctx, countQuery, args...).Scan(&totalItems)
	if err != nil {
		r.logger.Error("Failed to count stories for ListByUserID", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка подсчета историй пользователя: %w", err)
	}

	// Apply Sorting (Default)
	orderBy := "ps.created_at DESC"
	/* // Sorting logic commented out due to missing types
	if pagination != nil && pagination.SortBy != "" {
		switch pagination.SortBy {
		case "created_at", "updated_at", "title", "likes_count":
			colName := pagination.SortBy
			orderBy = "ps." + colName
			if pagination.SortOrder == models.SortOrderAsc { // <<< TYPE ERROR
				orderBy += " ASC NULLS FIRST"
			} else {
				orderBy += " DESC NULLS LAST"
			}
		default:
			r.logger.Warn("Invalid SortBy parameter for ListByUserID", zap.String("sortBy", pagination.SortBy))
		}
	}
	*/
	queryBuilder.WriteString(fmt.Sprintf(" ORDER BY %s", orderBy))
	logFields = append(logFields, zap.String("orderBy", orderBy))

	// Apply Pagination (Default limit/offset for now - NEEDS REFACTOR to use cursor)
	if limit <= 0 {
		limit = 10 // Default limit if not provided or invalid
	}
	offset := 0 // Placeholder: cursor needs decoding logic
	// TODO: Decode cursor to determine where to start fetching from
	// cursorTime, cursorID, err := utils.DecodeCursor(cursor)
	// if err != nil { ... handle error ... }
	// Modify queryBuilder WHERE clause based on cursorTime, cursorID

	args = append(args, limit, offset)                                                     // Still using offset for now
	queryBuilder.WriteString(fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))) // Still using offset for now
	logFields = append(logFields, zap.Int("limit", limit), zap.Int("offset", offset))

	// Execute Query
	finalQuery := queryBuilder.String()
	r.logger.Debug("Executing ListByUserID query", append(logFields, zap.String("query", finalQuery))...)

	rows, err := r.db.Query(ctx, finalQuery, args...)
	if err != nil {
		r.logger.Error("Failed to query published stories by user ID", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка запроса опубликованных историй пользователя: %w", err)
	}
	defer rows.Close()

	// Scan Results
	for rows.Next() {
		story, err := scanPublishedStory(rows)
		if err != nil {
			r.logger.Error("Failed to scan published story row in ListByUserID", append(logFields, zap.Error(err))...)
			return nil, "", fmt.Errorf("ошибка сканирования строки опубликованной истории: %w", err)
		}
		stories = append(stories, story)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating published story rows in ListByUserID", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка итерации результатов запроса историй: %w", err)
	}

	// Calculate PaginationInfo / Next Cursor (Placeholder)
	nextCursor := "" // Placeholder: Needs cursor encoding logic
	// TODO: Encode nextCursor based on the last item's created_at and ID
	// if len(stories) == limit { ... encode cursor ... }

	r.logger.Debug("Successfully listed published stories by user ID", append(logFields, zap.Int("count", len(stories)), zap.Int64("total", totalItems))...)
	return stories, nextCursor, nil
}

// ListLikedByUser возвращает список историй, лайкнутых пользователем.
// !!! Проблема с типами пагинации/сортировки, временно используем placeholders !!!
// TODO: Implement proper cursor pagination
func (r *pgPublishedStoryRepository) ListLikedByUser(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]models.PublishedStorySummaryWithProgress, string, error) {
	logFields := []zap.Field{zap.String("userID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit)}
	r.logger.Debug("Listing liked stories by user ID", logFields...)

	summaries := make([]models.PublishedStorySummaryWithProgress, 0) // <<< Change type
	var args []interface{}
	args = append(args, userID) // $1 = userID

	queryBuilder := strings.Builder{}
	queryBuilder.WriteString(listLikedStoriesByUserQueryBase)

	// Count Total
	countQuery := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM published_stories ps
		JOIN story_likes sl ON ps.id = sl.published_story_id
		WHERE sl.user_id = $1 AND ps.status = 'published'
	`)
	var totalItems int64
	err := r.db.QueryRow(ctx, countQuery, args...).Scan(&totalItems)
	if err != nil {
		r.logger.Error("Failed to count stories for ListLikedByUser", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка подсчета лайкнутых историй: %w", err)
	}

	// Apply Sorting (Default)
	orderBy := "sl.created_at DESC"
	/* // Sorting logic commented out
	if pagination != nil && pagination.SortBy != "" {
		if pagination.SortBy == "liked_at" {
			orderBy = "sl.created_at"
			if pagination.SortOrder == models.SortOrderAsc { // <<< TYPE ERROR
				orderBy += " ASC NULLS FIRST"
			} else {
				orderBy += " DESC NULLS LAST"
			}
		} else {
			r.logger.Warn("Invalid SortBy parameter for ListLikedByUser", zap.String("sortBy", pagination.SortBy))
		}
	}
	*/
	queryBuilder.WriteString(fmt.Sprintf(" ORDER BY %s", orderBy))
	logFields = append(logFields, zap.String("orderBy", orderBy))

	// Apply Pagination (Default limit/offset - NEEDS REFACTOR to cursor)
	if limit <= 0 {
		limit = 10
	}
	offset := 0 // Placeholder for cursor logic
	// TODO: Decode cursor and modify query WHERE clause

	args = append(args, limit, offset)                                                     // Still using offset for now
	queryBuilder.WriteString(fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))) // Still using offset for now
	logFields = append(logFields, zap.Int("limit", limit), zap.Int("offset", offset))

	// Execute Query
	finalQuery := queryBuilder.String()
	r.logger.Debug("Executing ListLikedByUser query", append(logFields, zap.String("query", finalQuery))...)

	rows, err := r.db.Query(ctx, finalQuery, args...)
	if err != nil {
		r.logger.Error("Failed to query liked stories by user ID", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка запроса лайкнутых историй пользователя: %w", err)
	}
	defer rows.Close()

	// Scan Results
	for rows.Next() {
		// Use the helper function that scans into PublishedStorySummaryWithProgress
		summary, err := scanPublishedStorySummaryWithProgress(rows) // <<< Use correct scanner
		if err != nil {
			r.logger.Error("Failed to scan liked story row in ListLikedByUser", append(logFields, zap.Error(err))...)
			return nil, "", fmt.Errorf("ошибка сканирования строки лайкнутой истории: %w", err)
		}
		summaries = append(summaries, *summary) // <<< Append value
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating liked story rows in ListLikedByUser", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка итерации результатов запроса лайкнутых историй: %w", err)
	}

	// Calculate PaginationInfo / Next Cursor (Placeholder)
	nextCursor := "" // Placeholder
	// TODO: Encode nextCursor based on last item's liked_at (sl.created_at) and ID

	r.logger.Debug("Successfully listed liked stories by user ID", append(logFields, zap.Int("count", len(summaries)), zap.Int64("total", totalItems))...)
	return summaries, nextCursor, nil // <<< Return correct types
}

// ListPublicSummaries возвращает список сводок общедоступных опубликованных историй.
// !!! Проблема с типами пагинации/сортировки, временно используем placeholders !!!
// TODO: Implement more robust cursor logic based on sortBy
func (r *pgPublishedStoryRepository) ListPublicSummaries(ctx context.Context, userID *uuid.UUID, cursor string, limit int, sortBy string, filterAdult bool) ([]models.PublishedStorySummaryWithProgress, string, error) {
	logFields := []zap.Field{zap.String("cursor", cursor), zap.Int("limit", limit), zap.String("sortBy", sortBy), zap.Bool("filterAdult", filterAdult)}
	if userID != nil {
		logFields = append(logFields, zap.String("userID", userID.String()))
	}
	r.logger.Debug("Listing public story summaries", logFields...)

	summaries := make([]models.PublishedStorySummaryWithProgress, 0) // <<< Change type
	var args []interface{}
	args = append(args, userID) // $1 = userID (can be nil)
	paramIndex := 2

	queryBuilder := strings.Builder{}
	queryBuilder.WriteString(listPublicSummariesQueryBase)

	// Add filters
	if filterAdult {
		queryBuilder.WriteString(fmt.Sprintf(" AND ps.is_adult_content = $%d", paramIndex))
		args = append(args, false)
		paramIndex++
		logFields = append(logFields, zap.Bool("is_adult_content_filtered", true))
	}

	// Count Total (needs to include filters)
	countQueryBuilder := strings.Builder{}
	countQueryBuilder.WriteString("SELECT COUNT(*) FROM published_stories ps WHERE ps.status = 'published'")
	countArgs := []interface{}{}
	if filterAdult {
		countQueryBuilder.WriteString(" AND ps.is_adult_content = $1")
		countArgs = append(countArgs, false)
	}
	countQuery := countQueryBuilder.String()

	var totalItems int64
	err := r.db.QueryRow(ctx, countQuery, countArgs...).Scan(&totalItems)
	if err != nil {
		r.logger.Error("Failed to count stories for ListPublicSummaries", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка подсчета публичных историй: %w", err)
	}

	// Apply Sorting
	orderByClause := "ps.created_at DESC, ps.id DESC" // Default sort
	orderByColumn := "created_at"
	switch sortBy {
	case "created_at", "published_at": // Treat as the same for now
		orderByClause = "ps.created_at DESC, ps.id DESC"
		orderByColumn = "created_at"
	case "likes_count":
		orderByClause = "ps.likes_count DESC, ps.created_at DESC, ps.id DESC"
		orderByColumn = "likes_count"
	case "title":
		orderByClause = "ps.title ASC, ps.created_at DESC, ps.id DESC"
		orderByColumn = "title"
	// Add other sort options if needed
	default:
		r.logger.Warn("Invalid SortBy parameter for ListPublicSummaries, using default", zap.String("sortBy", sortBy))
	}
	logFields = append(logFields, zap.String("orderByActual", orderByClause))

	// Apply Cursor Pagination
	if limit <= 0 {
		limit = 20 // Default limit
	}
	fetchLimit := limit + 1

	// TODO: Refine cursor decoding/encoding based on the actual orderByColumn type
	cursorTime, cursorID, cursorErr := utils.DecodeCursor(cursor)
	if cursorErr != nil && cursor != "" {
		r.logger.Warn("Invalid cursor provided for ListPublicSummaries", zap.String("cursor", cursor), zap.Error(cursorErr))
		return nil, "", fmt.Errorf("invalid cursor: %w", cursorErr)
	}

	if cursor != "" {
		// Simple cursor logic assumes sorting by time/id
		// NEEDS ADJUSTMENT FOR OTHER SORT ORDERS (likes_count, title)
		queryBuilder.WriteString(fmt.Sprintf(" AND (ps.created_at, ps.id) < ($%d, $%d)", paramIndex, paramIndex+1))
		args = append(args, cursorTime, cursorID)
		paramIndex += 2
	}

	queryBuilder.WriteString(fmt.Sprintf(" ORDER BY %s LIMIT $%d", orderByClause, paramIndex))
	args = append(args, fetchLimit)
	logFields = append(logFields, zap.Int("fetchLimit", fetchLimit))

	// Execute Query
	finalQuery := queryBuilder.String()
	r.logger.Debug("Executing ListPublicSummaries query", append(logFields, zap.String("query", finalQuery))...)

	rows, err := r.db.Query(ctx, finalQuery, args...)
	if err != nil {
		r.logger.Error("Failed to query public story summaries", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка запроса публичных сводок историй: %w", err)
	}
	defer rows.Close()

	// Scan Results
	var lastCreatedAt time.Time // For cursor encoding
	var lastID uuid.UUID        // For cursor encoding

	for rows.Next() {
		// Use the helper that scans SummaryWithProgress
		summary, err := scanPublishedStorySummaryWithProgress(rows)
		if err != nil {
			r.logger.Error("Failed to scan public story summary row", append(logFields, zap.Error(err))...)
			return nil, "", fmt.Errorf("ошибка сканирования строки сводки истории: %w", err)
		}
		summaries = append(summaries, *summary) // Append value

		// Keep track of the last item's sort key for cursor generation
		// TODO: This only works correctly for created_at sorting
		if orderByColumn == "created_at" {
			lastCreatedAt = summary.PublishedAt
			lastID = summary.ID
		}
		// Add logic for other orderByColumn types if needed
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating public story summary rows", append(logFields, zap.Error(err))...)
		return nil, "", fmt.Errorf("ошибка итерации результатов запроса сводок: %w", err)
	}

	// Calculate Next Cursor
	nextCursor := ""
	if len(summaries) == fetchLimit {
		summaries = summaries[:limit] // Trim extra item
		// TODO: Encode cursor based on the actual orderByColumn and the last item's value
		if orderByColumn == "created_at" && lastID != uuid.Nil {
			nextCursor = utils.EncodeCursor(lastCreatedAt, lastID)
		}
		// Add encoding logic for other sort orders
	}

	r.logger.Debug("Successfully listed public story summaries", append(logFields, zap.Int("count", len(summaries)), zap.Int64("total", totalItems), zap.Bool("hasNext", nextCursor != ""))...)
	return summaries, nextCursor, nil // <<< Return correct types
}

// SearchPublic выполняет полнотекстовый поиск по общедоступным историям.
// !!! Проблема с типами пагинации/сортировки, временно используем placeholders !!!
// !!! Проблема с типом PublishedStorySearchResult, возвращаем []*models.PublishedStorySummaryWithProgress !!!
func (r *pgPublishedStoryRepository) SearchPublic(ctx context.Context, query string, userID *uuid.UUID /*, pagination *models.PaginationParams, filters *models.StoryFilters */) ([]*models.PublishedStorySummaryWithProgress /* *utils.PaginationInfo */, interface{}, error) {
	logFields := []zap.Field{zap.String("searchQuery", query)}
	if userID != nil {
		logFields = append(logFields, zap.String("userID", userID.String()))
	}
	r.logger.Debug("Searching public stories", logFields...)

	results := make([]*models.PublishedStorySummaryWithProgress, 0) // Change return type for now
	var args []interface{}

	if userID != nil {
		args = append(args, *userID) // $1 = userID
	} else {
		args = append(args, nil) // $1 = NULL::uuid
	}
	args = append(args, query) // $2 = search query term

	queryBuilder := strings.Builder{}
	queryBuilder.WriteString(searchPublicQueryBase)

	// Count Total
	countQueryBuilder := strings.Builder{}
	countQueryBuilder.WriteString(searchPublicCountQueryBase)
	countQuery := countQueryBuilder.String()

	countArgs := []interface{}{query} // Query is $1

	var totalItems int64
	err := r.db.QueryRow(ctx, countQuery, countArgs...).Scan(&totalItems)
	if err != nil {
		r.logger.Error("Failed to count stories for SearchPublic", append(logFields, zap.Error(err), zap.String("count_query", countQuery))...)
		return nil, nil, fmt.Errorf("ошибка подсчета результатов поиска: %w", err)
	}

	// Apply Sorting (Default)
	orderBy := "rank DESC"
	/* // Sorting logic commented out
	if pagination != nil && pagination.SortBy != "" {
		switch pagination.SortBy {
		case "rank":
			orderBy = "rank"
		case "created_at":
			orderBy = "rank DESC, ps.created_at"
		case "likes_count":
			orderBy = "rank DESC, ps.likes_count"
		default:
			r.logger.Warn("Invalid SortBy parameter for SearchPublic, using rank", zap.String("sortBy", pagination.SortBy))
		}
		if pagination.SortOrder == models.SortOrderAsc { // <<< TYPE ERROR
			orderBy += " ASC NULLS FIRST"
		} else {
			orderBy += " DESC NULLS LAST"
		}
	}
	*/
	queryBuilder.WriteString(fmt.Sprintf(" ORDER BY %s", orderBy))
	logFields = append(logFields, zap.String("orderBy", orderBy))

	// Apply Pagination (Default)
	limit := 10
	offset := 0
	/* // Pagination logic commented out
	limit, offset = pagination.GetLimitOffset()
	*/
	args = append(args, limit, offset)
	queryBuilder.WriteString(fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args)))
	logFields = append(logFields, zap.Int("limit", limit), zap.Int("offset", offset))

	// Execute Query
	finalQuery := queryBuilder.String()
	r.logger.Debug("Executing SearchPublic query", append(logFields, zap.String("query", finalQuery))...)

	rows, err := r.db.Query(ctx, finalQuery, args...)
	if err != nil {
		r.logger.Error("Failed to execute public story search query", append(logFields, zap.Error(err))...)
		return nil, nil, fmt.Errorf("ошибка выполнения поиска публичных историй: %w", err)
	}
	defer rows.Close()

	// Scan Results
	for rows.Next() {
		var tempSummary models.PublishedStorySummaryWithProgress
		var coverImageURL sql.NullString
		var publishedAt sql.NullTime
		var playerStatus sql.NullString
		var gameStateID sql.NullString
		var rank float32

		scanErr := rows.Scan(
			&tempSummary.ID,
			&tempSummary.Title,
			&tempSummary.ShortDescription,
			&tempSummary.AuthorID,
			&tempSummary.AuthorName,
			&publishedAt,
			&tempSummary.IsAdultContent,
			&tempSummary.LikesCount,
			&tempSummary.Status,
			&coverImageURL,
			&tempSummary.IsPublic,
			&tempSummary.IsLiked,
			&tempSummary.HasPlayerProgress,
			&playerStatus,
			&gameStateID,
			&rank, // Scan rank
		)
		if scanErr != nil {
			r.logger.Error("Failed to scan search result row manually", append(logFields, zap.Error(scanErr))...)
			return nil, nil, fmt.Errorf("ошибка сканирования строки результата поиска: %w", scanErr)
		}

		if coverImageURL.Valid {
			tempSummary.CoverImageURL = &coverImageURL.String
		}
		if publishedAt.Valid {
			tempSummary.PublishedAt = publishedAt.Time // Corrected assignment
		}
		if playerStatus.Valid {
			s := models.PlayerStatus(playerStatus.String)
			tempSummary.PlayerGameStatus = string(s) // Corrected: Assign string(s)
		}
		// GameStateID is not part of PublishedStorySummaryWithProgress in the model

		/* // Commented out SearchResult usage
		searchResult := &models.PublishedStorySearchResult{
			PublishedStorySummaryWithProgress: tempSummary,
			Rank:                              rank,
		}
		results = append(results, searchResult)
		*/
		results = append(results, &tempSummary) // Append the summary directly for now
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating search result rows", append(logFields, zap.Error(err))...)
		return nil, nil, fmt.Errorf("ошибка итерации результатов поиска: %w", err)
	}

	// Calculate PaginationInfo (Placeholder)
	var paginationInfo interface{} // Placeholder
	/* // Pagination calculation commented out
	paginationInfo := pagination.CalculateInfo(totalItems)
	*/
	r.logger.Debug("Successfully searched public stories", append(logFields, zap.Int("count", len(results)), zap.Int64("total", totalItems))...)
	return results, paginationInfo, nil
}
