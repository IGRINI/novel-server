package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"novel-server/gameplay-service/internal/messaging"
	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"sort"
	"strings"
	"time"

	database "novel-server/shared/database"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Define local service-level errors
var (
	ErrInvalidOperation      = errors.New("invalid operation")
	ErrInvalidLimit          = errors.New("invalid limit value")
	ErrInvalidOffset         = errors.New("invalid offset value")
	ErrChoiceNotFound        = errors.New("choice or scene not found")
	ErrInvalidChoiceIndex    = errors.New("invalid choice index")
	ErrCannotPublish         = errors.New("story cannot be published in its current status")
	ErrCannotPublishNoConfig = errors.New("cannot publish without a generated config")
	// Errors defined in shared/models/errors.go will be used directly:
	// sharedModels.ErrUserHasActiveGeneration
	// sharedModels.ErrCannotRevise
	// sharedModels.ErrStoryNotReadyYet
	// sharedModels.ErrSceneNeedsGeneration

	// Add errors expected in handler/http.go
	ErrStoryNotFound          = errors.New("published story not found")
	ErrSceneNotFound          = errors.New("current scene not found")
	ErrPlayerProgressNotFound = errors.New("player progress not found")
	ErrStoryNotReady          = errors.New("story is not ready for gameplay yet")
	ErrInternal               = errors.New("internal service error")
	ErrInvalidChoice          = errors.New("invalid choice")
	ErrNoChoicesAvailable     = errors.New("no choices available in the current scene")

	// <<< Добавляем ошибки для лайков >>>
	ErrAlreadyLiked = errors.New("story already liked by this user")
	ErrNotLikedYet  = errors.New("story not liked by this user yet")
)

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

