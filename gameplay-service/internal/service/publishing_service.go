package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"novel-server/gameplay-service/internal/messaging"
	database "novel-server/shared/database"
	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Define errors specific to publishing operations
var (
	ErrCannotPublish              = errors.New("story cannot be published in its current status (must be Draft or Error)")
	ErrCannotPublishNoConfig      = errors.New("cannot publish without a generated config")
	ErrStoryNotReadyForPublishing = errors.New("story is not ready for publishing (must be in Ready status)")
	ErrAdultContentCannotBePublic = errors.New("adult content cannot be made public") // Currently unused but kept for potential future logic
)

// PublishingService defines the interface for publishing stories and managing visibility.
type PublishingService interface {
	PublishDraft(ctx context.Context, draftID uuid.UUID, userID uuid.UUID) (publishedStoryID uuid.UUID, err error)
	SetStoryVisibility(ctx context.Context, storyID, userID uuid.UUID, isPublic bool) error
	DeletePublishedStory(ctx context.Context, id uuid.UUID, userID uuid.UUID) error
}

type publishingServiceImpl struct {
	configRepo    interfaces.StoryConfigRepository
	publishedRepo interfaces.PublishedStoryRepository
	publisher     messaging.TaskPublisher
	pool          *pgxpool.Pool
	logger        *zap.Logger
}

// NewPublishingService creates a new instance of PublishingService.
func NewPublishingService(
	configRepo interfaces.StoryConfigRepository,
	publishedRepo interfaces.PublishedStoryRepository,
	publisher messaging.TaskPublisher,
	pool *pgxpool.Pool,
	logger *zap.Logger,
) PublishingService {
	return &publishingServiceImpl{
		configRepo:    configRepo,
		publishedRepo: publishedRepo,
		publisher:     publisher,
		pool:          pool,
		logger:        logger.Named("PublishingService"),
	}
}

// PublishDraft publishes a draft, deletes it, and creates a PublishedStory.
func (s *publishingServiceImpl) PublishDraft(ctx context.Context, draftID uuid.UUID, userID uuid.UUID) (publishedStoryID uuid.UUID, err error) {
	log := s.logger.With(zap.String("draftID", draftID.String()), zap.String("userID", userID.String()))
	log.Info("PublishDraft called")

	// Begin transaction
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Error("Failed to begin transaction", zap.Error(err))
		return uuid.Nil, fmt.Errorf("error beginning transaction: %w", err)
	}
	defer func() {
		if r := recover(); r != nil {
			log.Error("Panic recovered during PublishDraft, rolling back transaction", zap.Any("panic", r))
			_ = tx.Rollback(context.Background()) // Ignore rollback error after panic
			err = fmt.Errorf("panic during publish: %v", r)
		} else if err != nil {
			log.Warn("Rolling back transaction due to error", zap.Error(err))
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
				log.Error("Failed to rollback transaction", zap.Error(rollbackErr))
			}
		} else {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				log.Error("Failed to commit transaction", zap.Error(commitErr))
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
			log.Warn("Draft not found for publishing")
			return uuid.Nil, sharedModels.ErrNotFound // Use standard error
		}
		log.Error("Error getting draft within transaction", zap.Error(err))
		return uuid.Nil, fmt.Errorf("error getting draft: %w", err)
	}

	// 2. Check status and Config presence
	if draft.Status != sharedModels.StatusDraft && draft.Status != sharedModels.StatusError {
		log.Warn("Attempt to publish draft in invalid status", zap.String("status", string(draft.Status)))
		return uuid.Nil, ErrCannotPublish // Use local error
	}
	if draft.Config == nil || len(draft.Config) == 0 {
		log.Warn("Attempt to publish draft without generated config")
		return uuid.Nil, ErrCannotPublishNoConfig // Use local error
	}

	// 3. Extract necessary fields from draft.Config
	var tempConfig struct {
		IsAdultContent bool `json:"ac"`
	}
	if err = json.Unmarshal(draft.Config, &tempConfig); err != nil {
		log.Error("Failed to unmarshal draft config to extract flags", zap.Error(err))
		return uuid.Nil, fmt.Errorf("error reading draft configuration: %w", err)
	}

	// 4. Create PublishedStory within the transaction
	newPublishedStory := &sharedModels.PublishedStory{
		ID:             uuid.New(),
		UserID:         userID,
		Config:         draft.Config,
		Setup:          nil, // Will be generated later
		Status:         sharedModels.StatusSetupPending,
		Language:       draft.Language, // <<< КОПИРУЕМ ЯЗЫК ИЗ DRAFT >>>
		IsPublic:       false,          // Private by default
		IsAdultContent: tempConfig.IsAdultContent,
		Title:          &draft.Title,
		Description:    &draft.Description,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}

	if err = publishedRepoTx.Create(ctx, newPublishedStory); err != nil {
		log.Error("Error creating published story within transaction", zap.Error(err))
		return uuid.Nil, fmt.Errorf("error creating published story: %w", err)
	}
	log.Info("Published story created in DB", zap.String("publishedStoryID", newPublishedStory.ID.String()))

	// 5. Delete the draft within the transaction
	if err = repoTx.Delete(ctx, draftID, userID); err != nil {
		log.Error("Error deleting draft within transaction", zap.Error(err))
		return uuid.Nil, fmt.Errorf("error deleting draft: %w", err)
	}
	log.Info("Draft deleted from DB")

	// 6. Send task for Setup generation (outside the transaction, after commit)
	taskID := uuid.New().String()
	configJSONString := string(newPublishedStory.Config) // Конфиг истории как строка
	setupPayload := sharedMessaging.GenerationTaskPayload{
		TaskID:           taskID,
		UserID:           newPublishedStory.UserID.String(),
		PromptType:       sharedModels.PromptTypeNovelSetup,
		UserInput:        configJSONString,              // <-- Передаем JSON конфиг сюда
		PublishedStoryID: newPublishedStory.ID.String(), // Link to published story
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
	log.Info("PublishDraft completed successfully", zap.String("publishedStoryID", publishedStoryID.String()))
	return publishedStoryID, nil
}

