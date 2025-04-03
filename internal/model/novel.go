package model

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Novel представляет игровую новеллу
type Novel struct {
	ID          uuid.UUID   `json:"id" db:"id"`
	Title       string      `json:"title" db:"title"`
	Description string      `json:"description" db:"description"`
	AuthorID    uuid.UUID   `json:"author_id" db:"author_id"`
	IsPublic    bool        `json:"is_public" db:"is_public"`
	CoverImage  *string     `json:"cover_image" db:"cover_image"`
	Tags        []string    `json:"tags" db:"tags"`
	Config      NovelConfig `json:"config" db:"config"`
	Setup       NovelSetup  `json:"setup" db:"setup"`
	CreatedAt   time.Time   `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at" db:"updated_at"`
	PublishedAt *time.Time  `json:"published_at,omitempty" db:"published_at"`
}

// NovelConfig содержит базовую конфигурацию новеллы, соответствующую выводу narrator.md
type NovelConfig struct {
	Title             string              `json:"title"`
	ShortDescription  string              `json:"short_description"` // Renamed from Description
	Franchise         string              `json:"franchise"`
	Genre             string              `json:"genre"`
	Language          string              `json:"language"`
	IsAdultContent    bool                `json:"is_adult_content"`     // Optional in narrator.md, but kept here
	PlayerName        string              `json:"player_name"`          // Added
	PlayerGender      string              `json:"player_gender"`        // Added
	PlayerDescription string              `json:"player_description"`   // Added
	EndingPreference  string              `json:"ending_preference"`    // Added
	WorldContext      string              `json:"world_context"`        // Optional in narrator.md, but kept here
	StorySummary      string              `json:"story_summary"`        // Renamed from StorySum
	StorySummarySoFar string              `json:"story_summary_so_far"` // Added
	FutureDirection   string              `json:"future_direction"`     // Added
	CoreStats         map[string]CoreStat `json:"core_stats"`
	PlayerPrefs       PlayerPreferences   `json:"player_preferences"`
	StoryConfig       StoryConfig         `json:"story_config"` // Added
	// Removed RequiredOutput field
	// Removed Setting, Characters
	// Moved Themes to PlayerPreferences
}

// CoreStat определяет характеристику в игре
type CoreStat struct {
	Description        string             `json:"description"`
	InitialValue       int                `json:"initial_value"`
	GameOverConditions GameOverConditions `json:"game_over_conditions"`
}

// ToView конвертирует CoreStat в CoreStatView
func (s CoreStat) ToView() CoreStatView {
	return CoreStatView{
		Description:        s.Description,
		InitialValue:       s.InitialValue,
		GameOverConditions: s.GameOverConditions,
	}
}

// GameOverConditions определяет условия проигрыша (min/max)
// Структура соответствует тому, что возвращает AI нарратор
type GameOverConditions struct {
	Min bool `json:"min"`
	Max bool `json:"max"`
}

// PlayerPreferences содержит предпочтения игрока
type PlayerPreferences struct {
	Themes               []string `json:"themes"` // Moved from NovelConfig
	Style                string   `json:"style"`
	Tone                 string   `json:"tone"`               // Added
	DialogDensity        string   `json:"dialog_density"`     // Added
	ChoiceFrequency      string   `json:"choice_frequency"`   // Added
	WorldLore            []string `json:"world_lore"`         // Added
	DesiredLocations     []string `json:"desired_locations"`  // Added
	DesiredCharacters    []string `json:"desired_characters"` // Added (replaces NovelConfig.Characters)
	CharacterVisualStyle string   `json:"character_visual_style"`
	// Note: Removed player_description from here as it's ambiguous with the top-level one.
}

// StoryConfig содержит технические параметры истории
type StoryConfig struct {
	Length           string `json:"length"`
	CharacterCount   int    `json:"character_count"`
	SceneEventTarget int    `json:"scene_event_target"`
}

// NovelSetup содержит полную настройку новеллы после обработки
type NovelSetup struct {
	CoreStatsDefinition map[string]CoreStatDefinition `json:"core_stats_definition"`
	Characters          []CharacterSetup              `json:"characters"`
}

// CoreStatDefinition содержит описание характеристики после настройки
type CoreStatDefinition struct {
	InitialValue       int                `json:"initial_value"`
	Description        string             `json:"description"`
	GameOverConditions GameOverConditions `json:"game_over_conditions"`
}

// CharacterSetup содержит настройку персонажа
type CharacterSetup struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	VisualTags     []string `json:"visual_tags"`
	Personality    string   `json:"personality,omitempty"`
	Prompt         string   `json:"prompt"`
	NegativePrompt string   `json:"negative_prompt"`
}

