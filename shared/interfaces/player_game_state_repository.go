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
	// GetByPlayerAndStory retrieves the current game state for a specific player and story.
	// Returns models.ErrNotFound if no active game state exists.
	GetByPlayerAndStory(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) (*models.PlayerGameState, error)

	// GetByID retrieves a player game state by its unique ID.
	// Returns models.ErrNotFound if no game state with the given ID exists.
	GetByID(ctx context.Context, gameStateID uuid.UUID) (*models.PlayerGameState, error)

	// Save creates a new player game state if state.ID is zero UUID,
	// or updates an existing one based on state.ID.
	// Use this to update status, current scene, progress link, etc.
	// Returns the ID of the created/updated record.
	Save(ctx context.Context, state *models.PlayerGameState) (uuid.UUID, error)

	// DeleteByPlayerAndStory removes the game state record for a specific player and story.
	// This is typically used when a player explicitly "resets" their progress for a story.
	// Returns nil if the record was deleted or did not exist.
	DeleteByPlayerAndStory(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) error

	// CheckGameStateExistsForStories checks if active player game states exist for a given player and a list of story IDs.
	// Returns a map where keys are story IDs and values are booleans indicating game state existence.
	// Useful for UI indications (e.g., "Continue Playing").
	CheckGameStateExistsForStories(ctx context.Context, playerID uuid.UUID, storyIDs []uuid.UUID) (map[uuid.UUID]bool, error)

	// ListByStoryID получает все состояния игры для указанной истории.
	ListByStoryID(ctx context.Context, publishedStoryID uuid.UUID) ([]models.PlayerGameState, error)

	// FindAndMarkStaleGeneratingAsError находит состояния игры игрока, которые 'зависли'
	// в статусе генерации сцены или концовки, и обновляет их статус на Error.
	// staleThreshold: длительность, после которой состояние считается зависшим.
	// Возвращает количество обновленных записей и ошибку.
	FindAndMarkStaleGeneratingAsError(ctx context.Context, staleThreshold time.Duration) (int64, error)

	// TODO: Potentially add methods like ListPlayerGameStates(ctx, playerID) if needed.
}
