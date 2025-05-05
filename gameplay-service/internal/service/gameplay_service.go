package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"novel-server/gameplay-service/internal/config"
	"novel-server/gameplay-service/internal/messaging"
	sharedConfigService "novel-server/shared/configservice"
	interfaces "novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Define local service-level errors
var (
	ErrInvalidOperation     = errors.New("invalid operation")
	ErrInvalidLimit         = errors.New("invalid limit value")
	ErrInvalidOffset        = errors.New("invalid offset value")
	ErrChoiceNotFound       = errors.New("choice or scene not found")
	ErrInvalidChoiceIndex   = errors.New("invalid choice index")
	ErrStoryNotReadyYet     = errors.New("story is not ready for gameplay yet") // Moved this up slightly for grouping
	ErrSceneNeedsGeneration = errors.New("current scene needs generation")      // Moved this up slightly for grouping

	// Add errors expected in handler/http.go
	ErrStoryNotFound          = errors.New("published story not found")
	ErrSceneNotFound          = errors.New("current scene not found")
	ErrPlayerProgressNotFound = errors.New("player progress not found")
	ErrStoryNotReady          = errors.New("story is not ready for gameplay yet")
	ErrInternal               = errors.New("internal service error")
	ErrInvalidChoice          = errors.New("invalid choice")
	ErrNoChoicesAvailable     = errors.New("no choices available in the current scene")

	// <<< Добавляем ошибки для лайков >>>
	// ErrAlreadyLiked = errors.New("story already liked by this user") // Moved to like_service.go
	// ErrNotLikedYet  = errors.New("story not liked by this user yet") // Moved to like_service.go

	// <<< НОВАЯ РЕАЛИЗАЦИЯ: SetStoryVisibility >>>
	// ErrStoryNotReadyForPublishing - ошибка, если история не готова к публикации
	// ErrStoryNotReadyForPublishing = errors.New("story is not ready for publishing (must be in Ready status)") // Moved to publishing_service.go
	// ErrAdultContentCannotBePublic - ошибка, если контент для взрослых пытаются сделать публичным
	// ErrAdultContentCannotBePublic = errors.New("adult content cannot be made public") // Moved to publishing_service.go
)

