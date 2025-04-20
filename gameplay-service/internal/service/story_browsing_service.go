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

// StoryBrowsingService defines the interface for browsing published stories.
type StoryBrowsingService interface {
	ListMyPublishedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*PublishedStoryDetailWithProgressDTO, string, error)
	ListPublicStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*PublishedStoryDetailWithProgressDTO, string, error)
	GetPublishedStoryDetails(ctx context.Context, storyID, userID uuid.UUID) (*PublishedStoryDetailDTO, error)
	ListUserPublishedStories(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*sharedModels.PublishedStory, error)
	GetPublishedStoryDetailsInternal(ctx context.Context, storyID uuid.UUID) (*sharedModels.PublishedStory, error)
	ListStoryScenesInternal(ctx context.Context, storyID uuid.UUID) ([]sharedModels.StoryScene, error)
	UpdateStoryInternal(ctx context.Context, storyID uuid.UUID, configJSON, setupJSON json.RawMessage, status sharedModels.StoryStatus) error
	GetPublishedStoryDetailsWithProgress(ctx context.Context, userID, publishedStoryID uuid.UUID) (*PublishedStoryDetailWithProgressDTO, error)
}

type storyBrowsingServiceImpl struct {
	publishedRepo interfaces.PublishedStoryRepository
	sceneRepo     interfaces.StorySceneRepository
	progressRepo  interfaces.PlayerProgressRepository
	authClient    interfaces.AuthServiceClient
	logger        *zap.Logger
}

// NewStoryBrowsingService creates a new instance of StoryBrowsingService.
func NewStoryBrowsingService(
	publishedRepo interfaces.PublishedStoryRepository,
	sceneRepo interfaces.StorySceneRepository,
	progressRepo interfaces.PlayerProgressRepository,
	authClient interfaces.AuthServiceClient,
	logger *zap.Logger,
) StoryBrowsingService {
	return &storyBrowsingServiceImpl{
		publishedRepo: publishedRepo,
		sceneRepo:     sceneRepo,
		progressRepo:  progressRepo,
		authClient:    authClient,
		logger:        logger.Named("StoryBrowsingService"),
	}
}

// ListMyPublishedStories returns a list of the user's published stories with progress flag.
func (s *storyBrowsingServiceImpl) ListMyPublishedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*PublishedStoryDetailWithProgressDTO, string, error) {
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

	// <<< ДОБАВЛЕНО: Получение имен авторов >>>
	authorNames := s.fetchAuthorNames(ctx, stories)
	// <<< КОНЕЦ: Получение имен авторов >>>

	// Проверка прогресса для каждой истории
	results := make([]*PublishedStoryDetailWithProgressDTO, 0, len(stories))
	for _, story := range stories {
		// Check progress (similar to ListPublicStories, adapting error handling if needed)
		_, errProgress := s.progressRepo.GetByUserIDAndStoryID(ctx, userID, story.ID)
		hasProgress := errProgress == nil || !errors.Is(errProgress, sharedModels.ErrNotFound) // Check against shared error
		if errProgress != nil && !errors.Is(errProgress, sharedModels.ErrNotFound) {
			log.Error("Error checking progress for my story in list", zap.String("storyID", story.ID.String()), zap.Error(errProgress))
			hasProgress = false // Default to false on error
		}

		results = append(results, &PublishedStoryDetailWithProgressDTO{
			PublishedStory:    *story,
			AuthorName:        authorNames[story.UserID], // <<< ДОБАВЛЕНО: Заполняем имя автора
			HasPlayerProgress: hasProgress,
		})
	}

	log.Debug("Successfully listed user published stories with progress", zap.Int("count", len(results)), zap.Bool("hasNext", nextCursor != ""))
	return results, nextCursor, nil
}

