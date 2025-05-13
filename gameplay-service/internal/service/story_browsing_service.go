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
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

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

// CoreStatDetailDTO представляет свойства одного параметра статистики и условия конца игры
type CoreStatDetailDTO struct {
	Description  string `json:"description"`
	InitialValue int    `json:"initial_value"`
	GameOverMin  bool   `json:"game_over_min"`
	GameOverMax  bool   `json:"game_over_max"`
	Icon         string `json:"icon,omitempty"`
}

type ParsedCharacterDTO struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Personality    string `json:"personality,omitempty"`
	ImageReference string `json:"imageReference,omitempty"`
}

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

	Title            string                       `json:"title"`
	ShortDescription string                       `json:"short_description"`
	Genre            string                       `json:"genre"`
	Language         string                       `json:"language"`
	PlayerName       string                       `json:"player_name"`
	CoreStats        map[string]CoreStatDetailDTO `json:"core_stats"`
	Characters       []ParsedCharacterDTO         `json:"characters"`

	GameStates []*GameStateSummary `json:"game_states,omitempty"`
}

type StoryBrowsingService interface {
	ListMyPublishedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.PublishedStorySummary, string, error)
	ListPublicStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.PublishedStorySummary, string, error)
	GetPublishedStoryDetails(ctx context.Context, storyID, userID uuid.UUID) (*PublishedStoryParsedDetailDTO, error)
	ListUserPublishedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*sharedModels.PublishedStory, string, error)
	GetPublishedStoryDetailsInternal(ctx context.Context, storyID uuid.UUID) (*sharedModels.PublishedStory, error)
	ListStoryScenesInternal(ctx context.Context, storyID uuid.UUID) ([]sharedModels.StoryScene, error)
	UpdateStoryInternal(ctx context.Context, storyID uuid.UUID, configJSON, setupJSON json.RawMessage, status sharedModels.StoryStatus) error
	GetPublishedStoryDetailsWithProgress(ctx context.Context, userID, publishedStoryID uuid.UUID) (*sharedModels.PublishedStorySummary, error)
	GetStoriesWithProgress(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]sharedModels.PublishedStorySummary, string, error)
	GetParsedSetup(ctx context.Context, storyID uuid.UUID) (*sharedModels.NovelSetupContent, error)
	GetActiveStoryCount(ctx context.Context) (int, error)
	ListMyStoriesWithProgress(ctx context.Context, userID uuid.UUID, cursor string, limit int, filterAdult bool) ([]sharedModels.PublishedStorySummary, string, error)
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
	db                  *pgxpool.Pool
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
	db *pgxpool.Pool,
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
		db:                  db,
	}
}