// GameplayService defines the interface for gameplay business logic.
type GameplayService interface {
	GenerateInitialStory(ctx context.Context, userID uuid.UUID, initialPrompt string) (*sharedModels.StoryConfig, error)
	ReviseDraft(ctx context.Context, id uuid.UUID, userID uuid.UUID, revisionPrompt string) error
	GetStoryConfig(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*sharedModels.StoryConfig, error)
	PublishDraft(ctx context.Context, draftID uuid.UUID, userID uuid.UUID) (publishedStoryID uuid.UUID, err error)
	ListMyDrafts(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.StoryConfig, string, error)
	ListMyPublishedStories(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*sharedModels.PublishedStory, error)
	ListPublicStories(ctx context.Context, limit, offset int) ([]*sharedModels.PublishedStory, error)
	GetStoryScene(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (*sharedModels.StoryScene, error)
	MakeChoice(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID, selectedOptionIndex int) error
	DeletePlayerProgress(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error
	ListUserDrafts(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.StoryConfig, string, error)
	ListUserPublishedStories(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*sharedModels.PublishedStory, error)
	RetryDraftGeneration(ctx context.Context, draftID uuid.UUID, userID uuid.UUID) error
	LikeStory(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error
	UnlikeStory(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error
}

type gameplayServiceImpl struct {
	repo               interfaces.StoryConfigRepository // Используем interfaces.StoryConfigRepository
	publishedRepo      interfaces.PublishedStoryRepository
	sceneRepo          interfaces.StorySceneRepository
	playerProgressRepo interfaces.PlayerProgressRepository // Использует uuid.UUID UserID
	likeRepo           interfaces.LikeRepository           // <<< Добавляем репозиторий лайков
	publisher          messaging.TaskPublisher
	pool               *pgxpool.Pool
	logger             *zap.Logger
}

func NewGameplayService(
	repo interfaces.StoryConfigRepository, // Используем interfaces.StoryConfigRepository
	publishedRepo interfaces.PublishedStoryRepository,
	sceneRepo interfaces.StorySceneRepository,
	playerProgressRepo interfaces.PlayerProgressRepository, // Использует uuid.UUID UserID
	likeRepo interfaces.LikeRepository, // <<< Инжектируем репозиторий лайков
	publisher messaging.TaskPublisher,
	pool *pgxpool.Pool,
	logger *zap.Logger,
) GameplayService {
	return &gameplayServiceImpl{
		repo:               repo,
		publishedRepo:      publishedRepo,
		sceneRepo:          sceneRepo,
		playerProgressRepo: playerProgressRepo,
		likeRepo:           likeRepo, // <<< Сохраняем репозиторий лайков
		publisher:          publisher,
		pool:               pool,
		logger:             logger.Named("GameplayService"),
	}
}

// GenerateInitialStory creates a new StoryConfig entry and sends a generation task.
func (s *gameplayServiceImpl) GenerateInitialStory(ctx context.Context, userID uuid.UUID, initialPrompt string) (*sharedModels.StoryConfig, error) {
	// Check the number of active generations for this userID
	activeCount, err := s.repo.CountActiveGenerations(ctx, userID)
	if err != nil {
		// Error during check, return 500
		log.Printf("[GameplayService] Error counting active generations for UserID %s: %v", userID.String(), err)
		return nil, fmt.Errorf("error checking generation status: %w", err)
	}
	// TODO: Make the limit configurable (e.g., via config or user profile)
	generationLimit := 1
	if activeCount >= generationLimit {
		log.Printf("[GameplayService] User UserID %s reached the active generation limit (%d).", userID.String(), generationLimit)
		return nil, sharedModels.ErrUserHasActiveGeneration // Use the same error
	}

	// Create a JSON array with the initial prompt
	userInputJSON, err := json.Marshal([]string{initialPrompt})
	if err != nil {
		log.Printf("[GameplayService] Error marshalling initialPrompt for UserID %s: %v", userID.String(), err)
		return nil, fmt.Errorf("error preparing data for DB: %w", err)
	}

	config := &sharedModels.StoryConfig{
		ID:          uuid.New(),
		UserID:      userID,
		Title:       "",                            // Will be filled after generation
		Description: "",                            // <<< ИЗМЕНЕНО: Не сохраняем initialPrompt в Description >>>
		UserInput:   userInputJSON,                 // Array of prompts
		Config:      nil,                           // JSON config will be available after generation
		Status:      sharedModels.StatusGenerating, // <<< Use constant
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	// 1. Save the draft to the DB with status 'generating'
	err = s.repo.Create(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("error saving initial draft: %w", err)
	}
	log.Printf("[GameplayService] Initial draft created and saved: ID=%s, UserID=%s", config.ID, config.UserID.String())

	// 2. Form and send the generation task
	taskID := uuid.New().String() // ID for the generation task
	generationPayload := sharedMessaging.GenerationTaskPayload{
		TaskID:        taskID,
		UserID:        config.UserID.String(),
		PromptType:    sharedMessaging.PromptTypeNarrator, // Use only Narrator for now
		InputData:     make(map[string]interface{}),       // Empty for initial generation
		UserInput:     initialPrompt,                      // User's initial prompt
		StoryConfigID: config.ID.String(),                 // Link to the created config
	}

	if err := s.publisher.PublishGenerationTask(ctx, generationPayload); err != nil {
		log.Printf("[GameplayService] Error publishing initial generation task for ConfigID=%s, TaskID=%s: %v. Attempting to roll back status...", config.ID, taskID, err)
		// Try to roll back the status to Error
		config.Status = sharedModels.StatusError
		config.UpdatedAt = time.Now().UTC()
		if rollbackErr := s.repo.Update(context.Background(), config); rollbackErr != nil {
			log.Printf("[GameplayService] CRITICAL ERROR: Failed to roll back status to Error for ConfigID=%s after publish error: %v", config.ID, rollbackErr)
		}
		// Return the error to the client, but the config in DB remains with status Error
		return config, fmt.Errorf("error sending generation task: %w", err) // Return config with ID and error
	}

	log.Printf("[GameplayService] Initial generation task sent successfully: ConfigID=%s, TaskID=%s", config.ID, taskID)

	// Return the created config (with status generating) so the client knows the ID
	return config, nil
}

// ReviseDraft updates an existing story draft
func (s *gameplayServiceImpl) ReviseDraft(ctx context.Context, id uuid.UUID, userID uuid.UUID, revisionPrompt string) error {
	// 1. Get the current config
	config, err := s.repo.GetByID(ctx, id, userID)
	log.Printf("!!!!!! DEBUG [ReviseDraft]: GetByID returned -> Config: %+v, Error: %v", config, err) // <-- DEBUG LOG
	if err != nil {
		return fmt.Errorf("error getting draft for revision: %w", err)
	}

	// 2. Check status
	if config.Status != sharedModels.StatusDraft && config.Status != sharedModels.StatusError {
		log.Printf("[UserID: %s][StoryID: %s] Attempt to revise in invalid status: %s", userID.String(), id, config.Status)
		return sharedModels.ErrCannotRevise
	}

	// Check the number of active generations for this userID
	activeCount, err := s.repo.CountActiveGenerations(ctx, userID)
	if err != nil {
		log.Printf("[GameplayService] Error counting active generations for UserID %s before revising ConfigID %s: %v", userID.String(), id, err)
		return fmt.Errorf("error checking generation status: %w", err)
	}
	// TODO: Make the limit configurable
	generationLimit := 1
	if activeCount >= generationLimit {
		log.Printf("[GameplayService] User UserID %s reached active generation limit (%d), revision for ConfigID %s rejected.", userID.String(), generationLimit, id)
		return sharedModels.ErrUserHasActiveGeneration
	}

	// 3. Update UserInput history
	var userInputs []string
	if config.UserInput != nil {
		if err := json.Unmarshal(config.UserInput, &userInputs); err != nil {
			log.Printf("[GameplayService] Error deserializing UserInput for ConfigID %s: %v. Creating new array.", config.ID, err)
			userInputs = make([]string, 0)
		}
	}
	userInputs = append(userInputs, revisionPrompt)
	updatedUserInputJSON, err := json.Marshal(userInputs)
	if err != nil {
		log.Printf("[GameplayService] Error marshalling updated UserInput for ConfigID %s: %v", config.ID, err)
		return fmt.Errorf("error preparing data for DB: %w", err)
	}
	config.UserInput = updatedUserInputJSON

	// 4. Update status to 'generating' and save the MODIFIED UserInput
	config.Status = sharedModels.StatusGenerating
	config.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, config); err != nil {
		return fmt.Errorf("error updating status/UserInput before revision: %w", err)
	}

	// 5. Form payload for the revision task
	taskID := uuid.New().String()
	generationPayload := sharedMessaging.GenerationTaskPayload{
		TaskID:        taskID,
		UserID:        config.UserID.String(),
		PromptType:    sharedMessaging.PromptTypeNarrator,
		InputData:     map[string]interface{}{"current_config": string(config.Config)}, // Pass the current JSON from Config field
		UserInput:     revisionPrompt,                                                  // Pass only the latest revision prompt
		StoryConfigID: config.ID.String(),
	}

	// 6. Publish the task to the queue
	if err := s.publisher.PublishGenerationTask(ctx, generationPayload); err != nil {
		log.Printf("[GameplayService] Error publishing revision task for ConfigID=%s, TaskID=%s: %v. Attempting to roll back status...", config.ID, taskID, err)
		// Try to roll back the status to the previous one (Draft or Error)
		if len(userInputs) > 1 { // If this was a revision, not the first generation after an error
			config.Status = sharedModels.StatusDraft
		} else {
			config.Status = sharedModels.StatusError
		}
		// Remove the last UserInput since the revision failed
		config.UserInput, _ = json.Marshal(userInputs[:len(userInputs)-1])
		config.UpdatedAt = time.Now().UTC()
		if rollbackErr := s.repo.Update(context.Background(), config); rollbackErr != nil {
			log.Printf("[GameplayService] CRITICAL ERROR: Failed to roll back status/UserInput for ConfigID=%s after revision publish error: %v", config.ID, rollbackErr)
		}
		return fmt.Errorf("error publishing revision task: %w", err)
	}

	log.Printf("[GameplayService] Revision task sent successfully: ConfigID=%s, TaskID=%s", config.ID, taskID)
	return nil // Success, return only nil
}

// GetStoryConfig gets the story config
func (s *gameplayServiceImpl) GetStoryConfig(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*sharedModels.StoryConfig, error) {
	config, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		// Error handling (including NotFound) happens in the repository
		return nil, fmt.Errorf("error getting StoryConfig in service: %w", err)
	}
	return config, nil
}

// PublishDraft publishes a draft, deletes it, and creates a PublishedStory.
func (s *gameplayServiceImpl) PublishDraft(ctx context.Context, draftID uuid.UUID, userID uuid.UUID) (publishedStoryID uuid.UUID, err error) {
	// Begin transaction
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("Failed to begin transaction for publishing draft", zap.String("draftID", draftID.String()), zap.Error(err))
		return uuid.Nil, fmt.Errorf("error beginning transaction: %w", err)
	}
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("Panic recovered during PublishDraft, rolling back transaction", zap.Any("panic", r))
			_ = tx.Rollback(context.Background()) // Ignore rollback error after panic
			err = fmt.Errorf("panic during publish: %v", r)
		} else if err != nil {
			s.logger.Warn("Rolling back transaction due to error", zap.String("draftID", draftID.String()), zap.Error(err))
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
				s.logger.Error("Failed to rollback transaction", zap.String("draftID", draftID.String()), zap.Error(rollbackErr))
			}
		} else {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				s.logger.Error("Failed to commit transaction", zap.String("draftID", draftID.String()), zap.Error(commitErr))
				err = fmt.Errorf("error committing transaction: %w", commitErr)
			}
		}
	}()

	// Use transaction for repositories
	repoTx := database.NewPgStoryConfigRepository(tx, s.logger)
	publishedRepoTx := database.NewPgPublishedStoryRepository(tx, s.logger)

	// 1. Get the draft within the transaction
	draft, err := repoTx.GetByID(ctx, draftID, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, sharedModels.ErrNotFound // Use standard error
		}
		return uuid.Nil, fmt.Errorf("error getting draft: %w", err)
	}

	// 2. Check status and Config presence
	if draft.Status != sharedModels.StatusDraft && draft.Status != sharedModels.StatusError {
		return uuid.Nil, ErrCannotPublish // Use local error
	}
	if draft.Config == nil || len(draft.Config) == 0 {
		s.logger.Warn("Attempt to publish draft without generated config", zap.String("draftID", draftID.String()))
		return uuid.Nil, ErrCannotPublishNoConfig // Use local error
	}

	// 3. Extract necessary fields from draft.Config
	var tempConfig struct {
		IsAdultContent bool `json:"ac"`
	}
	if err = json.Unmarshal(draft.Config, &tempConfig); err != nil {
		s.logger.Error("Failed to unmarshal draft config to extract adult content flag", zap.String("draftID", draftID.String()), zap.Error(err))
		return uuid.Nil, fmt.Errorf("error reading draft configuration: %w", err)
	}

	// 4. Create PublishedStory within the transaction
	newPublishedStory := &sharedModels.PublishedStory{
		ID:             uuid.New(),
		UserID:         userID,
		Config:         draft.Config,
		Setup:          nil, // Will be generated later
		Status:         sharedModels.StatusSetupPending,
		IsPublic:       false, // Private by default
		IsAdultContent: tempConfig.IsAdultContent,
		Title:          &draft.Title,       // <<< Fixed: pass pointer
		Description:    &draft.Description, // <<< Fixed: pass pointer
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}

	if err = publishedRepoTx.Create(ctx, newPublishedStory); err != nil {
		return uuid.Nil, fmt.Errorf("error creating published story: %w", err)
	}
	s.logger.Info("Published story created in DB", zap.String("publishedStoryID", newPublishedStory.ID.String()))

	// 5. Delete the draft within the transaction
	if err = repoTx.Delete(ctx, draftID, userID); err != nil {
		return uuid.Nil, fmt.Errorf("error deleting draft: %w", err)
	}
	s.logger.Info("Draft deleted from DB", zap.String("draftID", draftID.String()))

	// 6. Send task for Setup generation
	taskID := uuid.New().String()
	setupPayload := sharedMessaging.GenerationTaskPayload{
		TaskID:           taskID,
		UserID:           newPublishedStory.UserID.String(),
		PromptType:       sharedMessaging.PromptTypeNovelSetup,
		InputData:        map[string]interface{}{"config": string(newPublishedStory.Config)}, // Pass JSON config
		PublishedStoryID: newPublishedStory.ID.String(),                                      // Link to published story
	}

	// Publish task OUTSIDE the transaction, after it's almost certainly committed
	go func(payload sharedMessaging.GenerationTaskPayload) {
		publishCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := s.publisher.PublishGenerationTask(publishCtx, payload); err != nil {
			// Error publishing Setup task - critical, as the transaction is already committed.
			// Status will remain SetupPending, but the task won't be sent.
			// A retry system or monitoring is needed for such cases.
			s.logger.Error("CRITICAL: Failed to publish setup generation task after DB commit",
				zap.String("publishedStoryID", payload.PublishedStoryID),
				zap.String("taskID", payload.TaskID),
				zap.Error(err))
		} else {
			s.logger.Info("Setup generation task published successfully",
				zap.String("publishedStoryID", payload.PublishedStoryID),
				zap.String("taskID", payload.TaskID))
		}
	}(setupPayload)

	// If we reached here without errors, defer tx.Commit() will work on exit
	publishedStoryID = newPublishedStory.ID
	return publishedStoryID, nil
}