// SceneBatch представляет батч событий, сгенерированный ИИ
type SceneBatch struct {
	ID                uuid.UUID       `json:"id" db:"id"`
	NovelID           uuid.UUID       `json:"novel_id" db:"novel_id"`
	BatchNumber       int             `json:"batch_number" db:"batch_number"`
	StorySummarySoFar string          `json:"story_summary_so_far" db:"story_summary_so_far"`
	FutureDirection   string          `json:"future_direction" db:"future_direction"`
	Choices           json.RawMessage `json:"choices" db:"choices"` // Храним JSON как есть
	CreatedAt         time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at" db:"updated_at"`
}

// NovelState содержит текущее состояние игры
type NovelState struct {
	ID                 uuid.UUID              `json:"id" db:"id"`
	UserID             uuid.UUID              `json:"user_id" db:"user_id"`
	NovelID            uuid.UUID              `json:"novel_id" db:"novel_id"`
	CurrentBatchNumber int                    `json:"current_batch_number" db:"current_batch_number"` // Добавлено
	StorySummarySoFar  string                 `json:"story_summary_so_far" db:"story_summary_so_far"`
	FutureDirection    string                 `json:"future_direction" db:"future_direction"`
	CoreStats          map[string]int         `json:"core_stats" db:"core_stats"`
	GlobalFlags        []string               `json:"global_flags" db:"global_flags"`
	StoryVariables     map[string]interface{} `json:"story_variables" db:"story_variables"`
	History            []uuid.UUID            `json:"history" db:"history"` // Возможно, стоит хранить историю выборов или ID батчей?
	CreatedAt          time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time              `json:"updated_at" db:"updated_at"`
}

// NarratorPromptRequest содержит запрос к нарратору для генерации/модификации драфта
type NarratorPromptRequest struct {
	UserPrompt string       `json:"user_prompt" binding:"required"` // Для первой генерации или текст модификации
	PrevConfig *NovelConfig `json:"prev_config,omitempty"`          // Предыдущая конфигурация для модификации
}

// SetupNovelRequest содержит запрос на установку новеллы из драфта
type SetupNovelRequest struct {
	DraftID     uuid.UUID   `json:"draft_id" binding:"required"`
	NovelConfig NovelConfig `json:"novel_config" binding:"required"`
}

// GenerateNovelRequest содержит запрос на генерацию новеллы
type GenerateNovelRequest struct {
	UserPrompt string `json:"user_prompt" binding:"required"`
}

// ClientGameplayPayload represents data sent to the client after generating content
type ClientGameplayPayload struct {
	Choices             []ChoiceEvent                 `json:"choices"`                         // Array of available events/situations
	CoreStats           map[string]int                `json:"core_stats,omitempty"`            // Initial core stats (sent only with the first response)
	CoreStatsDefinition map[string]CoreStatDefinition `json:"core_stats_definition,omitempty"` // Definitions for game over checks (sent only with the first response)
	IsGameOver          bool                          `json:"is_game_over,omitempty"`          // Game over flag
	EndingText          string                        `json:"ending_text,omitempty"`           // Ending text if there is one
}

// ClientCalculatedState represents the final state calculated by the client after processing a batch
type ClientCalculatedState struct {
	CoreStats      map[string]int         `json:"core_stats"`
	GlobalFlags    []string               `json:"global_flags"`
	StoryVariables map[string]interface{} `json:"story_variables"`
	// Можно добавить ID последнего события или историю выборов батча, если нужно серверу
}

// GenerateNovelContentRequest содержит запрос на генерацию КОНТЕНТА для новеллы
// Теперь принимает рассчитанное клиентом состояние
type GenerateNovelContentRequest struct {
	UserID      string                `json:"-"` // Берется из токена, не из тела запроса
	NovelID     uuid.UUID             `json:"novel_id" binding:"required"`
	ClientState ClientCalculatedState `json:"client_state" binding:"required"` // Рассчитанное состояние от клиента
	// Убираем поля Config, Setup, NovelState, UserChoice, т.к. они больше не нужны в запросе контента
}

// ModifyNovelDraftRequest содержит запрос на модификацию существующего драфта
type ModifyNovelDraftRequest struct {
	ModificationPrompt string `json:"modification_prompt" binding:"required"`
}

// GenerateFirstSceneRequest содержит данные для генерации первой сцены
type GenerateFirstSceneRequest struct {
	Config NovelConfig `json:"config"`
	Setup  NovelSetup  `json:"setup"`
}

// UserChoice содержит выбор, сделанный пользователем
type UserChoice struct {
	ChoiceID uuid.UUID `json:"choice_id" binding:"required"`
	Text     string    `json:"text" binding:"required"`
}

