package interfaces

import (
	"context"
	"novel-server/shared/models"
	"time"

	"github.com/google/uuid"
)

// PlayerGameStateRepository defines the interface for interacting with player game session data.
//
//go:generate mockery --name PlayerGameStateRepository --output ./mocks --outpkg mocks --case=underscore
type PlayerGameStateRepository interface {
	// ListByPlayerAndStory retrieves all game states for a specific player and story.
	// Returns an empty slice if no game states exist.
	ListByPlayerAndStory(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) ([]*models.PlayerGameState, error)

	// GetByID retrieves a player game state by its unique ID.
	// Returns models.ErrNotFound if no game state with the given ID exists.
	GetByID(ctx context.Context, gameStateID uuid.UUID) (*models.PlayerGameState, error)

	// Save creates a new player game state if state.ID is zero UUID,
	// or updates an existing one based on state.ID.
	// Use this to update status, current scene, progress link, etc.
	// Returns the ID of the created/updated record.
	Save(ctx context.Context, state *models.PlayerGameState) (uuid.UUID, error)

	// DeleteByPlayerAndStory removes the game state record for a specific player and story.
	// DEPRECATED: Prefer DeleteByID for specific save slots.
	// This might still be useful for admin cleanup or specific scenarios.
	DeleteByPlayerAndStory(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) error

	// CheckGameStateExistsForStories checks if *any* player game states exist for a given player and a list of story IDs.
	// Returns a map where keys are story IDs and values are booleans indicating game state existence.
	// Useful for UI indications (e.g., "Continue Playing" button visibility).
	CheckGameStateExistsForStories(ctx context.Context, playerID uuid.UUID, storyIDs []uuid.UUID) (map[uuid.UUID]bool, error)

	// ListByStoryID получает все состояния игры для указанной истории (для админ целей?).
	ListByStoryID(ctx context.Context, publishedStoryID uuid.UUID) ([]models.PlayerGameState, error)

	// FindAndMarkStaleGeneratingAsError находит состояния игры игрока, которые 'зависли'
	// в статусе генерации сцены или концовки, и обновляет их статус на Error.
	// staleThreshold: длительность, после которой состояние считается зависшим.
	// Возвращает количество обновленных записей и ошибку.
	FindAndMarkStaleGeneratingAsError(ctx context.Context, staleThreshold time.Duration) (int64, error)

	// Delete removes a game state record by its unique ID.
	// Returns models.ErrNotFound if the record does not exist.
	Delete(ctx context.Context, gameStateID uuid.UUID) error

	// ListSummariesByPlayerAndStory retrieves a list of game state summaries (ID, LastActivityAt, SceneIndex)
	// for a specific player and story, joined with player_progress.
	// Returns an empty slice if no game states are found.
	ListSummariesByPlayerAndStory(ctx context.Context, userID, publishedStoryID uuid.UUID) ([]*models.GameStateSummaryDTO, error)

	// TODO: Potentially add methods like ListPlayerGameStates(ctx, playerID) if needed.
}