// ListMyDrafts retrieves a paginated list of story configurations (drafts) for a specific user.
func (s *gameplayServiceImpl) ListMyDrafts(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.StoryConfig, string, error) {
	const defaultLimit = 10
	const maxLimit = 50 // Prevent requesting too many items

	if limit <= 0 {
		limit = defaultLimit
	} else if limit > maxLimit {
		limit = maxLimit // Enforce a reasonable limit
	}

	// Fetch one extra record to determine if there's a next page
	configs, nextCursor, err := s.repo.ListByUserID(ctx, userID, cursor, limit+1)
	if err != nil {
		if errors.Is(err, interfaces.ErrInvalidCursor) {
			s.logger.Warn("Invalid cursor provided for ListMyDrafts", zap.String("userID", userID.String()), zap.String("cursor", cursor))
			return []sharedModels.StoryConfig{}, "", interfaces.ErrInvalidCursor
		}
		s.logger.Error("Failed to list user drafts", zap.String("userID", userID.String()), zap.Error(err))
		return nil, "", ErrInternal
	}

	if len(configs) > limit {
		configs = configs[:limit]
	}

	return configs, nextCursor, nil
}

// ListMyPublishedStories returns a list of the user's published stories.
func (s *gameplayServiceImpl) ListMyPublishedStories(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*sharedModels.PublishedStory, error) {
	// Validate limit and offset (could be moved to handler)
	if limit <= 0 || limit > 100 {
		s.logger.Warn("Invalid limit requested for ListMyPublishedStories", zap.Int("limit", limit), zap.String("userID", userID.String()))
		limit = 20 // Default
	}
	if offset < 0 {
		s.logger.Warn("Invalid offset requested for ListMyPublishedStories", zap.Int("offset", offset), zap.String("userID", userID.String()))
		offset = 0 // Default
	}

	s.logger.Debug("Calling publishedRepo.ListByUserID", zap.String("userID", userID.String()), zap.Int("limit", limit), zap.Int("offset", offset))
	stories, err := s.publishedRepo.ListByUserID(ctx, userID, limit, offset)
	if err != nil {
		s.logger.Error("Failed to list user published stories from repository", zap.String("userID", userID.String()), zap.Error(err))
		return nil, fmt.Errorf("error getting list of user's published stories: %w", err)
	}

	s.logger.Info("User published stories listed successfully", zap.String("userID", userID.String()), zap.Int("count", len(stories)))
	return stories, nil
}

