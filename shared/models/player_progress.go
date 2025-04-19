package models

import (
	"time"

	"github.com/google/uuid"
)

// PlayerProgress хранит текущее состояние игрока в рамках опубликованной истории.
type PlayerProgress struct {
	// ID               uuid.UUID              `db:"id" json:"id"`
	UserID           uuid.UUID              `db:"user_id" json:"userId"`
	PublishedStoryID uuid.UUID              `db:"published_story_id" json:"publishedStoryId"`
	CoreStats        map[string]int         `db:"core_stats" json:"coreStats"`
	StoryVariables   map[string]interface{} `db:"story_variables" json:"storyVariables"`
	GlobalFlags      []string               `db:"global_flags" json:"globalFlags"`
	CurrentStateHash string                 `db:"current_state_hash" json:"currentStateHash"`
	// Поля для хранения последних сводок от AI
	LastStorySummary     string    `db:"last_story_summary" json:"lastStorySummary,omitempty"`
	LastFutureDirection  string    `db:"last_future_direction" json:"lastFutureDirection,omitempty"`
	LastVarImpactSummary string    `db:"last_var_impact_summary" json:"lastVarImpactSummary,omitempty"`
	CreatedAt            time.Time `db:"created_at" json:"createdAt"`
	UpdatedAt            time.Time `db:"updated_at" json:"updatedAt"`
}

// InitialStateHash - константа для хэша начального состояния.
const InitialStateHash = "initial"
