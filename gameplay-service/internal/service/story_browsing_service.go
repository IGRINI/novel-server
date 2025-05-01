package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	interfaces "novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	// pgx/v5 might be needed if we fetch author names or other details directly
)

// Define common errors used within this service
var (
// ErrInternal will be used from sharedModels.ErrInternalServer
// ErrForbidden will be used from sharedModels.ErrForbidden
// ErrStoryNotFound will be used from sharedModels.ErrStoryNotFound
)

// DTOs for browsing stories

type PublishedStoryDetailWithProgressDTO struct {
	sharedModels.PublishedStory
	AuthorName        string `json:"author_name"`
	HasPlayerProgress bool   `json:"hasPlayerProgress"`
}

type PublishedStoryDetailDTO struct {
	ID                uuid.UUID
	Title             string
	ShortDescription  string
	AuthorID          uuid.UUID
	AuthorName        string // Needs to be fetched, potentially from another service or joined query
	PublishedAt       time.Time
	Genre             string
	Language          string
	IsAdultContent    bool
	PlayerName        string
	PlayerDescription string
	WorldContext      string
	StorySummary      string
	CoreStats         map[string]CoreStatDetailDTO
	LastPlayedAt      *time.Time // Needs to be fetched from player progress
	IsAuthor          bool       // Determined by comparing AuthorID with the requesting UserID
}

type CoreStatDetailDTO struct {
	Description        string
	InitialValue       int
	GameOverConditions []sharedModels.StatDefinition
}

// PublishedStorySummaryDTO represents a summary of a published story for lists.
type PublishedStorySummaryDTO struct {
	ID                uuid.UUID                `json:"id"`
	Title             string                   `json:"title"`
	ShortDescription  string                   `json:"short_description"`
	AuthorID          uuid.UUID                `json:"author_id"`
	AuthorName        string                   `json:"author_name"`
	PublishedAt       time.Time                `json:"published_at"`
	IsAdultContent    bool                     `json:"is_adult_content"`
	LikesCount        int                      `json:"likes_count"`
	IsLiked           bool                     `json:"is_liked"`          // Is liked by the requesting user
	HasPlayerProgress bool                     `json:"hasPlayerProgress"` // Does the requesting user have progress
	IsPublic          bool                     `json:"is_public"`         // Visibility
	Status            sharedModels.StoryStatus `json:"status"`            // Added Status
}

// ParsedCharacterDTO represents concise character information for client response.
type ParsedCharacterDTO struct {
	Name           string `json:"name"`                     // 'n' from setup
	Description    string `json:"description"`              // 'd' from setup
	Personality    string `json:"personality,omitempty"`    // 'p' from setup
	ImageReference string `json:"imageReference,omitempty"` // NEW: Unique reference for the character image
}

// GameStateSummaryDTO represents a summary of a game state (save).
type GameStateSummaryDTO struct {
	ID             uuid.UUID `json:"id"`             // ID of the game state (gameStateID)
	LastActivityAt time.Time `json:"lastActivityAt"` // Time of the last activity in this save
}

// CoreStatDTO represents parsed data for a single stat.
type CoreStatDTO struct {
	Description  string `json:"description"`
	InitialValue int    `json:"initialValue"`
	GameOverMin  bool   `json:"gameOverMin"` // Game Over when Min is reached?
	GameOverMax  bool   `json:"gameOverMax"` // Game Over when Max is reached?
	Icon         string `json:"icon,omitempty"`
}

// CharacterDTO represents parsed data for a single character.
type CharacterDTO struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Personality    string `json:"personality,omitempty"`
	ImageReference string `json:"imageReference,omitempty"`
}