// ListPublicStories returns a list of public published stories.
func (s *gameplayServiceImpl) ListPublicStories(ctx context.Context, limit, offset int) ([]*sharedModels.PublishedStory, error) {
	// Validate limit and offset
	if limit <= 0 || limit > 100 {
		s.logger.Warn("Invalid limit requested for ListPublicStories", zap.Int("limit", limit))
		limit = 20
	}
	if offset < 0 {
		s.logger.Warn("Invalid offset requested for ListPublicStories", zap.Int("offset", offset))
		offset = 0
	}

	s.logger.Debug("Calling publishedRepo.ListPublic", zap.Int("limit", limit), zap.Int("offset", offset))
	stories, err := s.publishedRepo.ListPublic(ctx, limit, offset)
	if err != nil {
		s.logger.Error("Failed to list public stories from repository", zap.Error(err))
		return nil, fmt.Errorf("error getting list of public stories: %w", err)
	}

	s.logger.Info("Public stories listed successfully", zap.Int("count", len(stories)))
	return stories, nil
}

// --- Gameplay Loop Methods ---

// GetStoryScene gets the current scene for the player.
func (s *gameplayServiceImpl) GetStoryScene(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (*sharedModels.StoryScene, error) {
	s.logger.Info("GetStoryScene called", zap.String("userID", userID.String()), zap.String("publishedStoryID", publishedStoryID.String()))

	// 1. Get the published story
	publishedStory, err := s.publishedRepo.GetByID(ctx, publishedStoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, sharedModels.ErrNotFound
		}
		return nil, fmt.Errorf("error getting published story: %w", err)
	}

	// 2. Check UserID access (assuming UserID is uint64 in PublishedStory for now)
	// if publishedStory.UserID != userID {
	// 	 return nil, sharedModels.ErrForbidden // Or ErrNotFound?
	// }

	// 3. Check story status
	if publishedStory.Status == sharedModels.StatusSetupPending || publishedStory.Status == sharedModels.StatusFirstScenePending {
		return nil, sharedModels.ErrStoryNotReadyYet
	}
	if publishedStory.Status != sharedModels.StatusReady && publishedStory.Status != sharedModels.StatusGeneratingScene {
		s.logger.Warn("Attempt to get scene for story in invalid state",
			zap.String("publishedStoryID", publishedStoryID.String()),
			zap.String("status", string(publishedStory.Status)))
		return nil, fmt.Errorf("story is in a non-playable state: %s", publishedStory.Status)
	}

	// 4. Get player progress or create initial progress
	playerProgress, err := s.playerProgressRepo.GetByUserIDAndStoryID(ctx, userID, publishedStoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.logger.Info("Player progress not found, creating initial progress", zap.String("userID", userID.String()), zap.String("publishedStoryID", publishedStoryID.String()))
			playerProgress = &sharedModels.PlayerProgress{
				UserID:           userID,
				PublishedStoryID: publishedStoryID,
				CurrentStateHash: sharedModels.InitialStateHash,
				CoreStats:        make(map[string]int),
				StoryVariables:   make(map[string]interface{}),
				GlobalFlags:      []string{},
				CreatedAt:        time.Now().UTC(),
				UpdatedAt:        time.Now().UTC(),
			}
			if errCreate := s.playerProgressRepo.CreateOrUpdate(ctx, playerProgress); errCreate != nil {
				return nil, fmt.Errorf("error creating initial player progress: %w", errCreate)
			}
		} else {
			return nil, fmt.Errorf("error getting player progress: %w", err)
		}
	}

	// 5. Get scene by hash
	scene, err := s.sceneRepo.FindByStoryAndHash(ctx, publishedStoryID, playerProgress.CurrentStateHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.logger.Info("Scene not found for hash, requires generation",
				zap.String("publishedStoryID", publishedStoryID.String()),
				zap.String("stateHash", playerProgress.CurrentStateHash))
			return nil, sharedModels.ErrSceneNeedsGeneration
		}
		return nil, fmt.Errorf("error getting scene: %w", err)
	}

	s.logger.Info("Scene found and returned", zap.String("sceneID", scene.ID.String()))
	return scene, nil
}

