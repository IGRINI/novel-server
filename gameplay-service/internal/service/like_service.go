package service

import (
	"context"
	"errors"
	"fmt"
	interfaces "novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

var (
// ... (определения ошибок)
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
	ListLikedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.PublishedStorySummary, string, error)
}

type likeServiceImpl struct {
	// likeRepo      interfaces.LikeRepository // <<< УДАЛЯЕМ >>>
	publishedRepo interfaces.PublishedStoryRepository
	gameStateRepo interfaces.PlayerGameStateRepository
	authClient    interfaces.AuthServiceClient
	logger        *zap.Logger
	pool          *pgxpool.Pool // <<< ДОБАВЛЕНО
}

// NewLikeService создает новый экземпляр LikeService.
func NewLikeService(
	// likeRepo interfaces.LikeRepository, // <<< УДАЛЯЕМ >>>
	publishedRepo interfaces.PublishedStoryRepository,
	gameStateRepo interfaces.PlayerGameStateRepository,
	authClient interfaces.AuthServiceClient,
	logger *zap.Logger,
	pool *pgxpool.Pool, // <<< ДОБАВЛЕНО
) LikeService {
	return &likeServiceImpl{
		publishedRepo: publishedRepo,
		gameStateRepo: gameStateRepo,
		authClient:    authClient,
		logger:        logger.Named("LikeService"),
		pool:          pool, // <<< ДОБАВЛЕНО
	}
}

// LikeStory добавляет лайк к опубликованной истории от пользователя.
func (s *likeServiceImpl) LikeStory(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (err error) { // <<< Добавляем именованный err
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	s.logger.Info("Attempting to like story using MarkStoryAsLiked", logFields...)

	// <<< ДОБАВЛЕНО: Начинаем транзакцию >>>
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("Failed to begin transaction for liking story", append(logFields, zap.Error(err))...)
		return sharedModels.ErrInternalServer // Возвращаем общую ошибку
	}
	// Гарантируем откат или коммит
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("Panic recovered during LikeStory, rolling back transaction", append(logFields, zap.Any("panic", r))...)
			_ = tx.Rollback(context.Background()) // Ignore rollback error after panic
			err = fmt.Errorf("panic during like: %v", r)
		} else if err != nil {
			s.logger.Warn("Rolling back transaction due to error during like", append(logFields, zap.Error(err))...)
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
				s.logger.Error("Failed to rollback transaction", append(logFields, zap.Error(rollbackErr))...)
			}
		} else {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				s.logger.Error("Failed to commit transaction after successful like", append(logFields, zap.Error(commitErr))...)
				err = fmt.Errorf("error committing transaction: %w", commitErr)
			}
		}
	}()

	// <<< ИЗМЕНЕНО: Вызываем метод репозитория с транзакцией >>>
	err = s.publishedRepo.MarkStoryAsLiked(ctx, tx, publishedStoryID, userID)
	if err != nil {
		// Проверяем стандартные ошибки, которые может вернуть MarkStoryAsLiked
		if errors.Is(err, sharedModels.ErrNotFound) {
			// Эта ошибка может возникнуть, если published_story не существует
			// Ошибка будет передана в defer для отката
			s.logger.Warn("Story not found for liking", append(logFields, zap.Error(err))...)
			return sharedModels.ErrNotFound // Use shared error directly
		}

		// Любая другая ошибка (ошибка транзакции, ошибка БД)
		// Ошибка будет передана в defer для отката
		s.logger.Error("Failed to mark story as liked in repository", append(logFields, zap.Error(err))...)
		return sharedModels.ErrInternalServer // Use shared error directly
	}

	// MarkStoryAsLiked возвращает nil, если лайк уже существовал или был успешно добавлен.
	// Поэтому отдельная проверка на ErrAlreadyLiked здесь не нужна.
	s.logger.Info("Story liked successfully (or was already liked, transaction pending commit)", logFields...)
	// defer tx.Commit() сработает, если err == nil
	return nil
}

// UnlikeStory удаляет лайк с опубликованной истории от пользователя.
func (s *likeServiceImpl) UnlikeStory(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (err error) { // <<< Добавляем именованный err
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	s.logger.Info("Attempting to unlike story using MarkStoryAsUnliked", logFields...)

	// <<< ДОБАВЛЕНО: Начинаем транзакцию >>>
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("Failed to begin transaction for unliking story", append(logFields, zap.Error(err))...)
		return sharedModels.ErrInternalServer
	}
	// Гарантируем откат или коммит
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("Panic recovered during UnlikeStory, rolling back transaction", append(logFields, zap.Any("panic", r))...)
			_ = tx.Rollback(context.Background())
			err = fmt.Errorf("panic during unlike: %v", r)
		} else if err != nil {
			s.logger.Warn("Rolling back transaction due to error during unlike", append(logFields, zap.Error(err))...)
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
				s.logger.Error("Failed to rollback transaction", append(logFields, zap.Error(rollbackErr))...)
			}
		} else {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				s.logger.Error("Failed to commit transaction after successful unlike", append(logFields, zap.Error(commitErr))...)
				err = fmt.Errorf("error committing transaction: %w", commitErr)
			}
		}
	}()

	// <<< ИЗМЕНЕНО: Вызываем метод репозитория с транзакцией >>>
	err = s.publishedRepo.MarkStoryAsUnliked(ctx, tx, publishedStoryID, userID)
	if err != nil {
		// Проверяем стандартные ошибки, которые может вернуть MarkStoryAsUnliked
		if errors.Is(err, sharedModels.ErrNotFound) {
			// Эта ошибка может возникнуть, если published_story не существует
			// Ошибка будет передана в defer для отката
			s.logger.Warn("Story not found for unliking", append(logFields, zap.Error(err))...)
			return sharedModels.ErrNotFound // Use shared error directly
		}

		// Любая другая ошибка (ошибка транзакции, ошибка БД)
		// Ошибка будет передана в defer для отката
		s.logger.Error("Failed to mark story as unliked in repository", append(logFields, zap.Error(err))...)
		return sharedModels.ErrInternalServer // Use shared error directly
	}

	// MarkStoryAsUnliked возвращает nil, если лайк не существовал или был успешно удален.
	// Поэтому отдельная проверка на ErrNotLikedYet здесь не нужна.
	s.logger.Info("Story unliked successfully (or was not liked, transaction pending commit)", logFields...)
	// defer tx.Commit() сработает, если err == nil
	return nil
}

// ListLikedStories retrieves a paginated list of stories liked by a user, with progress flag.
func (s *likeServiceImpl) ListLikedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.PublishedStorySummary, string, error) {
	log := s.logger.With(zap.String("method", "ListLikedStories"), zap.String("userID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit))
	log.Debug("Listing liked stories for user (single query)")

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	// Pass the database pool s.pool as the DBTX argument
	summaries, nextCursor, err := s.publishedRepo.ListLikedByUser(ctx, s.pool, userID, cursor, limit)
	if err != nil {
		log.Error("Failed to list liked stories from publishedRepo", zap.Error(err))
		// Возвращаем внутреннюю ошибку, так как ошибка пришла из репозитория
		return nil, "", sharedModels.ErrInternalServer
	}

	log.Debug("Successfully listed liked stories (single query)", zap.Int("count", len(summaries)), zap.Bool("hasNext", nextCursor != ""))
	return summaries, nextCursor, nil
}