// PublishedStoryParsedDetailDTO represents detailed information about a published story
// with parsed config and setup fields, suitable for client response.
type PublishedStoryParsedDetailDTO struct {
	ID             uuid.UUID `json:"id"`             // ID of the story
	AuthorID       uuid.UUID `json:"authorId"`       // ID of the author
	AuthorName     string    `json:"authorName"`     // Author's name
	PublishedAt    time.Time `json:"publishedAt"`    // Publication time (actual creation time)
	LikesCount     int       `json:"likesCount"`     // Number of likes
	IsLiked        bool      `json:"isLiked"`        // Did the current user like the story
	IsAuthor       bool      `json:"isAuthor"`       // Is the current user the author of the story
	IsPublic       bool      `json:"isPublic"`       // Is the story public
	IsAdultContent bool      `json:"isAdultContent"` // 18+ flag (from config)
	Status         string    `json:"status"`         // Current story status (from PublishedStory)

	// Parsed fields from Config/Setup:
	Title            string                 `json:"title"`                     // Title (from Config)
	ShortDescription string                 `json:"shortDescription"`          // Short description (from Config)
	Genre            string                 `json:"genre"`                     // Genre (from Config)
	Language         string                 `json:"language"`                  // Language (from Config)
	PlayerName       string                 `json:"playerName"`                // Player name (from Config)
	CoreStats        map[string]CoreStatDTO `json:"coreStats"`                 // Stats (from Setup)
	Characters       []CharacterDTO         `json:"characters"`                // Characters (from Setup)
	PreviewImageURL  *string                `json:"previewImageUrl,omitempty"` // Preview image URL

	// Progress information (SAVE LIST):
	GameStates []GameStateSummaryDTO `json:"gameStates,omitempty"` // <<< NEW FIELD: List of user saves (TYPE FROM HANDLER)
}

// StoryBrowsingService defines methods for browsing and retrieving story details.
type StoryBrowsingService interface {
	ListMyPublishedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*PublishedStorySummaryDTO, string, error)
	ListPublicStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*PublishedStorySummaryDTO, string, error)
	GetPublishedStoryDetails(ctx context.Context, storyID, userID uuid.UUID) (*PublishedStoryParsedDetailDTO, error)
	ListUserPublishedStories(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*sharedModels.PublishedStory, bool, error)
	GetPublishedStoryDetailsInternal(ctx context.Context, storyID uuid.UUID) (*sharedModels.PublishedStory, error)
	ListStoryScenesInternal(ctx context.Context, storyID uuid.UUID) ([]sharedModels.StoryScene, error)
	UpdateStoryInternal(ctx context.Context, storyID uuid.UUID, configJSON, setupJSON json.RawMessage, status sharedModels.StoryStatus) error
	GetPublishedStoryDetailsWithProgress(ctx context.Context, userID, publishedStoryID uuid.UUID) (*PublishedStorySummaryDTO, error)
	GetStoriesWithProgress(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]sharedModels.PublishedStorySummaryWithProgress, string, error)
	GetParsedSetup(ctx context.Context, storyID uuid.UUID) (*sharedModels.NovelSetupContent, error)
	GetActiveStoryCount(ctx context.Context) (int, error)
}

type storyBrowsingServiceImpl struct {
	publishedRepo       interfaces.PublishedStoryRepository
	sceneRepo           interfaces.StorySceneRepository
	playerProgressRepo  interfaces.PlayerProgressRepository
	playerGameStateRepo interfaces.PlayerGameStateRepository
	likeRepo            interfaces.LikeRepository
	imageReferenceRepo  interfaces.ImageReferenceRepository
	authClient          interfaces.AuthServiceClient
	logger              *zap.Logger
}

// NewStoryBrowsingService creates a new instance of StoryBrowsingService.
func NewStoryBrowsingService(
	publishedRepo interfaces.PublishedStoryRepository,
	sceneRepo interfaces.StorySceneRepository,
	playerProgressRepo interfaces.PlayerProgressRepository,
	playerGameStateRepo interfaces.PlayerGameStateRepository,
	likeRepo interfaces.LikeRepository,
	imageReferenceRepo interfaces.ImageReferenceRepository,
	authClient interfaces.AuthServiceClient,
	logger *zap.Logger,
) StoryBrowsingService {
	return &storyBrowsingServiceImpl{
		publishedRepo:       publishedRepo,
		sceneRepo:           sceneRepo,
		playerProgressRepo:  playerProgressRepo,
		playerGameStateRepo: playerGameStateRepo,
		likeRepo:            likeRepo,
		imageReferenceRepo:  imageReferenceRepo,
		authClient:          authClient,
		logger:              logger.Named("StoryBrowsingService"),
	}
}

