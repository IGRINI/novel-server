package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	interfaces "novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

var ()

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
	AuthorName        string
	PublishedAt       time.Time
	Genre             string
	Language          string
	IsAdultContent    bool
	PlayerName        string
	PlayerDescription string
	WorldContext      string
	StorySummary      string
	CoreStats         map[string]CoreStatDetailDTO
	LastPlayedAt      *time.Time
	IsAuthor          bool
}

type CoreStatDetailDTO struct {
	Description        string
	InitialValue       int
	GameOverConditions []sharedModels.StatDefinition
}

type PublishedStorySummaryDTO struct {
	ID                uuid.UUID                `json:"id"`
	Title             string                   `json:"title"`
	ShortDescription  string                   `json:"short_description"`
	AuthorID          uuid.UUID                `json:"author_id"`
	AuthorName        string                   `json:"author_name"`
	PublishedAt       time.Time                `json:"published_at"`
	IsAdultContent    bool                     `json:"is_adult_content"`
	LikesCount        int                      `json:"likes_count"`
	IsLiked           bool                     `json:"is_liked"`
	HasPlayerProgress bool                     `json:"hasPlayerProgress"`
	IsPublic          bool                     `json:"is_public"`
	Status            sharedModels.StoryStatus `json:"status"`
}

type ParsedCharacterDTO struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Personality    string `json:"personality,omitempty"`
	ImageReference string `json:"imageReference,omitempty"`
}

/*
type GameStateSummaryDTO struct {
	ID             uuid.UUID `json:"id"`             // ID of the game state (gameStateID)
	LastActivityAt time.Time `json:"lastActivityAt"` // Time of the last activity in this save
	SceneIndex     int       `json:"sceneIndex"`     // <<< ДОБАВЛЕНО: Index of the current scene for this save
}*/

/*
type CoreStatDTO struct {
	Description  string `json:"description"`
	InitialValue int    `json:"initialValue"`
	GameOverMin  bool   `json:"gameOverMin"` // Game Over when Min is reached?
	GameOverMax  bool   `json:"gameOverMax"` // Game Over when Max is reached?
	Icon         string `json:"icon,omitempty"`
}*/

/*
type CharacterDTO struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Personality    string `json:"personality,omitempty"`
	ImageReference string `json:"imageReference,omitempty"`
}*/

type PublishedStoryParsedDetailDTO struct {
	ID             uuid.UUID `json:"id"`
	AuthorID       uuid.UUID `json:"author_id"`
	AuthorName     string    `json:"author_name"`
	PublishedAt    time.Time `json:"published_at"`
	LikesCount     int       `json:"likes_count"`
	IsLiked        bool      `json:"is_liked"`
	IsAuthor       bool      `json:"is_author"`
	IsPublic       bool      `json:"is_public"`
	IsAdultContent bool      `json:"is_adult_content"`
	Status         string    `json:"status"`

	Title            string                              `json:"title"`
	ShortDescription string                              `json:"short_description"`
	Genre            string                              `json:"genre"`
	Language         string                              `json:"language"`
	PlayerName       string                              `json:"player_name"`
	CoreStats        map[string]sharedModels.CoreStatDTO `json:"core_stats"`
	Characters       []sharedModels.CharacterDTO         `json:"characters"`
	CoverImageURL    *string                             `json:"cover_image_url,omitempty"`

	GameStates []*sharedModels.GameStateSummaryDTO `json:"game_states,omitempty"`
}

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

