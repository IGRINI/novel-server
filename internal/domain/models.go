package domain

import (
	"time"

	"github.com/google/uuid"
)

// Здесь будут определения основных моделей данных

// NovelGenerationRequest представляет запрос на генерацию новеллы
type NovelGenerationRequest struct {
	UserPrompt string `json:"user_prompt"` // Текстовое описание новеллы от пользователя
}

// NovelConfig представляет конфигурацию новеллы
type NovelConfig struct {
	Title             string `json:"title"`                // Краткое название новеллы для отображения в списке
	ShortDescription  string `json:"short_description"`    // Краткое описание для отображения в списке
	Franchise         string `json:"franchise"`            // Франшиза или сеттинг
	Genre             string `json:"genre"`                // Жанр
	Language          string `json:"language"`             // Язык
	IsAdultContent    bool   `json:"is_adult_content"`     // Содержит ли контент 18+
	PlayerName        string `json:"player_name"`          // Имя игрока
	PlayerGender      string `json:"player_gender"`        // Пол игрока
	EndingPreference  string `json:"ending_preference"`    // Предпочтительный тип концовки
	WorldContext      string `json:"world_context"`        // Контекст мира
	StorySummary      string `json:"story_summary"`        // Краткое описание истории
	StorySummarySoFar string `json:"story_summary_so_far"` // Текущее состояние истории
	FutureDirection   string `json:"future_direction"`     // Направление развития сюжета
	PlayerPreferences struct {
		Themes            []string `json:"themes"`             // Темы
		Style             string   `json:"style"`              // Стиль
		Tone              string   `json:"tone"`               // Тон
		DialogDensity     string   `json:"dialog_density"`     // Плотность диалогов
		ChoiceFrequency   string   `json:"choice_frequency"`   // Частота выборов
		PlayerDescription string   `json:"player_description"` // Описание игрока
		WorldLore         []string `json:"world_lore"`         // Лор мира
		DesiredLocations  []string `json:"desired_locations"`  // Желаемые локации
		DesiredCharacters []string `json:"desired_characters"` // Желаемые персонажи
	} `json:"player_preferences"`
	StoryConfig struct {
		Length           string `json:"length"`             // Длина истории
		CharacterCount   int    `json:"character_count"`    // Количество персонажей
		SceneEventTarget int    `json:"scene_event_target"` // Целевое количество событий в сцене
	} `json:"story_config"`
	RequiredOutput struct {
		IncludePrompts         bool `json:"include_prompts"`          // Включать промпты
		IncludeNegativePrompts bool `json:"include_negative_prompts"` // Включать негативные промпты
		GenerateBackgrounds    bool `json:"generate_backgrounds"`     // Генерировать фоны
		GenerateCharacters     bool `json:"generate_characters"`      // Генерировать персонажей
		GenerateStartScene     bool `json:"generate_start_scene"`     // Генерировать начальную сцену
	} `json:"required_output"`
	// ID                uuid.UUID         `json:"id"` // ID теперь берется из таблицы novels, не храним в JSON
}

// NovelGenerationResponse представляет ответ от API генерации новеллы
type NovelGenerationResponse struct {
	Config NovelConfig `json:"config"` // Сконфигурированные параметры новеллы
}

// NovelContentRequest представляет запрос на генерацию контента новеллы
type NovelContentRequest struct {
	NovelID               uuid.UUID   `json:"novel_id"`                           // ID новеллы, для которой генерируется контент
	UserID                string      `json:"user_id"`                            // ID пользователя (для авторизации и связи)
	UserChoice            *UserChoice `json:"user_choice,omitempty"`              // Выбор пользователя (если есть)
	RestartFromSceneIndex *int        `json:"restart_from_scene_index,omitempty"` // Индекс сцены, с которой нужно начать переигровку (опционально)
	// Config     NovelConfig `json:"config,omitempty"` // Убираем передачу Config
	// State      *NovelState `json:"state,omitempty"` // Убираем передачу State
}

// SimplifiedNovelContentRequest представляет упрощенный запрос на генерацию контента новеллы
type SimplifiedNovelContentRequest struct {
	NovelID               uuid.UUID   `json:"novel_id"`                           // ID новеллы, для которой генерируется контент
	UserChoice            *UserChoice `json:"user_choice,omitempty"`              // Выбор пользователя (если есть)
	RestartFromSceneIndex *int        `json:"restart_from_scene_index,omitempty"` // Индекс сцены, с которой нужно начать переигровку (опционально)
}

