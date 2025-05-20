package domain

import "github.com/google/uuid"

// NovelContentRequest представляет запрос на генерацию контента новеллы
// упрощенный клиентом формат.
type NovelContentRequest struct {
	NovelID               uuid.UUID   `json:"novel_id"`
	UserID                string      `json:"user_id"`
	UserChoice            *UserChoice `json:"user_choice,omitempty"`
	RestartFromSceneIndex *int        `json:"restart_from_scene_index,omitempty"`
}

type SimplifiedNovelContentRequest struct {
	NovelID               uuid.UUID   `json:"novel_id"`
	UserChoice            *UserChoice `json:"user_choice,omitempty"`
	RestartFromSceneIndex *int        `json:"restart_from_scene_index,omitempty"`
}

// UserChoice представляет выбор пользователя в новелле
type UserChoice struct {
	SceneIndex int    `json:"scene_index"`
	ChoiceText string `json:"choice_text"`
}

// NovelContentResponse представляет ответ на запрос генерации контента
// State содержит обновленное состояние, а NewContent - новый контент
// (setup или сцену)
type NovelContentResponse struct {
	State      NovelState  `json:"state"`
	NewContent interface{} `json:"new_content,omitempty"`
}

// SetupContent представляет данные, возвращаемые на этапе setup
// содержит базовый список персонажей и фонов.
type SetupContent struct {
	StorySummary string         `json:"story_summary"`
	Backgrounds  []Background   `json:"backgrounds"`
	Characters   []Character    `json:"characters"`
	Relationship map[string]int `json:"relationship"`
}

type SceneContent struct {
	BackgroundID string           `json:"background_id"`
	Characters   []SceneCharacter `json:"characters"`
	Events       []Event          `json:"events"`
}

type SceneCharacter struct {
	Name       string `json:"name"`
	Position   string `json:"position,omitempty"`
	Expression string `json:"expression,omitempty"`
}

type Background struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Prompt         string `json:"prompt,omitempty"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
}

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

type Scene struct {
	BackgroundID string  `json:"background_id"`
	Events       []Event `json:"events"`
}

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

type Choice struct {
	Text         string                 `json:"text"`
	Consequences map[string]interface{} `json:"consequences,omitempty"`
}

const (
	StageSetup      = "setup"
	StageSceneReady = "scene_ready"
	StageComplete   = "complete"
)

// Simplified structures for client

type SimplifiedNovelContentResponse struct {
	CurrentSceneIndex int               `json:"current_scene_index"`
	BackgroundID      string            `json:"background_id"`
	Characters        []SceneCharacter  `json:"characters"`
	Events            []SimplifiedEvent `json:"events"`
	HasNextScene      bool              `json:"has_next_scene"`
	HasPreviousScene  bool              `json:"has_previous_scene"`
	IsComplete        bool              `json:"is_complete"`
	IsSetup           bool              `json:"is_setup"`
	Summary           string            `json:"summary,omitempty"`
	Backgrounds       []Background      `json:"backgrounds,omitempty"`
	SetupCharacters   []Character       `json:"setup_characters,omitempty"`
}

type SimplifiedEvent struct {
	EventType   string               `json:"event_type"`
	Speaker     string               `json:"speaker,omitempty"`
	Text        string               `json:"text,omitempty"`
	Character   string               `json:"character,omitempty"`
	From        string               `json:"from,omitempty"`
	To          string               `json:"to,omitempty"`
	Description string               `json:"description,omitempty"`
	Choices     []SimplifiedChoice   `json:"choices,omitempty"`
	ChoiceID    string               `json:"choice_id,omitempty"`
	Responses   []SimplifiedResponse `json:"responses,omitempty"`
}

type SimplifiedChoice struct {
	Text string `json:"text"`
}

type SimplifiedResponse struct {
	ChoiceText     string            `json:"choice_text"`
	ResponseEvents []SimplifiedEvent `json:"response_events"`
}

type InlineResponseRequest struct {
	NovelID     uuid.UUID `json:"novel_id"`
	SceneIndex  int       `json:"scene_index"`
	ChoiceID    string    `json:"choice_id"`
	ChoiceText  string    `json:"choice_text"`
	ResponseIdx int       `json:"response_idx"`
}

type InlineResponseResult struct {
	Success      bool               `json:"success"`
	UpdatedState *NovelStateChanges `json:"updated_state,omitempty"`
	NextEvents   []SimplifiedEvent  `json:"next_events,omitempty"`
}

// NovelStateChanges представляет изменения состояния, которые нужно вернуть клиенту.
type NovelStateChanges struct {
	GlobalFlags    []string               `json:"global_flags,omitempty"`
	Relationship   map[string]int         `json:"relationship,omitempty"`
	StoryVariables map[string]interface{} `json:"story_variables,omitempty"`
}