func (s *storyBrowsingServiceImpl) ListMyPublishedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.PublishedStorySummary, string, error) {
	log := s.logger.With(zap.String("userID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit))
	log.Info("ListMyPublishedStories called")

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	summaries, nextCursor, err := s.publishedRepo.ListUserSummariesWithProgress(ctx, s.db, userID, cursor, limit, false)
	if err != nil {
		log.Error("Failed to list user published stories summaries with progress", zap.Error(err))
		return nil, "", sharedModels.ErrInternalServer
	}

	// Добавляем поля player_game_state_id и player_game_status по логике выбора состояния
	for i := range summaries {
		if summaries[i].HasPlayerProgress {
			states, err := s.playerGameStateRepo.ListByPlayerAndStory(ctx, s.db, userID, summaries[i].ID)
			if err == nil && len(states) > 0 {
				// Выбираем все состояния со статусом != error
				var valid []*sharedModels.PlayerGameState
				for _, st := range states {
					if st.PlayerStatus != sharedModels.PlayerStatusError {
						valid = append(valid, st)
					}
				}
				// Если нет валидных, берем все
				if len(valid) == 0 {
					valid = states
				}
				// Выбираем самое последнее по LastActivityAt
				chosen := valid[0]
				for _, st := range valid[1:] {
					if st.LastActivityAt.After(chosen.LastActivityAt) {
						chosen = st
					}
				}
				// Заполняем поля в summary
				summaries[i].PlayerGameStateID = &chosen.ID
				status := string(chosen.PlayerStatus)
				summaries[i].PlayerGameStatus = &status
			}
		}
	}
	return summaries, nextCursor, nil
}

func (s *storyBrowsingServiceImpl) ListPublicStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.PublishedStorySummary, string, error) {
	log := s.logger.With(zap.String("requestingUserID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit))
	log.Info("ListPublicStories called")

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	var requestingUserID *uuid.UUID
	if userID != uuid.Nil {
		requestingUserID = &userID
	}

	summaries, nextCursor, err := s.publishedRepo.ListPublicSummaries(ctx, s.db, requestingUserID, cursor, limit, "default")
	if err != nil {
		s.logger.Error("Failed to list public stories summaries", zap.Error(err))
		return nil, "", sharedModels.ErrInternalServer
	}

	log.Info("Successfully listed public stories summaries", zap.Int("count", len(summaries)), zap.Bool("hasNext", nextCursor != ""))
	return summaries, nextCursor, nil
}

func (s *storyBrowsingServiceImpl) GetPublishedStoryDetails(ctx context.Context, storyID, userID uuid.UUID) (*PublishedStoryParsedDetailDTO, error) {
	log := s.logger.With(zap.String("storyID", storyID.String()), zap.String("userID", userID.String()))
	log.Info("GetPublishedStoryDetails called")

	story, isLiked, err := s.publishedRepo.GetWithLikeStatus(ctx, s.db, storyID, userID)
	if err != nil {
		if errors.Is(err, sharedModels.ErrNotFound) {
			log.Warn("Published story not found")
			return nil, sharedModels.ErrNotFound
		}
		log.Error("Failed to get published story with like status from repository", zap.Error(err))
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

	var config sharedModels.Config
	var setup sharedModels.NovelSetupContent
	var coreStatsDTO map[string]CoreStatDetailDTO
	var charactersDTO []ParsedCharacterDTO

	if story.Config != nil && len(story.Config) > 0 && string(story.Config) != "null" {
		if err := json.Unmarshal(story.Config, &config); err != nil {
			log.Error("Failed to unmarshal story Config JSON", zap.Stringer("storyID", story.ID), zap.Error(err))
			return nil, fmt.Errorf("%w: invalid config data: %v", sharedModels.ErrInternalServer, err)
		}
	} else {
		log.Warn("Story Config JSON is nil or empty", zap.Stringer("storyID", story.ID))
	}

	if story.Setup != nil && len(story.Setup) > 0 && string(story.Setup) != "null" {
		if err := json.Unmarshal(story.Setup, &setup); err != nil {
			log.Error("Failed to unmarshal story Setup JSON directly", zap.Stringer("storyID", story.ID), zap.Error(err))
			return nil, fmt.Errorf("%w: invalid setup data: %v", sharedModels.ErrInternalServer, err)
		}

		coreStatsDTO = make(map[string]CoreStatDetailDTO)
		for statID, statDef := range setup.CoreStatsDefinition {
			coreStatsDTO[statID] = CoreStatDetailDTO{
				Description:  statDef.Description,
				InitialValue: statDef.Initial,
				GameOverMin:  statDef.Go.Min,
				GameOverMax:  statDef.Go.Max,
				Icon:         statDef.Icon,
			}
		}
		charactersDTO = make([]ParsedCharacterDTO, 0, len(setup.Characters))
		for _, charDef := range setup.Characters {
			charactersDTO = append(charactersDTO, ParsedCharacterDTO{
				Name:           charDef.Name,
				Personality:    charDef.Traits,
				ImageReference: charDef.ImageReferenceName,
			})
		}
	} else {
		log.Warn("Story Setup JSON is nil or empty", zap.Stringer("storyID", story.ID))
		coreStatsDTO = make(map[string]CoreStatDetailDTO)
		charactersDTO = make([]ParsedCharacterDTO, 0)
	}

	states, err := s.playerGameStateRepo.ListByPlayerAndStory(ctx, s.db, userID, storyID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.logger.Error("Failed to list player game states", zap.String("storyID", storyID.String()), zap.Stringer("userID", userID), zap.Error(err))
		return nil, fmt.Errorf("%w: failed to list game states: %v", sharedModels.ErrInternalServer, err)
	}
	gameStates := make([]*GameStateSummary, 0, len(states))
	for _, gs := range states {
		progress, errProg := s.playerProgressRepo.GetByID(ctx, s.db, gs.PlayerProgressID)
		if errProg != nil {
			s.logger.Error("Failed to get player progress for state summary", zap.String("stateID", gs.ID.String()), zap.Error(errProg))
			continue
		}
		gameStates = append(gameStates, &GameStateSummary{
			ID:                  gs.ID,
			LastActivityAt:      gs.LastActivityAt,
			SceneIndex:          progress.SceneIndex,
			CurrentSceneSummary: progress.CurrentSceneSummary,
			PlayerStatus:        string(gs.PlayerStatus),
		})
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
		Language:         story.Language,
		PlayerName:       config.ProtagonistName,
		CoreStats:        coreStatsDTO,
		Characters:       charactersDTO,
		GameStates:       gameStates,
	}

	log.Info("Successfully retrieved published story details with parsed data", zap.String("title", dto.Title))
	return dto, nil
}

func (s *storyBrowsingServiceImpl) ListUserPublishedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*sharedModels.PublishedStory, string, error) {
	log := s.logger.With(zap.String("userID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit))
	log.Info("ListUserPublishedStories called (cursor-based)")

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	stories, nextCursor, err := s.publishedRepo.ListByUserID(ctx, s.db, userID, cursor, limit)
	if err != nil {
		log.Error("Error listing user published stories from repository (cursor-based)", zap.Error(err))
		return nil, "", fmt.Errorf("repository error fetching stories: %w", err)
	}

	log.Info("Successfully listed user published stories (cursor-based)", zap.Int("count", len(stories)), zap.Bool("hasNext", nextCursor != ""))
	return stories, nextCursor, nil
}

func (s *storyBrowsingServiceImpl) GetPublishedStoryDetailsInternal(ctx context.Context, storyID uuid.UUID) (*sharedModels.PublishedStory, error) {
	log := s.logger.With(zap.String("storyID", storyID.String()))
	log.Info("GetPublishedStoryDetailsInternal called")

	story, err := s.publishedRepo.GetByID(ctx, s.db, storyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("Published story not found")
			return nil, sharedModels.ErrStoryNotFound
		}
		log.Error("Failed to get published story by ID", zap.Error(err))
		return nil, sharedModels.ErrInternalServer
	}

	log.Info("Successfully retrieved published story details for internal use")
	return story, nil
}

func (s *storyBrowsingServiceImpl) ListStoryScenesInternal(ctx context.Context, storyID uuid.UUID) ([]sharedModels.StoryScene, error) {
	log := s.logger.With(zap.String("storyID", storyID.String()))
	log.Info("ListStoryScenesInternal called")

	scenes, err := s.sceneRepo.ListByStoryID(ctx, s.db, storyID)
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

	err := s.publishedRepo.UpdateConfigAndSetupAndStatus(ctx, s.db, storyID, validatedConfig, validatedSetup, status)
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

func (s *storyBrowsingServiceImpl) GetPublishedStoryDetailsWithProgress(ctx context.Context, userID, publishedStoryID uuid.UUID) (*sharedModels.PublishedStorySummary, error) {
	log := s.logger.With(zap.String("storyID", publishedStoryID.String()), zap.String("requestingUserID", userID.String()))
	log.Info("GetPublishedStoryDetailsWithProgress called")

	details, err := s.publishedRepo.GetSummaryWithDetails(ctx, s.db, publishedStoryID, userID)
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

	log.Info("Successfully retrieved published story summary with progress")
	return details, nil
}

func (s *storyBrowsingServiceImpl) GetStoriesWithProgress(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]sharedModels.PublishedStorySummary, string, error) {
	log := s.logger.With(zap.String("method", "GetStoriesWithProgress"), zap.String("userID", userID.String()), zap.Int("limit", limit), zap.String("cursor", cursor))
	log.Debug("Fetching stories with progress for user")

	stories, nextCursor, err := s.publishedRepo.FindWithProgressByUserID(ctx, s.db, userID, limit, cursor)
	if err != nil {
		log.Error("Failed to find stories with progress in repository", zap.Error(err))
		return nil, "", fmt.Errorf("%w: failed to retrieve stories with progress from repository: %v", sharedModels.ErrInternalServer, err)
	}

	log.Info("Successfully fetched stories with progress", zap.Int("count", len(stories)), zap.Bool("hasNext", nextCursor != ""))
	return stories, nextCursor, nil
}

func (s *storyBrowsingServiceImpl) GetParsedSetup(ctx context.Context, storyID uuid.UUID) (*sharedModels.NovelSetupContent, error) {
	log := s.logger.With(zap.String("storyID", storyID.String()))
	log.Debug("GetParsedSetup called")

	story, err := s.publishedRepo.GetByID(ctx, s.db, storyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sharedModels.ErrNotFound) {
			log.Warn("Published story not found for GetParsedSetup")
			return nil, sharedModels.ErrNotFound
		}
		log.Error("Failed to get published story for GetParsedSetup", zap.Error(err))
		return nil, sharedModels.ErrInternalServer
	}

	if len(story.Setup) == 0 || string(story.Setup) == "null" {
		log.Warn("Published story has nil or empty Setup JSON for GetParsedSetup")
		return nil, nil
	}

	var novelSetup sharedModels.NovelSetupContent
	if err := json.Unmarshal(story.Setup, &novelSetup); err != nil {
		log.Error("Failed to unmarshal story Setup JSON in GetParsedSetup", zap.Error(err))
		return nil, fmt.Errorf("%w: failed to parse setup data in GetParsedSetup", sharedModels.ErrInternalServer)
	}

	return &novelSetup, nil
}

func (s *storyBrowsingServiceImpl) GetActiveStoryCount(ctx context.Context) (int, error) {
	log := s.logger.With(zap.String("method", "GetActiveStoryCount"))
	log.Debug("Counting active stories")

	count, err := s.publishedRepo.CountByStatus(ctx, s.db, sharedModels.StatusReady)
	if err != nil {
		log.Error("Failed to count active stories", zap.Error(err))
		return 0, fmt.Errorf("failed to count active stories: %w", err)
	}

	log.Info("Successfully counted active stories", zap.Int("count", count))
	return count, nil
}

func (s *storyBrowsingServiceImpl) ListMyStoriesWithProgress(ctx context.Context, userID uuid.UUID, cursor string, limit int, filterAdult bool) ([]sharedModels.PublishedStorySummary, string, error) {
	log := s.logger.With(zap.String("userID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit), zap.Bool("filterAdult", filterAdult))
	log.Info("ListMyStoriesWithProgress called (service layer)")

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	summaries, nextCursor, err := s.publishedRepo.ListUserSummariesOnlyWithProgress(ctx, s.db, userID, cursor, limit, filterAdult)
	if err != nil {
		log.Error("Failed to list user stories only with progress", zap.Error(err))
		return nil, "", sharedModels.ErrInternalServer
	}

	log.Info("Successfully listed user stories only with progress", zap.Int("count", len(summaries)), zap.Bool("hasNext", nextCursor != ""))
	return summaries, nextCursor, nil
}

// GameStateSummary представляет сводку состояния игры для клиента.
type GameStateSummary struct {
	ID                  uuid.UUID `json:"id"`
	LastActivityAt      time.Time `json:"last_activity_at"`
	SceneIndex          int       `json:"scene_index"`
	CurrentSceneSummary *string   `json:"current_scene_summary,omitempty"`
	PlayerStatus        string    `json:"player_status"`
}
