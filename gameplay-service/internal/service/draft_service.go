package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"novel-server/gameplay-service/internal/messaging"
	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// Define errors specific to draft operations
var (
// ErrCannotRevise = errors.New("story is not in a state that allows revision (must be Draft or Error)") // Use sharedModels.ErrCannotRevise
// ErrCannotRetry  = errors.New("story is not in Error status, cannot retry generation") // Use sharedModels.ErrCannotRetry
// sharedModels.ErrUserHasActiveGeneration will be used directly
// sharedModels.ErrNotFound will be used directly
)

// DraftService defines the interface for managing story drafts.
type DraftService interface {
	GenerateInitialStory(ctx context.Context, userID uuid.UUID, initialPrompt string, language string) (*sharedModels.StoryConfig, error)
	ReviseDraft(ctx context.Context, id uuid.UUID, userID uuid.UUID, revisionPrompt string) error
	GetStoryConfig(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*sharedModels.StoryConfig, error)
	ListUserDrafts(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.StoryConfig, string, error)
	RetryDraftGeneration(ctx context.Context, draftID uuid.UUID, userID uuid.UUID) error
	GetDraftDetailsInternal(ctx context.Context, draftID uuid.UUID) (*sharedModels.StoryConfig, error)
	UpdateDraftInternal(ctx context.Context, draftID uuid.UUID, configJSON, userInputJSON string) error
}

type draftServiceImpl struct {
	repo      interfaces.StoryConfigRepository
	publisher messaging.TaskPublisher
	logger    *zap.Logger
}

// NewDraftService creates a new instance of DraftService.
func NewDraftService(
	repo interfaces.StoryConfigRepository,
	publisher messaging.TaskPublisher,
	logger *zap.Logger,
) DraftService {
	return &draftServiceImpl{
		repo:      repo,
		publisher: publisher,
		logger:    logger.Named("DraftService"),
	}
}

// GenerateInitialStory creates a new StoryConfig entry and sends a generation task.
func (s *draftServiceImpl) GenerateInitialStory(ctx context.Context, userID uuid.UUID, initialPrompt string, language string) (*sharedModels.StoryConfig, error) {
	log := s.logger.With(zap.String("userID", userID.String()), zap.String("language", language))
	log.Info("GenerateInitialStory called")

	// Check the number of active generations for this userID
	activeCount, err := s.repo.CountActiveGenerations(ctx, userID)
	if err != nil {
		log.Error("Error counting active generations", zap.Error(err))
		return nil, fmt.Errorf("error checking generation status: %w", err)
	}
	// TODO: Make the limit configurable
	generationLimit := 1
	if activeCount >= generationLimit {
		log.Warn("User reached the active generation limit", zap.Int("limit", generationLimit))
		return nil, sharedModels.ErrUserHasActiveGeneration
	}

	userInputJSON, err := json.Marshal([]string{initialPrompt})
	if err != nil {
		log.Error("Error marshalling initialPrompt", zap.Error(err))
		return nil, fmt.Errorf("error preparing data for DB: %w", err)
	}

	config := &sharedModels.StoryConfig{
		ID:          uuid.New(),
		UserID:      userID,
		Title:       "", // Will be filled after generation
		Description: "", // Not saving initialPrompt here
		UserInput:   userInputJSON,
		Config:      nil,
		Status:      sharedModels.StatusGenerating,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
		Language:    language,
	}

	err = s.repo.Create(ctx, config)
	if err != nil {
		log.Error("Error saving initial draft", zap.String("draftID", config.ID.String()), zap.Error(err))
		return nil, fmt.Errorf("error saving initial draft: %w", err)
	}
	log.Info("Initial draft created and saved", zap.String("draftID", config.ID.String()))

	taskID := uuid.New().String()
	generationPayload := sharedMessaging.GenerationTaskPayload{
		TaskID:        taskID,
		UserID:        config.UserID.String(),
		PromptType:    sharedMessaging.PromptTypeNarrator,
		UserInput:     initialPrompt,
		StoryConfigID: config.ID.String(),
	}

	if err := s.publisher.PublishGenerationTask(ctx, generationPayload); err != nil {
		log.Error("Error publishing initial generation task, attempting rollback", zap.String("draftID", config.ID.String()), zap.String("taskID", taskID), zap.Error(err))
		config.Status = sharedModels.StatusError
		config.UpdatedAt = time.Now().UTC()
		if rollbackErr := s.repo.Update(context.Background(), config); rollbackErr != nil {
			log.Error("CRITICAL ERROR: Failed to roll back status to Error after publish error", zap.String("draftID", config.ID.String()), zap.Error(rollbackErr))
		}
		return config, fmt.Errorf("error sending generation task: %w", err)
	}

	log.Info("Initial generation task sent successfully", zap.String("draftID", config.ID.String()), zap.String("taskID", taskID))
	return config, nil
}