// UserChoice представляет выбор пользователя в новелле
type UserChoice struct {
	SceneIndex int    `json:"scene_index"`
	ChoiceText string `json:"choice_text"`
}

// NovelState представляет текущее состояние новеллы
type NovelState struct {
	StateHash            string                 `json:"-"` // SHA-256 хеш ключевых полей состояния (не сохраняется в state_data JSON)
	SceneCount           int                    `json:"scene_count"`
	CurrentSceneIndex    int                    `json:"current_scene_index"`
	WorldContext         string                 `json:"world_context,omitempty"` // Static world context from narrator
	OriginalStorySummary string                 `json:"-"`                       // Original summary from narrator config (not sent to client)
	StorySummary         string                 `json:"story_summary,omitempty"` // AI-generated summary (updated during setup/scenes)
	Language             string                 `json:"language"`
	PlayerName           string                 `json:"player_name"`
	PlayerGender         string                 `json:"player_gender"`
	EndingPreference     string                 `json:"ending_preference"`
	CurrentStage         string                 `json:"current_stage"`
	Backgrounds          []Background           `json:"backgrounds"`
	Characters           []Character            `json:"characters"`
	Scenes               []Scene                `json:"scenes,omitempty"`
	GlobalFlags          []string               `json:"global_flags"`
	Relationship         map[string]int         `json:"relationship"`
	StoryVariables       map[string]interface{} `json:"story_variables"`
	PreviousChoices      []string               `json:"previous_choices"`
	StoryBranches        []string               `json:"story_branches,omitempty"`
	StorySummarySoFar    string                 `json:"story_summary_so_far,omitempty"`
	FutureDirection      string                 `json:"future_direction,omitempty"`
	IsAdultContent       bool                   `json:"is_adult_content"`
}

