package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// StoryScene represents a single state/set of choices within a published story.
type StoryScene struct {
	ID               uuid.UUID       `db:"id" json:"id"`
	PublishedStoryID uuid.UUID       `db:"published_story_id" json:"publishedStoryId"`
	StateHash        string          `db:"state_hash" json:"stateHash"`
	Content          json.RawMessage `db:"scene_content" json:"content"`
	CreatedAt        time.Time       `db:"created_at" json:"createdAt"`
}

// SceneContent defines the expected structure of the JSON stored in StoryScene.Content.
type SceneContent struct {
	StorySummarySoFar      string            `json:"sssf,omitempty"`
	FutureDirection        string            `json:"fd,omitempty"`
	StoryVariableDefs      map[string]string `json:"svd,omitempty"`
	Choices                []ChoiceBlock     `json:"ch,omitempty"`
	EndingText             string            `json:"et,omitempty"`
	NewPlayerDescription   string            `json:"npd,omitempty"`
	CoreStatsReset         map[string]int    `json:"csr,omitempty"`
	EndingTextPreviousChar string            `json:"etp,omitempty"`
}

// ChoiceBlock represents a single decision point in the scene.
type ChoiceBlock struct {
	Description string        `json:"desc"`
	Options     []SceneOption `json:"opts"`
}

// SceneOption represents one of the two choices available in a ChoiceBlock.
type SceneOption struct {
	Text         string       `json:"txt"`
	Consequences Consequences `json:"cons"`
}

// Consequences defines the effects of choosing a specific option.
type Consequences struct {
	CoreStatsChange map[string]int `json:"cs,omitempty"`
	ResponseText    string         `json:"rt,omitempty"`
}

// --- Типы, необходимые для InitialSceneContent (могут быть скопированы или импортированы) ---

type ChoiceSuggestion struct {
	Text        string `json:"text"`
	OutcomeHint string `json:"outcome_hint,omitempty"`
}

type SceneCard struct { // Переименовано из NewCardSuggestionFromPlanner для ясности в контексте сцены
	Pr    string `json:"pr"` // Было: ImagePromptDescriptor string `json:"image_prompt_descriptor"`
	Ir    string `json:"ir"` // Было: ImageReferenceName    string `json:"image_reference_name"`
	Title string `json:"title"`
}

// InitialSceneContent defines the structure stored in SceneContent for the initial scene.
type InitialSceneContent struct {
	SceneFocus string                `json:"scene_focus,omitempty"` // Focus from planner
	Cards      []SceneCard           `json:"cards,omitempty"`       // Cards for the initial scene
	Characters []CharacterDefinition `json:"characters,omitempty"`  // Characters present at the start of this scene (definitions)
}

// --- Конец скопированных типов ---