// MakeChoice handles player choice.
func (s *gameplayServiceImpl) MakeChoice(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID, selectedOptionIndex int) error {
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
		zap.Int("selectedOptionIndex", selectedOptionIndex),
	}
	s.logger.Info("MakeChoice called", logFields...)

	// 1. Get the progress
	progress, err := s.playerProgressRepo.GetByUserIDAndStoryID(ctx, userID, publishedStoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.logger.Warn("Player progress not found for MakeChoice", logFields...)
			return ErrPlayerProgressNotFound
		}
		s.logger.Error("Failed to get player progress", append(logFields, zap.Error(err))...)
		return ErrInternal
	}

	// 2. Get the story
	publishedStory, err := s.publishedRepo.GetByID(ctx, publishedStoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.logger.Warn("Published story not found for MakeChoice", logFields...)
			return ErrStoryNotFound
		}
		s.logger.Error("Failed to get published story", append(logFields, zap.Error(err))...)
		return ErrInternal
	}

	// Check the status of the published story
	if publishedStory.Status != sharedModels.StatusReady && publishedStory.Status != sharedModels.StatusGeneratingScene {
		s.logger.Warn("Attempt to make choice in non-ready/generating story state", append(logFields, zap.String("status", string(publishedStory.Status)))...)
		return ErrStoryNotReady
	}

	// Get the current scene by hash from progress
	currentScene, err := s.sceneRepo.FindByStoryAndHash(ctx, publishedStoryID, progress.CurrentStateHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.logger.Error("CRITICAL: Current scene not found for hash in player progress", append(logFields, zap.String("stateHash", progress.CurrentStateHash))...)
			return ErrSceneNotFound
		}
		s.logger.Error("Failed to get current scene by hash", append(logFields, zap.String("stateHash", progress.CurrentStateHash), zap.Error(err))...)
		return ErrInternal
	}

	// Parse the content of the current scene
	var sceneData sceneContentChoices
	if err := json.Unmarshal(currentScene.Content, &sceneData); err != nil {
		s.logger.Error("Failed to unmarshal current scene content", append(logFields, zap.String("sceneID", currentScene.ID.String()), zap.Error(err))...)
		return ErrInternal
	}

	if sceneData.Type != "choices" {
		s.logger.Warn("Scene content is not of type 'choices'", append(logFields, zap.String("sceneID", currentScene.ID.String()), zap.String("type", sceneData.Type))...)
		return ErrInternal
	}

	// Validate selectedOptionIndex
	if len(sceneData.Choices) == 0 {
		s.logger.Error("Current scene has no choice blocks", append(logFields, zap.String("sceneID", currentScene.ID.String()))...)
		return ErrNoChoicesAvailable
	}
	choiceBlock := sceneData.Choices[0]
	if selectedOptionIndex < 0 || selectedOptionIndex >= len(choiceBlock.Options) {
		s.logger.Warn("Invalid selected option index for choice block 0",
			append(logFields, zap.Int("optionsAvailable", len(choiceBlock.Options)))...)
		return ErrInvalidChoiceIndex
	}

	// Load NovelSetup
	if publishedStory.Setup == nil {
		s.logger.Error("CRITICAL: PublishedStory Setup is nil, but scene exists and status is Ready/Generating", append(logFields, zap.String("status", string(publishedStory.Status)))...)
		return ErrInternal
	}
	var setupContent sharedModels.NovelSetupContent
	if err := json.Unmarshal(publishedStory.Setup, &setupContent); err != nil {
		s.logger.Error("Failed to unmarshal NovelSetup content", append(logFields, zap.Error(err))...)
		return ErrInternal
	}

	// 7. Apply consequences
	selectedOption := choiceBlock.Options[selectedOptionIndex]
	gameOverStat, isGameOver := applyConsequences(progress, selectedOption.Consequences, &setupContent)

	// 8. Handle Game Over
	if isGameOver {
		s.logger.Info("Game Over condition met", append(logFields, zap.String("gameOverStat", gameOverStat))...)
		if err := s.publishedRepo.UpdateStatusDetails(ctx, publishedStoryID, sharedModels.StatusGameOverPending, nil, nil, nil); err != nil {
			s.logger.Error("Failed to update published story status to GameOverPending", append(logFields, zap.Error(err))...)
		}
		progress.UpdatedAt = time.Now().UTC()
		if err := s.playerProgressRepo.CreateOrUpdate(ctx, progress); err != nil {
			s.logger.Error("Failed to save final player progress before game over", append(logFields, zap.Error(err))...)
		}

		taskID := uuid.New().String()
		var reasonCondition string
		finalValue := progress.CoreStats[gameOverStat]
		if def, ok := setupContent.CoreStatsDefinition[gameOverStat]; ok {
			if finalValue <= def.Min {
				reasonCondition = "min"
			}
			if finalValue >= def.Max {
				reasonCondition = "max"
			}
		}
		reason := sharedMessaging.GameOverReason{
			StatName:  gameOverStat,
			Condition: reasonCondition,
			Value:     finalValue,
		}
		var novelConfig sharedModels.Config
		if err := json.Unmarshal(publishedStory.Config, &novelConfig); err != nil {
			s.logger.Error("Failed to unmarshal novel config for game over task", append(logFields, zap.Error(err))...)
		}

		gameOverPayload := sharedMessaging.GameOverTaskPayload{
			TaskID:           taskID,
			UserID:           userID.String(),
			PublishedStoryID: publishedStoryID.String(),
			LastState:        *progress,
			Reason:           reason,
			NovelConfig:      novelConfig,
			NovelSetup:       setupContent,
		}
		if err := s.publisher.PublishGameOverTask(ctx, gameOverPayload); err != nil {
			s.logger.Error("Failed to publish game over generation task", append(logFields, zap.Error(err))...)
			// Note: Status remains GameOverPending, progress is saved.
			// Manual intervention or a retry mechanism might be needed if publishing fails.
			return ErrInternal
		}
		s.logger.Info("Game over task published", append(logFields, zap.String("taskID", taskID))...)
		return nil
	}

	// 9. Calculate new hash
	previousHash := progress.CurrentStateHash
	newStateHash, err := calculateStateHash(previousHash, progress.CoreStats, progress.StoryVariables, progress.GlobalFlags)
	if err != nil {
		s.logger.Error("Failed to calculate new state hash", append(logFields, zap.Error(err))...)
		return ErrInternal
	}
	logFields = append(logFields, zap.String("newStateHash", newStateHash))
	s.logger.Debug("New state hash calculated", logFields...)

	// 10. Update hash in progress
	progress.CurrentStateHash = newStateHash
	progress.UpdatedAt = time.Now().UTC()

	// 11. Find next scene
	nextScene, err := s.sceneRepo.FindByStoryAndHash(ctx, publishedStoryID, newStateHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.logger.Info("Next scene not found, publishing generation task", logFields...)
			if errStatus := s.publishedRepo.UpdateStatusDetails(ctx, publishedStoryID, sharedModels.StatusGeneratingScene, nil, nil, nil); errStatus != nil {
				s.logger.Error("Failed to update published story status to GeneratingScene", append(logFields, zap.Error(errStatus))...)
			}

			generationPayload, errGenPayload := createGenerationPayload(
				userID,
				publishedStory,
				progress,
				previousHash,
				newStateHash,
				choiceBlock.Description,
				selectedOption.Text,
			)
			if errGenPayload != nil {
				s.logger.Error("Failed to create generation payload", append(logFields, zap.Error(errGenPayload))...)
				return ErrInternal
			}

			if errPub := s.publisher.PublishGenerationTask(ctx, generationPayload); errPub != nil {
				s.logger.Error("Failed to publish next scene generation task", append(logFields, zap.Error(errPub))...)
				// Note: Status remains GeneratingScene, progress will be saved shortly.
				// Manual intervention or a retry mechanism might be needed.
				return ErrInternal
			}
			s.logger.Info("Next scene generation task published", append(logFields, zap.String("taskID", generationPayload.TaskID))...)

			s.logger.Debug("Clearing StoryVariables and GlobalFlags before saving progress", logFields...)
			progress.StoryVariables = make(map[string]interface{})
			progress.GlobalFlags = []string{}

			if err := s.playerProgressRepo.CreateOrUpdate(ctx, progress); err != nil {
				s.logger.Error("Ошибка сохранения обновленного PlayerProgress после запуска генерации", append(logFields, zap.Error(err))...)
				return ErrInternal
			}
			s.logger.Info("PlayerProgress (с очищенными sv/gf) успешно обновлен после запуска генерации", logFields...)

			return nil
		} else {
			s.logger.Error("Error searching for next scene", append(logFields, zap.Error(err))...)
			return ErrInternal
		}
	}

	// 12. Next scene found
	s.logger.Info("Next scene found in DB", logFields...)

	type SceneOutputFormat struct {
		Sssf string `json:"sssf"`
		Fd   string `json:"fd"`
		Vis  string `json:"vis"`
	}
	var sceneOutput SceneOutputFormat
	if errUnmarshal := json.Unmarshal(nextScene.Content, &sceneOutput); errUnmarshal != nil {
		s.logger.Error("Failed to unmarshal next scene content to get summaries",
			append(logFields, zap.String("nextSceneID", nextScene.ID.String()), zap.Error(errUnmarshal))...)
	} else {
		progress.LastStorySummary = sceneOutput.Sssf
		progress.LastFutureDirection = sceneOutput.Fd
		progress.LastVarImpactSummary = sceneOutput.Vis
	}

	s.logger.Debug("Clearing StoryVariables and GlobalFlags before saving progress", logFields...)
	progress.StoryVariables = make(map[string]interface{})
	progress.GlobalFlags = []string{}

	if err := s.playerProgressRepo.CreateOrUpdate(ctx, progress); err != nil {
		s.logger.Error("Ошибка сохранения обновленного PlayerProgress после нахождения след. сцены", append(logFields, zap.Error(err))...)
		return ErrInternal
	}
	s.logger.Info("PlayerProgress (с очищенными sv/gf и новыми сводками) успешно обновлен после нахождения след. сцены", logFields...)

	return nil
}

