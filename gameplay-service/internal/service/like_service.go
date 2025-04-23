package service

import (
	"context"
	"errors"
	"fmt"
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

// LikedStoryDetailDTO structure for returning liked story details
type LikedStoryDetailDTO struct {
	sharedModels.PublishedStory        // Embed base story details
	HasPlayerProgress           bool   `json:"-"`           // Internal flag, not marshalled directly
	AuthorName                  string `json:"author_name"` // <<< ДОБАВЛЕНО: Поле для имени автора
}

// LikeService defines the interface for managing story likes.
type LikeService interface {
	LikeStory(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error
	UnlikeStory(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error
	ListLikedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*LikedStoryDetailDTO, string, error)
}

type likeServiceImpl struct {
	likeRepo      interfaces.LikeRepository
	publishedRepo interfaces.PublishedStoryRepository
	gameStateRepo interfaces.PlayerGameStateRepository // <<< ДОБАВЛЕНО
	// <<< ДОБАВЛЕНО: Клиент для взаимодействия с auth-service >>>
	authClient interfaces.AuthServiceClient // Используем интерфейс
	logger     *zap.Logger
}

// NewLikeService creates a new instance of LikeService.
func NewLikeService(
	likeRepo interfaces.LikeRepository,
	publishedRepo interfaces.PublishedStoryRepository,
	gameStateRepo interfaces.PlayerGameStateRepository, // <<< ДОБАВЛЕНО
	authClient interfaces.AuthServiceClient, // <<< ДОБАВЛЕНО: Инъекция клиента
	logger *zap.Logger,
) LikeService {
	return &likeServiceImpl{
		likeRepo:      likeRepo,
		publishedRepo: publishedRepo,
		gameStateRepo: gameStateRepo, // <<< ДОБАВЛЕНО
		authClient:    authClient,    // <<< ДОБАВЛЕНО
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

// ListLikedStories retrieves a paginated list of stories liked by a user, with progress flag.
func (s *likeServiceImpl) ListLikedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]*LikedStoryDetailDTO, string, error) {
	log := s.logger.With(zap.String("method", "ListLikedStories"), zap.String("userID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit))
	log.Debug("Listing liked stories for user")

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	// 1. Get liked story IDs and next cursor from LikeRepository
	likedStoryIDs, nextCursor, err := s.likeRepo.ListLikedStoryIDsByUserID(ctx, userID, cursor, limit)
	if err != nil {
		log.Error("Failed to list liked story IDs", zap.Error(err))
		return nil, "", fmt.Errorf("failed to get liked stories: %w", err)
	}
	if len(likedStoryIDs) == 0 {
		log.Debug("No liked stories found for user")
		return []*LikedStoryDetailDTO{}, "", nil // Return empty list
	}

	// 2. Fetch details for the liked stories using the correct repository method
	stories, err := s.publishedRepo.ListByIDs(ctx, likedStoryIDs) // <-- Исправлено имя метода
	if err != nil {
		log.Error("Failed to fetch published story details for liked stories", zap.Error(err))
		return nil, "", fmt.Errorf("failed to fetch story details: %w", err)
	}

	// --- Начало получения данных об авторах ---
	// Получение DisplayName авторов из auth-service уже реализовано ниже
	// 1. Собрать уникальные UserID авторов из 'stories'
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

	// 2. Сделать запрос к auth-service (например, через gRPC клиент или HTTP)
	//    Предположим, есть метод GetUserDetails(ctx, userIDs) -> map[uuid.UUID]UserInfo, где UserInfo содержит DisplayName
	authorNames := make(map[uuid.UUID]string) // Карта для хранения имен авторов
	if len(uniqueAuthorIDs) > 0 {
		// Вызываем метод клиента auth-service
		authorInfos, err := s.authClient.GetUsersInfo(ctx, uniqueAuthorIDs)
		if err != nil {
			log.Warn("Failed to fetch author details from auth-service, names will be empty", zap.Error(err))
			// Не прерываем выполнение, просто имена будут пустые
			// Можно добавить метрику или более серьезное оповещение
		} else {
			// Заполняем карту authorNames
			for userID, info := range authorInfos {
				authorNames[userID] = info.DisplayName
			}
		}
		// log.Warn("Author name fetching not implemented yet. Names will be empty.") // Убираем временное предупреждение
	}
	// --- Конец получения данных об авторах ---

	// 3. Check progress existence for all liked stories in one go
	progressExistsMap := make(map[uuid.UUID]bool)
	var errProgress error
	if len(likedStoryIDs) > 0 { // Only check if there are stories
		progressExistsMap, errProgress = s.gameStateRepo.CheckGameStateExistsForStories(ctx, userID, likedStoryIDs)
		if errProgress != nil {
			log.Error("Failed to check player progress for liked stories (batch)", zap.Error(errProgress))
			// Decide how to handle: return error or proceed with progress as false?
			// Proceeding with false might be acceptable for this read-only list.
			progressExistsMap = make(map[uuid.UUID]bool) // Reset to empty map (all false) on error
		}
	}

	// 4. Combine data into DTOs, maintaining the original order from likedStoryIDs
	// Build a map for quick lookup of story details
	storyMap := make(map[uuid.UUID]*sharedModels.PublishedStory)
	for _, story := range stories {
		if story != nil {
			storyMap[story.ID] = story
		}
	}

	results := make([]*LikedStoryDetailDTO, 0, len(likedStoryIDs)) // Use original ID list for order
	for _, storyID := range likedStoryIDs {
		if story, ok := storyMap[storyID]; ok {
			// Get progress status from the pre-fetched map
			hasProgress := progressExistsMap[storyID] // Defaults to false if not found

			results = append(results, &LikedStoryDetailDTO{
				PublishedStory:    *story,
				HasPlayerProgress: hasProgress,
				AuthorName:        authorNames[story.UserID], // Используем полученное имя (или пустое, если не получено)
			})
		} else {
			log.Warn("Liked story details not found after fetch", zap.String("storyID", storyID.String()))
		}
	}

	log.Debug("Successfully listed liked stories", zap.Int("count", len(results)), zap.Bool("hasNext", nextCursor != ""))
	return results, nextCursor, nil
}
