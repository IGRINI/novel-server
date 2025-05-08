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
	VarImpactSummary       string            `json:"vis,omitempty"`
	StoryVariableDefs      map[string]string `json:"svd,omitempty"`
	Choices                []ChoiceBlock     `json:"ch,omitempty"`
	EndingText             string            `json:"et,omitempty"`
	NewPlayerDescription   string            `json:"npd,omitempty"`
	CoreStatsReset         map[string]int    `json:"csr,omitempty"`
	EndingTextPreviousChar string            `json:"etp,omitempty"`
}

// ChoiceBlock represents a single decision point in the scene.
type ChoiceBlock struct {
	Char        string        `json:"char,omitempty"`
	Description string        `json:"desc"`
	Options     []SceneOption `json:"opts,omitempty"`
}

// SceneOption represents one of the two choices available in a ChoiceBlock.
type SceneOption struct {
	Text         string       `json:"txt"`
	Consequences Consequences `json:"cons"`
}

// Consequences defines the effects of choosing a specific option.
type Consequences struct {
	CoreStatsChange   map[string]int         `json:"cs,omitempty"`
	StoryVariables    map[string]interface{} `json:"sv,omitempty"`
	GlobalFlags       []string               `json:"gf,omitempty"`
	GlobalFlagsRemove []string               `json:"gf_rem,omitempty"`
	ResponseText      string                 `json:"rt,omitempty"`
}