// ReviseDraft updates an existing story draft
func (s *draftServiceImpl) ReviseDraft(ctx context.Context, id uuid.UUID, userID uuid.UUID, revisionPrompt string) error {
	log := s.logger.With(zap.String("draftID", id.String()), zap.String("userID", userID.String()))
	log.Info("ReviseDraft called")

	config, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		log.Error("Error getting draft for revision", zap.Error(err))
		// Let the caller handle ErrNotFound if necessary
		return fmt.Errorf("error getting draft for revision: %w", err)
	}

	if config.Status != sharedModels.StatusDraft && config.Status != sharedModels.StatusError {
		log.Warn("Attempt to revise in invalid status", zap.String("status", string(config.Status)))
		return sharedModels.ErrCannotRevise // Use shared error
	}

	activeCount, err := s.repo.CountActiveGenerations(ctx, userID)
	if err != nil {
		log.Error("Error counting active generations before revision", zap.Error(err))
		return fmt.Errorf("error checking generation status: %w", err)
	}
	generationLimit := 1 // TODO: Configurable
	if activeCount >= generationLimit {
		log.Warn("User reached active generation limit, revision rejected", zap.Int("limit", generationLimit))
		return sharedModels.ErrUserHasActiveGeneration
	}

	var userInputs []string
	if config.UserInput != nil {
		if err := json.Unmarshal(config.UserInput, &userInputs); err != nil {
			log.Warn("Error deserializing UserInput, creating new array", zap.Error(err))
			userInputs = make([]string, 0)
		}
	}
	userInputs = append(userInputs, revisionPrompt)
	updatedUserInputJSON, err := json.Marshal(userInputs)
	if err != nil {
		log.Error("Error marshalling updated UserInput", zap.Error(err))
		return fmt.Errorf("error preparing data for DB: %w", err)
	}
	config.UserInput = updatedUserInputJSON

	originalStatus := config.Status // Store original status for potential rollback
	config.Status = sharedModels.StatusGenerating
	config.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, config); err != nil {
		log.Error("Error updating status/UserInput before revision", zap.Error(err))
		return fmt.Errorf("error updating status/UserInput before revision: %w", err)
	}

	taskID := uuid.New().String()
	generationPayload := sharedMessaging.GenerationTaskPayload{
		TaskID:        taskID,
		UserID:        config.UserID.String(),
		PromptType:    sharedMessaging.PromptTypeNarrator,
		UserInput:     revisionPrompt,
		StoryConfigID: config.ID.String(),
	}

	if err := s.publisher.PublishGenerationTask(ctx, generationPayload); err != nil {
		log.Error("Error publishing revision task, attempting rollback", zap.String("taskID", taskID), zap.Error(err))
		config.Status = originalStatus                                     // Rollback to Draft or Error
		config.UserInput, _ = json.Marshal(userInputs[:len(userInputs)-1]) // Remove the last input
		config.UpdatedAt = time.Now().UTC()
		if rollbackErr := s.repo.Update(context.Background(), config); rollbackErr != nil {
			log.Error("CRITICAL ERROR: Failed to roll back status/UserInput after revision publish error", zap.Error(rollbackErr))
		}
		return fmt.Errorf("error publishing revision task: %w", err)
	}

	log.Info("Revision task sent successfully", zap.String("taskID", taskID))
	return nil
}

// GetStoryConfig gets the story config
func (s *draftServiceImpl) GetStoryConfig(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*sharedModels.StoryConfig, error) {
	log := s.logger.With(zap.String("draftID", id.String()), zap.String("userID", userID.String()))
	log.Info("GetStoryConfig called")

	config, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("StoryConfig not found")
			return nil, sharedModels.ErrNotFound // Use standard error
		}
		log.Error("Error getting StoryConfig", zap.Error(err))
		return nil, fmt.Errorf("error getting StoryConfig: %w", err)
	}
	return config, nil
}