// ListPublicStories returns a list of public published stories with progress flag for the requesting user.
func (s *storyBrowsingServiceImpl) ListPublicStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*PublishedStoryDetailWithProgressDTO, string, error) {
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

	// <<< ДОБАВЛЕНО: Получение имен авторов >>>
	authorNames := s.fetchAuthorNames(ctx, stories)
	// <<< КОНЕЦ: Получение имен авторов >>>

	// <<< ДОБАВЛЕНО: Пакетная проверка прогресса >>>
	progressExistsMap := make(map[uuid.UUID]bool)
	if userID != uuid.Nil && len(stories) > 0 {
		storyIDs := make([]uuid.UUID, len(stories))
		for i, story := range stories {
			storyIDs[i] = story.ID
		}
		var errProgress error
		progressExistsMap, errProgress = s.progressRepo.CheckProgressExistsForStories(ctx, userID, storyIDs)
		if errProgress != nil {
			log.Error("Failed to check player progress for public stories (batch)", zap.Error(errProgress))
			// Proceed with progress as false for all
			progressExistsMap = make(map[uuid.UUID]bool)
		}
	}
	// <<< КОНЕЦ: Пакетная проверка прогресса >>>

	results := make([]*PublishedStoryDetailWithProgressDTO, 0, len(stories))
	for _, story := range stories {
		hasProgress := progressExistsMap[story.ID] // Defaults to false
		results = append(results, &PublishedStoryDetailWithProgressDTO{
			PublishedStory:    *story,
			AuthorName:        authorNames[story.UserID], // <<< ДОБАВЛЕНО: Заполняем имя автора
			HasPlayerProgress: hasProgress,
		})
	}

	log.Debug("Successfully listed public stories with progress", zap.Int("count", len(results)), zap.Bool("hasNext", nextCursor != ""))
	return results, nextCursor, nil
}

// <<< ДОБАВЛЕНО: Вспомогательная функция для получения имен авторов >>>
func (s *storyBrowsingServiceImpl) fetchAuthorNames(ctx context.Context, stories []*sharedModels.PublishedStory) map[uuid.UUID]string {
	authorIDs := make(map[uuid.UUID]struct{})
	for _, story := range stories {
		if story != nil {
			authorIDs[story.UserID] = struct{}{}
		}
	}
	uniqueAuthorIDs := make([]uuid.UUID, 0, len(authorIDs))
	for id := range authorIDs {
		uniqueAuthorIDs = append(uniqueAuthorIDs, id)
	}

	authorNames := make(map[uuid.UUID]string)
	if len(uniqueAuthorIDs) > 0 {
		authorInfos, err := s.authClient.GetUsersInfo(ctx, uniqueAuthorIDs)
		if err != nil {
			s.logger.Warn("Failed to fetch author details from auth-service, names will be empty", zap.Error(err))
		} else {
			for userID, info := range authorInfos {
				authorNames[userID] = info.DisplayName
			}
		}
	}
	return authorNames
}

// GetPublishedStoryDetails retrieves the details of a published story.
func (s *storyBrowsingServiceImpl) GetPublishedStoryDetails(ctx context.Context, storyID, userID uuid.UUID) (*PublishedStoryDetailDTO, error) {
	log := s.logger.With(zap.String("storyID", storyID.String()), zap.String("requestingUserID", userID.String()))
	log.Info("GetPublishedStoryDetails called")

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

	// 2. Check visibility (if the story is not public and the user is not the author)
	isAuthor := story.UserID == userID
	if !story.IsPublic && !isAuthor {
		log.Warn("User does not have permission to view story details (private and not author)")
		return nil, sharedModels.ErrForbidden // Use shared error
	}

	// 3. Parse Config and Setup JSON (assuming they exist)
	var novelConfig sharedModels.Config // Using the structure from shared models
	if story.Config != nil {
		if err := json.Unmarshal(story.Config, &novelConfig); err != nil {
			log.Error("Failed to unmarshal story Config JSON", zap.Error(err))
			// Decide if this is a fatal error or if we can proceed with partial data
			return nil, ErrInternal // Use local error
		}
	} else {
		log.Warn("Story Config is nil")
		// Handle missing config if needed
		// Ensure novelConfig has default values if possible
		novelConfig = sharedModels.Config{}
	}

	var setupContent sharedModels.NovelSetupContent
	if story.Setup != nil {
		if err := json.Unmarshal(story.Setup, &setupContent); err != nil {
			log.Error("Failed to unmarshal story Setup JSON", zap.Error(err))
			return nil, ErrInternal // Use local error
		}
	} else {
		log.Warn("Story Setup is nil")
		// Handle missing setup if needed (especially for CoreStats)
		setupContent.CoreStatsDefinition = make(map[string]sharedModels.StatDefinition)
	}

	// 4. Fetch additional data (AuthorName, LastPlayedAt) - REQUIRES MORE REPOS
	authorName := "[Unknown Author]" // Placeholder
	// if s.userRepo != nil { ... fetch author name ... }

	var lastPlayedAt *time.Time
	// if s.playerProgressRepo != nil { ... fetch progress ... }

	// 5. Assemble the DTO
	details := &PublishedStoryDetailDTO{
		ID:               story.ID,
		Title:            *story.Title,       // Dereference pointer
		ShortDescription: *story.Description, // Dereference pointer
		AuthorID:         story.UserID,
		AuthorName:       authorName,        // Fetched or placeholder
		PublishedAt:      story.CreatedAt,   // Assuming CreatedAt is the publish time
		Genre:            novelConfig.Genre, // From parsed Config
		Language:         novelConfig.Language,
		IsAdultContent:   story.IsAdultContent,
		// TODO: Verify these fields exist in sharedModels.Config
		// PlayerName:        novelConfig.Player.Name,
		// PlayerDescription: novelConfig.Player.Description,
		// WorldContext:      novelConfig.WorldContext,
		// TODO: Verify this field exists in sharedModels.NovelSetupContent
		// StorySummary:      setupContent.StorySummary, // From parsed Setup
		CoreStats:    make(map[string]CoreStatDetailDTO),
		LastPlayedAt: lastPlayedAt, // Fetched or nil
		IsAuthor:     isAuthor,
	}

	// Populate CoreStats details
	for statKey, definition := range setupContent.CoreStatsDefinition {
		details.CoreStats[statKey] = CoreStatDetailDTO{
			Description: definition.Description,
			// TODO: Verify this field exists in sharedModels.StatDefinition
			// InitialValue:       definition.Initial,
			GameOverConditions: []sharedModels.StatDefinition{definition}, // Simplified, may need adjustment
		}
	}

	log.Info("Successfully retrieved published story details")
	return details, nil
}

