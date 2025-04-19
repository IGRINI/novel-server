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
	ListMyPublishedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*sharedModels.PublishedStory, string, error)
	ListPublicStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*sharedModels.PublishedStory, string, error)
	GetPublishedStoryDetails(ctx context.Context, storyID, userID uuid.UUID) (*PublishedStoryDetailDTO, error)
	ListUserPublishedStories(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*sharedModels.PublishedStory, error) // Note: Offset-based, might be deprecated
	GetPublishedStoryDetailsInternal(ctx context.Context, storyID uuid.UUID) (*sharedModels.PublishedStory, error)
	ListStoryScenesInternal(ctx context.Context, storyID uuid.UUID) ([]sharedModels.StoryScene, error)
}

type storyBrowsingServiceImpl struct {
	publishedRepo interfaces.PublishedStoryRepository
	sceneRepo     interfaces.StorySceneRepository
	// playerProgressRepo interfaces.PlayerProgressRepository // May be needed for LastPlayedAt
	// userRepo interfaces.UserRepository // May be needed for AuthorName
	logger *zap.Logger
}

// NewStoryBrowsingService creates a new instance of StoryBrowsingService.
func NewStoryBrowsingService(
	publishedRepo interfaces.PublishedStoryRepository,
	sceneRepo interfaces.StorySceneRepository,
	// playerProgressRepo interfaces.PlayerProgressRepository,
	// userRepo interfaces.UserRepository,
	logger *zap.Logger,
) StoryBrowsingService {
	return &storyBrowsingServiceImpl{
		publishedRepo: publishedRepo,
		sceneRepo:     sceneRepo,
		// playerProgressRepo: playerProgressRepo,
		// userRepo: userRepo,
		logger: logger.Named("StoryBrowsingService"),
	}
}

// ListMyPublishedStories returns a list of the user's published stories.
func (s *storyBrowsingServiceImpl) ListMyPublishedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*sharedModels.PublishedStory, string, error) {
	log := s.logger.With(zap.String("userID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit))
	log.Info("ListMyPublishedStories called")

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	// Call the repository method with cursor pagination
	stories, nextCursor, err := s.publishedRepo.ListByUserID(ctx, userID, cursor, limit)
	if err != nil {
		// Check for specific cursor errors if the repository returns them
		// if errors.Is(err, interfaces.ErrInvalidCursor) { ... }
		log.Error("Failed to list user published stories using cursor", zap.Error(err))
		return nil, "", sharedModels.ErrInternalServer // Use shared error
	}

	log.Debug("Successfully listed user published stories", zap.Int("count", len(stories)), zap.Bool("hasNext", nextCursor != ""))
	return stories, nextCursor, nil
}

// ListPublicStories returns a list of public published stories.
func (s *storyBrowsingServiceImpl) ListPublicStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*sharedModels.PublishedStory, string, error) {
	// userID is passed but currently unused in this specific method's core logic,
	// but might be used later for filtering (e.g., liked status, blocking)
	log := s.logger.With(zap.String("requestingUserID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit))
	log.Info("ListPublicStories called")

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	// Call the repository method with cursor pagination
	// Note: userID is not directly used by ListPublic in the repo currently
	stories, nextCursor, err := s.publishedRepo.ListPublic(ctx, cursor, limit)
	if err != nil {
		log.Error("Failed to list public stories using cursor", zap.Error(err))
		return nil, "", sharedModels.ErrInternalServer // Use shared error
	}

	log.Debug("Successfully listed public stories", zap.Int("count", len(stories)), zap.Bool("hasNext", nextCursor != ""))
	return stories, nextCursor, nil
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