// GameplayService defines the interface for gameplay business logic.
type GameplayService interface {
	// Draft methods (delegated)
	GenerateInitialStory(ctx context.Context, userID uuid.UUID, initialPrompt string, language string) (*sharedModels.StoryConfig, error)
	ReviseDraft(ctx context.Context, id uuid.UUID, userID uuid.UUID, revisionPrompt string) error
	GetStoryConfig(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*sharedModels.StoryConfig, error)
	ListUserDrafts(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.StoryConfig, string, error)
	RetryDraftGeneration(ctx context.Context, draftID uuid.UUID, userID uuid.UUID) error

	// Publishing methods (delegated)
	PublishDraft(ctx context.Context, draftID uuid.UUID, userID uuid.UUID) (publishedStoryID uuid.UUID, err error)
	SetStoryVisibility(ctx context.Context, storyID, userID uuid.UUID, isPublic bool) error

	// Like methods (delegated)
	LikeStory(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error
	UnlikeStory(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error
	ListLikedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.PublishedStorySummary, string, error)

	// Story Browsing methods (delegated)
	ListMyPublishedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.PublishedStorySummary, string, error)
	ListPublicStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.PublishedStorySummary, string, error)
	GetPublishedStoryDetails(ctx context.Context, storyID, userID uuid.UUID) (*PublishedStoryParsedDetailDTO, error)
	ListUserPublishedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*sharedModels.PublishedStory, string, error)
	GetPublishedStoryDetailsWithProgress(ctx context.Context, userID, publishedStoryID uuid.UUID) (*sharedModels.PublishedStorySummary, error)

	// Core Gameplay methods
	GetStoryScene(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (*sharedModels.StoryScene, error)
	MakeChoice(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID, selectedOptionIndices []int) error
	DeletePlayerGameState(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error
	RetryInitialGeneration(ctx context.Context, userID, storyID uuid.UUID) error
	RetryGenerationForGameState(ctx context.Context, userID, storyID, gameStateID uuid.UUID) error

	// ListMyDrafts (duplicated?) - Keep for now, handled by DraftService
	// ListMyDrafts(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.StoryConfig, string, error)

	// <<< ДОБАВЛЕНО: Методы для внутреннего API админки >>>
	GetDraftDetailsInternal(ctx context.Context, draftID uuid.UUID) (*sharedModels.StoryConfig, error)
	GetPublishedStoryDetailsInternal(ctx context.Context, storyID uuid.UUID) (*sharedModels.PublishedStory, error)
	ListStoryScenesInternal(ctx context.Context, storyID uuid.UUID) ([]sharedModels.StoryScene, error)
	// <<< ДОБАВЛЕНО: Методы обновления для внутреннего API админки >>>
	UpdateDraftInternal(ctx context.Context, draftID uuid.UUID, configJSON, userInputJSON string, status sharedModels.StoryStatus) error
	UpdateStoryInternal(ctx context.Context, storyID uuid.UUID, configJSON, setupJSON json.RawMessage, status sharedModels.StoryStatus) error
	UpdateSceneInternal(ctx context.Context, sceneID uuid.UUID, contentJSON string) error

	// <<< ДОБАВЛЕНО: Метод для удаления сцены админкой >>>
	DeleteSceneInternal(ctx context.Context, sceneID uuid.UUID) error

	// <<< ДОБАВЛЕНО: Метод для получения списка состояний игроков админкой >>>
	ListStoryPlayersInternal(ctx context.Context, storyID uuid.UUID) ([]sharedModels.PlayerGameState, error)

	// <<< ДОБАВЛЕНО: Метод для получения деталей прогресса игрока админкой >>>
	GetPlayerProgressInternal(ctx context.Context, progressID uuid.UUID) (*sharedModels.PlayerProgress, error)

	// <<< Новый метод для удаления черновика >>>
	DeleteDraft(ctx context.Context, id uuid.UUID, userID uuid.UUID) error

	// <<< ДОБАВЛЯЕМ МЕТОД >>>
	DeletePublishedStory(ctx context.Context, id uuid.UUID, userID uuid.UUID) error

	// <<< Методы StoryBrowsingService >>>
	ListPublishedStoriesPublic(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.PublishedStorySummary, string, error)

	// <<< Новый метод для получения историй с прогрессом >>>
	GetStoriesWithProgress(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]sharedModels.PublishedStorySummary, string, error)

	// <<< НОВЫЙ МЕТОД >>>
	ListMyStoriesWithProgress(ctx context.Context, userID uuid.UUID, cursor string, limit int, filterAdult bool) ([]sharedModels.PublishedStorySummary, string, error)

	// <<< ДОБАВЛЕНО: Метод для получения прогресса игрока >>>
	GetPlayerProgress(ctx context.Context, userID, storyID uuid.UUID) (*sharedModels.PlayerProgress, error)

	// <<< ДОБАВЛЕНО: Метод для получения распарсенного Setup >>>
	GetParsedSetup(ctx context.Context, storyID uuid.UUID) (*sharedModels.NovelSetupContent, error)

	// <<< ДОБАВЛЕНО: Сигнатура для обновления прогресса игрока >>>
	UpdatePlayerProgressInternal(ctx context.Context, progressID uuid.UUID, progressData map[string]interface{}) error

	// <<< ДОБАВЛЕНО: Явное добавление метода для подсчета активных историй >>>
	GetActiveStoryCount(ctx context.Context) (int, error)

	// <<< ДОБАВЛЕНО: Методы для управления состояниями игры >>>
	ListGameStates(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) ([]*sharedModels.PlayerGameState, error)
	CreateNewGameState(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) (*sharedModels.PlayerGameState, error)
}

type gameplayServiceImpl struct {
	configRepo           interfaces.StoryConfigRepository
	publishedRepo        interfaces.PublishedStoryRepository
	sceneRepo            interfaces.StorySceneRepository
	playerProgressRepo   interfaces.PlayerProgressRepository
	playerGameStateRepo  interfaces.PlayerGameStateRepository
	likeRepo             interfaces.LikeRepository
	draftService         DraftService
	publishingService    PublishingService
	likeService          LikeService
	storyBrowsingService StoryBrowsingService
	gameLoopService      GameLoopService
	publisher            messaging.TaskPublisher
	imgBatchPublisher    messaging.CharacterImageTaskBatchPublisher
	imageRefRepo         interfaces.ImageReferenceRepository
	dynamicConfigRepo    interfaces.DynamicConfigRepository
	pool                 *pgxpool.Pool
	logger               *zap.Logger
	authClient           interfaces.AuthServiceClient
	cfg                  *config.Config
	configService        *sharedConfigService.ConfigService
}