// ListMyPublishedStories returns a list of the user's published stories with progress flag.
func (s *storyBrowsingServiceImpl) ListMyPublishedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*PublishedStorySummaryDTO, string, error) {
	log := s.logger.With(zap.String("userID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit))
	log.Info("ListMyPublishedStories called")

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	stories, nextCursor, err := s.publishedRepo.ListByUserID(ctx, userID, cursor, limit)
	if err != nil {
		log.Error("Failed to list user published stories using cursor", zap.Error(err))
		return nil, "", sharedModels.ErrInternalServer
	}

	storyIDs := make([]uuid.UUID, len(stories))
	for i, story := range stories {
		storyIDs[i] = story.ID
	}

	// Fetch auxiliary data
	authorNames := s.fetchAuthorNames(ctx, stories)
	progressExistsMap := s.fetchProgressExists(ctx, userID, storyIDs)
	likesMap := make(map[uuid.UUID]bool)
	if userID != uuid.Nil && len(storyIDs) > 0 {
		for _, storyID := range storyIDs {
			liked, errLike := s.likeRepo.CheckLike(ctx, userID, storyID)
			if errLike != nil {
				s.logger.Error("Failed to check like for story in list", zap.String("storyID", storyID.String()), zap.Error(errLike))
				// Continue, assume not liked on error
			}
			likesMap[storyID] = liked
		}
	}

	results := make([]*PublishedStorySummaryDTO, 0, len(stories))
	for _, story := range stories {
		var title, description string
		var likesCount int
		if story.Title != nil {
			title = *story.Title
		}
		if story.Description != nil {
			description = *story.Description
		}
		likesCount = int(story.LikesCount)

		results = append(results, &PublishedStorySummaryDTO{
			ID:                story.ID,
			Title:             title,
			ShortDescription:  description,
			AuthorID:          story.UserID,
			AuthorName:        authorNames[story.UserID],
			PublishedAt:       story.CreatedAt,
			IsAdultContent:    story.IsAdultContent,
			LikesCount:        likesCount,
			IsLiked:           likesMap[story.ID],
			HasPlayerProgress: progressExistsMap[story.ID],
			IsPublic:          story.IsPublic,
			Status:            story.Status,
		})
	}

	log.Debug("Successfully listed user published stories with progress", zap.Int("count", len(results)), zap.Bool("hasNext", nextCursor != ""))
	return results, nextCursor, nil
}

// ListPublicStories returns a list of public published stories with progress flag for the requesting user.
func (s *storyBrowsingServiceImpl) ListPublicStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*PublishedStorySummaryDTO, string, error) {
	log := s.logger.With(zap.String("requestingUserID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit))
	log.Info("ListPublicStories called")

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	stories, nextCursor, err := s.publishedRepo.ListPublic(ctx, cursor, limit)
	if err != nil {
		log.Error("Failed to list public stories using cursor", zap.Error(err))
		return nil, "", sharedModels.ErrInternalServer
	}

	storyIDs := make([]uuid.UUID, len(stories))
	for i, story := range stories {
		storyIDs[i] = story.ID
	}

	// Fetch auxiliary data
	authorNames := s.fetchAuthorNames(ctx, stories)
	progressExistsMap := s.fetchProgressExists(ctx, userID, storyIDs)
	likesMap := make(map[uuid.UUID]bool)
	if userID != uuid.Nil && len(storyIDs) > 0 {
		for _, storyID := range storyIDs {
			liked, errLike := s.likeRepo.CheckLike(ctx, userID, storyID)
			if errLike != nil {
				s.logger.Error("Failed to check like for public story in list", zap.String("storyID", storyID.String()), zap.Error(errLike))
				// Continue, assume not liked on error
			}
			likesMap[storyID] = liked
		}
	}

	results := make([]*PublishedStorySummaryDTO, 0, len(stories))
	for _, story := range stories {
		var title, description string
		var likesCount int
		if story.Title != nil {
			title = *story.Title
		}
		if story.Description != nil {
			description = *story.Description
		}
		likesCount = int(story.LikesCount)

		results = append(results, &PublishedStorySummaryDTO{
			ID:                story.ID,
			Title:             title,
			ShortDescription:  description,
			AuthorID:          story.UserID,
			AuthorName:        authorNames[story.UserID],
			PublishedAt:       story.CreatedAt,
			IsAdultContent:    story.IsAdultContent,
			LikesCount:        likesCount,
			IsLiked:           likesMap[story.ID],
			HasPlayerProgress: progressExistsMap[story.ID],
			IsPublic:          story.IsPublic,
			Status:            story.Status,
		})
	}

	log.Debug("Successfully listed public stories with progress", zap.Int("count", len(results)), zap.Bool("hasNext", nextCursor != ""))
	return results, nextCursor, nil
}

// Helper to fetch author names
func (s *storyBrowsingServiceImpl) fetchAuthorNames(ctx context.Context, stories []*sharedModels.PublishedStory) map[uuid.UUID]string {
	authorIDsMap := make(map[uuid.UUID]struct{})
	for _, story := range stories {
		if story != nil {
			authorIDsMap[story.UserID] = struct{}{}
		}
	}
	uniqueAuthorIDs := make([]uuid.UUID, 0, len(authorIDsMap))
	for id := range authorIDsMap {
		uniqueAuthorIDs = append(uniqueAuthorIDs, id)
	}

	authorNames := make(map[uuid.UUID]string)
	if len(uniqueAuthorIDs) > 0 {
		authorInfos, err := s.authClient.GetUsersInfo(ctx, uniqueAuthorIDs)
		if err != nil {
			s.logger.Warn("Failed to fetch author details from auth-service, names will be empty", zap.Error(err))
		} else {
			for _, id := range uniqueAuthorIDs {
				if info, ok := authorInfos[id]; ok {
					authorNames[id] = info.DisplayName
				} else {
					authorNames[id] = "[unknown]"
				}
			}
		}
	}
	return authorNames
}

// Helper to fetch progress existence
func (s *storyBrowsingServiceImpl) fetchProgressExists(ctx context.Context, userID uuid.UUID, storyIDs []uuid.UUID) map[uuid.UUID]bool {
	progressExistsMap := make(map[uuid.UUID]bool)
	if userID != uuid.Nil && len(storyIDs) > 0 {
		var errProgress error
		progressExistsMap, errProgress = s.playerGameStateRepo.CheckGameStateExistsForStories(ctx, userID, storyIDs)
		if errProgress != nil {
			s.logger.Error("Failed to check player progress (batch)", zap.Error(errProgress))
			progressExistsMap = make(map[uuid.UUID]bool) // Return empty map on error
		}
	}
	return progressExistsMap
}

// GetPublishedStoryDetails retrieves the details of a published story with parsed config/setup.
func (s *storyBrowsingServiceImpl) GetPublishedStoryDetails(ctx context.Context, storyID, userID uuid.UUID) (*PublishedStoryParsedDetailDTO, error) {
	log := s.logger.With(zap.String("storyID", storyID.String()), zap.String("userID", userID.String()))
	log.Info("GetPublishedStoryDetails called")

	// 1. Получаем опубликованную историю
	story, err := s.publishedRepo.GetByID(ctx, storyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("Published story not found")
			return nil, sharedModels.ErrNotFound // Используем стандартную ошибку
		}
		log.Error("Failed to get published story from repository", zap.Error(err))
		return nil, sharedModels.ErrInternalServer // Используем стандартную ошибку
	}

	// 2. Проверяем доступ (если история не публичная и пользователь не автор)
	isAuthor := story.UserID == userID
	if !story.IsPublic && !isAuthor {
		log.Warn("User forbidden to access private story")
		return nil, sharedModels.ErrForbidden
	}

	// 3. Получаем имя автора
	authorName := "Unknown Author"
	authorInfos, errAuth := s.authClient.GetUsersInfo(ctx, []uuid.UUID{story.UserID})
	if errAuth == nil {
		if info, ok := authorInfos[story.UserID]; ok {
			authorName = info.DisplayName
		}
	} else {
		log.Warn("Failed to get author info from auth service", zap.Error(errAuth))
		// Продолжаем без имени автора
	}

	// 4. Проверяем, лайкнул ли пользователь историю
	isLiked := false
	if userID != uuid.Nil {
		liked, errLike := s.likeRepo.CheckLike(ctx, userID, storyID)
		if errLike != nil {
			log.Error("Failed to check if user liked the story", zap.Error(errLike))
			// Не фатально, продолжаем
		} else {
			isLiked = liked
		}
	}

	// 5. Парсим Config и Setup
	var config sharedModels.Config
	var setup sharedModels.NovelSetupContent
	var coreStatsDTO map[string]CoreStatDTO
	var charactersDTO []CharacterDTO

	if story.Config != nil {
		if err := json.Unmarshal(story.Config, &config); err != nil {
			log.Error("Failed to unmarshal story Config JSON", zap.Error(err))
		}
	}
	parsedSetup, errSetup := s.GetParsedSetup(ctx, storyID)
	if errSetup != nil {
		log.Error("Failed to get/parse story Setup JSON", zap.Error(errSetup))
	} else {
		setup = *parsedSetup
		coreStatsDTO = make(map[string]CoreStatDTO)
		for key, statDef := range setup.CoreStatsDefinition {
			coreStatsDTO[key] = CoreStatDTO{
				Description:  statDef.Description,
				InitialValue: statDef.Initial,
				GameOverMin:  statDef.GameOverConditions.Min,
				GameOverMax:  statDef.GameOverConditions.Max,
				Icon:         statDef.Icon,
			}
		}
		charactersDTO = make([]CharacterDTO, 0, len(setup.Characters))
		for _, char := range setup.Characters {
			charactersDTO = append(charactersDTO, CharacterDTO{
				Name:           char.Name,
				Description:    char.Description,
				Personality:    char.Personality,
				ImageReference: char.ImageRef,
			})
		}
	}

	// 6. Получаем URL превью-картинки
	previewRef := fmt.Sprintf("history_preview_%s", story.ID.String())
	var previewURL *string
	if s.imageReferenceRepo != nil {
		url, errPreview := s.imageReferenceRepo.GetImageURLByReference(ctx, previewRef)
		if errPreview != nil && !errors.Is(errPreview, sharedModels.ErrNotFound) {
			log.Error("Failed to get preview image URL", zap.String("ref", previewRef), zap.Error(errPreview))
		} else if errors.Is(errPreview, sharedModels.ErrNotFound) {
			log.Debug("Preview image not found for story", zap.String("ref", previewRef))
		} else {
			previewURL = &url
		}
	} else {
		log.Warn("imageReferenceRepo is nil in storyBrowsingServiceImpl, cannot fetch preview URL")
	}

	// 7. Получаем список состояний игры (сохранений) для пользователя
	gameStates := make([]GameStateSummaryDTO, 0)
	if userID != uuid.Nil {
		playerStates, errStates := s.playerGameStateRepo.ListByPlayerAndStory(ctx, userID, storyID)
		if errStates != nil {
			log.Error("Failed to get player game states for story details", zap.Error(errStates))
		} else {
			for _, gs := range playerStates {
				if gs != nil {
					gameStates = append(gameStates, GameStateSummaryDTO{
						ID:             gs.ID,
						LastActivityAt: gs.LastActivityAt,
					})
				}
			}
			sort.Slice(gameStates, func(i, j int) bool {
				return gameStates[i].LastActivityAt.After(gameStates[j].LastActivityAt)
			})
		}
	}

	// 8. Формируем финальный DTO
	dto := &PublishedStoryParsedDetailDTO{
		ID:               story.ID,
		AuthorID:         story.UserID,
		AuthorName:       authorName,
		PublishedAt:      story.CreatedAt,
		LikesCount:       int(story.LikesCount),
		IsLiked:          isLiked,
		IsAuthor:         isAuthor,
		IsPublic:         story.IsPublic,
		IsAdultContent:   config.IsAdultContent,
		Status:           string(story.Status),
		Title:            config.Title,
		ShortDescription: config.ShortDescription,
		Genre:            config.Genre,
		Language:         config.Language,
		PlayerName:       config.PlayerName,
		CoreStats:        coreStatsDTO,
		Characters:       charactersDTO,
		PreviewImageURL:  previewURL,
		GameStates:       gameStates,
	}

	log.Info("Published story details retrieved successfully")
	return dto, nil
}

// ListUserPublishedStories retrieves a paginated list of PublishedStory for a specific user ID.
// Note: This uses offset/limit, which is generally discouraged in favor of cursors.
// It might be a legacy method or used internally.
func (s *storyBrowsingServiceImpl) ListUserPublishedStories(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*sharedModels.PublishedStory, bool, error) {
	log := s.logger.With(zap.String("userID", userID.String()), zap.Int("limit", limit), zap.Int("offset", offset))
	log.Info("ListUserPublishedStories called (offset-based)")

	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	// <<< ИЗМЕНЕНО: Запрашиваем limit + 1 элемент для определения hasMore >>>
	fetchLimit := limit + 1

	// <<< ИЗМЕНЕНО: Вызываем новый метод репозитория с offset/limit >>>
	stories, err := s.publishedRepo.ListByUserIDOffset(ctx, userID, fetchLimit, offset)
	if err != nil {
		log.Error("Error listing user published stories from repository (offset-based)", zap.Error(err))
		// Не возвращаем ErrInternal, а пробрасываем ошибку из репозитория
		return nil, false, fmt.Errorf("repository error fetching stories: %w", err)
	}

	// <<< ИЗМЕНЕНО: Определяем hasMore и обрезаем список >>>
	hasMore := len(stories) == fetchLimit
	if hasMore {
		stories = stories[:limit] // Возвращаем только limit элементов
	}

	log.Info("Successfully listed user published stories (offset-based)", zap.Int("count", len(stories)), zap.Bool("hasMore", hasMore))
	return stories, hasMore, nil
}

// GetPublishedStoryDetailsInternal retrieves the details of a published story for internal use.
func (s *storyBrowsingServiceImpl) GetPublishedStoryDetailsInternal(ctx context.Context, storyID uuid.UUID) (*sharedModels.PublishedStory, error) {
	log := s.logger.With(zap.String("storyID", storyID.String()))
	log.Info("GetPublishedStoryDetailsInternal called")

	// 1. Get the core PublishedStory data
	story, err := s.publishedRepo.GetByID(ctx, storyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) { // Assuming repo uses pgx errors
			log.Warn("Published story not found")
			return nil, sharedModels.ErrStoryNotFound // Use specific shared error
		}
		log.Error("Failed to get published story by ID", zap.Error(err))
		return nil, ErrInternal // Use local error from gameplay_service scope
	}

	log.Info("Successfully retrieved published story details for internal use")
	return story, nil
}

// ListStoryScenesInternal retrieves a list of scenes for a published story for internal use.
func (s *storyBrowsingServiceImpl) ListStoryScenesInternal(ctx context.Context, storyID uuid.UUID) ([]sharedModels.StoryScene, error) {
	log := s.logger.With(zap.String("storyID", storyID.String()))
	log.Info("ListStoryScenesInternal called")

	// 2. Fetch scenes
	scenes, err := s.sceneRepo.ListByStoryID(ctx, storyID)
	if err != nil {
		log.Error("Failed to list story scenes", zap.Error(err))
		return nil, fmt.Errorf("failed to list story scenes from repository: %w", err)
	}

	log.Info("Successfully retrieved story scenes for internal use", zap.Int("count", len(scenes)))
	return scenes, nil
}

// UpdateStoryInternal updates the Config and Setup JSON fields of a published story. (Admin only)
// It also validates the input JSON fields.
func (s *storyBrowsingServiceImpl) UpdateStoryInternal(ctx context.Context, storyID uuid.UUID, configJSON, setupJSON json.RawMessage, status sharedModels.StoryStatus) error {
	log := s.logger.With(zap.String("publishedStoryID", storyID.String()))
	log.Info("UpdateStoryInternal called")

	var validatedConfig, validatedSetup json.RawMessage

	// Validate Config JSON
	if configJSON != nil && len(configJSON) > 0 && string(configJSON) != "null" {
		if !json.Valid(configJSON) {
			log.Warn("Invalid Config JSON provided for update")
			return fmt.Errorf("%w: invalid config JSON format", sharedModels.ErrBadRequest)
		}
		validatedConfig = configJSON
	} else {
		// Allow clearing config by passing null or empty
		validatedConfig = nil
	}

	// Validate Setup JSON
	if setupJSON != nil && len(setupJSON) > 0 && string(setupJSON) != "null" {
		if !json.Valid(setupJSON) {
			log.Warn("Invalid Setup JSON provided for update")
			return fmt.Errorf("%w: invalid setup JSON format", sharedModels.ErrBadRequest)
		}
		// <<< ДОПОЛНИТЕЛЬНО: Проверка структуры Setup, если нужно >>>
		/*
			var tempSetup models.NovelSetupContent
			if err := json.Unmarshal(setupJSON, &tempSetup); err != nil {
			    log.Warn("Failed to unmarshal Setup JSON into expected structure", zap.Error(err))
			    return fmt.Errorf("%w: setup JSON does not match expected structure: %v", sharedModels.ErrBadRequest, err)
			}
		*/
		validatedSetup = setupJSON
	} else {
		// Allow clearing setup by passing null or empty
		validatedSetup = nil
	}

	// Call repository to update
	err := s.publishedRepo.UpdateConfigAndSetupAndStatus(ctx, storyID, validatedConfig, validatedSetup, status)
	if err != nil {
		if errors.Is(err, sharedModels.ErrNotFound) {
			log.Warn("Story not found for internal update")
			return sharedModels.ErrNotFound
		}
		log.Error("Failed to update story internally in repository", zap.Error(err))
		return sharedModels.ErrInternalServer
	}

	log.Info("Story config/setup/status updated successfully internally")
	return nil
}

// GetPublishedStoryDetailsWithProgress retrieves details for display, including progress flag.
func (s *storyBrowsingServiceImpl) GetPublishedStoryDetailsWithProgress(ctx context.Context, userID, publishedStoryID uuid.UUID) (*PublishedStorySummaryDTO, error) {
	log := s.logger.With(zap.String("storyID", publishedStoryID.String()), zap.String("requestingUserID", userID.String()))
	log.Info("GetPublishedStoryDetailsWithProgress called") // Log the correct function name

	// 1. Get the core PublishedStory data
	story, err := s.publishedRepo.GetByID(ctx, publishedStoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sharedModels.ErrNotFound) {
			log.Warn("Published story not found for detail with progress")
			return nil, sharedModels.ErrNotFound
		}
		log.Error("Failed to get published story by ID for detail with progress", zap.Error(err))
		return nil, sharedModels.ErrInternalServer
	}

	// 2. Check visibility
	isAuthor := story.UserID == userID
	if !story.IsPublic && !isAuthor {
		log.Warn("User does not have permission to view story detail with progress (private and not author)")
		return nil, sharedModels.ErrForbidden
	}

	// 3. Fetch Author Name
	authorName := s.fetchAuthorNames(ctx, []*sharedModels.PublishedStory{story})[story.UserID]

	// 4. Check Player Progress
	hasProgress := s.fetchProgressExists(ctx, userID, []uuid.UUID{story.ID})[story.ID]

	// 5. Check if Liked by user
	isLiked := false
	if userID != uuid.Nil {
		liked, errLike := s.likeRepo.CheckLike(ctx, userID, publishedStoryID)
		if errLike != nil {
			s.logger.Error("Failed to check like for story summary", zap.Error(errLike))
		} else {
			isLiked = liked
		}
	}

	// 6. Construct the result DTO
	var title, description string
	var likesCount int
	if story.Title != nil {
		title = *story.Title
	}
	if story.Description != nil {
		description = *story.Description
	}
	likesCount = int(story.LikesCount)

	resultDTO := &PublishedStorySummaryDTO{
		ID:                story.ID,
		Title:             title,
		ShortDescription:  description,
		AuthorID:          story.UserID,
		AuthorName:        authorName,
		PublishedAt:       story.CreatedAt,
		IsAdultContent:    story.IsAdultContent,
		LikesCount:        likesCount,
		IsLiked:           isLiked,
		HasPlayerProgress: hasProgress,
		IsPublic:          story.IsPublic,
		Status:            story.Status,
	}

	log.Info("Successfully retrieved published story summary with progress")
	return resultDTO, nil
}

