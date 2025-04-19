package service

import (
	"context"
	"encoding/json"
	"errors"
	"novel-server/gameplay-service/internal/messaging"
	interfaces "novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
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

// <<< Structs moved to game_loop_service.go >>>
/*
type userChoiceInfo struct {
	Desc string `json:"d"` // Description of the choice block
	Text string `json:"t"` // Text of the chosen option
}

// --- Структуры для парсинга SceneContent ---

type sceneContentChoices struct {
	Type    string        `json:"type"` // "choices"
	Choices []sceneChoice `json:"ch"`
	// svd (story_variable_definitions) пока игнорируем, они не влияют на текущий state
}

type sceneChoice struct {
	Shuffleable int           `json:"sh"` // 0 или 1
	Description string        `json:"desc"`
	Options     []sceneOption `json:"opts"` // Должно быть ровно 2
}

type sceneOption struct {
	Text         string                    `json:"txt"`
	Consequences sharedModels.Consequences `json:"cons"` // Используем общую структуру
}
*/

// <<< DTOs for browsing stories moved to story_browsing_service.go >>>
/*
type PublishedStoryDetailDTO struct {
	ID                uuid.UUID
	Title             string
	ShortDescription  string
	AuthorID          uuid.UUID
	AuthorName        string
	PublishedAt       time.Time
	Genre             string
	Language          string
	IsAdultContent    bool
	PlayerName        string
	PlayerDescription string
	WorldContext      string
	StorySummary      string
	CoreStats         map[string]CoreStatDetailDTO // Нужно определить CoreStatDetailDTO или использовать shared
	LastPlayedAt      *time.Time
	IsAuthor          bool
}

type CoreStatDetailDTO struct {
	Description        string
	InitialValue       int
	GameOverConditions []sharedModels.StatDefinition // <<< Исправляем StatRule на StatDefinition
}
*/

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
	ListLikedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*LikedStoryDetailDTO, string, error)

	// Story Browsing methods (delegated)
	ListMyPublishedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*PublishedStoryDetailWithProgressDTO, string, error)
	ListPublicStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*PublishedStoryDetailWithProgressDTO, string, error)
	GetPublishedStoryDetails(ctx context.Context, storyID, userID uuid.UUID) (*PublishedStoryDetailDTO, error)
	ListUserPublishedStories(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*sharedModels.PublishedStory, error)
	GetPublishedStoryDetailsWithProgress(ctx context.Context, userID, publishedStoryID uuid.UUID) (*PublishedStoryDetailWithProgressDTO, error)

	// Core Gameplay methods
	GetStoryScene(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (*sharedModels.StoryScene, error)
	MakeChoice(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID, selectedOptionIndices []int) error
	DeletePlayerProgress(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error
	RetryStoryGeneration(ctx context.Context, storyID, userID uuid.UUID) error

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
}

type gameplayServiceImpl struct {
	configRepo           interfaces.StoryConfigRepository
	publishedRepo        interfaces.PublishedStoryRepository
	sceneRepo            interfaces.StorySceneRepository
	playerProgressRepo   interfaces.PlayerProgressRepository
	likeRepo             interfaces.LikeRepository
	draftService         DraftService
	publishingService    PublishingService
	likeService          LikeService
	storyBrowsingService StoryBrowsingService
	gameLoopService      GameLoopService
	publisher            messaging.TaskPublisher
	pool                 *pgxpool.Pool
	logger               *zap.Logger
}

func NewGameplayService(
	configRepo interfaces.StoryConfigRepository,
	publishedRepo interfaces.PublishedStoryRepository,
	sceneRepo interfaces.StorySceneRepository,
	playerProgressRepo interfaces.PlayerProgressRepository,
	likeRepo interfaces.LikeRepository,
	publisher messaging.TaskPublisher,
	pool *pgxpool.Pool,
	logger *zap.Logger,
) GameplayService {
	// <<< СОЗДАЕМ DraftService >>>
	draftSvc := NewDraftService(configRepo, publisher, logger)
	// <<< СОЗДАЕМ PublishingService >>>
	publishingSvc := NewPublishingService(configRepo, publishedRepo, publisher, pool, logger)
	// <<< СОЗДАЕМ LikeService >>>
	likeSvc := NewLikeService(likeRepo, publishedRepo, playerProgressRepo, logger)
	// <<< СОЗДАЕМ StoryBrowsingService >>>
	storyBrowsingSvc := NewStoryBrowsingService(publishedRepo, sceneRepo, playerProgressRepo, logger)
	// <<< СОЗДАЕМ GameLoopService >>>
	gameLoopSvc := NewGameLoopService(publishedRepo, sceneRepo, playerProgressRepo, publisher, logger)

	return &gameplayServiceImpl{
		configRepo:           configRepo,
		publishedRepo:        publishedRepo,
		sceneRepo:            sceneRepo,
		playerProgressRepo:   playerProgressRepo,
		likeRepo:             likeRepo,
		draftService:         draftSvc,
		publishingService:    publishingSvc,
		likeService:          likeSvc,
		storyBrowsingService: storyBrowsingSvc,
		gameLoopService:      gameLoopSvc,
		publisher:            publisher,
		pool:                 pool,
		logger:               logger.Named("GameplayService"),
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
func (s *gameplayServiceImpl) ListLikedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*LikedStoryDetailDTO, string, error) {
	return s.likeService.ListLikedStories(ctx, userID, cursor, limit)
}

// === Конец делегированных методов LikeService ===

// === Методы, делегированные StoryBrowsingService ===

// ListMyPublishedStories delegates to StoryBrowsingService.
func (s *gameplayServiceImpl) ListMyPublishedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*PublishedStoryDetailWithProgressDTO, string, error) {
	return s.storyBrowsingService.ListMyPublishedStories(ctx, userID, cursor, limit)
}

// ListPublicStories delegates to StoryBrowsingService.
func (s *gameplayServiceImpl) ListPublicStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*PublishedStoryDetailWithProgressDTO, string, error) {
	return s.storyBrowsingService.ListPublicStories(ctx, userID, cursor, limit)
}

