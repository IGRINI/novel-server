package models

import (
	"time"

	"github.com/google/uuid"
)

// PlayerProgress хранит текущее состояние игрока в рамках опубликованной истории.
type PlayerProgress struct {
	ID               uuid.UUID              `db:"id" json:"id"`                               // Primary Key
	UserID           uuid.UUID              `db:"user_id" json:"userId"`                      // Nullable in DB, kept for potential direct lookups
	PublishedStoryID uuid.UUID              `db:"published_story_id" json:"publishedStoryId"` // Nullable in DB, kept for potential direct lookups
	CoreStats        map[string]int         `db:"core_stats" json:"coreStats"`
	StoryVariables   map[string]interface{} `db:"story_variables" json:"story_variables"`
	GlobalFlags      []string               `db:"global_flags" json:"global_flags"`
	CurrentStateHash string                 `db:"current_state_hash" json:"current_state_hash"`
	SceneIndex       int                    `db:"scene_index" json:"scene_index"`
	// Поля для хранения последних сводок от AI
	LastStorySummary     *string   `db:"last_story_summary" json:"last_story_summary,omitempty"`
	LastFutureDirection  *string   `db:"last_future_direction" json:"last_future_direction,omitempty"`
	LastVarImpactSummary *string   `db:"last_var_impact_summary" json:"last_var_impact_summary,omitempty"`
	CurrentSceneSummary  *string   `db:"current_scene_summary" json:"current_scene_summary,omitempty"`
	CreatedAt            time.Time `db:"created_at" json:"createdAt"`
	UpdatedAt            time.Time `db:"updated_at" json:"updatedAt"`
}

// InitialStateHash - константа для хэша начального состояния.
const InitialStateHash = "initial"
