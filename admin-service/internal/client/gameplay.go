package client

import (
	"context"
	"novel-server/shared/models"

	"github.com/google/uuid"
)

// GameplayServiceClient определяет интерфейс для взаимодействия с внутренним API gameplay-service.
type GameplayServiceClient interface {
	// ListUserDrafts получает список черновиков пользователя.
	ListUserDrafts(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]models.StoryConfig, string, error)
	// ListUserPublishedStories получает список опубликованных историй пользователя.
	ListUserPublishedStories(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*models.PublishedStory, bool, error)

	// SetInterServiceToken устанавливает межсервисный JWT токен для клиента.
	SetInterServiceToken(token string)

	// <<< ДОБАВЛЕНО: Методы для получения деталей и сцен >>>
	GetDraftDetails(ctx context.Context, userID, draftID uuid.UUID) (*models.StoryConfig, error)
	GetPublishedStoryDetails(ctx context.Context, userID, storyID uuid.UUID) (*models.PublishedStory, error)
	ListStoryScenes(ctx context.Context, userID, storyID uuid.UUID) ([]models.StoryScene, error)
}