func (s *storyBrowsingServiceImpl) ListMyPublishedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*PublishedStorySummaryDTO, string, error) {
	log := s.logger.With(zap.String("userID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit))
	log.Info("ListMyPublishedStories called")

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	summaries, nextCursor, err := s.publishedRepo.ListUserSummariesWithProgress(ctx, userID, cursor, limit, false)
	if err != nil {
		log.Error("Failed to list user published stories summaries with progress", zap.Error(err))
		return nil, "", sharedModels.ErrInternalServer
	}

	results := make([]*PublishedStorySummaryDTO, 0, len(summaries))
	for _, summary := range summaries {
		results = append(results, &PublishedStorySummaryDTO{
			ID:                summary.ID,
			Title:             summary.Title,
			ShortDescription:  summary.ShortDescription,
			AuthorID:          summary.AuthorID,
			AuthorName:        summary.AuthorName,
			PublishedAt:       summary.PublishedAt,
			IsAdultContent:    summary.IsAdultContent,
			LikesCount:        int(summary.LikesCount),
			IsLiked:           summary.IsLiked,
			HasPlayerProgress: summary.HasPlayerProgress,
			IsPublic:          summary.IsPublic,
			Status:            summary.Status,
		})
	}

	return results, nextCursor, nil
}

func (s *storyBrowsingServiceImpl) ListPublicStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*PublishedStorySummaryDTO, string, error) {
	log := s.logger.With(zap.String("requestingUserID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit))
	log.Info("ListPublicStories called")

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	summaries, nextCursor, err := s.publishedRepo.ListUserSummariesWithProgress(ctx, userID, cursor, limit, true)
	if err != nil {
		s.logger.Error("Failed to list public stories summaries with progress", zap.Error(err))
		return nil, "", sharedModels.ErrInternalServer
	}

	results := make([]*PublishedStorySummaryDTO, 0, len(summaries))
	for _, summary := range summaries {
		results = append(results, &PublishedStorySummaryDTO{
			ID:                summary.ID,
			Title:             summary.Title,
			ShortDescription:  summary.ShortDescription,
			AuthorID:          summary.AuthorID,
			AuthorName:        summary.AuthorName,
			PublishedAt:       summary.PublishedAt,
			IsAdultContent:    summary.IsAdultContent,
			LikesCount:        int(summary.LikesCount),
			IsLiked:           summary.IsLiked,
			HasPlayerProgress: summary.HasPlayerProgress,
			IsPublic:          summary.IsPublic,
			Status:            summary.Status,
		})
	}

	log.Debug("Successfully listed public stories summaries with progress", zap.Int("count", len(results)), zap.Bool("hasNext", nextCursor != ""))
	return results, nextCursor, nil
}

func (s *storyBrowsingServiceImpl) GetPublishedStoryDetails(ctx context.Context, storyID, userID uuid.UUID) (*PublishedStoryParsedDetailDTO, error) {
	log := s.logger.With(zap.String("storyID", storyID.String()), zap.String("userID", userID.String()))
	log.Info("GetPublishedStoryDetails called")

	story, err := s.publishedRepo.GetByID(ctx, storyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("Published story not found")
			return nil, sharedModels.ErrNotFound
		}
		log.Error("Failed to get published story from repository", zap.Error(err))
		return nil, sharedModels.ErrInternalServer
	}

	isAuthor := story.UserID == userID
	if !story.IsPublic && !isAuthor {
		log.Warn("User forbidden to access private story")
		return nil, sharedModels.ErrForbidden
	}

	authorName := "Unknown Author"
	authorInfos, errAuth := s.authClient.GetUsersInfo(ctx, []uuid.UUID{story.UserID})
	if errAuth == nil {
		if info, ok := authorInfos[story.UserID]; ok {
			authorName = info.DisplayName
		}
	} else {
		log.Warn("Failed to get author info from auth service", zap.Error(errAuth))
	}

	isLiked := false
	if userID != uuid.Nil {
		liked, errLike := s.likeRepo.CheckLike(ctx, userID, storyID)
		if errLike != nil {
			log.Error("Failed to check if user liked the story", zap.Error(errLike))
		} else {
			isLiked = liked
		}
	}

	var config sharedModels.Config
	var setup sharedModels.NovelSetupContent
	var coreStatsDTO map[string]sharedModels.CoreStatDTO
	var charactersDTO []sharedModels.CharacterDTO

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
		coreStatsDTO = make(map[string]sharedModels.CoreStatDTO)
		for statID, statDef := range setup.CoreStatsDefinition {
			coreStatsDTO[statID] = sharedModels.CoreStatDTO{
				Description:  statDef.Description,
				InitialValue: statDef.Initial,
				GameOverMin:  statDef.GameOverConditions.Min,
				GameOverMax:  statDef.GameOverConditions.Max,
				Icon:         statDef.Icon,
			}
		}
		charactersDTO = make([]sharedModels.CharacterDTO, 0, len(setup.Characters))
		for _, charDef := range setup.Characters {
			charactersDTO = append(charactersDTO, sharedModels.CharacterDTO{
				Name:           charDef.Name,
				Description:    charDef.Description,
				Personality:    charDef.Personality,
				ImageReference: charDef.ImageRef,
			})
		}
	}

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

	var title string
	if story.Title != nil {
		title = *story.Title
	}
	var shortDesc string
	if story.Description != nil {
		shortDesc = *story.Description
	}

	dto := &PublishedStoryParsedDetailDTO{
		ID:             story.ID,
		AuthorID:       story.UserID,
		AuthorName:     authorName,
		PublishedAt:    story.CreatedAt,
		LikesCount:     int(story.LikesCount),
		IsLiked:        isLiked,
		IsAuthor:       isAuthor,
		IsPublic:       story.IsPublic,
		IsAdultContent: story.IsAdultContent,
		Status:         string(story.Status),

		Title:            title,
		ShortDescription: shortDesc,
		Genre:            config.Genre,
		Language:         config.Language,
		PlayerName:       config.PlayerName,
		CoreStats:        coreStatsDTO,
		Characters:       charactersDTO,
		CoverImageURL:    previewURL,
		GameStates:       make([]*sharedModels.GameStateSummaryDTO, 0),
	}

	gameStates, err := s.playerGameStateRepo.ListSummariesByPlayerAndStory(ctx, userID, storyID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.logger.Error("Failed to list game states for story details", zap.String("storyID", storyID.String()), zap.Stringer("userID", userID), zap.Error(err))
		return nil, fmt.Errorf("%w: failed to list game states: %v", sharedModels.ErrInternalServer, err)
	}

	dto.GameStates = gameStates

	log.Info("Successfully retrieved published story details with parsed data", zap.String("title", dto.Title))
	return dto, nil
}

func (s *storyBrowsingServiceImpl) ListUserPublishedStories(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*sharedModels.PublishedStory, bool, error) {
	log := s.logger.With(zap.String("userID", userID.String()), zap.Int("limit", limit), zap.Int("offset", offset))
	log.Info("ListUserPublishedStories called (offset-based)")

	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	fetchLimit := limit + 1

	stories, err := s.publishedRepo.ListByUserIDOffset(ctx, userID, fetchLimit, offset)
	if err != nil {
		log.Error("Error listing user published stories from repository (offset-based)", zap.Error(err))
		return nil, false, fmt.Errorf("repository error fetching stories: %w", err)
	}

	hasMore := len(stories) == fetchLimit
	if hasMore {
		stories = stories[:limit]
	}

	log.Info("Successfully listed user published stories (offset-based)", zap.Int("count", len(stories)), zap.Bool("hasMore", hasMore))
	return stories, hasMore, nil
}

func (s *storyBrowsingServiceImpl) GetPublishedStoryDetailsInternal(ctx context.Context, storyID uuid.UUID) (*sharedModels.PublishedStory, error) {
	log := s.logger.With(zap.String("storyID", storyID.String()))
	log.Info("GetPublishedStoryDetailsInternal called")

	story, err := s.publishedRepo.GetByID(ctx, storyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("Published story not found")
			return nil, sharedModels.ErrStoryNotFound
		}
		log.Error("Failed to get published story by ID", zap.Error(err))
		return nil, ErrInternal
	}

	log.Info("Successfully retrieved published story details for internal use")
	return story, nil
}

