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
	// is_first_scene_pending, are_images_pending, internal_generation_step, pending_char_gen_tasks, pending_card_img_tasks, pending_char_img_tasks
	err := row.Scan(
		&story.ID,
		&story.UserID,
		&configBytes, // Scan into bytes first
		&setupBytes,  // Scan into bytes first
		&story.Status,
		&story.InternalGenerationStep,
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
		&story.PendingCharGenTasks,
		&story.PendingCardImgTasks,
		&story.PendingCharImgTasks,
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

	// Сканиурем только поля, которые есть в модели PublishedStorySummary
	err := row.Scan(
		&summary.ID,
		&summary.Title,
		&summary.ShortDescription,
		&summary.AuthorID,
		&summary.AuthorName,
		&summary.PublishedAt,
		&summary.IsAdultContent,
		&summary.LikesCount,
		&summary.IsLiked,
		&summary.Status,
		&summary.HasPlayerProgress,
		&summary.IsPublic,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("ошибка сканирования строки PublishedStorySummaryWithProgress: %w", err)
	}
	return &summary, nil
}