func NewGameplayService(
	configRepo interfaces.StoryConfigRepository,
	publishedRepo interfaces.PublishedStoryRepository,
	sceneRepo interfaces.StorySceneRepository,
	playerProgressRepo interfaces.PlayerProgressRepository,
	playerGameStateRepo interfaces.PlayerGameStateRepository,
	likeRepo interfaces.LikeRepository,
	imageRefRepo interfaces.ImageReferenceRepository,
	dynamicConfigRepo interfaces.DynamicConfigRepository,
	taskPublisher messaging.TaskPublisher,
	imgBatchPublisher messaging.CharacterImageTaskBatchPublisher,
	pool *pgxpool.Pool,
	logger *zap.Logger,
	authClient interfaces.AuthServiceClient,
	cfg *config.Config,
	configService *sharedConfigService.ConfigService,
) GameplayService {
	// <<< СОЗДАЕМ DraftService >>>
	draftSvc := NewDraftService(configRepo, taskPublisher, logger, cfg)
	// <<< СОЗДАЕМ PublishingService >>>
	publishingSvc := NewPublishingService(configRepo, publishedRepo, taskPublisher, pool, logger)
	// <<< СОЗДАЕМ LikeService >>>
	likeSvc := NewLikeService(publishedRepo, playerGameStateRepo, authClient, logger)
	// <<< СОЗДАЕМ StoryBrowsingService >>>
	storyBrowsingSvc := NewStoryBrowsingService(
		publishedRepo,
		sceneRepo,
		playerProgressRepo,
		playerGameStateRepo,
		likeRepo,
		imageRefRepo,
		authClient,
		logger,
	)
	// <<< СОЗДАЕМ GameLoopService >>>
	gameLoopSvc := NewGameLoopService(
		publishedRepo, sceneRepo, playerProgressRepo, playerGameStateRepo,
		taskPublisher,
		configRepo,
		imageRefRepo,
		imgBatchPublisher,
		dynamicConfigRepo,
		logger,
		cfg,
	)

	return &gameplayServiceImpl{
		configRepo:           configRepo,
		publishedRepo:        publishedRepo,
		sceneRepo:            sceneRepo,
		playerProgressRepo:   playerProgressRepo,
		playerGameStateRepo:  playerGameStateRepo,
		likeRepo:             likeRepo,
		draftService:         draftSvc,
		publishingService:    publishingSvc,
		likeService:          likeSvc,
		storyBrowsingService: storyBrowsingSvc,
		gameLoopService:      gameLoopSvc,
		publisher:            taskPublisher,
		imgBatchPublisher:    imgBatchPublisher,
		imageRefRepo:         imageRefRepo,
		dynamicConfigRepo:    dynamicConfigRepo,
		pool:                 pool,
		logger:               logger.Named("GameplayService"),
		authClient:           authClient,
		cfg:                  cfg,
		configService:        configService,
	}
}

// === Методы, делегированные DraftService ===

// GenerateInitialStory delegates to DraftService.
func (s *gameplayServiceImpl) GenerateInitialStory(ctx context.Context, userID uuid.UUID, initialPrompt string, language string) (*sharedModels.StoryConfig, error) {
	return s.draftService.GenerateInitialStory(ctx, userID, initialPrompt, language)
}

// ReviseDraft delegates to DraftService.
func (s *gameplayServiceImpl) ReviseDraft(ctx context.Context, id uuid.UUID, userID uuid.UUID, revisionPrompt string) error {
	return s.draftService.ReviseDraft(ctx, id, userID, revisionPrompt)
}

// GetStoryConfig delegates to DraftService.
func (s *gameplayServiceImpl) GetStoryConfig(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*sharedModels.StoryConfig, error) {
	return s.draftService.GetStoryConfig(ctx, id, userID)
}

// ListUserDrafts delegates to DraftService.
func (s *gameplayServiceImpl) ListUserDrafts(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.StoryConfig, string, error) {
	return s.draftService.ListUserDrafts(ctx, userID, cursor, limit)
}

