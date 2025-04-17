package client

import (
	"context"
	"novel-server/shared/models"
)

// GameplayServiceClient определяет интерфейс для взаимодействия с внутренним API gameplay-service.
type GameplayServiceClient interface {
	// ListUserDrafts получает список черновиков пользователя.
	ListUserDrafts(ctx context.Context, userID uint64, limit int, cursor string) ([]models.StoryConfig, string, error)
	// ListUserPublishedStories получает список опубликованных историй пользователя.
	ListUserPublishedStories(ctx context.Context, userID uint64, limit, offset int) ([]*models.PublishedStory, bool, error)

	// SetInterServiceToken устанавливает межсервисный JWT токен для клиента.
	SetInterServiceToken(token string)
}
