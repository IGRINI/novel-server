package service

import (
	"context"

	"novel-server/gameplay-service/internal/messaging"
	interfaces "novel-server/shared/interfaces"
	"novel-server/shared/models"

	"novel-server/gameplay-service/internal/config"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type GameLoopService interface {
	// GetStoryScene retrieves the scene associated with a specific game state ID.
	GetStoryScene(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) (*models.StoryScene, error)

	// MakeChoice applies player choices to a specific game state.
	MakeChoice(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID, selectedOptionIndices []int) error

	// ListGameStates lists all active game states (save slots) for a player and a story.
	ListGameStates(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) ([]*models.PlayerGameState, error)

	// CreateNewGameState creates a new save slot (game state) for a player and a story.
	// Returns an error if the player exceeds their save slot limit (TODO: implement limit check).
	CreateNewGameState(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) (*models.PlayerGameState, error)

	// DeletePlayerGameState deletes a specific game state (save slot) by its ID.
	DeletePlayerGameState(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) error

	// RetryGenerationForGameState handles retrying generation for a specific game state.
	// It determines if setup or scene generation failed and triggers the appropriate task.
	RetryGenerationForGameState(ctx context.Context, userID, storyID, gameStateID uuid.UUID) error

	// UpdateSceneInternal updates the content of a specific scene (internal admin func).
	UpdateSceneInternal(ctx context.Context, sceneID uuid.UUID, contentJSON string) error

	// GetPlayerProgress retrieves the progress node linked to a specific game state ID.
	GetPlayerProgress(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) (*models.PlayerProgress, error)

	// DeleteSceneInternal deletes a scene (internal admin func).
	DeleteSceneInternal(ctx context.Context, sceneID uuid.UUID) error

	// UpdatePlayerProgressInternal updates a specific progress node (internal func).
	UpdatePlayerProgressInternal(ctx context.Context, progressID uuid.UUID, progressData map[string]interface{}) error

	// RetryInitialGeneration handles retrying generation for a published story's Setup or Initial Scene.
	RetryInitialGeneration(ctx context.Context, userID, storyID uuid.UUID) error
}

type gameLoopServiceImpl struct {
	publishedRepo              interfaces.PublishedStoryRepository
	sceneRepo                  interfaces.StorySceneRepository
	playerProgressRepo         interfaces.PlayerProgressRepository
	playerGameStateRepo        interfaces.PlayerGameStateRepository
	publisher                  messaging.TaskPublisher
	storyConfigRepo            interfaces.StoryConfigRepository
	imageReferenceRepo         interfaces.ImageReferenceRepository
	characterImageTaskBatchPub messaging.CharacterImageTaskBatchPublisher
	dynamicConfigRepo          interfaces.DynamicConfigRepository
	clientPub                  messaging.ClientUpdatePublisher
	logger                     *zap.Logger
	cfg                        *config.Config
	pool                       *pgxpool.Pool
}

// NewGameLoopService creates a new instance of GameLoopService.
func NewGameLoopService(
	publishedRepo interfaces.PublishedStoryRepository,
	sceneRepo interfaces.StorySceneRepository,
	playerProgressRepo interfaces.PlayerProgressRepository,
	playerGameStateRepo interfaces.PlayerGameStateRepository,
	publisher messaging.TaskPublisher,
	storyConfigRepo interfaces.StoryConfigRepository,
	imageReferenceRepo interfaces.ImageReferenceRepository,
	characterImageTaskBatchPub messaging.CharacterImageTaskBatchPublisher,
	dynamicConfigRepo interfaces.DynamicConfigRepository,
	clientPub messaging.ClientUpdatePublisher,
	logger *zap.Logger,
	cfg *config.Config,
	pool *pgxpool.Pool,
) GameLoopService {
	if cfg == nil {
		panic("cfg cannot be nil for NewGameLoopService")
	}
	return &gameLoopServiceImpl{
		publishedRepo:              publishedRepo,
		sceneRepo:                  sceneRepo,
		playerProgressRepo:         playerProgressRepo,
		playerGameStateRepo:        playerGameStateRepo,
		publisher:                  publisher,
		storyConfigRepo:            storyConfigRepo,
		imageReferenceRepo:         imageReferenceRepo,
		characterImageTaskBatchPub: characterImageTaskBatchPub,
		dynamicConfigRepo:          dynamicConfigRepo,
		clientPub:                  clientPub,
		logger:                     logger.Named("GameLoopService"),
		cfg:                        cfg,
		pool:                       pool,
	}
}

// --- Helper Functions ---