// GetStoriesWithProgress возвращает список историй, в которых у пользователя есть прогресс.
func (s *storyBrowsingServiceImpl) GetStoriesWithProgress(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]sharedModels.PublishedStorySummaryWithProgress, string, error) {
	log := s.logger.With(zap.String("method", "GetStoriesWithProgress"), zap.String("userID", userID.String()), zap.Int("limit", limit), zap.String("cursor", cursor))
	log.Debug("Fetching stories with progress for user")

	// Вызываем метод репозитория
	stories, nextCursor, err := s.publishedRepo.FindWithProgressByUserID(ctx, userID, limit, cursor)
	if err != nil {
		log.Error("Failed to find stories with progress in repository", zap.Error(err))
		return nil, "", fmt.Errorf("%w: failed to retrieve stories with progress from repository: %v", ErrInternal, err)
	}

	// Обогащение данных: получаем имена авторов
	if len(stories) > 0 {
		authorIDs := make([]uuid.UUID, 0, len(stories))
		authorIDSet := make(map[uuid.UUID]struct{})
		for _, s := range stories {
			if _, exists := authorIDSet[s.AuthorID]; !exists {
				authorIDSet[s.AuthorID] = struct{}{}
				authorIDs = append(authorIDs, s.AuthorID)
			}
		}

		authorNames := make(map[uuid.UUID]string)
		if len(authorIDs) > 0 {
			authorInfos, authErr := s.authClient.GetUsersInfo(ctx, authorIDs)
			if authErr != nil {
				log.Warn("Failed to fetch author names for stories with progress", zap.Error(authErr))
				// Не прерываем, просто имена будут пустыми или "[unknown]"
			} else {
				for id, info := range authorInfos {
					authorNames[id] = info.DisplayName
				}
			}
		}

		// Применяем полученные имена
		for i := range stories {
			if name, ok := authorNames[stories[i].AuthorID]; ok {
				stories[i].AuthorName = name
			} else {
				stories[i].AuthorName = "[unknown]" // Заглушка, если имя не найдено
			}
		}
	}

	log.Info("Successfully fetched stories with progress", zap.Int("count", len(stories)), zap.Bool("hasNext", nextCursor != ""))
	return stories, nextCursor, nil
}