// RetryDraftGeneration delegates to DraftService.
func (s *gameplayServiceImpl) RetryDraftGeneration(ctx context.Context, draftID uuid.UUID, userID uuid.UUID) error {
	return s.draftService.RetryDraftGeneration(ctx, draftID, userID)
}

// === Конец делегированных методов DraftService ===

// === Методы, делегированные PublishingService ===

// PublishDraft delegates to PublishingService.
func (s *gameplayServiceImpl) PublishDraft(ctx context.Context, draftID uuid.UUID, userID uuid.UUID) (publishedStoryID uuid.UUID, err error) {
	return s.publishingService.PublishDraft(ctx, draftID, userID)
}

// SetStoryVisibility delegates to PublishingService.
func (s *gameplayServiceImpl) SetStoryVisibility(ctx context.Context, storyID, userID uuid.UUID, isPublic bool) error {
	return s.publishingService.SetStoryVisibility(ctx, storyID, userID, isPublic)
}

// === Конец делегированных методов PublishingService ===

// === Методы, делегированные LikeService ===

// LikeStory delegates to LikeService.
func (s *gameplayServiceImpl) LikeStory(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error {
	return s.likeService.LikeStory(ctx, userID, publishedStoryID)
}

// UnlikeStory delegates to LikeService.
func (s *gameplayServiceImpl) UnlikeStory(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error {
	return s.likeService.UnlikeStory(ctx, userID, publishedStoryID)
}

// ListLikedStories delegates to LikeService.
func (s *gameplayServiceImpl) ListLikedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.PublishedStorySummary, string, error) {
	return s.likeService.ListLikedStories(ctx, userID, cursor, limit)
}

// === Конец делегированных методов LikeService ===

// === Методы, делегированные StoryBrowsingService ===

// ListMyPublishedStories delegates to StoryBrowsingService.
func (s *gameplayServiceImpl) ListMyPublishedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.PublishedStorySummary, string, error) {
	return s.storyBrowsingService.ListMyPublishedStories(ctx, userID, cursor, limit)
}

// ListPublicStories delegates to StoryBrowsingService.
func (s *gameplayServiceImpl) ListPublicStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.PublishedStorySummary, string, error) {
	return s.storyBrowsingService.ListPublicStories(ctx, userID, cursor, limit)
}

// GetPublishedStoryDetails delegates to StoryBrowsingService.
// Note: The return type *PublishedStoryDetailDTO now refers to the one defined in story_browsing_service.go
func (s *gameplayServiceImpl) GetPublishedStoryDetails(ctx context.Context, storyID, userID uuid.UUID) (*PublishedStoryParsedDetailDTO, error) {
	return s.storyBrowsingService.GetPublishedStoryDetails(ctx, storyID, userID)
}

// ListUserPublishedStories delegates to StoryBrowsingService.
func (s *gameplayServiceImpl) ListUserPublishedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*sharedModels.PublishedStory, string, error) {
	return s.storyBrowsingService.ListUserPublishedStories(ctx, userID, cursor, limit)
}

// GetPublishedStoryDetailsWithProgress delegates to StoryBrowsingService.
func (s *gameplayServiceImpl) GetPublishedStoryDetailsWithProgress(ctx context.Context, userID, publishedStoryID uuid.UUID) (*sharedModels.PublishedStorySummary, error) {
	return s.storyBrowsingService.GetPublishedStoryDetailsWithProgress(ctx, userID, publishedStoryID)
}

// GetParsedSetup delegates to StoryBrowsingService.
func (s *gameplayServiceImpl) GetParsedSetup(ctx context.Context, storyID uuid.UUID) (*sharedModels.NovelSetupContent, error) {
	return s.storyBrowsingService.GetParsedSetup(ctx, storyID)
}

// === Конец делегированных методов StoryBrowsingService ===

// === Методы, делегированные GameLoopService ===

