package models

import (
	"time"

	"github.com/google/uuid"
)

// --- Shared DTOs ---
// These DTOs are used across different services or layers.

// GameStateSummaryDTO represents a summary of a game state (save).
// Used to provide a concise list of saves to the client.
type GameStateSummaryDTO struct {
	ID                  uuid.UUID `json:"id"`                              // ID of the game state (gameStateID)
	LastActivityAt      time.Time `json:"last_activity_at"`                // Time of the last activity in this save
	SceneIndex          int       `json:"scene_index"`                     // Index of the current scene for this save
	CurrentSceneSummary *string   `json:"current_scene_summary,omitempty"` // Summary of the current scene (from player_progress)
	// CurrentSceneSummary *string   `json:"currentSceneSummary,omitempty"` // REMOVED: Cannot fetch efficiently
}

// CoreStatDTO represents parsed data for a single stat.
// Used for displaying stat information in story details.
type CoreStatDTO struct {
	Description  string `json:"description"`
	InitialValue int    `json:"initial_value"`
	GameOverMin  bool   `json:"game_over_min"`
	GameOverMax  bool   `json:"game_over_max"`
	Icon         string `json:"icon,omitempty"`
}

// CharacterDTO represents parsed data for a single character.
// Used for displaying character information in story details.
type CharacterDTO struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Personality    string `json:"personality,omitempty"`
	ImageReference string `json:"image_reference,omitempty"`
}