// GetParsedSetup retrieves the parsed setup for a published story.
func (s *storyBrowsingServiceImpl) GetParsedSetup(ctx context.Context, storyID uuid.UUID) (*sharedModels.NovelSetupContent, error) {
	log := s.logger.With(zap.String("storyID", storyID.String()))

	story, err := s.publishedRepo.GetByID(ctx, storyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("Published story not found for GetParsedSetup")
			return nil, sharedModels.ErrNotFound // Используем общую ошибку
		}
		log.Error("Failed to get published story for GetParsedSetup", zap.Error(err))
		return nil, sharedModels.ErrInternalServer // Используем общую ошибку
	}

	if len(story.Setup) == 0 || string(story.Setup) == "null" {
		log.Warn("Published story has nil or empty Setup JSON")
		// Возвращаем ошибку или пустой объект? Если Setup обязателен, то ошибка.
		return nil, fmt.Errorf("setup data is missing or invalid for story %s", storyID)
	}

	var novelSetup sharedModels.NovelSetupContent
	if err := json.Unmarshal(story.Setup, &novelSetup); err != nil {
		log.Error("Failed to unmarshal story Setup JSON", zap.Error(err))
		// Ошибка парсинга - считаем внутренней ошибкой сервера
		return nil, fmt.Errorf("%w: failed to parse setup data", sharedModels.ErrInternalServer)
	}

	return &novelSetup, nil
}

// GetActiveStoryCount возвращает количество историй со статусом 'ready'.
func (s *storyBrowsingServiceImpl) GetActiveStoryCount(ctx context.Context) (int, error) {
	log := s.logger.With(zap.String("method", "GetActiveStoryCount"))
	log.Debug("Counting active stories")

	count, err := s.publishedRepo.CountByStatus(ctx, sharedModels.StatusReady)
	if err != nil {
		log.Error("Failed to count active stories", zap.Error(err))
		// Не маскируем ошибку репозитория
		return 0, fmt.Errorf("failed to count active stories: %w", err)
	}

	log.Info("Successfully counted active stories", zap.Int("count", count))
	return count, nil
}
