package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"novel-server/shared/models"

	"github.com/jackc/pgx/v5"
	// "github.com/lib/pq" // pq.Array is not needed for scanning base PublishedStory
)

// scanPublishedStory сканирует одну строку в структуру models.PublishedStory.
// Обрабатывает pgx.ErrNoRows для QueryRow, возвращая models.ErrNotFound.
// Порядок полей ДОЛЖЕН СООТВЕТСТВОВАТЬ константе publishedStoryFields в основном файле репозитория.
func scanPublishedStory(row pgx.Row) (*models.PublishedStory, error) {
	var story models.PublishedStory
	var title, description, errorDetails sql.NullString // Fields that are *string in the model
	var configBytes, setupBytes []byte                  // For json.RawMessage fields

	// IMPORTANT: The order here MUST match the SELECT order in the query using this helper.
	// Assuming order based on fields in PublishedStory struct for now:
	// id, user_id, config, setup, status, language, is_public, is_adult_content,
	// title, description, error_details, likes_count, created_at, updated_at,
	// is_first_scene_pending, are_images_pending
	err := row.Scan(
		&story.ID,
		&story.UserID,
		&configBytes, // Scan into bytes first
		&setupBytes,  // Scan into bytes first
		&story.Status,
		&story.Language,       // Scan Language
		&story.IsPublic,       // Scan IsPublic
		&story.IsAdultContent, // Scan IsAdultContent
		&title,                // Scan into NullString
		&description,          // Scan into NullString
		&errorDetails,         // Scan into NullString
		&story.LikesCount,     // Scan LikesCount
		&story.CreatedAt,
		&story.UpdatedAt,
		&story.IsFirstScenePending, // Scan IsFirstScenePending
		&story.AreImagesPending,    // Scan AreImagesPending
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("ошибка сканирования строки PublishedStory: %w", err)
	}

	// Assign scanned nullable values
	if title.Valid {
		story.Title = &title.String
	}
	if description.Valid {
		story.Description = &description.String
	}
	if errorDetails.Valid {
		story.ErrorDetails = &errorDetails.String
	}

	// Assign json.RawMessage (handle potential NULL from DB)
	if len(configBytes) > 0 {
		story.Config = json.RawMessage(configBytes)
	} else {
		story.Config = nil // Or empty message: json.RawMessage("{}") ?
	}
	if len(setupBytes) > 0 {
		story.Setup = json.RawMessage(setupBytes)
	} else {
		story.Setup = nil
	}

	return &story, nil
}

// scanPublishedStorySummaryWithProgress сканирует одну строку в структуру models.PublishedStorySummaryWithProgress.
// Обрабатывает pgx.ErrNoRows для QueryRow, возвращая models.ErrNotFound.
// Порядок полей ДОЛЖЕН СООТВЕТСТВОВАТЬ запросу, использующему этот хелпер.
// !! ВАЖНО: Этот хелпер НЕ сканирует поле 'rank' для поисковых запросов. !!
func scanPublishedStorySummaryWithProgress(row pgx.Row) (*models.PublishedStorySummary, error) {
	var summary models.PublishedStorySummary
	var playerGameStatus sql.NullString // Handle nullable player_game_status

	// Assuming order based on updated publishedStorySummaryWithProgressFields:
	// ID, Title, ShortDescription, AuthorID, AuthorName, PublishedAt,
	// IsAdultContent, LikesCount, Status, /* REMOVED */ IsPublic, IsLiked,
	// HasPlayerProgress, PlayerGameStatus
	err := row.Scan(
		&summary.ID,
		&summary.Title,
		&summary.ShortDescription,  // Scan directly into ShortDescription (string) -> Maps to ps.description
		&summary.AuthorID,          // -> Maps to ps.user_id
		&summary.AuthorName,        // -> Maps to u.display_name
		&summary.PublishedAt,       // -> Maps to ps.created_at
		&summary.IsAdultContent,    // -> Maps to ps.is_adult_content
		&summary.LikesCount,        // 8 -> Maps to ps.likes_count
		&summary.IsLiked,           // 9 -> Maps to (sl.user_id IS NOT NULL)
		&summary.Status,            // 10 -> Maps to ps.status
		&summary.HasPlayerProgress, // 11 -> Maps to (pgs.player_progress_id IS NOT NULL)
		&summary.IsPublic,          // 12 -> Maps to ps.is_public
		&playerGameStatus,          // 13 -> Maps to pgs.player_status
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("ошибка сканирования строки PublishedStorySummaryWithProgress: %w", err)
	}

	if playerGameStatus.Valid {
		summary.PlayerGameStatus = playerGameStatus.String
	}

	return &summary, nil
}