// ListUserPublishedStories retrieves a paginated list of PublishedStory for a specific user ID.
// Note: This uses offset/limit, which is generally discouraged in favor of cursors.
// It might be a legacy method or used internally.
func (s *storyBrowsingServiceImpl) ListUserPublishedStories(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*sharedModels.PublishedStory, error) {
	log := s.logger.With(zap.String("userID", userID.String()), zap.Int("limit", limit), zap.Int("offset", offset))
	log.Info("ListUserPublishedStories called (offset-based)")

	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	// Pass empty cursor "" and correct limit. Ignore returned nextCursor.
	stories, _, err := s.publishedRepo.ListByUserID(ctx, userID, "", limit)
	if err != nil {
		log.Error("Error listing user published stories from repository (offset-based wrapper)", zap.Error(err))
		return nil, ErrInternal // Use local error
	}

	return stories, nil
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

// GetPublishedStoryDetailsWithProgress fetches published story details and checks if the user has progress.
func (s *storyBrowsingServiceImpl) GetPublishedStoryDetailsWithProgress(ctx context.Context, userID, publishedStoryID uuid.UUID) (*PublishedStoryDetailWithProgressDTO, error) {
	log := s.logger.With(zap.String("userID", userID.String()), zap.String("publishedStoryID", publishedStoryID.String()))
	log.Info("GetPublishedStoryDetailsWithProgress called")

	// 1. Get the published story
	story, err := s.publishedRepo.GetByID(ctx, publishedStoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("Published story not found")
			return nil, sharedModels.ErrStoryNotFound
		}
		log.Error("Error getting published story", zap.Error(err))
		return nil, sharedModels.ErrInternalServer
	}

	// 2. Check if player progress exists
	var hasProgress bool
	if userID != uuid.Nil { // Check progress only if userID is provided
		_, errProgress := s.progressRepo.GetByUserIDAndStoryID(ctx, userID, publishedStoryID)
		if errProgress == nil {
			hasProgress = true
		} else if errors.Is(errProgress, pgx.ErrNoRows) {
			hasProgress = false
		} else {
			log.Error("Error checking player progress", zap.Error(errProgress))
			// Return error if progress check fails?
			// return nil, sharedModels.ErrInternalServer
			hasProgress = false // Default to false on error
		}
	} else {
		hasProgress = false // No user ID, no progress
	}

	// 3. Create DTO
	responseDTO := &PublishedStoryDetailWithProgressDTO{
		PublishedStory:    *story,
		HasPlayerProgress: hasProgress,
	}

	log.Info("Successfully fetched story details and progress status", zap.Bool("hasProgress", hasProgress))
	return responseDTO, nil
}