// calculateStateHash calculates a deterministic state hash, including the previous state hash.
func calculateStateHash(previousHash string, coreStats map[string]int, storyVars map[string]interface{}, globalFlags []string) (string, error) {
	// 1. Prepare data
	stateMap := make(map[string]interface{})

	stateMap["_ph"] = previousHash

	for k, v := range coreStats {
		stateMap["cs_"+k] = v
	}

	for k, v := range storyVars {
		stateMap["sv_"+k] = v
	}

	sortedFlags := make([]string, len(globalFlags))
	copy(sortedFlags, globalFlags)
	sort.Strings(sortedFlags)
	stateMap["gf"] = sortedFlags

	keys := make([]string, 0, len(stateMap))
	for k := range stateMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString("{")
	for i, k := range keys {
		valueBytes, err := json.Marshal(stateMap[k])
		if err != nil {
			return "", fmt.Errorf("error serializing value for key '%s': %w", k, err)
		}
		sb.WriteString(fmt.Sprintf("\"%s\":%s", k, string(valueBytes)))
		if i < len(keys)-1 {
			sb.WriteString(",")
		}
	}
	sb.WriteString("}")
	canonicalJSON := sb.String()

	hasher := sha256.New()
	hasher.Write([]byte(canonicalJSON))
	hashBytes := hasher.Sum(nil)

	return hex.EncodeToString(hashBytes), nil
}

// applyConsequences applies consequences of choice to player progress
// and checks Game Over conditions.
// Returns stat name causing Game Over and Game Over flag.
func applyConsequences(progress *sharedModels.PlayerProgress, cons sharedModels.Consequences, setup *sharedModels.NovelSetupContent) (gameOverStat string, isGameOver bool) {
	if progress == nil || setup == nil {
		log.Println("ERROR: applyConsequences called with nil progress or setup")
		return "", false
	}

	if progress.CoreStats == nil {
		progress.CoreStats = make(map[string]int)
	}
	if progress.StoryVariables == nil {
		progress.StoryVariables = make(map[string]interface{})
	}
	if progress.GlobalFlags == nil {
		progress.GlobalFlags = []string{}
	}

	if cons.CoreStatsChange != nil {
		for statName, change := range cons.CoreStatsChange {
			progress.CoreStats[statName] += change
		}
	}

	if cons.StoryVariables != nil {
		for varName, value := range cons.StoryVariables {
			if value == nil {
				delete(progress.StoryVariables, varName)
			} else {
				progress.StoryVariables[varName] = value
			}
		}
	}

	if len(cons.GlobalFlagsRemove) > 0 {
		flagsToRemove := make(map[string]struct{})
		for _, flag := range cons.GlobalFlagsRemove {
			flagsToRemove[flag] = struct{}{}
		}
		newFlags := make([]string, 0, len(progress.GlobalFlags))
		for _, flag := range progress.GlobalFlags {
			if _, found := flagsToRemove[flag]; !found {
				newFlags = append(newFlags, flag)
			}
		}
		progress.GlobalFlags = newFlags
	}

	if len(cons.GlobalFlags) > 0 {
		existingFlags := make(map[string]struct{}, len(progress.GlobalFlags))
		for _, flag := range progress.GlobalFlags {
			existingFlags[flag] = struct{}{}
		}
		for _, flagToAdd := range cons.GlobalFlags {
			if _, found := existingFlags[flagToAdd]; !found {
				progress.GlobalFlags = append(progress.GlobalFlags, flagToAdd)
				existingFlags[flagToAdd] = struct{}{}
			}
		}
	}

	if setup.CoreStatsDefinition != nil {
		for statName, definition := range setup.CoreStatsDefinition {
			currentValue := progress.CoreStats[statName]
			if currentValue <= definition.Min {
				return statName, true
			}
			if currentValue >= definition.Max {
				return statName, true
			}
		}
	}

	return "", false
}

// DeletePlayerProgress deletes player progress for the specified story.
func (s *gameplayServiceImpl) DeletePlayerProgress(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error {
	s.logger.Info("Deleting player progress",
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()))

	_, err := s.publishedRepo.GetByID(ctx, publishedStoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sharedModels.ErrNotFound
		}
		return fmt.Errorf("error checking published story: %w", err)
	}

	err = s.playerProgressRepo.Delete(ctx, userID, publishedStoryID)
	if err != nil {
		// Wrap the error from the repository, could be ErrNoRows (progress not found) or other DB errors
		return fmt.Errorf("error deleting player progress: %w", err)
	}

	return nil
}

