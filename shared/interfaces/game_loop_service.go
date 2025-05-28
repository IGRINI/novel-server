package interfaces

import (
	"context"

	"novel-server/shared/models"

	"github.com/google/uuid"
)

// GameLoopService defines the operations for managing the core game loop.
// Moved from gameplay-service/internal/service to shared/interfaces to break import cycle.
type GameLoopService interface {
	// GetStoryScene retrieves the current scene for a specific game state.
	// It uses the game state ID to find the appropriate scene for the player.
	GetStoryScene(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) (*models.StoryScene, error)

	// MakeChoice applies player choices to a specific game state.
	// It uses the game state ID to find the appropriate state for the player.
	MakeChoice(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID, selectedOptionIndices []int) error

	// ListGameStates lists all active game states (save slots) for a player and a story.
	ListGameStates(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) ([]*models.PlayerGameState, error)

	// CreateNewGameState creates a new save slot (game state) for a player and a story.
	// Returns an error if the player exceeds their save slot limit (TODO: implement limit check).
	CreateNewGameState(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) (*models.PlayerGameState, error)

	// DeletePlayerGameState deletes a specific game state (save slot) by its ID.
	DeletePlayerGameState(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error

	// RetryGenerationForGameState handles retrying generation for a specific game state.
	// It determines if setup or scene generation failed and triggers the appropriate task.
	RetryGenerationForGameState(ctx context.Context, userID, storyID, gameStateID uuid.UUID) error

	// UpdateSceneInternal updates the content of a specific scene (internal admin func).
	UpdateSceneInternal(ctx context.Context, sceneID uuid.UUID, contentJSON string) error

	// GetPlayerProgress retrieves the progress node for a specific game state.
	// It uses the game state ID to find the appropriate progress for the player.
	GetPlayerProgress(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) (*models.PlayerProgress, error)

	// DeleteSceneInternal deletes a scene (internal admin func).
	DeleteSceneInternal(ctx context.Context, sceneID uuid.UUID) error

	// UpdatePlayerProgressInternal updates a specific progress node (internal func).
	UpdatePlayerProgressInternal(ctx context.Context, progressID uuid.UUID, progressData map[string]interface{}) error

	// RetryInitialGeneration handles retrying generation for a published story's Setup or Initial Scene.
	RetryInitialGeneration(ctx context.Context, userID, storyID uuid.UUID) error

	// DispatchNextGenerationTask determines the next AI generation task based on the story's current status
	// and publishes it to the message queue. It operates within the provided transaction.
	// It accepts optional context (gameState, progress, narrative) which might be needed for certain statuses.
	// NOTE: This method is primarily for internal use by the service and message handlers.
	// It should operate within an existing transaction `tx`.
	DispatchNextGenerationTask(
		ctx context.Context,
		tx DBTX, // Use DBTX to accept pool or tx
		storyID uuid.UUID,
		// Optional context, pass nil/empty if not available/relevant for the current status
		optionalGameState *models.PlayerGameState,
		optionalProgress *models.PlayerProgress,
		optionalNarrative string,
	) error
}