// GetPublishedStoryDetails delegates to StoryBrowsingService.
// Note: The return type *PublishedStoryDetailDTO now refers to the one defined in story_browsing_service.go
func (s *gameplayServiceImpl) GetPublishedStoryDetails(ctx context.Context, storyID, userID uuid.UUID) (*PublishedStoryDetailDTO, error) {
	return s.storyBrowsingService.GetPublishedStoryDetails(ctx, storyID, userID)
}

// ListUserPublishedStories delegates to StoryBrowsingService.
func (s *gameplayServiceImpl) ListUserPublishedStories(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*sharedModels.PublishedStory, error) {
	return s.storyBrowsingService.ListUserPublishedStories(ctx, userID, limit, offset)
}

// GetPublishedStoryDetailsWithProgress delegates to StoryBrowsingService.
func (s *gameplayServiceImpl) GetPublishedStoryDetailsWithProgress(ctx context.Context, userID, publishedStoryID uuid.UUID) (*PublishedStoryDetailWithProgressDTO, error) {
	return s.storyBrowsingService.GetPublishedStoryDetailsWithProgress(ctx, userID, publishedStoryID)
}

// === Конец делегированных методов StoryBrowsingService ===

// === Методы, делегированные GameLoopService ===

// GetStoryScene gets the current scene for the player.
func (s *gameplayServiceImpl) GetStoryScene(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (*sharedModels.StoryScene, error) {
	return s.gameLoopService.GetStoryScene(ctx, userID, publishedStoryID)
}

// MakeChoice handles player choice.
func (s *gameplayServiceImpl) MakeChoice(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID, selectedOptionIndices []int) error {
	return s.gameLoopService.MakeChoice(ctx, userID, publishedStoryID, selectedOptionIndices)
}

// DeletePlayerProgress deletes player progress for the specified story.
func (s *gameplayServiceImpl) DeletePlayerProgress(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error {
	return s.gameLoopService.DeletePlayerProgress(ctx, userID, publishedStoryID)
}

// RetryStoryGeneration delegates to GameLoopService.
func (s *gameplayServiceImpl) RetryStoryGeneration(ctx context.Context, storyID, userID uuid.UUID) error {
	return s.gameLoopService.RetrySceneGeneration(ctx, storyID, userID)
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

// UpdateSceneInternal delegates to GameLoopService.
func (s *gameplayServiceImpl) UpdateSceneInternal(ctx context.Context, sceneID uuid.UUID, contentJSON string) error {
	return s.gameLoopService.UpdateSceneInternal(ctx, sceneID, contentJSON)
}

// --- Helper Functions moved to game_loop_service.go ---
/*
func calculateStateHash(previousHash string, coreStats map[string]int, storyVars map[string]interface{}, globalFlags []string) (string, error) {
// ... (removed code) ...
}

func applyConsequences(progress *sharedModels.PlayerProgress, cons sharedModels.Consequences, setup *sharedModels.NovelSetupContent) (gameOverStat string, isGameOver bool) {
// ... (removed code) ...
}

func createGenerationPayload(
	userID uuid.UUID,
	story *sharedModels.PublishedStory,
	progress *sharedModels.PlayerProgress,
	previousHash string,
	nextStateHash string,
	madeChoicesInfo []userChoiceInfo,
) (sharedMessaging.GenerationTaskPayload, error) {
// ... (removed code) ...
}
*/
