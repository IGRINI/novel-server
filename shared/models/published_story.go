package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// StoryStatus определяет возможные статусы опубликованной истории.
// Совпадает с типом ENUM 'story_status' в БД.
type StoryStatus string

const (
	StatusSetupPending      StoryStatus = "setup_pending"       // Ожидает генерации Setup
	StatusSetupGenerating   StoryStatus = "setup_generating"    // Идет генерация Setup
	StatusFirstScenePending StoryStatus = "first_scene_pending" // Setup готов, ожидает генерации 1й сцены
	StatusGeneratingScene   StoryStatus = "generating_scene"    // Идет генерация следующей сцены
	StatusReady             StoryStatus = "ready"               // Готова к игре (Setup и 1я сцена сгенерированы)
	StatusGameOverPending   StoryStatus = "game_over_pending"   // Ожидает генерации концовки
	StatusError             StoryStatus = "error"               // Ошибка при генерации Setup или сцены
	StatusCompleted         StoryStatus = "completed"           // Игра завершена (концовка сгенерирована)
	StatusDraft             StoryStatus = "draft"               // Черновик, доступен для редактирования
	StatusGenerating        StoryStatus = "generating"          // Идет генерация (начальная или ревизия)
	StatusRevising          StoryStatus = "revising"            // (Возможно) Отдельный статус для ревизии, если нужно
	// StatusAvailable         StoryStatus = "available" // Возможно, этот статус не нужен?
)

// PublishedStory представляет опубликованную историю в базе данных.
type PublishedStory struct {
	ID             uuid.UUID       `json:"id" db:"id"`
	UserID         uuid.UUID       `json:"user_id" db:"user_id"`       // Или uuid.UUID, если User.ID - UUID
	Config         json.RawMessage `json:"config" db:"config"`         // Изначальный конфиг из драфта
	Setup          json.RawMessage `json:"setup,omitempty" db:"setup"` // Сгенерированный setup
	Status         StoryStatus     `json:"status" db:"status"`
	EndingText     *string         `json:"ending_text,omitempty" db:"ending_text"` // Текст концовки (если StatusCompleted)
	IsPublic       bool            `json:"is_public" db:"is_public"`
	IsAdultContent bool            `json:"is_adult_content" db:"is_adult_content"`
	Title          *string         `json:"title,omitempty" db:"title"`             // Указатель, так как может быть NULL
	Description    *string         `json:"description,omitempty" db:"description"` // Указатель, так как может быть NULL
	CoverImageURL  *string         `json:"cover_image_url,omitempty" db:"cover_image_url"`
	ErrorDetails   *string         `json:"error_details,omitempty" db:"error_details"` // Указатель, так как может быть NULL
	LikesCount     int64           `json:"likes_count" db:"likes_count"`               // Добавляем счетчик лайков
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at" db:"updated_at"`
	IsLiked        bool            `json:"is_liked" db:"-"`
}

// CharacterDefinition defines the structure for a character within NovelSetupContent.
type CharacterDefinition struct {
	Name        string   `json:"n"`            // name
	Description string   `json:"d"`            // description
	VisualTags  []string `json:"vt,omitempty"` // visual_tags (English)
	Personality string   `json:"p,omitempty"`  // personality (optional)
	Prompt      string   `json:"pr,omitempty"` // prompt (English)
	NegPrompt   string   `json:"np,omitempty"` // negative_prompt (English)
	ImageRef    string   `json:"ir,omitempty"` // image_reference (English, snake_case)
}

// NovelSetupContent defines the expected structure of the JSON stored in PublishedStory.Setup.
// Based on the AI prompt format.
type NovelSetupContent struct {
	CoreStatsDefinition map[string]StatDefinition `json:"csd"`             // core_stats_definition
	Characters          []CharacterDefinition     `json:"chars,omitempty"` // characters (NEW)
	// TODO: Добавить другие поля из setup по мере необходимости (backgrounds и т.д.)
}

// GameOverConditions defines the game over conditions based on min/max values.
// Matches the "go" field in the setup JSON.
type GameOverConditions struct {
	Min bool `json:"min"`
	Max bool `json:"max"`
}

// StatDefinition defines the properties of a core stat, as defined in the setup JSON.
type StatDefinition struct {
	Description        string             `json:"d"`            // description
	Initial            int                `json:"iv"`           // initial_value
	GameOverConditions GameOverConditions `json:"go"`           // game_over_conditions
	Icon               string             `json:"ic,omitempty"` // Icon name from the list
}

// Config defines the structure expected within PublishedStory.Config (JSONB).
// Based on the AI prompt format (compressed keys where applicable).
type Config struct {
	Language         string `json:"ln,omitempty"` // Language code (e.g., "en", "ru")
	Genre            string `json:"gn,omitempty"` // Genre
	IsAdultContent   bool   `json:"ac,omitempty"` // Adult content flag
	Title            string `json:"t,omitempty"`  // Title generated by AI
	ShortDescription string `json:"sd,omitempty"` // Short description generated by AI
	PlayerName       string `json:"pn,omitempty"` // Player Name
	// Add other fields from the config format as needed (e.g., pp, wc, chars)
	// PlayerPreferences PlayerPreferences `json:"pp,omitempty"`
	// WorldContext      string `json:"wc,omitempty"`
}

// TODO: Define PlayerPreferences struct if needed

// PublishedStorySummary provides a concise view of a published story, often used in lists.
type PublishedStorySummary struct {
	ID               uuid.UUID   `json:"id"`
	Title            string      `json:"title"`
	ShortDescription string      `json:"short_description"` // Changed from Description
	AuthorID         uuid.UUID   `json:"author_id"`
	AuthorName       string      `json:"author_name"`
	PublishedAt      time.Time   `json:"published_at"`
	IsAdultContent   bool        `json:"is_adult_content"`
	LikesCount       int64       `json:"likes_count"` // Added LikesCount
	IsLiked          bool        `json:"is_liked"`    // Added IsLiked (specific to user context)
	Status           StoryStatus `json:"status"`      // Added Status
}

// PublishedStorySummaryWithProgress extends PublishedStorySummary with player progress info.
type PublishedStorySummaryWithProgress struct {
	PublishedStorySummary
	HasPlayerProgress bool `json:"hasPlayerProgress"`
}

// StatRule defines conditions for game over based on core stats.
// DEPRECATED: Use StatDefinition instead.