// createGenerationPayload creates the payload for the next scene generation task,
// using compressed keys and summaries from the previous step.
func createGenerationPayload(
	userID uuid.UUID,
	story *sharedModels.PublishedStory,
	progress *sharedModels.PlayerProgress,
	previousHash string,
	nextStateHash string,
	userChoiceDescription string,
	userChoiceText string,
) (sharedMessaging.GenerationTaskPayload, error) {
	var configMap map[string]interface{}
	if len(story.Config) > 0 {
		if err := json.Unmarshal(story.Config, &configMap); err != nil {
			log.Printf("WARN: Failed to parse Config JSON for generation task StoryID %s: %v", story.ID, err)
			return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error parsing Config JSON: %w", err)
		}
	} else {
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("missing Config in PublishedStory ID %s", story.ID)
	}

	var setupMap map[string]interface{}
	if len(story.Setup) > 0 {
		if err := json.Unmarshal(story.Setup, &setupMap); err != nil {
			log.Printf("WARN: Failed to parse Setup JSON for generation task StoryID %s: %v", story.ID, err)
			return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error parsing Setup JSON: %w", err)
		}
	} else {
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("missing Setup in PublishedStory ID %s", story.ID)
	}

	compressedInputData := make(map[string]interface{})

	compressedInputData["cfg"] = configMap
	compressedInputData["stp"] = setupMap

	if progress.CoreStats != nil {
		compressedInputData["cs"] = progress.CoreStats
	}

	compressedInputData["pss"] = progress.LastStorySummary
	compressedInputData["pfd"] = progress.LastFutureDirection
	compressedInputData["pvis"] = progress.LastVarImpactSummary

	if progress.StoryVariables != nil {
		compressedInputData["sv"] = progress.StoryVariables
	}
	if progress.GlobalFlags != nil {
		sortedFlags := make([]string, len(progress.GlobalFlags))
		copy(sortedFlags, progress.GlobalFlags)
		sort.Strings(sortedFlags)
		compressedInputData["gf"] = sortedFlags
	} else {
		compressedInputData["gf"] = []string{}
	}

	type CompressedUserChoiceContext struct {
		Desc string `json:"d"` // description
		Text string `json:"t"` // choice_text
	}

	compressedInputData["uc"] = CompressedUserChoiceContext{
		Desc: userChoiceDescription,
		Text: userChoiceText,
	}

	payload := sharedMessaging.GenerationTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           userID.String(),
		PublishedStoryID: story.ID.String(),
		PromptType:       sharedMessaging.PromptTypeNovelCreator,
		InputData:        compressedInputData,
		StateHash:        nextStateHash,
	}

	return payload, nil
}

// ListUserDrafts retrieves a paginated list of StoryConfig drafts for a specific user ID.
func (s *gameplayServiceImpl) ListUserDrafts(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.StoryConfig, string, error) {
	if limit <= 0 || limit > 100 { // Apply a reasonable limit
		limit = 20
	}

	// Fetch drafts using the repository method that takes userID as argument
	configs, nextCursor, err := s.repo.ListByUserID(ctx, userID, cursor, limit)
	if err != nil {
		// Handle specific errors like invalid cursor
		if errors.Is(err, interfaces.ErrInvalidCursor) {
			s.logger.Warn("Invalid cursor provided for ListUserDrafts", zap.String("userID", userID.String()), zap.String("cursor", cursor))
			return nil, "", err // Return the specific error
		}
		// Log other errors
		s.logger.Error("Error listing user drafts from repository", zap.String("userID", userID.String()), zap.Error(err))
		return nil, "", ErrInternal // Return a generic internal error
	}

	// Return the fetched configs and the next cursor
	return configs, nextCursor, nil
}

// ListUserPublishedStories retrieves a paginated list of PublishedStory for a specific user ID.
func (s *gameplayServiceImpl) ListUserPublishedStories(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*sharedModels.PublishedStory, error) {
	if limit <= 0 || limit > 100 { // Apply a reasonable limit
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	// Fetch published stories using the repository method
	stories, err := s.publishedRepo.ListByUserID(ctx, userID, limit, offset)
	if err != nil {
		s.logger.Error("Error listing user published stories from repository", zap.String("userID", userID.String()), zap.Error(err))
		return nil, ErrInternal // Return a generic internal error
	}

	return stories, nil
}

// RetryDraftGeneration attempts to resend a generation task for a draft in Error status.
func (s *gameplayServiceImpl) RetryDraftGeneration(ctx context.Context, draftID uuid.UUID, userID uuid.UUID) error {
	log := s.logger.With(zap.String("draftID", draftID.String()), zap.String("userID", userID.String()))
	log.Info("Attempting to retry draft generation")

	// 1. Get the draft
	config, err := s.repo.GetByID(ctx, draftID, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("Draft not found for retry")
			return sharedModels.ErrNotFound
		}
		log.Error("Error getting draft for retry", zap.Error(err))
		return fmt.Errorf("error getting draft: %w", err)
	}

	// 2. Check status
	if config.Status != sharedModels.StatusError {
		log.Warn("Attempt to retry generation for draft not in Error status", zap.String("status", string(config.Status)))
		return ErrCannotRetry
	}

	// 3. Check active generation limit
	activeCount, err := s.repo.CountActiveGenerations(ctx, userID)
	if err != nil {
		log.Error("Error counting active generations before retry", zap.Error(err))
		return fmt.Errorf("error checking generation status: %w", err)
	}
	generationLimit := 1 // TODO: Configurable
	if activeCount >= generationLimit {
		log.Info("User reached active generation limit, retry rejected")
		return sharedModels.ErrUserHasActiveGeneration
	}

	// 4. Determine the prompt type and input data for retry
	// We assume the last entry in UserInput is the one that caused the error
	var userInputs []string
	var lastUserInput string
	var promptType sharedMessaging.PromptType
	inputData := make(map[string]interface{})

	if config.UserInput != nil {
		if err := json.Unmarshal(config.UserInput, &userInputs); err == nil && len(userInputs) > 0 {
			lastUserInput = userInputs[len(userInputs)-1]
			if len(userInputs) == 1 {
				// If only one input, it was the initial generation
				promptType = sharedMessaging.PromptTypeNarrator
				// InputData remains empty for initial
			} else {
				// If more than one, it was a revision
				promptType = sharedMessaging.PromptTypeNarrator
				if config.Config != nil { // Config should exist if it was a revision, even if error happened later
					inputData["current_config"] = string(config.Config)
				} else {
					log.Warn("Config is missing for a presumed revision retry", zap.String("draftID", draftID.String()))
					// Proceed without config? Or return error? For now, proceed.
				}
			}
		} else {
			log.Error("Failed to unmarshal UserInput or UserInput is empty for retry", zap.Error(err))
			return ErrInternal // Cannot determine the context for retry
		}
	} else {
		log.Error("UserInput is nil for retry", zap.String("draftID", draftID.String()))
		return ErrInternal
	}

	// 5. Update draft status to 'generating'
	config.Status = sharedModels.StatusGenerating
	config.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, config); err != nil {
		log.Error("Error updating draft status before retry task publish", zap.Error(err))
		return ErrInternal
	}

	// 6. Publish the generation task again
	taskID := uuid.New().String()
	generationPayload := sharedMessaging.GenerationTaskPayload{
		TaskID:        taskID,
		UserID:        config.UserID.String(),
		PromptType:    promptType,
		InputData:     inputData,
		UserInput:     lastUserInput,
		StoryConfigID: config.ID.String(),
	}

	if err := s.publisher.PublishGenerationTask(ctx, generationPayload); err != nil {
		log.Error("Error publishing retry generation task. Rolling back status...", zap.Error(err))
		// Attempt to roll back status to Error
		config.Status = sharedModels.StatusError
		config.UpdatedAt = time.Now().UTC()
		if rollbackErr := s.repo.Update(context.Background(), config); rollbackErr != nil {
			log.Error("CRITICAL: Failed to roll back status to Error after retry publish error", zap.String("draftID", config.ID.String()), zap.Error(rollbackErr))
		}
		return ErrInternal // Return internal error to handler
	}

	log.Info("Retry generation task published successfully")
	return nil
}