// NovelMetadata представляет краткую информацию о новелле (для списков)
type NovelMetadata struct {
	NovelID          uuid.UUID `json:"novel_id"`
	UserID           string    `json:"user_id"`
	Title            string    `json:"title"`
	ShortDescription string    `json:"short_description"` // Краткое описание новеллы
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// NovelContentResponse представляет ответ на запрос генерации контента новеллы
type NovelContentResponse struct {
	State      NovelState  `json:"state"`
	NewContent interface{} `json:"new_content,omitempty"`
}

// SetupContent представляет контент, возвращаемый на этапе setup
type SetupContent struct {
	StorySummary string         `json:"story_summary"`
	Backgrounds  []Background   `json:"backgrounds"`
	Characters   []Character    `json:"characters"`
	Relationship map[string]int `json:"relationship"`
}

// SceneContent представляет контент, возвращаемый на этапе scene_ready
type SceneContent struct {
	BackgroundID string           `json:"background_id"`
	Characters   []SceneCharacter `json:"characters"`
	Events       []Event          `json:"events"`
}

// SceneCharacter представляет персонажа в конкретной сцене
type SceneCharacter struct {
	Name       string `json:"name"`
	Position   string `json:"position,omitempty"`
	Expression string `json:"expression,omitempty"`
}

// Background представляет фон для сцены
type Background struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Prompt         string `json:"prompt,omitempty"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
}

// Character представляет персонажа в новелле
type Character struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	VisualTags     []string `json:"visual_tags,omitempty"`
	Personality    string   `json:"personality,omitempty"`
	Position       string   `json:"position,omitempty"`
	Expression     string   `json:"expression,omitempty"`
	Prompt         string   `json:"prompt,omitempty"`
	NegativePrompt string   `json:"negative_prompt,omitempty"`
}

// Scene представляет сцену в новелле (для хранения)
type Scene struct {
	BackgroundID string  `json:"background_id"`
	Events       []Event `json:"events"`
}

// Event представляет событие в сцене
type Event struct {
	EventType   string                 `json:"event_type"`
	Speaker     string                 `json:"speaker,omitempty"`
	Text        string                 `json:"text,omitempty"`
	Character   string                 `json:"character,omitempty"`
	From        string                 `json:"from,omitempty"`
	To          string                 `json:"to,omitempty"`
	Description string                 `json:"description,omitempty"`
	Choices     []Choice               `json:"choices,omitempty"`
	Data        map[string]interface{} `json:"data,omitempty"`
}

// Choice представляет выбор в событии типа "choice"
type Choice struct {
	Text         string                 `json:"text"`
	Consequences map[string]interface{} `json:"consequences,omitempty"`
}

// Константы для current_stage
const (
	StageSetup      = "setup"
	StageSceneReady = "scene_ready"
	StageComplete   = "complete"
)

// Validate проверяет NovelConfig на наличие обязательных полей
func (c *NovelConfig) Validate() error {
	// Проверка обязательных полей
	if c.Franchise == "" {
		return NewValidationError("franchise is required")
	}
	if c.Genre == "" {
		return NewValidationError("genre is required")
	}
	if c.Language == "" {
		return NewValidationError("language is required")
	}
	if c.PlayerName == "" {
		return NewValidationError("player_name is required")
	}
	if c.PlayerGender == "" {
		return NewValidationError("player_gender is required")
	}
	return nil
}

// ValidationError представляет ошибку валидации
type ValidationError struct {
	Message string
}

// Error возвращает сообщение об ошибке
func (e ValidationError) Error() string {
	return e.Message
}

// NewValidationError создает новую ошибку валидации
func NewValidationError(message string) ValidationError {
	return ValidationError{Message: message}
}

// ListNovelsRequest представляет запрос на получение списка новелл с пагинацией
type ListNovelsRequest struct {
	Limit  int        `json:"limit,omitempty"`  // Количество элементов на странице (по умолчанию 10)
	Cursor *uuid.UUID `json:"cursor,omitempty"` // ID новеллы, с которой начинать следующую страницу
}

// ListNovelsResponse представляет ответ со списком новелл и информацией для пагинации
type ListNovelsResponse struct {
	Novels       []NovelListItem `json:"novels"`        // Список новелл
	HasMore      bool            `json:"has_more"`      // Есть ли еще новеллы после этой страницы
	NextCursor   *uuid.UUID      `json:"next_cursor"`   // ID для получения следующей страницы
	TotalResults int             `json:"total_results"` // Общее количество новелл
}

// NovelListItem представляет краткую информацию о новелле для списка
type NovelListItem struct {
	NovelID          uuid.UUID `json:"novel_id"`          // ID новеллы
	Title            string    `json:"title"`             // Название новеллы
	ShortDescription string    `json:"short_description"` // Краткое описание новеллы
	IsAdultContent   bool      `json:"is_adult_content"`  // Содержит ли контент 18+
	CreatedAt        time.Time `json:"created_at"`        // Дата создания
	UpdatedAt        time.Time `json:"updated_at"`        // Дата обновления
	IsSetuped        bool      `json:"is_setuped"`        // Был ли выполнен сетап новеллы

	// Поля для отображения прогресса пользователя
	IsStartedByUser       bool `json:"is_started_by_user"`                 // Начата ли новелла текущим пользователем
	CurrentUserSceneIndex *int `json:"current_user_scene_index,omitempty"` // Индекс последней сцены, достигнутой пользователем (nil если не начата)
	TotalScenesCount      int  `json:"total_scenes_count"`                 // Общее количество сцен в новелле (из конфигурации)
}

// NovelDetailsResponse представляет детальную информацию о новелле
type NovelDetailsResponse struct {
	NovelID          uuid.UUID   `json:"novel_id"`          // ID новеллы
	Title            string      `json:"title"`             // Название новеллы
	ShortDescription string      `json:"short_description"` // Краткое описание
	Genre            string      `json:"genre"`             // Жанр
	Language         string      `json:"language"`          // Язык
	WorldContext     string      `json:"world_context"`     // Описание мира
	EndingPreference string      `json:"ending_preference"` // Тип концовки
	PlayerName       string      `json:"player_name"`       // Имя главного персонажа
	PlayerGender     string      `json:"player_gender"`     // Пол главного персонажа
	PlayerDesc       string      `json:"player_desc"`       // Описание главного персонажа
	Style            string      `json:"style"`             // Стиль
	Tone             string      `json:"tone"`              // Тон
	Characters       []Character `json:"characters"`        // Персонажи из сетапа
	CreatedAt        time.Time   `json:"created_at"`        // Дата создания
	UpdatedAt        time.Time   `json:"updated_at"`        // Дата обновления
	ScenesCount      int         `json:"scenes_count"`      // Количество сцен
}

// SimplifiedNovelContentResponse представляет упрощенный ответ для клиента
type SimplifiedNovelContentResponse struct {
	CurrentSceneIndex int               `json:"current_scene_index"`
	BackgroundID      string            `json:"background_id"`              // ID фона для текущей сцены
	Characters        []SceneCharacter  `json:"characters"`                 // Список персонажей в сцене
	Events            []SimplifiedEvent `json:"events"`                     // События в сцене (упрощенная версия)
	HasNextScene      bool              `json:"has_next_scene"`             // Есть ли следующая сцена
	HasPreviousScene  bool              `json:"has_previous_scene"`         // Есть ли предыдущая сцена
	IsComplete        bool              `json:"is_complete"`                // Завершена ли история
	IsSetup           bool              `json:"is_setup"`                   // Является ли ответ этапом setup
	Summary           string            `json:"summary,omitempty"`          // Итоговое резюме истории (только при is_complete=true)
	Backgrounds       []Background      `json:"backgrounds,omitempty"`      // Список фонов (только при is_setup=true)
	SetupCharacters   []Character       `json:"setup_characters,omitempty"` // Список персонажей из этапа setup (только при is_setup=true)
}

// SimplifiedEvent представляет событие в сцене (версия для клиента без технических деталей)
type SimplifiedEvent struct {
	EventType   string               `json:"event_type"`
	Speaker     string               `json:"speaker,omitempty"`
	Text        string               `json:"text,omitempty"`
	Character   string               `json:"character,omitempty"`
	From        string               `json:"from,omitempty"`
	To          string               `json:"to,omitempty"`
	Description string               `json:"description,omitempty"`
	Choices     []SimplifiedChoice   `json:"choices,omitempty"`
	ChoiceID    string               `json:"choice_id,omitempty"` // Для inline_choice
	Responses   []SimplifiedResponse `json:"responses,omitempty"` // Для inline_response
}

// SimplifiedChoice представляет выбор в событии типа "choice" (версия для клиента)
type SimplifiedChoice struct {
	Text string `json:"text"`
	// Последствия (consequences) не передаются клиенту
}

// SimplifiedResponse представляет ответ на промежуточный выбор (для inline_response)
type SimplifiedResponse struct {
	ChoiceText     string            `json:"choice_text"`
	ResponseEvents []SimplifiedEvent `json:"response_events"`
}

// InlineResponseRequest представляет запрос на обработку встроенного (inline) ответа
type InlineResponseRequest struct {
	NovelID     uuid.UUID `json:"novel_id"`     // ID новеллы
	SceneIndex  int       `json:"scene_index"`  // Индекс текущей сцены
	ChoiceID    string    `json:"choice_id"`    // ID выбора
	ChoiceText  string    `json:"choice_text"`  // Текст выбора
	ResponseIdx int       `json:"response_idx"` // Индекс выбранного ответа
}

// InlineResponseResult представляет результат обработки inline_response
type InlineResponseResult struct {
	Success      bool               `json:"success"`                 // Успешно ли обработан запрос
	UpdatedState *NovelStateChanges `json:"updated_state,omitempty"` // Изменения в состоянии новеллы
	NextEvents   []SimplifiedEvent  `json:"next_events,omitempty"`   // События, которые нужно отобразить после выбора
}

// NovelStateChanges представляет только те изменения в состоянии, которые нужно отправить клиенту
type NovelStateChanges struct {
	GlobalFlags    []string               `json:"global_flags,omitempty"`    // Добавленные глобальные флаги
	Relationship   map[string]int         `json:"relationship,omitempty"`    // Изменения в отношениях
	StoryVariables map[string]interface{} `json:"story_variables,omitempty"` // Измененные переменные истории
}

// UserStoryProgress представляет динамические элементы прогресса пользователя в истории
type UserStoryProgress struct {
	NovelID           uuid.UUID              `json:"novel_id"`
	UserID            string                 `json:"user_id"`
	SceneIndex        int                    `json:"scene_index"`
	GlobalFlags       []string               `json:"global_flags"`
	Relationship      map[string]int         `json:"relationship"`
	StoryVariables    map[string]interface{} `json:"story_variables"`
	PreviousChoices   []string               `json:"previous_choices"`
	StorySummarySoFar string                 `json:"story_summary_so_far"`
	FutureDirection   string                 `json:"future_direction"`
	StateHash         string                 `json:"state_hash"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}