// SetStoryVisibility устанавливает видимость истории для пользователя.
func (s *publishingServiceImpl) SetStoryVisibility(ctx context.Context, storyID, userID uuid.UUID, isPublic bool) error {
	logFields := []zap.Field{
		zap.String("publishedStoryID", storyID.String()),
		zap.String("userID", userID.String()),
		zap.Bool("isPublic", isPublic),
	}
	s.logger.Info("Attempting to set story visibility", logFields...)

	// 4. Вызываем метод репозитория для обновления флага, передавая требуемый статус
	err := s.publishedRepo.UpdateVisibility(ctx, storyID, userID, isPublic, sharedModels.StatusReady)
	if err != nil {
		// Обрабатываем ошибки, возвращенные репозиторием
		if errors.Is(err, sharedModels.ErrNotFound) || errors.Is(err, sharedModels.ErrForbidden) || errors.Is(err, sharedModels.ErrStoryNotReadyForPublishing) {
			s.logger.Warn("Visibility update failed due to precondition", append(logFields, zap.Error(err))...)
			return err // Возвращаем конкретную ошибку (NotFound, Forbidden, NotReady)
		}
		// Другая ошибка репозитория или БД
		s.logger.Error("Failed to update visibility in repository", append(logFields, zap.Error(err))...)
		return sharedModels.ErrInternalServer // Возвращаем общую внутреннюю ошибку
	}

	s.logger.Info("Story visibility updated successfully", logFields...)
	return nil
}

// DeletePublishedStory удаляет историю для пользователя.
func (s *publishingServiceImpl) DeletePublishedStory(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	log := s.logger.With(zap.String("publishedStoryID", id.String()), zap.String("userID", userID.String()))
	log.Info("DeletePublishedStory called")

	// Начинаем транзакцию
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Error("Failed to begin transaction for deleting published story", zap.Error(err))
		return fmt.Errorf("error starting transaction: %w", err)
	}
	// Гарантируем откат или коммит
	defer func() {
		if r := recover(); r != nil {
			log.Error("Panic recovered during DeletePublishedStory, rolling back transaction", zap.Any("panic", r))
			_ = tx.Rollback(context.Background()) // Ignore rollback error after panic
			err = fmt.Errorf("panic during deletion: %v", r)
		} else if err != nil {
			log.Warn("Rolling back transaction due to error during delete", zap.Error(err))
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
				log.Error("Failed to rollback transaction", zap.Error(rollbackErr))
			}
		} else {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				log.Error("Failed to commit transaction after successful delete", zap.Error(commitErr))
				err = fmt.Errorf("error committing transaction: %w", commitErr)
			}
		}
	}()

	// Создаем репозиторий с транзакцией
	publishedRepoTx := database.NewPgPublishedStoryRepository(tx, s.logger)

	// Вызываем метод Delete репозитория с транзакцией
	err = publishedRepoTx.Delete(ctx, tx, id, userID) // <<< ИЗМЕНЕНО: Передаем tx
	if err != nil {
		// Не логируем здесь снова, т.к. defer обработает rollback
		// Возвращаем стандартные ошибки, если это возможно
		if errors.Is(err, sharedModels.ErrNotFound) || errors.Is(err, sharedModels.ErrForbidden) {
			return err // Ошибка будет обработана defer для отката
		}
		// В остальных случаях возвращаем обобщенную ошибку
		return fmt.Errorf("error deleting published story: %w", err) // Ошибка будет обработана defer для отката
	}

	log.Info("Published story deleted successfully (transaction pending commit)")
	// defer tx.Commit() сработает, если err == nil
	return nil
}
