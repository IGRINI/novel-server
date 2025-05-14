package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"novel-server/gameplay-service/internal/config"
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
	UpdateDraftInternal(ctx context.Context, draftID uuid.UUID, configJSON, userInputJSON string, status sharedModels.StoryStatus) error
	DeleteDraft(ctx context.Context, id uuid.UUID, userID uuid.UUID) error
}

type draftServiceImpl struct {
	repo      interfaces.StoryConfigRepository
	publisher messaging.TaskPublisher
	logger    *zap.Logger
	cfg       *config.Config
}

// NewDraftService creates a new instance of DraftService.
func NewDraftService(
	repo interfaces.StoryConfigRepository,
	publisher messaging.TaskPublisher,
	logger *zap.Logger,
	cfg *config.Config,
) DraftService {
	return &draftServiceImpl{
		repo:      repo,
		publisher: publisher,
		logger:    logger.Named("DraftService"),
		cfg:       cfg,
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
	// Используем лимит из конфига
	generationLimit := s.cfg.GenerationLimitPerUser
	if activeCount >= generationLimit {
		log.Warn("User reached the active generation limit", zap.Int("limit", generationLimit))
		return nil, sharedModels.ErrUserHasActiveGeneration
	}

	userInputJSON, err := json.Marshal([]string{initialPrompt})
	if err != nil {
		log.Error("Error marshalling initialPrompt", zap.Error(err))
		return nil, fmt.Errorf("error preparing data for DB: %w", err)
	}

	log.Debug("Language parameter received before config creation", zap.String("languageParam", language))

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

	log.Debug("StoryConfig object created", zap.String("config.ID", config.ID.String()), zap.String("config.Language", config.Language))

	err = s.repo.Create(ctx, config)
	if err != nil {
		log.Error("Error saving initial draft", zap.String("draftID", config.ID.String()), zap.Error(err))
		return nil, fmt.Errorf("error saving initial draft: %w", err)
	}
	log.Info("Initial draft created and saved", zap.String("draftID", config.ID.String()))

	taskID := uuid.New().String()

	// Префикс языка больше не добавляем, используем поле Language в payload
	userInputForTask := initialPrompt
	/*
		if prefix, ok := languagePrefixes[language]; ok {
			userInputForTask = prefix + initialPrompt
			log.Debug("Added language prefix to prompt", zap.String("prefix", prefix))
		} else {
			log.Warn("Language code not found in prefixes, using original prompt", zap.String("language", language))
		}
	*/

	log.Debug("Language value from config before payload creation", zap.String("draftID", config.ID.String()), zap.String("config.Language", config.Language))

	generationPayload := sharedMessaging.GenerationTaskPayload{
		TaskID:        taskID,
		UserID:        config.UserID.String(),
		PromptType:    sharedModels.PromptTypeNarrator,
		UserInput:     userInputForTask, // Передаем исходный prompt пользователя
		StoryConfigID: config.ID.String(),
		Language:      config.Language, // <<< ПЕРЕДАЕМ ЯЗЫК ОТДЕЛЬНО >>>
	}

	log.Debug("GenerationTaskPayload created", zap.Any("payload", generationPayload))

	if err := s.publishPayload(ctx, config, generationPayload); err != nil {
		return config, fmt.Errorf("error sending generation task: %w", err)
	}

	log.Info("Initial generation task sent successfully", zap.String("draftID", config.ID.String()), zap.String("taskID", taskID))
	return config, nil
}

// ReviseDraft sends a revision generation task for an existing draft.
func (s *draftServiceImpl) ReviseDraft(ctx context.Context, id uuid.UUID, userID uuid.UUID, revisionPrompt string) error {
	log := s.logger.With(zap.String("draftID", id.String()), zap.String("userID", userID.String()))
	log.Info("ReviseDraft called, delegating to RetryDraftGeneration")
	return s.RetryDraftGeneration(ctx, id, userID)
}

// GetStoryConfig gets the story config
func (s *draftServiceImpl) GetStoryConfig(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*sharedModels.StoryConfig, error) {
	log := s.logger.With(zap.String("draftID", id.String()), zap.String("userID", userID.String()))
	log.Info("GetStoryConfig called")

	config, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		return nil, WrapRepoError(s.logger, err, "StoryConfig")
	}
	return config, nil
}

