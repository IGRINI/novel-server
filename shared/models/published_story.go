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
	ID             uuid.UUID       `json:"id" db:"id"`
	UserID         uuid.UUID       `json:"user_id" db:"user_id"`       // ID автора истории
	Config         json.RawMessage `json:"config" db:"config"`         // Изначальный конфиг из драфта
	Setup          json.RawMessage `json:"setup,omitempty" db:"setup"` // Сгенерированный setup
	Status         StoryStatus     `json:"status" db:"status"`
	Language       string          `json:"language,omitempty" db:"language"` // <<< ДОБАВЛЕНО: Язык истории
	IsPublic       bool            `json:"is_public" db:"is_public"`
	IsAdultContent bool            `json:"is_adult_content" db:"is_adult_content"`
	Title          *string         `json:"title,omitempty" db:"title"`                 // Указатель, так как может быть NULL
	Description    *string         `json:"description,omitempty" db:"description"`     // Указатель, так как может быть NULL
	ErrorDetails   *string         `json:"error_details,omitempty" db:"error_details"` // Детали ошибки *первоначальной генерации*
	LikesCount     int64           `json:"likes_count" db:"likes_count"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at" db:"updated_at"`
	IsLiked        bool            `json:"is_liked" db:"-"` // Это поле заполняется на уровне запроса для конкретного пользователя

	// --- Флаги для отслеживания параллельной генерации ---
	IsFirstScenePending bool `json:"is_first_scene_pending" db:"is_first_scene_pending"` // True, если первая сцена еще не сгенерирована
	AreImagesPending    bool `json:"are_images_pending" db:"are_images_pending"`         // True, если изображения еще не сгенерированы
}

// CharacterDefinition represents a character described in the setup JSON.
// Uses specific json tags for compact storage.
type CharacterDefinition struct {
	Name        string `json:"n"`            // name
	Description string `json:"d"`            // description
	VisualTags  string `json:"vt,omitempty"` // visual_tags (English)
	Personality string `json:"p,omitempty"`  // personality (optional)
	Prompt      string `json:"pr,omitempty"` // prompt (English)
	ImageRef    string `json:"ir,omitempty"` // image_reference (the unique ID used for the image file/URL)
}

// NovelSetupContent defines the expected structure of the JSON stored in PublishedStory.Setup.
// Based on the AI prompt format.
type NovelSetupContent struct {
	CoreStatsDefinition     map[string]StatDefinition `json:"csd"`             // core_stats_definition
	Characters              []CharacterDefinition     `json:"chars,omitempty"` // characters (NEW)
	StoryPreviewImagePrompt string                    `json:"spi,omitempty"`   // <<< ДОБАВЛЕНО
	StorySummarySoFar       string                    `json:"sssf,omitempty"`  // Story summary so far of story start
	FutureDirection         string                    `json:"fd,omitempty"`    // Future direction for the first scene
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
	Description string             `json:"d"`            // description
	Initial     int                `json:"iv"`           // initial_value
	Go          GameOverConditions `json:"go"`           // game_over_conditions
	Icon        string             `json:"ic,omitempty"` // Icon name from the list
}

// PlayerPreferences defines player preferences within the Config.
// Matches the "pp" field in the narrator JSON output.
type PlayerPrefs struct {
	Themes            []string `json:"th,omitempty"` // Themes
	PlayerDescription string   `json:"dt,omitempty"` // Optional extra player details
	WorldLore         string   `json:"wl,omitempty"` // Optional world_lore
	DesiredLocations  string   `json:"dl,omitempty"` // Optional desired locations, comma-separated if multiple
	DesiredCharacters string   `json:"dc,omitempty"` // Optional desired characters, comma-separated if multiple
	Style             string   `json:"st,omitempty"` // Style (Visual/narrative, English)
}

// Config defines the structure expected within PublishedStory.Config (JSONB).
// Based on the AI prompt format (compressed keys where applicable).
type Config struct {
	Franchise              string            `json:"fr,omitempty"` // Franchise, if popular
	Genre                  string            `json:"gn,omitempty"` // Genre
	IsAdultContent         bool              `json:"ac,omitempty"` // Adult content flag
	Title                  string            `json:"t,omitempty"`  // Title generated by AI
	ShortDescription       string            `json:"sd,omitempty"` // Short description generated by AI
	ProtagonistName        string            `json:"pn,omitempty"` // Protagonist Name
	ProtagonistDescription string            `json:"pd,omitempty"` // Protagonist Description (main)
	WorldContext           string            `json:"wc,omitempty"` // World Context
	StorySummary           string            `json:"ss,omitempty"` // Story Summary
	PlayerPrefs            PlayerPrefs       `json:"pp,omitempty"` // Player Preferences struct
	CoreStats              map[string]string `json:"cs,omitempty"` // Core Stats: map of stat name to description
	// Поля sssf и fd не храним в конфиге, они генерируются на лету (или больше не используются).
}

type PublishedStorySummary struct {
	ID                uuid.UUID     `json:"id" db:"id"`
	Title             string        `json:"title" db:"title"`
	ShortDescription  string        `json:"short_description" db:"short_description"`
	AuthorID          uuid.UUID     `json:"author_id" db:"user_id"`
	AuthorName        string        `json:"author_name" db:"author_name"`
	PublishedAt       time.Time     `json:"published_at" db:"created_at"`
	IsAdultContent    bool          `json:"is_adult_content" db:"is_adult_content"`
	LikesCount        int64         `json:"likes_count" db:"likes_count"`
	IsLiked           bool          `json:"is_liked" db:"is_liked"`
	Status            StoryStatus   `json:"status" db:"status"`
	HasPlayerProgress bool          `json:"has_player_progress" db:"has_player_progress"`
	IsPublic          bool          `json:"is_public" db:"is_public"`
	PlayerGameStatus  string        `json:"player_game_status,omitempty" db:"player_game_status"`
	PlayerGameStateID uuid.NullUUID `json:"player_game_state_id,omitempty" db:"player_game_state_id"`
	LastPlayedAt      *time.Time    `json:"last_played_at,omitempty" db:"last_played_at"`
}
