package model

import (
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
// Теперь используем фиксированные пороги 0 и 100
type GameOverConditions struct {
	Min bool `json:"min"` // Завершается ли игра, если стат достиг 0?
	Max bool `json:"max"` // Завершается ли игра, если стат достиг 100?
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

// SceneBatch представляет кешированный батч сцены, связанный с хешем состояния
type SceneBatch struct {
	ID                uuid.UUID      `db:"id" json:"id"`                                     // Уникальный ID батча
	NovelID           uuid.UUID      `db:"novel_id" json:"novel_id"`                         // ID новеллы
	StateHash         string         `db:"state_hash" json:"state_hash"`                     // Хеш состояния, которое ПРИВЕЛО к этому батчу
	StorySummarySoFar string         `db:"story_summary_so_far" json:"story_summary_so_far"` // Обновленное summary
	FutureDirection   string         `db:"future_direction" json:"future_direction"`         // Обновленное направление
	Choices           []ChoiceOption `db:"choices" json:"choices,omitempty"`                 // JSON со списком ChoiceOption (ИЗМЕНЕНО)
	EndingText        *string        `db:"ending_text" json:"ending_text,omitempty"`         // Текст концовки (если есть)
	CreatedAt         time.Time      `db:"created_at" json:"created_at"`                     // Время создания записи
}

// NovelState представляет текущее состояние игрового процесса для пользователя в конкретной новелле
type NovelState struct {
	ID                 uuid.UUID              `db:"id" json:"id"`
	UserID             uuid.UUID              `db:"user_id" json:"user_id"`
	NovelID            uuid.UUID              `db:"novel_id" json:"novel_id"`
	CurrentBatchNumber int                    `db:"current_batch_number" json:"current_batch_number"`
	StorySummarySoFar  string                 `db:"story_summary_so_far" json:"story_summary_so_far"`
	FutureDirection    string                 `db:"future_direction" json:"future_direction"`
	StoryVariables     map[string]interface{} `db:"story_variables" json:"story_variables"` // Заменили CoreStats, GlobalFlags, StoryVariables
	History            []UserChoice           `db:"history" json:"history"`
	CreatedAt          time.Time              `db:"created_at" json:"created_at"`
	UpdatedAt          time.Time              `db:"updated_at" json:"updated_at"`
	HistoryChoices     map[int][]ChoiceOption `db:"history_choices" json:"history_choices"` // Не хранится напрямую в DB
}

// NarratorPromptRequest содержит запрос к нарратору ТОЛЬКО для генерации/модификации драфта
type NarratorPromptRequest struct {
	UserPrompt string       `json:"user_prompt"`           // Текстовый промпт пользователя
	PrevConfig *NovelConfig `json:"prev_config,omitempty"` // Опционально: Существующий конфиг для МОДИФИКАЦИИ
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
	NovelID uuid.UUID `json:"novel_id"`
	UserID  uuid.UUID `json:"user_id"` // Используем UUID
	// Добавляем UserChoice и ContinuationTopic для последующих запросов
	UserChoice        UserChoice `json:"user_choice,omitempty"`        // Выбор, сделанный пользователем в предыдущем батче
	ContinuationTopic *string    `json:"continuation_topic,omitempty"` // Тема для продолжения (опционально)
}

// UserChoice - информация о выборе пользователя
type UserChoice struct {
	BatchNumber int `json:"batch_number"` // Номер батча, в котором был сделан выбор
	ChoiceIndex int `json:"choice_index"` // Индекс выбранного варианта (0 или 1)
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

// --- Структуры для ответа клиенту ---

// InitialStatInfo содержит информацию для инициализации стата на клиенте
type InitialStatInfo struct {
	Description        string             `json:"description"`
	InitialValue       int                `json:"initial_value"`
	GameOverConditions GameOverConditions `json:"game_over_conditions"`
}

// FirstSceneResponse содержит данные для самой первой сцены
type FirstSceneResponse struct {
	Choices      []ChoiceOption             `json:"choices"`               // Используем ChoiceOption
	InitialStats map[string]InitialStatInfo `json:"initial_stats"`         // Добавлено поле
	EndingText   *string                    `json:"ending_text,omitempty"` // На случай, если первая сцена - концовка
}

// NextBatchResponse содержит данные для последующих сцен
type NextBatchResponse struct {
	Choices    []ChoiceOption `json:"choices,omitempty"`     // Используем ChoiceOption
	EndingText *string        `json:"ending_text,omitempty"` // Текст концовки, если игра завершена
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

// Объявляем ошибку "не найдено" ОДИН РАЗ здесь, чтобы использовать в репозитории и сервисах
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

// GameOverReason описывает причину проигрыша по статам
type GameOverReason struct {
	StatName  string `json:"stat_name" binding:"required"` // Название стата
	Condition string `json:"condition" binding:"required"` // Условие (например, "min" или "max")
	Value     int    `json:"value"`                        // Значение стата на момент проигрыша
}

// GameOverNotificationRequest содержит данные, отправляемые клиентом при Game Over
type GameOverNotificationRequest struct {
	Reason         GameOverReason         `json:"reason" binding:"required"`
	NovelID        uuid.UUID              `json:"novel_id" binding:"required"`
	FinalStateVars map[string]interface{} `json:"final_state_vars"`
}

// GameOverEndingRequestForAI содержит данные для AI генератора концовок
type GameOverEndingRequestForAI struct {
	NovelID        uuid.UUID              `json:"novel_id"`
	UserID         uuid.UUID              `json:"user_id"`
	Reason         GameOverReason         `json:"reason"`
	FinalStateVars map[string]interface{} `json:"final_state_vars"`
	NovelConfig    NovelConfig            `json:"novel_config"`
	NovelSetup     NovelSetup             `json:"novel_setup"`
	LastNovelState NovelState             `json:"last_novel_state"`
}

// GameOverEndingResponseFromAI определяет структуру ответа от AI генератора концовок
type GameOverEndingResponseFromAI struct {
	EndingText string `json:"ending_text" binding:"required"`
}

// --- Структуры для взаимодействия с AI Client ---

// AIRequest - общая структура запроса к AI клиенту
type AIRequest struct {
	NovelConfig       NovelConfig `json:"novel_config"`                 // Конфигурация новеллы
	PreviousState     *NovelState `json:"previous_state,omitempty"`     // Предыдущее состояние (для narrator)
	LastUserChoice    UserChoice  `json:"last_user_choice,omitempty"`   // Последний выбор пользователя (для narrator)
	ContinuationTopic *string     `json:"continuation_topic,omitempty"` // Тема для продолжения (для narrator)
}

// AIResponse - общая структура ответа от AI, которую ожидает сервис
// Может содержать либо Choices, либо EndingText
type AIResponse struct {
	StorySummarySoFar string         `json:"story_summary_so_far"`
	FutureDirection   string         `json:"future_direction"`
	Choices           []ChoiceOption `json:"choices,omitempty"`     // Выборы для следующего шага (используем уже существующий тип ChoiceOption)
	EndingText        *string        `json:"ending_text,omitempty"` // Текст концовки, если игра завершена
}