// ListUserDrafts retrieves a paginated list of StoryConfig drafts for a specific user ID.
func (s *draftServiceImpl) ListUserDrafts(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.StoryConfig, string, error) {
	log := s.logger.With(zap.String("userID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit))
	log.Info("ListUserDrafts called")

	configs, nextCursor, err := PaginateList(&limit, 20, 100, func(l int) ([]sharedModels.StoryConfig, string, error) {
		return s.repo.ListByUserID(ctx, userID, cursor, l)
	})
	if err != nil {
		if errors.Is(err, interfaces.ErrInvalidCursor) {
			log.Warn("Invalid cursor provided")
			return nil, "", err
		}
		log.Error("Error listing user drafts from repository", zap.Error(err))
		return nil, "", sharedModels.ErrInternalServer
	}

	log.Debug("User drafts listed successfully", zap.Int("count", len(configs)), zap.Bool("hasNext", nextCursor != ""))
	return configs, nextCursor, nil
}

// RetryDraftGeneration attempts to resend a generation task for a draft in Error status.
func (s *draftServiceImpl) RetryDraftGeneration(ctx context.Context, draftID uuid.UUID, userID uuid.UUID) error {
	log := s.logger.With(zap.String("draftID", draftID.String()), zap.String("userID", userID.String()))
	log.Info("Attempting to retry draft generation")

	config, err := s.repo.GetByID(ctx, draftID, userID)
	if err != nil {
		return WrapRepoError(s.logger, err, "Draft")
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
	generationLimit := s.cfg.GenerationLimitPerUser
	if activeCount >= generationLimit {
		log.Info("User reached active generation limit, retry rejected")
		return sharedModels.ErrUserHasActiveGeneration
	}

	var userInputs []string
	var lastUserInput string
	var promptType sharedModels.PromptType
	var userInputForTask string

	if config.UserInput != nil {
		if err := DecodeStrictJSON(config.UserInput, &userInputs); err == nil && len(userInputs) > 0 {
			lastUserInput = userInputs[0] // Always use the first (and presumably only) input
			promptType = sharedModels.PromptTypeNarrator
			userInputForTask = lastUserInput
			log.Info("Retry is for initial generation (PromptTypeNarrator)", zap.String("userInput", lastUserInput))
		} else {
			log.Error("Failed to unmarshal UserInput or UserInput is empty for retry", zap.Error(err))
			return sharedModels.ErrInternalServer // Use shared error
		}
	} else {
		log.Error("UserInput is nil for retry")
		return sharedModels.ErrInternalServer // Use shared error
	}

	config.Status = sharedModels.StatusGenerating
	config.UpdatedAt = time.Now().UTC()
	// <<< ДОБАВЛЯЕМ ЛОГ ПЕРЕД ОБНОВЛЕНИЕМ >>>
	log.Debug("Updating draft status to Generating before retry task publish", zap.String("language_in_config", config.Language))
	// <<< КОНЕЦ ЛОГА >>>
	if err := s.repo.Update(ctx, config); err != nil {
		log.Error("Error updating draft status before retry task publish", zap.Error(err))
		return sharedModels.ErrInternalServer // Use shared error
	}

	taskID := uuid.New().String()
	// <<< ДОБАВЛЯЕМ ЛОГ ПЕРЕД СОЗДАНИЕМ PAYLOAD >>>
	log.Debug("Preparing generation payload for retry",
		zap.String("language_from_config", config.Language),
		zap.String("prompt_type", string(promptType)),
	)
	// <<< КОНЕЦ ЛОГА >>>
	generationPayload := sharedMessaging.GenerationTaskPayload{
		TaskID:        taskID,
		UserID:        config.UserID.String(),
		PromptType:    promptType,
		UserInput:     userInputForTask,
		StoryConfigID: config.ID.String(),
		Language:      config.Language, // <<< ПЕРЕДАЕМ ЯЗЫК ОТДЕЛЬНО >>>
	}

	// <<< ДОБАВЛЯЕМ ЛОГ ПОСЛЕ СОЗДАНИЯ PAYLOAD >>>
	log.Debug("Generation payload created for retry", zap.Any("payload", generationPayload))
	// <<< КОНЕЦ ЛОГА >>>

	if err := s.publishPayload(ctx, config, generationPayload); err != nil {
		return sharedModels.ErrInternalServer
	}

	log.Info("Retry generation task published successfully", zap.String("taskID", taskID))
	return nil
}

// GetDraftDetailsInternal retrieves the details of a story draft for internal API access.
func (s *draftServiceImpl) GetDraftDetailsInternal(ctx context.Context, draftID uuid.UUID) (*sharedModels.StoryConfig, error) {
	log := s.logger.With(zap.String("draftID", draftID.String()))
	log.Info("GetDraftDetailsInternal called")

	config, err := s.repo.GetByIDInternal(ctx, draftID)
	if err != nil {
		return nil, WrapRepoError(s.logger, err, "StoryConfigInternal")
	}
	return config, nil
}

// UpdateDraftInternal updates the Config and UserInput JSON fields of a draft. (Admin only)
func (s *draftServiceImpl) UpdateDraftInternal(ctx context.Context, draftID uuid.UUID, configJSON, userInputJSON string, status sharedModels.StoryStatus) error {
	log := s.logger.With(zap.String("draftID", draftID.String()))
	log.Info("UpdateDraftInternal called", zap.String("newStatus", string(status)))

	// 1. Валидация JSON (достаточно базовой проверки в обработчике, здесь доверяем)
	var rawConfig, rawUserInput json.RawMessage
	if err := DecodeStrictJSON([]byte(configJSON), &rawConfig); err != nil {
		log.Error("Invalid config JSON in internal update", zap.Error(err))
		// Эта ошибка не должна происходить, если валидация была в handler
		return fmt.Errorf("invalid config JSON provided internally: %w", err)
	}
	if err := DecodeStrictJSON([]byte(userInputJSON), &rawUserInput); err != nil {
		log.Error("Invalid user input JSON in internal update", zap.Error(err))
		return fmt.Errorf("invalid user input JSON provided internally: %w", err)
	}

	// 2. Вызов репозитория для обновления полей
	// Используем существующий GetByID для получения userID, если нужно будет валидировать.
	// Но для внутреннего метода, возможно, это избыточно.
	err := s.repo.UpdateConfigAndInputAndStatus(ctx, draftID, rawConfig, rawUserInput, status) // <<< ОБНОВЛЕН вызов репо
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("Draft not found for internal update")
			return sharedModels.ErrNotFound
		}
		log.Error("Failed to update draft internally in repository", zap.Error(err))
		return fmt.Errorf("repository update failed: %w", err)
	}

	log.Info("Draft updated successfully internally")
	return nil
}

func (s *draftServiceImpl) DeleteDraft(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	log := s.logger.With(zap.String("draftID", id.String()), zap.String("userID", userID.String()))
	log.Info("DeleteDraft called")

	// Вызываем метод Delete репозитория
	err := s.repo.Delete(ctx, id, userID)
	if err != nil {
		// Логируем ошибку
		log.Error("Error deleting draft from repository", zap.Error(err))

		// Возвращаем стандартные ошибки, если это возможно
		if errors.Is(err, sharedModels.ErrNotFound) {
			return err
		}
		// В остальных случаях возвращаем обобщенную ошибку
		return fmt.Errorf("error deleting draft: %w", err)
	}

	log.Info("Draft deleted successfully")
	return nil
}

// publishPayload публикует задачу и откатывает статус драфта на Error в случае ошибки.
func (s *draftServiceImpl) publishPayload(ctx context.Context, config *sharedModels.StoryConfig, payload sharedMessaging.GenerationTaskPayload) error {
	if err := s.publisher.PublishGenerationTask(ctx, payload); err != nil {
		s.logger.Error("Error publishing generation task, rolling back status", zap.Error(err))
		config.Status = sharedModels.StatusError
		config.UpdatedAt = time.Now().UTC()
		if rbErr := s.repo.Update(context.Background(), config); rbErr != nil {
			s.logger.Error("CRITICAL: Failed to roll back draft status after publish error", zap.Error(rbErr))
		}
		return err
	}
	return nil
}
