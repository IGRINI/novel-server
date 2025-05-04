package client

import (
	"context"
	"novel-server/shared/models"

	"github.com/google/uuid"
)

// GameplayServiceClient определяет интерфейс для взаимодействия с внутренним API gameplay-service.
type GameplayServiceClient interface {
	// ListUserDrafts получает список черновиков пользователя.
	ListUserDrafts(ctx context.Context, userID uuid.UUID, limit int, cursor string, adminAccessToken string) ([]models.StoryConfig, string, error)
	// ListUserPublishedStories получает список опубликованных историй пользователя.
	ListUserPublishedStories(ctx context.Context, userID uuid.UUID, limit, offset int, adminAccessToken string) ([]*models.PublishedStory, bool, error)

	// SetInterServiceToken устанавливает межсервисный JWT токен для клиента.
	SetInterServiceToken(token string)

	// <<< ОБНОВЛЕНО: Методы для внутреннего API >>>
	GetDraftDetailsInternal(ctx context.Context, draftID uuid.UUID, adminAccessToken string) (*models.StoryConfig, error)
	GetPublishedStoryDetailsInternal(ctx context.Context, storyID uuid.UUID, adminAccessToken string) (*models.PublishedStory, error)
	ListStoryScenesInternal(ctx context.Context, storyID uuid.UUID, adminAccessToken string) ([]models.StoryScene, error)

	// <<< ОБНОВЛЕНО: Методы для обновления через внутреннее API >>>
	UpdateDraftInternal(ctx context.Context, draftID uuid.UUID, configJSON, userInputJSON string, status models.StoryStatus, adminAccessToken string) error
	UpdateStoryInternal(ctx context.Context, storyID uuid.UUID, configJSON, setupJSON string, status models.StoryStatus, adminAccessToken string) error
	UpdateSceneInternal(ctx context.Context, sceneID uuid.UUID, contentJSON string, adminAccessToken string) error

	// DeleteSceneInternal удаляет сцену по ее ID.
	DeleteSceneInternal(ctx context.Context, sceneID uuid.UUID, adminAccessToken string) error

	// --- Методы для работы с прогрессом игроков --- //

	// ListStoryPlayersInternal получает список состояний игроков для данной истории.
	ListStoryPlayersInternal(ctx context.Context, storyID uuid.UUID, adminAccessToken string) ([]models.PlayerGameState, error)

	// GetPlayerProgressInternal получает детали прогресса игрока.
	GetPlayerProgressInternal(ctx context.Context, progressID uuid.UUID, adminAccessToken string) (*models.PlayerProgress, error)

	// UpdatePlayerProgressInternal обновляет детали прогресса игрока.
	UpdatePlayerProgressInternal(ctx context.Context, progressID uuid.UUID, progressData map[string]interface{}, adminAccessToken string) error

	// <<< ДОБАВЛЕНО: Получение количества активных историй >>>
	GetActiveStoryCount(ctx context.Context, adminAccessToken string) (int, error)

	// <<< ДОБАВЛЕНО: Методы для действий с контентом >>>
	DeleteDraft(ctx context.Context, userID, draftID uuid.UUID) error
	RetryDraftGeneration(ctx context.Context, userID, draftID uuid.UUID) error
	DeletePublishedStory(ctx context.Context, userID, storyID uuid.UUID) error
}
