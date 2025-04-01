package repository

import (
	"context"
	"novel-server/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NovelRepository определяет методы для взаимодействия с хранилищем новелл.
type NovelRepository interface {
	// --- Novels ---
	// CreateNovel создает новую запись о новелле в хранилище.
	// Возвращает ID созданной новеллы.
	CreateNovel(ctx context.Context, userID string, config *domain.NovelConfig) (uuid.UUID, error)
	// GetNovelMetadataByID возвращает краткую информацию (метаданные) о новелле по ID.
	GetNovelMetadataByID(ctx context.Context, novelID uuid.UUID, userID string) (*domain.NovelMetadata, error)
	// GetNovelConfigByID возвращает полную конфигурацию новеллы по ID.
	GetNovelConfigByID(ctx context.Context, novelID uuid.UUID, userID string) (*domain.NovelConfig, error)
	// ListNovelsByUser возвращает список метаданных новелл для указанного пользователя.
	ListNovelsByUser(ctx context.Context, userID string, limit, offset int) ([]domain.NovelMetadata, error)
	// ListNovels возвращает список новелл с пагинацией
	ListNovels(ctx context.Context, userID string, limit int, cursor *uuid.UUID) ([]domain.NovelListItem, int, *uuid.UUID, error)
	// GetNovelDetails возвращает детальную информацию о новелле, включая персонажей из сетапа
	GetNovelDetails(ctx context.Context, novelID uuid.UUID) (*domain.NovelDetailsResponse, error)
	// GetNovelIsAdult возвращает флаг is_adult_content для новеллы.
	GetNovelIsAdult(ctx context.Context, novelID uuid.UUID) (bool, error)
	// UpdateNovel(ctx context.Context, novelID uuid.UUID, userID string, title *string) error // Если понадобится редактирование
	// DeleteNovel(ctx context.Context, novelID uuid.UUID, userID string) error // Если понадобится удаление

	// --- Novel States ---
	// SaveNovelState сохраняет состояние новеллы (stateData) для определенной сцены,
	// связывая его с stateHash. Параметр userID используется только для обновления прогресса пользователя.
	SaveNovelState(ctx context.Context, novelID uuid.UUID, sceneIndex int, userID string, stateHash string, stateData []byte) error

	// GetLatestNovelState возвращает самое последнее сохраненное состояние новеллы (stateData)
	// и его индекс сцены для конкретного пользователя.
	GetLatestNovelState(ctx context.Context, novelID uuid.UUID, userID string) (stateData []byte, sceneIndex int, err error)

	// GetNovelStateByHash возвращает состояние новеллы (stateData) по его хешу.
	// Возвращает ошибку ErrNoRows, если состояние с таким хешом не найдено.
	GetNovelStateByHash(ctx context.Context, stateHash string) (stateData []byte, err error)

	// GetNovelStateBySceneIndex возвращает состояние новеллы (stateData) для определенного индекса сцены.
	// Возвращает самое раннее состояние (первое созданное), которое должно быть общим для всех пользователей.
	GetNovelStateBySceneIndex(ctx context.Context, novelID uuid.UUID, sceneIndex int) (stateData []byte, err error)

	// GetNovelSetupState возвращает сетап новеллы (состояние с индексом 0).
	// Это может быть общим для всех пользователей данной новеллы.
	GetNovelSetupState(ctx context.Context, novelID uuid.UUID) (stateData []byte, err error)

	// SaveNovelSetupState сохраняет сетап новеллы (состояние с индексом 0) непосредственно в таблицу novels
	SaveNovelSetupState(ctx context.Context, novelID uuid.UUID, setupData []byte) error

	// GetUserNovelProgress возвращает текущий прогресс пользователя в новелле
	// (индекс последней доступной сцены).
	// Возвращает -1, если прогресс не найден.
	GetUserNovelProgress(ctx context.Context, novelID uuid.UUID, userID string) (sceneIndex int, err error)

	// --- Методы для работы с динамическим прогрессом пользователя ---

	// SaveUserStoryProgress сохраняет динамические элементы прогресса пользователя
	// для конкретной сцены новеллы.
	SaveUserStoryProgress(ctx context.Context, novelID uuid.UUID, sceneIndex int, userID string,
		progress *domain.UserStoryProgress) error

	// GetLatestUserStoryProgress возвращает последний сохраненный прогресс пользователя
	// для конкретной новеллы. Возвращает nil и -1, если прогресс не найден.
	GetLatestUserStoryProgress(ctx context.Context, novelID uuid.UUID, userID string) (*domain.UserStoryProgress, int, error)

	// GetUserStoryProgressByHash возвращает прогресс истории по хешу состояния.
	// Этот метод заменяет GetNovelStateByHash для новой схемы данных.
	GetUserStoryProgressByHash(ctx context.Context, stateHash string) (*domain.UserStoryProgress, error)

	// --- Низкоуровневый доступ ---
	// DB возвращает пул соединений с базой данных для более низкоуровневых операций,
	// которые не охвачены стандартными методами репозитория.
	DB() *pgxpool.Pool
}
