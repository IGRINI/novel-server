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
	"novel-server/shared/utils"

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

	// Выполняем DB-операции в транзакции
	err = WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		// 1. Создать репозитории в контексте транзакции
		repoTx := database.NewPgStoryConfigRepository(tx, s.logger)
		publishedRepoTx := database.NewPgPublishedStoryRepository(tx, s.logger)

		// 2. Получить драфт
		draft, err := repoTx.GetByID(ctx, draftID, userID)
		if wrapErr := WrapRepoError(s.logger, err, "StoryConfig"); wrapErr != nil {
			if errors.Is(wrapErr, sharedModels.ErrNotFound) {
				return sharedModels.ErrNotFound
			}
			return wrapErr
		}

		// 3. Проверить статус и наличие config
		if draft.Status != sharedModels.StatusDraft && draft.Status != sharedModels.StatusError {
			log.Warn("Attempt to publish draft in invalid status", zap.String("status", string(draft.Status)))
			return ErrCannotPublish
		}
		if len(draft.Config) == 0 {
			log.Warn("Attempt to publish draft without generated config")
			return ErrCannotPublishNoConfig
		}

		// 4. Создать PublishedStory
		newStory := &sharedModels.PublishedStory{
			ID:             uuid.New(),
			UserID:         userID,
			Config:         draft.Config,
			Setup:          json.RawMessage("{}"),                // Initialize with empty JSON object
			Status:         sharedModels.StatusModerationPending, // <<< НАЧАЛЬНЫЙ СТАТУС
			Language:       draft.Language,                       // <<< КОПИРУЕМ ЯЗЫК ИЗ DRAFT >>>
			IsPublic:       false,                                // Private by default
			IsAdultContent: false,                                // Will be set after moderation
			Title:          &draft.Title,
			Description:    &draft.Description,
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		}
		if cErr := publishedRepoTx.Create(ctx, tx, newStory); cErr != nil {
			log.Error("Error creating published story within transaction", zap.Error(cErr))
			return fmt.Errorf("error creating published story: %w", cErr)
		}
		publishedStoryID = newStory.ID

		// 5. Удалить драфт
		if delErr := repoTx.Delete(ctx, draftID, userID); delErr != nil {
			log.Error("Error deleting draft within transaction", zap.Error(delErr))
			return fmt.Errorf("error deleting draft: %w", delErr)
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}

	// После успешной транзакции — создание задачи модерации
	go func() {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel2()
		pubStory, pErr := s.publishedRepo.GetByID(ctx2, s.pool, publishedStoryID)
		if pErr != nil {
			s.logger.Error("Failed to get published story for moderation task", zap.Error(pErr))
			return
		}
		// Подготовка конфигурации для модерации
		var cfg sharedModels.Config
		if err := DecodeStrictJSON(pubStory.Config, &cfg); err != nil {
			s.logger.Error("Failed to decode story Config for moderation", zap.Error(err))
			errMsg := err.Error()
			s.publishedRepo.UpdateStatusAndError(ctx2, s.pool, publishedStoryID, sharedModels.StatusError, &errMsg)
			return
		}
		userInput, fmtErr := utils.FormatInputForModeration(cfg)
		if fmtErr != nil {
			s.logger.Error("Failed to format input for moderation", zap.Error(fmtErr))
			errMsg := fmtErr.Error()
			s.publishedRepo.UpdateStatusAndError(ctx2, s.pool, publishedStoryID, sharedModels.StatusError, &errMsg)
			return
		}
		payload := sharedMessaging.GenerationTaskPayload{
			TaskID:           uuid.New().String(),
			UserID:           pubStory.UserID.String(),
			PublishedStoryID: pubStory.ID.String(),
			PromptType:       sharedModels.PromptTypeContentModeration,
			UserInput:        userInput,
			Language:         pubStory.Language,
		}
		if pubErr := s.publisher.PublishGenerationTask(ctx2, payload); pubErr != nil {
			s.logger.Error("Failed to publish moderation task", zap.Error(pubErr))
		} else {
			s.logger.Info("Moderation task published successfully", zap.String("taskID", payload.TaskID))
		}
	}()

	log.Info("PublishDraft completed successfully, transaction committed")
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

	// Pass the pool connection 's.pool' as the DBTX argument
	err := s.publishedRepo.UpdateVisibility(ctx, s.pool, storyID, userID, isPublic, sharedModels.StatusReady)
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

	err := WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		publishedRepoTx := database.NewPgPublishedStoryRepository(tx, s.logger)
		if delErr := publishedRepoTx.Delete(ctx, tx, id, userID); delErr != nil {
			if errors.Is(delErr, sharedModels.ErrNotFound) || errors.Is(delErr, sharedModels.ErrForbidden) {
				return delErr
			}
			return fmt.Errorf("error deleting published story: %w", delErr)
		}
		log.Info("Published story deleted successfully")
		return nil
	})
	return err
}
