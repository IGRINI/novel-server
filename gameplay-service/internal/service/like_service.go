package service

import (
	"context"
	"errors"
	interfaces "novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
func (s *likeServiceImpl) LikeStory(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (err error) {
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	s.logger.Info("Attempting to like story using MarkStoryAsLiked", logFields...)
	return WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		err := s.publishedRepo.MarkStoryAsLiked(ctx, tx, publishedStoryID, userID)
		if err != nil {
			if errors.Is(err, sharedModels.ErrNotFound) {
				s.logger.Warn("Story not found for liking", append(logFields, zap.Error(err))...)
				return sharedModels.ErrNotFound
			}
			s.logger.Error("Failed to mark story as liked in repository", append(logFields, zap.Error(err))...)
			return sharedModels.ErrInternalServer
		}
		s.logger.Info("Story liked successfully", logFields...)
		return nil
	})
}

// UnlikeStory удаляет лайк с опубликованной истории от пользователя.
func (s *likeServiceImpl) UnlikeStory(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (err error) {
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	s.logger.Info("Attempting to unlike story using MarkStoryAsUnliked", logFields...)
	return WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		err := s.publishedRepo.MarkStoryAsUnliked(ctx, tx, publishedStoryID, userID)
		if err != nil {
			if errors.Is(err, sharedModels.ErrNotFound) {
				s.logger.Warn("Story not found for unliking", append(logFields, zap.Error(err))...)
				return sharedModels.ErrNotFound
			}
			s.logger.Error("Failed to mark story as unliked in repository", append(logFields, zap.Error(err))...)
			return sharedModels.ErrInternalServer
		}
		s.logger.Info("Story unliked successfully", logFields...)
		return nil
	})
}

// ListLikedStories retrieves a paginated list of stories liked by a user, with progress flag.
func (s *likeServiceImpl) ListLikedStories(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]sharedModels.PublishedStorySummary, string, error) {
	log := s.logger.With(zap.String("method", "ListLikedStories"), zap.String("userID", userID.String()), zap.String("cursor", cursor), zap.Int("limit", limit))
	log.Debug("Listing liked stories for user (single query)")

	SanitizeLimit(&limit, 20, 100)

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