func (s *storyBrowsingServiceImpl) ListStoryScenesInternal(ctx context.Context, storyID uuid.UUID) ([]sharedModels.StoryScene, error) {
	log := s.logger.With(zap.String("storyID", storyID.String()))
	log.Info("ListStoryScenesInternal called")

	scenes, err := s.sceneRepo.ListByStoryID(ctx, storyID)
	if err != nil {
		log.Error("Failed to list story scenes", zap.Error(err))
		return nil, fmt.Errorf("failed to list story scenes from repository: %w", err)
	}

	log.Info("Successfully retrieved story scenes for internal use", zap.Int("count", len(scenes)))
	return scenes, nil
}

func (s *storyBrowsingServiceImpl) UpdateStoryInternal(ctx context.Context, storyID uuid.UUID, configJSON, setupJSON json.RawMessage, status sharedModels.StoryStatus) error {
	log := s.logger.With(zap.String("publishedStoryID", storyID.String()))
	log.Info("UpdateStoryInternal called")

	var validatedConfig, validatedSetup json.RawMessage

	if len(configJSON) > 0 && string(configJSON) != "null" {
		if !json.Valid(configJSON) {
			log.Warn("Invalid Config JSON provided for update")
			return fmt.Errorf("%w: invalid config JSON format", sharedModels.ErrBadRequest)
		}
		validatedConfig = configJSON
	} else {
		validatedConfig = nil
	}

	if len(setupJSON) > 0 && string(setupJSON) != "null" {
		if !json.Valid(setupJSON) {
			log.Warn("Invalid Setup JSON provided for update")
			return fmt.Errorf("%w: invalid setup JSON format", sharedModels.ErrBadRequest)
		}
		validatedSetup = setupJSON
	} else {
		validatedSetup = nil
	}

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

func (s *storyBrowsingServiceImpl) GetPublishedStoryDetailsWithProgress(ctx context.Context, userID, publishedStoryID uuid.UUID) (*PublishedStorySummaryDTO, error) {
	log := s.logger.With(zap.String("storyID", publishedStoryID.String()), zap.String("requestingUserID", userID.String()))
	log.Info("GetPublishedStoryDetailsWithProgress called")

	details, err := s.publishedRepo.GetSummaryWithDetails(ctx, publishedStoryID, userID)
	if err != nil {
		if errors.Is(err, sharedModels.ErrNotFound) {
			log.Warn("Published story summary with details not found")
			return nil, sharedModels.ErrNotFound
		}
		log.Error("Failed to get published story summary with details from repository", zap.Error(err))
		return nil, sharedModels.ErrInternalServer
	}

	isAuthor := details.AuthorID == userID
	if !details.IsPublic && !isAuthor {
		log.Warn("User does not have permission to view story detail with progress (private and not author)")
		return nil, sharedModels.ErrForbidden
	}

	resultDTO := &PublishedStorySummaryDTO{
		ID:                details.ID,
		Title:             details.Title,
		ShortDescription:  details.ShortDescription,
		AuthorID:          details.AuthorID,
		AuthorName:        details.AuthorName,
		PublishedAt:       details.PublishedAt,
		IsAdultContent:    details.IsAdultContent,
		LikesCount:        int(details.LikesCount),
		IsLiked:           details.IsLiked,
		HasPlayerProgress: details.HasPlayerProgress,
		IsPublic:          details.IsPublic,
		Status:            details.Status,
	}

	log.Info("Successfully retrieved published story summary with progress")
	return resultDTO, nil
}

func (s *storyBrowsingServiceImpl) GetStoriesWithProgress(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]sharedModels.PublishedStorySummaryWithProgress, string, error) {
	log := s.logger.With(zap.String("method", "GetStoriesWithProgress"), zap.String("userID", userID.String()), zap.Int("limit", limit), zap.String("cursor", cursor))
	log.Debug("Fetching stories with progress for user")

	stories, nextCursor, err := s.publishedRepo.FindWithProgressByUserID(ctx, userID, limit, cursor)
	if err != nil {
		log.Error("Failed to find stories with progress in repository", zap.Error(err))
		return nil, "", fmt.Errorf("%w: failed to retrieve stories with progress from repository: %v", ErrInternal, err)
	}

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
			} else {
				for id, info := range authorInfos {
					authorNames[id] = info.DisplayName
				}
			}
		}

		for i := range stories {
			if name, ok := authorNames[stories[i].AuthorID]; ok {
				stories[i].AuthorName = name
			} else {
				stories[i].AuthorName = "[unknown]"
			}
		}
	}

	log.Info("Successfully fetched stories with progress", zap.Int("count", len(stories)), zap.Bool("hasNext", nextCursor != ""))
	return stories, nextCursor, nil
}