// ListUserDrafts retrieves a paginated list of StoryConfig drafts for a specific user ID.
func (s *draftServiceImpl) ListUserDrafts(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.StoryConfig, string, error) {
	log := s.logger.With(zap.String("userID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit))
	log.Info("ListUserDrafts called")

	if limit <= 0 || limit > 100 {
		log.Warn("Invalid limit requested, adjusting", zap.Int("requestedLimit", limit))
		limit = 20
	}

	configs, nextCursor, err := s.repo.ListByUserID(ctx, userID, cursor, limit+1) // Fetch one extra
	if err != nil {
		if errors.Is(err, interfaces.ErrInvalidCursor) {
			log.Warn("Invalid cursor provided")
			return nil, "", err // Return the specific error
		}
		log.Error("Error listing user drafts from repository", zap.Error(err))
		return nil, "", ErrInternal // Return a generic internal error
	}

	hasNextPage := len(configs) > limit
	if hasNextPage {
		configs = configs[:limit]
	} else {
		nextCursor = "" // No next page if we didn't fetch extra
	}

	return configs, nextCursor, nil
}

// RetryDraftGeneration attempts to resend a generation task for a draft in Error status.
func (s *draftServiceImpl) RetryDraftGeneration(ctx context.Context, draftID uuid.UUID, userID uuid.UUID) error {
	log := s.logger.With(zap.String("draftID", draftID.String()), zap.String("userID", userID.String()))
	log.Info("Attempting to retry draft generation")

	config, err := s.repo.GetByID(ctx, draftID, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("Draft not found for retry")
			return sharedModels.ErrNotFound
		}
		log.Error("Error getting draft for retry", zap.Error(err))
		return fmt.Errorf("error getting draft: %w", err)
	}

	if config.Status != sharedModels.StatusError {
		log.Warn("Attempt to retry generation for draft not in Error status", zap.String("status", string(config.Status)))
		return sharedModels.ErrCannotRetry // Use shared error
	}

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

	var userInputs []string
	var lastUserInput string
	var promptType sharedMessaging.PromptType
	inputData := make(map[string]interface{})

	if config.UserInput != nil {
		if err := json.Unmarshal(config.UserInput, &userInputs); err == nil && len(userInputs) > 0 {
			lastUserInput = userInputs[len(userInputs)-1]
			if len(userInputs) == 1 {
				promptType = sharedMessaging.PromptTypeNarrator
			} else {
				promptType = sharedMessaging.PromptTypeNarrator
				if config.Config != nil {
					inputData["current_config"] = string(config.Config)
				} else {
					log.Warn("Config is missing for a presumed revision retry")
				}
			}
		} else {
			log.Error("Failed to unmarshal UserInput or UserInput is empty for retry", zap.Error(err))
			return ErrInternal
		}
	} else {
		log.Error("UserInput is nil for retry")
		return ErrInternal
	}

	config.Status = sharedModels.StatusGenerating
	config.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, config); err != nil {
		log.Error("Error updating draft status before retry task publish", zap.Error(err))
		return ErrInternal
	}

	taskID := uuid.New().String()
	generationPayload := sharedMessaging.GenerationTaskPayload{
		TaskID:        taskID,
		UserID:        config.UserID.String(),
		PromptType:    promptType,
		UserInput:     lastUserInput,
		StoryConfigID: config.ID.String(),
	}

	if err := s.publisher.PublishGenerationTask(ctx, generationPayload); err != nil {
		log.Error("Error publishing retry generation task. Rolling back status...", zap.Error(err))
		config.Status = sharedModels.StatusError
		config.UpdatedAt = time.Now().UTC()
		if rollbackErr := s.repo.Update(context.Background(), config); rollbackErr != nil {
			log.Error("CRITICAL: Failed to roll back status to Error after retry publish error", zap.Error(rollbackErr))
		}
		return ErrInternal
	}

	log.Info("Retry generation task published successfully", zap.String("taskID", taskID))
	return nil
}

// GetDraftDetailsInternal retrieves the details of a story draft for internal API access.
func (s *draftServiceImpl) GetDraftDetailsInternal(ctx context.Context, draftID uuid.UUID) (*sharedModels.StoryConfig, error) {
	log := s.logger.With(zap.String("draftID", draftID.String()))
	log.Info("GetDraftDetailsInternal called")

	config, err := s.repo.GetByID(ctx, draftID, uuid.Nil)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("StoryConfig not found")
			return nil, sharedModels.ErrNotFound // Use standard error
		}
		log.Error("Error getting StoryConfig", zap.Error(err))
		return nil, fmt.Errorf("error getting StoryConfig: %w", err)
	}
	return config, nil
}

// UpdateDraftInternal updates a story draft with new config and user input.
func (s *draftServiceImpl) UpdateDraftInternal(ctx context.Context, draftID uuid.UUID, configJSON, userInputJSON string) error {
	log := s.logger.With(zap.String("draftID", draftID.String()))
	log.Info("UpdateDraftInternal called")

	// Валидация JSON
	var configBytes, userInputBytes []byte
	var err error

	if configJSON != "" {
		if !json.Valid([]byte(configJSON)) {
			log.Warn("Invalid JSON received for config")
			return fmt.Errorf("%w: invalid config JSON format", sharedModels.ErrBadRequest)
		}
		configBytes = []byte(configJSON)
	} else {
		configBytes = nil // Разрешаем обнулять поле
	}

	if userInputJSON != "" {
		if !json.Valid([]byte(userInputJSON)) {
			log.Warn("Invalid JSON received for user input")
			return fmt.Errorf("%w: invalid user input JSON format", sharedModels.ErrBadRequest)
		}
		userInputBytes = []byte(userInputJSON)
	} else {
		userInputBytes = nil // Разрешаем обнулять поле
	}

	// Вызов репозитория
	err = s.repo.UpdateConfigAndInput(ctx, draftID, configBytes, userInputBytes)
	if err != nil {
		if errors.Is(err, sharedModels.ErrNotFound) {
			log.Warn("Draft not found for update")
			return sharedModels.ErrNotFound // Пробрасываем ошибку
		}
		log.Error("Failed to update draft config and input in repository", zap.Error(err))
		return ErrInternal // Возвращаем общую ошибку
	}

	log.Info("Draft updated successfully by internal request")
	return nil
}