// GetStoryScene delegates to GameLoopService.
func (s *gameplayServiceImpl) GetStoryScene(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (*sharedModels.StoryScene, error) {
	return s.gameLoopService.GetStoryScene(ctx, userID, publishedStoryID)
}

// MakeChoice delegates to GameLoopService.
func (s *gameplayServiceImpl) MakeChoice(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID, selectedOptionIndices []int) error {
	return s.gameLoopService.MakeChoice(ctx, userID, publishedStoryID, selectedOptionIndices)
}

// DeletePlayerGameState delegates to GameLoopService.
func (s *gameplayServiceImpl) DeletePlayerGameState(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error {
	return s.gameLoopService.DeletePlayerGameState(ctx, userID, publishedStoryID)
}

// RetryInitialGeneration delegates to GameLoopService.
func (s *gameplayServiceImpl) RetryInitialGeneration(ctx context.Context, userID, storyID uuid.UUID) error {
	return s.gameLoopService.RetryInitialGeneration(ctx, userID, storyID)
}

// RetryGenerationForGameState delegates to GameLoopService.
func (s *gameplayServiceImpl) RetryGenerationForGameState(ctx context.Context, userID, storyID, gameStateID uuid.UUID) error {
	return s.gameLoopService.RetryGenerationForGameState(ctx, userID, storyID, gameStateID)
}

// GetPlayerProgress delegates to GameLoopService.
func (s *gameplayServiceImpl) GetPlayerProgress(ctx context.Context, userID, storyID uuid.UUID) (*sharedModels.PlayerProgress, error) {
	return s.gameLoopService.GetPlayerProgress(ctx, userID, storyID)
}

// UpdateSceneInternal delegates to GameLoopService.
func (s *gameplayServiceImpl) UpdateSceneInternal(ctx context.Context, sceneID uuid.UUID, contentJSON string) error {
	return s.gameLoopService.UpdateSceneInternal(ctx, sceneID, contentJSON)
}

// DeleteSceneInternal delegates to GameLoopService.
func (s *gameplayServiceImpl) DeleteSceneInternal(ctx context.Context, sceneID uuid.UUID) error {
	return s.gameLoopService.DeleteSceneInternal(ctx, sceneID)
}

// UpdatePlayerProgressInternal delegates to GameLoopService.
func (s *gameplayServiceImpl) UpdatePlayerProgressInternal(ctx context.Context, progressID uuid.UUID, progressData map[string]interface{}) error {
	return s.gameLoopService.UpdatePlayerProgressInternal(ctx, progressID, progressData)
}

// ListGameStates delegates to GameLoopService.
func (s *gameplayServiceImpl) ListGameStates(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) ([]*sharedModels.PlayerGameState, error) {
	return s.gameLoopService.ListGameStates(ctx, playerID, publishedStoryID)
}

// CreateNewGameState delegates to GameLoopService.
func (s *gameplayServiceImpl) CreateNewGameState(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) (*sharedModels.PlayerGameState, error) {
	return s.gameLoopService.CreateNewGameState(ctx, playerID, publishedStoryID)
}

// === Конец делегированных методов GameLoopService ===

// <<< ДОБАВЛЕНО: Методы для внутреннего API админки >>>
func (s *gameplayServiceImpl) GetDraftDetailsInternal(ctx context.Context, draftID uuid.UUID) (*sharedModels.StoryConfig, error) {
	// Implementation of GetDraftDetailsInternal method
	return s.draftService.GetDraftDetailsInternal(ctx, draftID)
}

func (s *gameplayServiceImpl) GetPublishedStoryDetailsInternal(ctx context.Context, storyID uuid.UUID) (*sharedModels.PublishedStory, error) {
	// Implementation of GetPublishedStoryDetailsInternal method
	return s.storyBrowsingService.GetPublishedStoryDetailsInternal(ctx, storyID)
}

func (s *gameplayServiceImpl) ListStoryScenesInternal(ctx context.Context, storyID uuid.UUID) ([]sharedModels.StoryScene, error) {
	// Implementation of ListStoryScenesInternal method
	return s.storyBrowsingService.ListStoryScenesInternal(ctx, storyID)
}

// <<< ДОБАВЛЕНО: Делегаты для методов обновления внутреннего API админки >>>

// UpdateDraftInternal delegates to DraftService.
func (s *gameplayServiceImpl) UpdateDraftInternal(ctx context.Context, draftID uuid.UUID, configJSON, userInputJSON string, status sharedModels.StoryStatus) error {
	return s.draftService.UpdateDraftInternal(ctx, draftID, configJSON, userInputJSON, status)
}

// UpdateStoryInternal delegates to StoryBrowsingService.
func (s *gameplayServiceImpl) UpdateStoryInternal(ctx context.Context, storyID uuid.UUID, configJSON, setupJSON json.RawMessage, status sharedModels.StoryStatus) error {
	return s.storyBrowsingService.UpdateStoryInternal(ctx, storyID, configJSON, setupJSON, status)
}

// <<< ДОБАВЛЕНО: Метод для удаления черновика >>>
func (s *gameplayServiceImpl) DeleteDraft(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	// Implementation of DeleteDraft method
	return s.draftService.DeleteDraft(ctx, id, userID)
}

// <<< ДОБАВЛЯЕМ МЕТОД >>>
func (s *gameplayServiceImpl) DeletePublishedStory(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	// Implementation of DeletePublishedStory method
	return s.publishingService.DeletePublishedStory(ctx, id, userID)
}

// <<< Методы StoryBrowsingService >>>
func (s *gameplayServiceImpl) ListPublishedStoriesPublic(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.PublishedStorySummary, string, error) {
	log := s.logger.With(zap.String("method", "ListPublishedStoriesPublic"), zap.String("userID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit))
	log.Debug("Listing public published stories")

	// Delegate the call to the story browsing service which handles the core logic and pagination.
	stories, nextCursor, err := s.storyBrowsingService.ListPublicStories(ctx, userID, cursor, limit)
	if err != nil {
		log.Error("Failed to list public stories via story browsing service", zap.Error(err))
		// Assuming ListPublicStories returns appropriate shared errors like ErrInternalServer
		return nil, "", err
	}

	log.Info("Successfully listed public published stories", zap.Int("count", len(stories)), zap.Bool("hasNext", nextCursor != ""))
	// Return the results directly
	return stories, nextCursor, nil
}

// GetStoriesWithProgress делегирует вызов StoryBrowsingService
func (s *gameplayServiceImpl) GetStoriesWithProgress(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]sharedModels.PublishedStorySummary, string, error) {
	return s.storyBrowsingService.GetStoriesWithProgress(ctx, userID, limit, cursor)
}

// <<< ДОБАВЛЕНО: Метод для получения списка состояний игроков админкой >>>
func (s *gameplayServiceImpl) ListStoryPlayersInternal(ctx context.Context, storyID uuid.UUID) ([]sharedModels.PlayerGameState, error) {
	// Implementation of ListStoryPlayersInternal method
	return s.playerGameStateRepo.ListByStoryID(ctx, storyID)
}

// <<< ДОБАВЛЕНО: Метод для получения деталей прогресса игрока админкой >>>
func (s *gameplayServiceImpl) GetPlayerProgressInternal(ctx context.Context, progressID uuid.UUID) (*sharedModels.PlayerProgress, error) {
	s.logger.Debug("Getting player progress internally", zap.Stringer("progressID", progressID))

	progress, err := s.playerProgressRepo.GetByID(ctx, progressID)
	if err != nil {
		if errors.Is(err, sharedModels.ErrNotFound) {
			s.logger.Warn("Player progress not found for internal request", zap.Stringer("progressID", progressID))
			return nil, status.Errorf(codes.NotFound, "player progress with id %s not found", progressID)
		}
		s.logger.Error("Failed to get player progress from repository", zap.Stringer("progressID", progressID), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve player progress: %v", err)
	}

	s.logger.Info("Successfully retrieved player progress internally", zap.Stringer("progressID", progressID))
	return progress, nil
}

// GetActiveStoryCount делегирует вызов встроенному storyBrowsingService.
func (s *gameplayServiceImpl) GetActiveStoryCount(ctx context.Context) (int, error) {
	// <<< ИСПРАВЛЕНО: Возвращаем делегирование storyBrowsingService >>>
	// Проверяем, инициализирован ли storyBrowsingService
	if s.storyBrowsingService == nil {
		s.logger.Error("storyBrowsingService is not initialized in gameplayServiceImpl")
		return 0, fmt.Errorf("internal error: story browsing service not available")
	}
	return s.storyBrowsingService.GetActiveStoryCount(ctx)
}

// <<< НОВАЯ РЕАЛИЗАЦИЯ >>>
// ListMyStoriesWithProgress delegates to StoryBrowsingService.
func (s *gameplayServiceImpl) ListMyStoriesWithProgress(ctx context.Context, userID uuid.UUID, cursor string, limit int, filterAdult bool) ([]sharedModels.PublishedStorySummary, string, error) {
	return s.storyBrowsingService.ListMyStoriesWithProgress(ctx, userID, cursor, limit, filterAdult)
}