func (s *storyBrowsingServiceImpl) GetParsedSetup(ctx context.Context, storyID uuid.UUID) (*sharedModels.NovelSetupContent, error) {
	log := s.logger.With(zap.String("storyID", storyID.String()))

	story, err := s.publishedRepo.GetByID(ctx, storyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("Published story not found for GetParsedSetup")
			return nil, sharedModels.ErrNotFound
		}
		log.Error("Failed to get published story for GetParsedSetup", zap.Error(err))
		return nil, sharedModels.ErrInternalServer
	}

	if len(story.Setup) == 0 || string(story.Setup) == "null" {
		log.Warn("Published story has nil or empty Setup JSON")
		return nil, fmt.Errorf("setup data is missing or invalid for story %s", storyID)
	}

	var novelSetup sharedModels.NovelSetupContent
	if err := json.Unmarshal(story.Setup, &novelSetup); err != nil {
		log.Error("Failed to unmarshal story Setup JSON", zap.Error(err))
		return nil, fmt.Errorf("%w: failed to parse setup data", sharedModels.ErrInternalServer)
	}

	return &novelSetup, nil
}

func (s *storyBrowsingServiceImpl) GetActiveStoryCount(ctx context.Context) (int, error) {
	log := s.logger.With(zap.String("method", "GetActiveStoryCount"))
	log.Debug("Counting active stories")

	count, err := s.publishedRepo.CountByStatus(ctx, sharedModels.StatusReady)
	if err != nil {
		log.Error("Failed to count active stories", zap.Error(err))
		return 0, fmt.Errorf("failed to count active stories: %w", err)
	}

	log.Info("Successfully counted active stories", zap.Int("count", count))
	return count, nil
}
