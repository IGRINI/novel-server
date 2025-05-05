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
	var title, description, coverImageURL, errorDetails sql.NullString // Fields that are *string in the model
	var configBytes, setupBytes []byte                                 // For json.RawMessage fields

	// IMPORTANT: The order here MUST match the SELECT order in the query using this helper.
	// Assuming order based on fields in PublishedStory struct for now:
	// id, user_id, config, setup, status, language, is_public, is_adult_content,
	// title, description, cover_image_url, error_details, likes_count, created_at, updated_at,
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
		&coverImageURL,        // Scan into NullString
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
	if coverImageURL.Valid {
		story.CoverImageURL = &coverImageURL.String
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
func scanPublishedStorySummaryWithProgress(row pgx.Row) (*models.PublishedStorySummaryWithProgress, error) {
	var summary models.PublishedStorySummaryWithProgress
	var coverImageURL sql.NullString    // Handle nullable cover_image_url
	var playerGameStatus sql.NullString // Handle nullable player_game_status

	// Assuming order: ID, Title, ShortDescription, AuthorID, AuthorName, PublishedAt,
	// IsAdultContent, LikesCount, Status, CoverImageURL, IsPublic, IsLiked,
	// HasPlayerProgress, PlayerGameStatus
	err := row.Scan(
		&summary.ID,
		&summary.Title,
		&summary.ShortDescription, // Scan directly into ShortDescription (string)
		&summary.AuthorID,
		&summary.AuthorName,        // Scan directly into AuthorName (string)
		&summary.PublishedAt,       // Scan directly into PublishedAt (time.Time)
		&summary.IsAdultContent,    // Scan directly into IsAdultContent (bool)
		&summary.LikesCount,        // Scan directly into LikesCount (int64)
		&summary.Status,            // Scan directly into Status (StoryStatus -> string)
		&coverImageURL,             // Scan into NullString
		&summary.IsPublic,          // Scan directly into IsPublic (bool)
		&summary.IsLiked,           // Scan directly into IsLiked (bool)
		&summary.HasPlayerProgress, // Scan directly into HasPlayerProgress (bool)
		&playerGameStatus,          // Scan into NullString
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("ошибка сканирования строки PublishedStorySummaryWithProgress: %w", err)
	}

	// Assign scanned nullable values
	if coverImageURL.Valid {
		summary.CoverImageURL = &coverImageURL.String
	}
	if playerGameStatus.Valid {
		summary.PlayerGameStatus = playerGameStatus.String
	}

	return &summary, nil
}
