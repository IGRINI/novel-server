package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// StoryStatus определяет возможные статусы опубликованной истории как шаблона.
// Совпадает с типом ENUM 'story_status' в БД.
type StoryStatus string

const (
	StatusDraft             StoryStatus = "draft"               // Черновик, доступен для редактирования
	StatusSetupPending      StoryStatus = "setup_pending"       // Ожидает генерации Setup
	StatusSetupGenerating   StoryStatus = "setup_generating"    // Идет генерация Setup
	StatusFirstScenePending StoryStatus = "first_scene_pending" // Setup готов, ожидает генерации 1й сцены
	// StatusGeneratingScene   StoryStatus = "generating_scene"    // УДАЛЕНО: Статус генерации сцены для игрока
	StatusInitialGeneration StoryStatus = "initial_generation" // Идет первоначальная генерация (Setup и/или 1я сцена)
	StatusGenerating        StoryStatus = "generating"         // Идет генерация (для черновика StoryConfig)
	StatusReady             StoryStatus = "ready"              // Готова к игре (Setup и 1я сцена сгенерированы успешно)
	// StatusGameOverPending   StoryStatus = "game_over_pending"   // УДАЛЕНО: Статус ожидания концовки для игрока
	StatusError StoryStatus = "error" // Ошибка при первоначальной генерации Setup или 1й сцены
	// StatusCompleted         StoryStatus = "completed"           // УДАЛЕНО: Статус завершения игры для игрока
	// StatusRevising          StoryStatus = "revising"            // (Возможно) Отдельный статус для ревизии, если нужно - пока убрал для ясности
)

// PublishedStory представляет опубликованную историю в базе данных.
type PublishedStory struct {
	ID     uuid.UUID       `json:"id" db:"id"`
	UserID uuid.UUID       `json:"user_id" db:"user_id"`       // ID автора истории
	Config json.RawMessage `json:"config" db:"config"`         // Изначальный конфиг из драфта
	Setup  json.RawMessage `json:"setup,omitempty" db:"setup"` // Сгенерированный setup
	Status StoryStatus     `json:"status" db:"status"`
	// EndingText     *string         `json:"ending_text,omitempty" db:"ending_text"` // УДАЛЕНО: Концовка специфична для игрока
	IsPublic       bool      `json:"is_public" db:"is_public"`
	IsAdultContent bool      `json:"is_adult_content" db:"is_adult_content"`
	Title          *string   `json:"title,omitempty" db:"title"`             // Указатель, так как может быть NULL
	Description    *string   `json:"description,omitempty" db:"description"` // Указатель, так как может быть NULL
	CoverImageURL  *string   `json:"cover_image_url,omitempty" db:"cover_image_url"`
	ErrorDetails   *string   `json:"error_details,omitempty" db:"error_details"` // Детали ошибки *первоначальной генерации*
	LikesCount     int64     `json:"likes_count" db:"likes_count"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
	IsLiked        bool      `json:"is_liked" db:"-"` // Это поле заполняется на уровне запроса для конкретного пользователя
}

// CharacterDefinition represents a character described in the setup JSON.
// Uses specific json tags for compact storage.
type CharacterDefinition struct {
	Name        string   `json:"n"`            // name
	Description string   `json:"d"`            // description
	VisualTags  []string `json:"vt,omitempty"` // visual_tags (English)
	Personality string   `json:"p,omitempty"`  // personality (optional)
	Prompt      string   `json:"pr,omitempty"` // prompt (English)
	NegPrompt   string   `json:"np,omitempty"` // negative_prompt (English)
	ImageRef    string   `json:"ir,omitempty"` // image_reference (the unique ID used for the image file/URL)
}

// NovelSetupContent defines the expected structure of the JSON stored in PublishedStory.Setup.
// Based on the AI prompt format.
type NovelSetupContent struct {
	CoreStatsDefinition     map[string]StatDefinition `json:"csd"`             // core_stats_definition
	Characters              []CharacterDefinition     `json:"chars,omitempty"` // characters (NEW)
	StoryPreviewImagePrompt string                    `json:"spi,omitempty"`   // <<< ДОБАВЛЕНО
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