// LikeStory добавляет лайк к опубликованной истории от пользователя.
func (s *gameplayServiceImpl) LikeStory(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error {
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	s.logger.Info("Attempting to like story", logFields...)

	// 1. Проверяем, существует ли история (можно сделать внутри likeRepo.AddLike)
	//    Либо можно добавить отдельную проверку через publishedRepo.Exists(ctx, publishedStoryID)
	//    чтобы вернуть более точную ошибку ErrStoryNotFound.
	//    Для простоты пока положимся на то, что AddLike вернет ошибку, если story не найден (через FK constraint).

	// 2. Пытаемся добавить лайк в репозиторий
	err := s.likeRepo.AddLike(ctx, userID, publishedStoryID)
	if err != nil {
		// Проверяем, не ошибка ли это "уже лайкнуто" (зависит от реализации repo)
		if errors.Is(err, interfaces.ErrLikeAlreadyExists) { // Предполагаем, что репозиторий возвращает такую ошибку
			s.logger.Warn("User already liked this story", logFields...)
			return ErrAlreadyLiked // Возвращаем нашу сервисную ошибку
		}
		// Проверяем, не ошибка ли это "история не найдена" (используем общую ошибку)
		if errors.Is(err, sharedModels.ErrNotFound) { // <<< Используем sharedModels.ErrNotFound
			s.logger.Warn("Story not found for liking", logFields...)
			return ErrStoryNotFound // Возвращаем стандартную сервисную ошибку
		}

		// Другая ошибка репозитория
		s.logger.Error("Failed to add like in repository", append(logFields, zap.Error(err))...)
		return ErrInternal // Возвращаем общую внутреннюю ошибку
	}

	// 3. Успешно добавили лайк, теперь нужно инкрементировать счетчик.
	//    Это может быть сделано в likeRepo.AddLike атомарно, либо отдельным вызовом.
	//    Для надежности и гибкости лучше отдельный вызов к publishedRepo или кэшу.
	//    Пример с publishedRepo:
	if err := s.publishedRepo.IncrementLikesCount(ctx, publishedStoryID); err != nil {
		// Если счетчик не удалось обновить - это проблема, лайк уже стоит.
		// Логируем как ошибку, но пользователю можно вернуть успех (лайк поставлен).
		// В реальной системе здесь может быть логика компенсации (удалить лайк) или retry.
		s.logger.Error("Failed to increment likes count after adding like record", append(logFields, zap.Error(err))...)
		// Можно вернуть nil, т.к. лайк фактически добавлен.
	}

	s.logger.Info("Story liked successfully", logFields...)
	return nil
}

// UnlikeStory удаляет лайк с опубликованной истории от пользователя.
func (s *gameplayServiceImpl) UnlikeStory(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error {
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	s.logger.Info("Attempting to unlike story", logFields...)

	// 1. Пытаемся удалить лайк из репозитория
	err := s.likeRepo.RemoveLike(ctx, userID, publishedStoryID)
	if err != nil {
		// Проверяем, не ошибка ли это "лайка нет" (зависит от реализации repo)
		if errors.Is(err, interfaces.ErrLikeNotFound) { // Предполагаем, что репозиторий возвращает такую ошибку
			s.logger.Warn("User had not liked this story", logFields...)
			return ErrNotLikedYet // Возвращаем нашу сервисную ошибку
		}
		// Проверяем, не ошибка ли это "история не найдена" (используем общую ошибку)
		if errors.Is(err, sharedModels.ErrNotFound) { // <<< Используем sharedModels.ErrNotFound
			s.logger.Warn("Story not found for unliking", logFields...)
			return ErrStoryNotFound // Возвращаем стандартную сервисную ошибку
		}

		// Другая ошибка репозитория
		s.logger.Error("Failed to remove like in repository", append(logFields, zap.Error(err))...)
		return ErrInternal // Возвращаем общую внутреннюю ошибку
	}

	// 2. Успешно удалили лайк, теперь нужно декрементировать счетчик.
	//    Аналогично LikeStory, лучше отдельным вызовом.
	if err := s.publishedRepo.DecrementLikesCount(ctx, publishedStoryID); err != nil {
		// Если счетчик не удалось обновить - это проблема.
		// Логируем как ошибку, но пользователю можно вернуть успех (лайк снят).
		s.logger.Error("Failed to decrement likes count after removing like record", append(logFields, zap.Error(err))...)
		// Можно вернуть nil, т.к. лайк фактически удален.
	}

	s.logger.Info("Story unliked successfully", logFields...)
	return nil
}