// GenerateResponse содержит результат генерации
type GenerateResponse struct {
	State      NovelState             `json:"state"`
	NewContent map[string]interface{} `json:"new_content"`
	TaskID     uuid.UUID              `json:"task_id,omitempty"`
}

// TaskStatus содержит статус задачи по генерации
type TaskStatus struct {
	ID        uuid.UUID   `json:"id"`
	Status    string      `json:"status"`
	Progress  int         `json:"progress"`
	Message   string      `json:"message,omitempty"`
	Result    interface{} `json:"result,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// NovelDraft представляет сохраненный черновик новеллы
type NovelDraft struct {
	ID         uuid.UUID   `json:"id" db:"id"`
	UserID     uuid.UUID   `json:"user_id" db:"user_id"`
	Config     NovelConfig `json:"config" db:"config"` // Встраиваем конфигурацию
	UserPrompt string      `json:"user_prompt" db:"user_prompt"`
	CreatedAt  time.Time   `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time   `json:"updated_at" db:"updated_at"`
}

// CoreStatView представляет упрощенное представление стата для клиента
type CoreStatView struct {
	Description        string             `json:"description"`
	InitialValue       int                `json:"initial_value"`
	GameOverConditions GameOverConditions `json:"game_over_conditions"`
}

// NovelDraftView представляет урезанную версию конфигурации новеллы для отправки клиенту
// после успешной генерации черновика.
type NovelDraftView struct {
	ID                uuid.UUID               `json:"draft_id"`
	Title             string                  `json:"title"`
	ShortDescription  string                  `json:"short_description"`
	Franchise         string                  `json:"franchise"`
	Genre             string                  `json:"genre"`
	IsAdultContent    bool                    `json:"is_adult_content"`
	PlayerName        string                  `json:"player_name"`
	PlayerGender      string                  `json:"player_gender"`
	PlayerDescription string                  `json:"player_description"`
	WorldContext      string                  `json:"world_context"`
	CoreStats         map[string]CoreStatView `json:"core_stats"`
	Themes            []string                `json:"themes"`
}

// FirstSceneResponse defines the structure expected from novel_first_scene_creator.md
type FirstSceneResponse struct {
	StorySummarySoFar string          `json:"story_summary_so_far"`
	FutureDirection   string          `json:"future_direction"`
	Choices           []ChoiceDetails `json:"choices"`
}

// NextBatchResponse defines the structure expected from novel_creator.md for standard gameplay
type NextBatchResponse struct {
	StorySummarySoFar string          `json:"story_summary_so_far"`
	FutureDirection   string          `json:"future_direction"`
	Choices           []ChoiceDetails `json:"choices"`
}

// ChoiceDetails represents a single choice event in the response
type ChoiceDetails struct {
	Description string         `json:"description"`
	Choices     []ChoiceOption `json:"choices"`
	Shuffleable *bool          `json:"shuffleable,omitempty"` // Use pointer for optional boolean with default true
}

// ChoiceOption represents a single choice event in the response
type ChoiceOption struct {
	Text         string       `json:"text"`
	Consequences Consequences `json:"consequences"`
}

// ChoiceEvent represents a single choice event in the response
type ChoiceEvent struct {
	Description string         `json:"description"`
	Choices     []ChoiceOption `json:"choices"`               // Always should be two elements
	Shuffleable *bool          `json:"shuffleable,omitempty"` // Pointer to omit if not set
}

// Consequences define the outcome of choosing an option
type Consequences struct {
	CoreStatsChange map[string]int         `json:"core_stats_change"`
	GlobalFlags     []string               `json:"global_flags,omitempty"`
	StoryVariables  map[string]interface{} `json:"story_variables,omitempty"`
	ResponseText    string                 `json:"response_text,omitempty"`
}

// CharacterView represents a simplified character representation for the client
type CharacterView struct {
	ID          uuid.UUID `json:"id"` // ID from Character
	Name        string    `json:"name"`
	Description string    `json:"description"`
	AvatarImage *string   `json:"avatar_image,omitempty"` // Path to avatar (not implemented yet)
}

// Объявляем ошибку "не найдено", чтобы использовать в репозитории
var ErrNotFound = errors.New("запись не найдена")

// GenerateNovelContentRequestForAI содержит данные для генерации контента
// Используется внутри сервиса для вызова AI
type GenerateNovelContentRequestForAI struct {
	NovelState NovelState  `json:"novel_state"`
	Config     NovelConfig `json:"config"`
	Setup      NovelSetup  `json:"setup"`
}

// GenerateNovelSetupRequestForAI содержит данные для генерации setup
// Используется внутри сервиса для вызова AI
type GenerateNovelSetupRequestForAI struct {
	NovelConfig NovelConfig `json:"novel_config"`
}
