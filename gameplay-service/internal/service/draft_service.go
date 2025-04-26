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

// Define language prefix map
var languagePrefixes = map[string]string{
	"en": "Player's app language: English. ",
	"fr": "Langue de l'application du joueur : Français. ",
	"de": "Sprache der Spieler-App: Deutsch. ",
	"es": "Idioma de la aplicación del jugador: Español. ",
	"it": "Lingua dell'app del giocatore: Italiano. ",
	"pt": "Idioma do aplicativo do jogador: Português. ",
	"ru": "Язык приложения игрока: Русский. ",
	"zh": "玩家应用语言：中文。",       // Пробел после не нужен для китайского/японского
	"ja": "プレイヤーのアプリ言語：日本語。", // Пробел после не нужен для китайского/японского
}

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

	// Добавляем префикс языка к initialPrompt
	userInputForTask := initialPrompt
	if prefix, ok := languagePrefixes[language]; ok {
		userInputForTask = prefix + initialPrompt
		log.Debug("Added language prefix to prompt", zap.String("prefix", prefix))
	} else {
		log.Warn("Language code not found in prefixes, using original prompt", zap.String("language", language))
	}

	generationPayload := sharedMessaging.GenerationTaskPayload{
		TaskID:        taskID,
		UserID:        config.UserID.String(),
		PromptType:    sharedMessaging.PromptTypeNarrator,
		UserInput:     userInputForTask, // Используем модифицированный prompt
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
	generationLimit := s.cfg.GenerationLimitPerUser
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

	// <<< Новая логика формирования UserInput для ревизии >>>
	var userInputForTask string
	if config.Config == nil || len(config.Config) == 0 {
		// Если нет предыдущего конфига, ревизия невозможна по новой логике промпта
		log.Error("Cannot revise draft: previous config is missing or empty", zap.String("draftID", config.ID.String()))
		// Возвращаем ошибку, т.к. AI ожидает JSON с 'ur' для ревизии
		// Можно откатить статус обратно, но пока просто возвращаем ошибку
		return fmt.Errorf("cannot revise draft %s: previous config is missing", config.ID.String())
	}

	// Распарсиваем текущий конфиг
	var currentConfigMap map[string]interface{}
	if errUnmarshal := json.Unmarshal(config.Config, &currentConfigMap); errUnmarshal != nil {
		log.Error("Cannot revise draft: failed to unmarshal existing config JSON",
			zap.String("draftID", config.ID.String()),
			zap.Error(errUnmarshal))
		return fmt.Errorf("cannot revise draft %s: invalid existing config JSON: %w", config.ID.String(), errUnmarshal)
	}

	// Добавляем поле ur с текстом ревизии
	currentConfigMap["ur"] = revisionPrompt

	// Запарсиваем обратно в JSON для UserInput
	userInputBytes, errMarshal := json.Marshal(currentConfigMap)
	if errMarshal != nil {
		log.Error("Cannot revise draft: failed to marshal updated config JSON for UserInput",
			zap.String("draftID", config.ID.String()),
			zap.Error(errMarshal))
		return fmt.Errorf("cannot revise draft %s: failed to prepare input for AI: %w", config.ID.String(), errMarshal)
	}
	userInputForTask = string(userInputBytes)
	// <<< Конец новой логики >>>

	generationPayload := sharedMessaging.GenerationTaskPayload{
		TaskID:        taskID,
		UserID:        config.UserID.String(),
		PromptType:    sharedMessaging.PromptTypeNarrator, // Тип остается Narrator, AI сам разберется по UserInput
		UserInput:     userInputForTask,
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

	// Временная структура для парсинга только title из config JSON
	type configTitle struct {
		Title string `json:"title"`
	}

	for i := range configs {
		// Проверяем, есть ли config и не пуст ли он
		if configs[i].Config != nil && len(configs[i].Config) > 0 && string(configs[i].Config) != "null" {
			var ct configTitle
			if err := json.Unmarshal(configs[i].Config, &ct); err == nil {
				// Если парсинг успешен и title не пустой, обновляем поле Title в основной структуре
				if ct.Title != "" {
					configs[i].Title = ct.Title
				}
			} else {
				// Логируем ошибку парсинга, но не прерываем процесс
				log.Warn("Failed to unmarshal config JSON to extract title",
					zap.String("draftID", configs[i].ID.String()),
					zap.Error(err))
			}
		} else {
			// Если config пустой или null, Title остается пустым (или тем, что было в БД, если оно там есть)
		}
	}
	// <<< КОНЕЦ ИЗМЕНЕНИЙ >>>

	log.Debug("User drafts listed successfully", zap.Int("count", len(configs)), zap.Bool("hasNext", nextCursor != ""))
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
	generationLimit := s.cfg.GenerationLimitPerUser
	if activeCount >= generationLimit {
		log.Info("User reached active generation limit, retry rejected")
		return sharedModels.ErrUserHasActiveGeneration
	}

	var userInputs []string
	var lastUserInput string
	var promptType sharedMessaging.PromptType
	var userInputForTask string

	if config.UserInput != nil {
		if err := json.Unmarshal(config.UserInput, &userInputs); err == nil && len(userInputs) > 0 {
			lastUserInput = userInputs[len(userInputs)-1]
			if len(userInputs) == 1 {
				promptType = sharedMessaging.PromptTypeNarrator
				userInputForTask = lastUserInput
				log.Info("Retry is for initial generation")
			} else {
				promptType = sharedMessaging.PromptTypeNarrator
				log.Info("Retry is for a revision generation", zap.String("revisionPrompt", lastUserInput))

				if len(config.Config) == 0 {
					log.Error("Cannot retry revision: previous config is missing or empty", zap.String("draftID", config.ID.String()))
					return fmt.Errorf("cannot retry revision %s: previous config is missing", config.ID.String())
				}

				var currentConfigMap map[string]interface{}
				if errUnmarshal := json.Unmarshal(config.Config, &currentConfigMap); errUnmarshal != nil {
					log.Error("Cannot retry revision: failed to unmarshal existing config JSON", zap.Error(errUnmarshal))
					return fmt.Errorf("cannot retry revision %s: invalid existing config JSON: %w", config.ID.String(), errUnmarshal)
				}

				currentConfigMap["ur"] = lastUserInput

				userInputBytes, errMarshal := json.Marshal(currentConfigMap)
				if errMarshal != nil {
					log.Error("Cannot retry revision: failed to marshal updated config JSON for UserInput", zap.Error(errMarshal))
					return fmt.Errorf("cannot retry revision %s: failed to prepare input for AI: %w", config.ID.String(), errMarshal)
				}
				userInputForTask = string(userInputBytes)
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
		UserInput:     userInputForTask,
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

	config, err := s.repo.GetByIDInternal(ctx, draftID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("StoryConfig not found by internal call")
			return nil, sharedModels.ErrNotFound // Use standard error
		}
		log.Error("Error getting StoryConfig via internal call", zap.Error(err))
		return nil, fmt.Errorf("error getting StoryConfig internally: %w", err)
	}
	return config, nil
}

// UpdateDraftInternal updates the Config and UserInput JSON fields of a draft. (Admin only)
func (s *draftServiceImpl) UpdateDraftInternal(ctx context.Context, draftID uuid.UUID, configJSON, userInputJSON string, status sharedModels.StoryStatus) error {
	log := s.logger.With(zap.String("draftID", draftID.String()))
	log.Info("UpdateDraftInternal called", zap.String("newStatus", string(status)))

	// 1. Валидация JSON (достаточно базовой проверки в обработчике, здесь доверяем)
	var rawConfig, rawUserInput json.RawMessage
	if err := json.Unmarshal([]byte(configJSON), &rawConfig); err != nil {
		log.Error("Invalid config JSON in internal update", zap.Error(err))
		// Эта ошибка не должна происходить, если валидация была в handler
		return fmt.Errorf("invalid config JSON provided internally: %w", err)
	}
	if err := json.Unmarshal([]byte(userInputJSON), &rawUserInput); err != nil {
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
