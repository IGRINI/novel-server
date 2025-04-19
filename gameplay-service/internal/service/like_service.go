package service

import (
	"context"
	"errors"
	interfaces "novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Define errors specific to like operations
var (
	ErrAlreadyLiked = errors.New("story already liked by this user")
	ErrNotLikedYet  = errors.New("story not liked by this user yet")
	// ErrStoryNotFound will be returned directly from the repo or checked explicitly if needed
)

// LikeService defines the interface for managing story likes.
type LikeService interface {
	LikeStory(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error
	UnlikeStory(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error
	ListLikedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*sharedModels.PublishedStory, string, error)
}

type likeServiceImpl struct {
	likeRepo      interfaces.LikeRepository
	publishedRepo interfaces.PublishedStoryRepository // Needed to inc/dec counts and maybe for ListLikedStories
	logger        *zap.Logger
}

// NewLikeService creates a new instance of LikeService.
func NewLikeService(
	likeRepo interfaces.LikeRepository,
	publishedRepo interfaces.PublishedStoryRepository,
	logger *zap.Logger,
) LikeService {
	return &likeServiceImpl{
		likeRepo:      likeRepo,
		publishedRepo: publishedRepo,
		logger:        logger.Named("LikeService"),
	}
}

// LikeStory добавляет лайк к опубликованной истории от пользователя.
func (s *likeServiceImpl) LikeStory(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error {
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	s.logger.Info("Attempting to like story", logFields...)

	// 1. Пытаемся добавить лайк в репозиторий
	err := s.likeRepo.AddLike(ctx, userID, publishedStoryID)
	if err != nil {
		// Проверяем специфичные ошибки репозитория лайков
		if errors.Is(err, interfaces.ErrLikeAlreadyExists) {
			s.logger.Warn("User already liked this story", logFields...)
			return ErrAlreadyLiked // Возвращаем нашу сервисную ошибку
		}
		// Проверяем, не связана ли ошибка с отсутствием истории (FK constraint)
		// Это может зависеть от конкретной ошибки БД, но можно попытаться проверить story existence отдельно
		// или положиться на общую ошибку. Для простоты, пока оставим так.
		if errors.Is(err, sharedModels.ErrNotFound) { // Если repo возвращает эту ошибку при FK violation
			s.logger.Warn("Story not found for liking (inferred from repo error)", logFields...)
			return ErrStoryNotFound // Используем ошибку из gameplay_service (или общую)
		}

		// Другая ошибка репозитория
		s.logger.Error("Failed to add like in repository", append(logFields, zap.Error(err))...)
		return ErrInternal // Возвращаем общую внутреннюю ошибку
	}

	// 2. Успешно добавили лайк, инкрементируем счетчик.
	if err := s.publishedRepo.IncrementLikesCount(ctx, publishedStoryID); err != nil {
		// Если счетчик не удалось обновить - это проблема, лайк уже стоит.
		// Логируем как ошибку, но пользователю можно вернуть успех (лайк поставлен).
		s.logger.Error("Failed to increment likes count after adding like record", append(logFields, zap.Error(err))...)
		// Можно вернуть nil, т.к. лайк фактически добавлен. Или разработать логику компенсации.
	}

	s.logger.Info("Story liked successfully", logFields...)
	return nil
}

// UnlikeStory удаляет лайк с опубликованной истории от пользователя.
func (s *likeServiceImpl) UnlikeStory(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error {
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	s.logger.Info("Attempting to unlike story", logFields...)

	// 1. Пытаемся удалить лайк из репозитория
	err := s.likeRepo.RemoveLike(ctx, userID, publishedStoryID)
	if err != nil {
		// Проверяем специфичные ошибки репозитория лайков
		if errors.Is(err, interfaces.ErrLikeNotFound) {
			s.logger.Warn("User had not liked this story", logFields...)
			return ErrNotLikedYet // Возвращаем нашу сервисную ошибку
		}
		// Опять же, проверяем на случай ошибки из-за отсутствия истории
		if errors.Is(err, sharedModels.ErrNotFound) {
			s.logger.Warn("Story not found for unliking (inferred from repo error)", logFields...)
			return ErrStoryNotFound
		}

		// Другая ошибка репозитория
		s.logger.Error("Failed to remove like in repository", append(logFields, zap.Error(err))...)
		return ErrInternal // Возвращаем общую внутреннюю ошибку
	}

	// 2. Успешно удалили лайк, декрементируем счетчик.
	if err := s.publishedRepo.DecrementLikesCount(ctx, publishedStoryID); err != nil {
		// Если счетчик не удалось обновить - это проблема.
		// Логируем как ошибку, но пользователю можно вернуть успех (лайк снят).
		s.logger.Error("Failed to decrement likes count after removing like record", append(logFields, zap.Error(err))...)
	}

	s.logger.Info("Story unliked successfully", logFields...)
	return nil
}

// ListLikedStories retrieves a list of stories liked by a user.
func (s *likeServiceImpl) ListLikedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*sharedModels.PublishedStory, string, error) {
	log := s.logger.With(zap.String("userID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit))
	log.Info("ListLikedStories called")

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	// 1. Get the IDs of liked stories from the like repository
	log.Debug("Fetching liked story IDs from like repository")
	likedStoryIDs, nextCursor, err := s.likeRepo.ListLikedStoryIDsByUserID(ctx, userID, cursor, limit+1)
	if err != nil {
		if errors.Is(err, interfaces.ErrInvalidCursor) {
			log.Warn("Invalid cursor provided for ListLikedStories")
			return nil, "", err
		}
		log.Error("Error listing liked story IDs from like repository", zap.Error(err))
		// Assuming ErrInternal exists in sharedModels or gameplay_service scope
		return nil, "", ErrInternal
	}

	// 2. Check if there's a next page based on the number of IDs fetched
	hasNextPage := len(likedStoryIDs) > limit
	if hasNextPage {
		likedStoryIDs = likedStoryIDs[:limit]
	} else {
		nextCursor = "" // No next page if we didn't fetch extra
	}

	if len(likedStoryIDs) == 0 {
		log.Info("No liked stories found for user")
		return []*sharedModels.PublishedStory{}, "", nil // Return empty list and no error
	}

	// 3. Fetch the actual PublishedStory details using the IDs
	log.Debug("Fetching published story details for liked IDs", zap.Int("id_count", len(likedStoryIDs)))
	// Assuming PublishedStoryRepository has a ListByIDs method
	likedStories, err := s.publishedRepo.ListByIDs(ctx, likedStoryIDs)
	if err != nil {
		log.Error("Error fetching published stories by IDs", zap.Error(err))
		return nil, "", ErrInternal // Use appropriate internal error
	}

	// Note: The order of stories returned by ListByIDs might not match likedStoryIDs order.
	// If specific order (e.g., by like time) is needed, ListByIDs would need to support it,
	// or additional sorting logic would be required here.

	log.Info("Successfully listed liked stories", zap.Int("count", len(likedStories)))
	return likedStories, nextCursor, nil
}
