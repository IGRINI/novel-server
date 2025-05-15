package interfaces

import (
	"context"
	"encoding/json"
	"novel-server/shared/models"
	"time"

	"github.com/google/uuid"
)

// PublishedStoryRepository defines the interface for interacting with published story data.
//
//go:generate mockery --name PublishedStoryRepository --output ./mocks --outpkg mocks --case=underscore
type PublishedStoryRepository interface {
	// Create inserts a new published story record. Used internally during publishing.
	Create(ctx context.Context, querier DBTX, story *models.PublishedStory) error

	// GetByID retrieves a published story by its unique ID.
	GetByID(ctx context.Context, querier DBTX, id uuid.UUID) (*models.PublishedStory, error)

	// GetWithLikeStatus retrieves a published story by its unique ID and checks if the specified user has liked it.
	// If userID is uuid.Nil, isLiked will always be false.
	GetWithLikeStatus(ctx context.Context, querier DBTX, storyID, userID uuid.UUID) (story *models.PublishedStory, isLiked bool, err error)

	// UpdateStatusDetails updates the status, setup, error details, and potentially ending text of a published story.
	// Use this method for various state transitions after generation tasks.
	// Set setup, errorDetails, or endingText to nil if they shouldn't be updated.
	UpdateStatusDetails(ctx context.Context, querier DBTX, id uuid.UUID, status models.StoryStatus, setup json.RawMessage, title, description, errorDetails *string) error

	// SetPublic updates the is_public flag for a story.
	// Requires userID for ownership check.
	// SetPublic(ctx context.Context, id uuid.UUID, userID uuid.UUID, isPublic bool) error

	// ListPublic retrieves a paginated list of public, non-adult stories using cursor pagination.
	// ListPublic(ctx context.Context, cursor string, limit int) ([]*models.PublishedStory, string, error)

	// ListByUserID retrieves a paginated list of stories created by a specific user using cursor pagination.
	ListByUserID(ctx context.Context, querier DBTX, userID uuid.UUID, cursor string, limit int) ([]*models.PublishedStory, string, error)

	// IncrementLikesCount атомарно увеличивает счетчик лайков для истории.
	// IncrementLikesCount(ctx context.Context, id uuid.UUID) error

	// DecrementLikesCount атомарно уменьшает счетчик лайков для истории.
	// Реализация должна убедиться, что счетчик не уходит ниже нуля.
	// DecrementLikesCount(ctx context.Context, id uuid.UUID) error

	// UpdateVisibility updates the visibility of a story.
	// It ensures the operation is performed by the owner and potentially checks status.
	UpdateVisibility(ctx context.Context, querier DBTX, storyID, userID uuid.UUID, isPublic bool, requiredStatus models.StoryStatus) error

	// ListByIDs retrieves a list of published stories based on their IDs.
	// ListByIDs(ctx context.Context, ids []uuid.UUID) ([]*models.PublishedStory, error)

	// UpdateConfigAndSetup updates the config and setup of a story.
	// UpdateConfigAndSetup(ctx context.Context, id uuid.UUID, config, setup []byte) error

	// UpdateConfigAndSetupAndStatus updates config, setup and status for a published story.
	UpdateConfigAndSetupAndStatus(ctx context.Context, querier DBTX, id uuid.UUID, config, setup json.RawMessage, status models.StoryStatus) error

	// CountActiveGenerationsForUser counts the number of published stories with statuses indicating active generation for a specific user.
	CountActiveGenerationsForUser(ctx context.Context, querier DBTX, userID uuid.UUID) (int, error)

	// MarkStoryAsLiked marks a story as liked by a user.
	MarkStoryAsLiked(ctx context.Context, querier DBTX, storyID uuid.UUID, userID uuid.UUID) error

	// MarkStoryAsUnliked marks a story as unliked by a user.
	MarkStoryAsUnliked(ctx context.Context, querier DBTX, storyID uuid.UUID, userID uuid.UUID) error

	// IsStoryLikedByUser checks if a story is liked by a user.
	// IsStoryLikedByUser(ctx context.Context, storyID uuid.UUID, userID uuid.UUID) (bool, error)

	// ListLikedByUser retrieves a paginated list of stories liked by a specific user using cursor pagination.
	ListLikedByUser(ctx context.Context, querier DBTX, userID uuid.UUID, cursor string, limit int) ([]models.PublishedStorySummary, string, error)

	// Delete удаляет опубликованную историю и все связанные с ней данные (сцены, прогресс, лайки).
	Delete(ctx context.Context, querier DBTX, id uuid.UUID, userID uuid.UUID) error

	// CheckLike checks if a story is liked by a user.
	CheckLike(ctx context.Context, querier DBTX, userID, storyID uuid.UUID) (bool, error)

	// FindWithProgressByUserID retrieves a paginated list of stories with progress for a specific user using cursor pagination.
	FindWithProgressByUserID(ctx context.Context, querier DBTX, userID uuid.UUID, limit int, cursor string) ([]models.PublishedStorySummary, string, error)

	// CountByStatus подсчитывает количество историй по статусу.
	CountByStatus(ctx context.Context, querier DBTX, status models.StoryStatus) (int, error)

	// ListByUserIDOffset retrieves a paginated list of stories created by a specific user using cursor pagination with offset.
	// ListByUserIDOffset(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*models.PublishedStory, error)

	// ListUserSummariesWithProgress retrieves a paginated list of stories created by a specific user,
	// including a flag indicating if the current user has progress in that story.
	ListUserSummariesWithProgress(ctx context.Context, querier DBTX, userID uuid.UUID, cursor string, limit int, filterAdult bool) ([]models.PublishedStorySummary, string, error)

	// ListUserSummariesOnlyWithProgress retrieves a paginated list of stories where the user has progress,
	// sorted by last activity time.
	ListUserSummariesOnlyWithProgress(ctx context.Context, querier DBTX, userID uuid.UUID, cursor string, limit int, filterAdult bool) ([]models.PublishedStorySummary, string, error)

	// ListPublicSummaries retrieves a paginated list of public stories.
	// Requires the user ID to determine like/progress status for that user.
	// If userID is nil, like/progress status will not be checked.
	ListPublicSummaries(ctx context.Context, querier DBTX, userID *uuid.UUID, cursor string, limit int, sortBy string) ([]models.PublishedStorySummary, string, error)

	// SearchPublic performs a full-text search on public stories.
	// Requires the user ID to determine like/progress status for that user.

	// CheckInitialGenerationStatus проверяет, готовы ли Setup и Первая сцена.
	CheckInitialGenerationStatus(ctx context.Context, querier DBTX, id uuid.UUID) (bool, error)

	// GetConfigAndSetup получает Config и Setup по ID истории.
	GetConfigAndSetup(ctx context.Context, querier DBTX, id uuid.UUID) (json.RawMessage, json.RawMessage, error)

	// FindAndMarkStaleGeneratingAsError находит опубликованные истории, которые 'зависли' в статусе генерации,
	// и обновляет их статус на StatusError.
	// staleThreshold: длительность, после которой история считается зависшей (например, 1 час).
	// Возвращает количество обновленных записей и ошибку.
	FindAndMarkStaleGeneratingAsError(ctx context.Context, querier DBTX, staleThreshold time.Duration) (int64, error)

	// UpdateStatusFlagsAndSetup обновляет статус, Setup и флаги ожидания для истории.
	// Используется после успешной генерации Setup.
	// Добавлен параметр internalStep для обновления внутреннего шага генерации.
	UpdateStatusFlagsAndSetup(ctx context.Context, querier DBTX, id uuid.UUID, status models.StoryStatus, setup json.RawMessage, isFirstScenePending bool, areImagesPending bool, internalStep *models.InternalGenerationStep) error

	// UpdateStatusFlagsAndDetails обновляет статус, флаги ожидания, счётчики задач и детали ошибки.
	// Используется при установке статуса Error или потенциально других переходах.
	UpdateStatusFlagsAndDetails(ctx context.Context, querier DBTX, id uuid.UUID, status models.StoryStatus, isFirstScenePending bool, areImagesPending bool, pendingCharGenTasks int, pendingCardImgTasks int, pendingCharImgTasks int, errorDetails *string, internalStep *models.InternalGenerationStep) error

	// GetSummaryWithDetails получает детали истории, имя автора, флаг лайка и прогресса для указанного пользователя.
	GetSummaryWithDetails(ctx context.Context, querier DBTX, storyID, userID uuid.UUID) (*models.PublishedStorySummary, error)

	// UpdateAfterModeration обновляет историю после завершения задачи модерации.
	// Устанавливает IsAdultContent, новый статус, опционально детали ошибки и внутренний шаг генерации.
	UpdateAfterModeration(ctx context.Context, querier DBTX, id uuid.UUID, status models.StoryStatus, isAdultContent bool, errorDetails *string, internalStep *models.InternalGenerationStep) error

	// UpdateStatusAndError обновляет статус и детали ошибки для опубликованной истории.
	UpdateStatusAndError(ctx context.Context, querier DBTX, id uuid.UUID, status models.StoryStatus, errorDetails *string) error

	// UpdateSetup обновляет поле setup для опубликованной истории.
	UpdateSetup(ctx context.Context, querier DBTX, id uuid.UUID, setup json.RawMessage) error

	// UpdateSetupStatusAndCounters обновляет setup, статус и счетчики ожидающих задач для опубликованной истории.
	// Добавлен параметр internalStep для обновления внутреннего шага генерации.
	UpdateSetupStatusAndCounters(ctx context.Context, querier DBTX, id uuid.UUID, setup json.RawMessage, status models.StoryStatus, pendingCharGen, pendingCardImg, pendingCharImg int, internalStep *models.InternalGenerationStep) error

	// UpdateCountersAndMaybeStatus атомарно обновляет счетчики задач и, если все счетчики <= 0,
	// обновляет статус истории. Возвращает true, если все задачи завершены, и финальный статус.
	UpdateCountersAndMaybeStatus(ctx context.Context, querier DBTX, id uuid.UUID, decrementCharGen int, incrementCharImg int, decrementCardImg int, decrementCharImg int, newStatusIfComplete models.StoryStatus) (allTasksComplete bool, finalStatus models.StoryStatus, err error)
}
