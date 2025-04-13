package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"novel-server/gameplay-service/internal/messaging"
	"novel-server/gameplay-service/internal/models"
	"novel-server/gameplay-service/internal/repository"
	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"sort"
	"strconv"
	"strings"
	"time"

	database "novel-server/shared/database"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Определяем локальные ошибки уровня сервиса
var (
	ErrInvalidOperation      = errors.New("недопустимая операция")
	ErrInvalidLimit          = errors.New("недопустимое значение limit")
	ErrInvalidOffset         = errors.New("недопустимое значение offset")
	ErrInvalidCursor         = errors.New("недопустимый формат курсора")
	ErrChoiceNotFound        = errors.New("выбранный вариант или сцена не найдены")
	ErrInvalidChoiceIndex    = errors.New("недопустимый индекс выбора")
	ErrCannotPublish         = errors.New("историю нельзя опубликовать в текущем статусе")
	ErrCannotPublishNoConfig = errors.New("отсутствует сгенерированный конфиг для публикации")
	// Ошибки, определенные в shared/models/errors.go, будут использоваться напрямую:
	// sharedModels.ErrUserHasActiveGeneration
	// sharedModels.ErrCannotRevise
	// sharedModels.ErrStoryNotReadyYet
	// sharedModels.ErrSceneNeedsGeneration

	// Добавляем ошибки, ожидаемые в handler/http.go
	ErrStoryNotFound          = errors.New("опубликованная история не найдена")
	ErrSceneNotFound          = errors.New("текущая сцена не найдена")
	ErrPlayerProgressNotFound = errors.New("прогресс игрока не найден")
	ErrStoryNotReady          = errors.New("история еще не готова к игре")
	ErrInternal               = errors.New("внутренняя ошибка сервиса")
	ErrInvalidChoice          = errors.New("недопустимый выбор")
	ErrNoChoicesAvailable     = errors.New("в текущей сцене нет доступных выборов")
)

// --- Структуры для парсинга SceneContent ---

type sceneContentChoices struct {
	Type    string        `json:"type"` // "choices"
	Choices []sceneChoice `json:"ch"`
	// svd (story_variable_definitions) пока игнорируем, они не влияют на текущий state
}

type sceneChoice struct {
	Shuffleable int           `json:"sh"` // 0 или 1
	Description string        `json:"desc"`
	Options     []sceneOption `json:"opts"` // Должно быть ровно 2
}

type sceneOption struct {
	Text         string                    `json:"txt"`
	Consequences sharedModels.Consequences `json:"cons"` // Используем общую структуру
}

// GameplayService определяет интерфейс для бизнес-логики gameplay.
type GameplayService interface {
	// TODO: Поменять userID uint64 на uuid.UUID везде где он используется
	GenerateInitialStory(ctx context.Context, userID uint64, initialPrompt string) (*models.StoryConfig, error)
	ReviseDraft(ctx context.Context, id uuid.UUID, userID uint64, revisionPrompt string) error
	GetStoryConfig(ctx context.Context, id uuid.UUID, userID uint64) (*models.StoryConfig, error)
	PublishDraft(ctx context.Context, draftID uuid.UUID, userID uint64) (publishedStoryID uuid.UUID, err error)
	ListMyDrafts(ctx context.Context, userID uint64, limit int, cursor string) ([]models.StoryConfig, string, error)
	ListMyPublishedStories(ctx context.Context, userID uint64, limit, offset int) ([]*sharedModels.PublishedStory, error)

	// TODO: Остальные методы тоже должны использовать userID uuid.UUID
	ListPublicStories(ctx context.Context, limit, offset int) ([]*sharedModels.PublishedStory, error)
	GetStoryScene(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (*sharedModels.StoryScene, error)
	MakeChoice(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID, selectedOptionIndex int) error
	DeletePlayerProgress(ctx context.Context, userID, publishedStoryID uuid.UUID) error
}

type gameplayServiceImpl struct {
	repo               repository.StoryConfigRepository // Использует uint64 UserID
	publishedRepo      interfaces.PublishedStoryRepository
	sceneRepo          interfaces.StorySceneRepository
	playerProgressRepo interfaces.PlayerProgressRepository // Использует uuid.UUID UserID
	publisher          messaging.TaskPublisher
	pool               *pgxpool.Pool
	logger             *zap.Logger
}

func NewGameplayService(
	repo repository.StoryConfigRepository, // Использует uint64 UserID
	publishedRepo interfaces.PublishedStoryRepository,
	sceneRepo interfaces.StorySceneRepository,
	playerProgressRepo interfaces.PlayerProgressRepository, // Использует uuid.UUID UserID
	publisher messaging.TaskPublisher,
	pool *pgxpool.Pool,
	logger *zap.Logger,
) GameplayService {
	return &gameplayServiceImpl{
		repo:               repo,
		publishedRepo:      publishedRepo,
		sceneRepo:          sceneRepo,
		playerProgressRepo: playerProgressRepo,
		publisher:          publisher,
		pool:               pool,
		logger:             logger.Named("GameplayService"),
	}
}

// GenerateInitialStory создает новую запись StoryConfig и отправляет задачу на генерацию.
// TODO: Рефакторинг на userID uuid.UUID
func (s *gameplayServiceImpl) GenerateInitialStory(ctx context.Context, userID uint64, initialPrompt string) (*models.StoryConfig, error) {
	// Проверяем количество активных генераций для этого userID
	activeCount, err := s.repo.CountActiveGenerations(ctx, userID)
	if err != nil {
		// Ошибка при проверке, возвращаем 500
		log.Printf("[GameplayService] Ошибка подсчета активных генераций для UserID %d: %v", userID, err)
		return nil, fmt.Errorf("ошибка проверки статуса генерации: %w", err)
	}
	// TODO: Сделать лимит настраиваемым (например, через конфиг или профиль пользователя)
	generationLimit := 1
	if activeCount >= generationLimit {
		log.Printf("[GameplayService] Пользователь UserID %d достиг лимита активных генераций (%d).", userID, generationLimit)
		return nil, sharedModels.ErrUserHasActiveGeneration // Используем ту же ошибку
	}

	// Создаем JSON массив с начальным промптом
	userInputJSON, err := json.Marshal([]string{initialPrompt})
	if err != nil {
		log.Printf("[GameplayService] Ошибка маршалинга initialPrompt для UserID %d: %v", userID, err)
		return nil, fmt.Errorf("ошибка подготовки данных для БД: %w", err)
	}

	config := &models.StoryConfig{
		ID:          uuid.New(),
		UserID:      userID,
		Title:       "",                      // Будет заполнено после генерации
		Description: initialPrompt,           // Сохраняем исходный промпт в Description для первого запроса
		UserInput:   userInputJSON,           // Массив промптов
		Config:      nil,                     // JSON конфиг будет после генерации
		Status:      models.StatusGenerating, // Сразу ставим generating
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	// 1. Сохраняем черновик в БД со статусом 'generating'
	err = s.repo.Create(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("ошибка сохранения начального драфта: %w", err)
	}
	log.Printf("[GameplayService] Начальный драфт создан и сохранен: ID=%s, UserID=%d", config.ID, config.UserID)

	// 2. Формируем и отправляем задачу на генерацию
	taskID := uuid.New().String() // ID для задачи генерации
	generationPayload := sharedMessaging.GenerationTaskPayload{
		TaskID:        taskID,
		UserID:        strconv.FormatUint(config.UserID, 10),
		PromptType:    sharedMessaging.PromptTypeNarrator, // Пока используем только Narrator
		InputData:     make(map[string]interface{}),       // Пустой для начальной генерации
		UserInput:     initialPrompt,                      // Исходный промпт пользователя
		StoryConfigID: config.ID.String(),                 // Связь с созданным конфигом
	}

	if err := s.publisher.PublishGenerationTask(ctx, generationPayload); err != nil {
		log.Printf("[GameplayService] Ошибка публикации задачи начальной генерации для ConfigID=%s, TaskID=%s: %v. Попытка откатить статус...", config.ID, taskID, err)
		// Пытаемся откатить статус обратно на Error (или Draft?)
		config.Status = models.StatusError
		config.UpdatedAt = time.Now().UTC()
		if rollbackErr := s.repo.Update(context.Background(), config); rollbackErr != nil {
			log.Printf("[GameplayService] КРИТИЧЕСКАЯ ОШИБКА: Не удалось откатить статус на Error для ConfigID=%s после ошибки публикации: %v", config.ID, rollbackErr)
		}
		// Возвращаем ошибку клиенту, но конфиг в БД остался со статусом Error
		return config, fmt.Errorf("ошибка отправки задачи на генерацию: %w", err) // Возвращаем конфиг с ID и ошибку
	}

	log.Printf("[GameplayService] Задача начальной генерации успешно отправлена: ConfigID=%s, TaskID=%s", config.ID, taskID)

	// Возвращаем созданный конфиг (со статусом generating), чтобы клиент знал ID
	return config, nil
}

// ReviseDraft обновляет существующий черновик истории
// TODO: Рефакторинг на userID uuid.UUID
func (s *gameplayServiceImpl) ReviseDraft(ctx context.Context, id uuid.UUID, userID uint64, revisionPrompt string) error {
	// 1. Получаем текущий конфиг
	config, err := s.repo.GetByID(ctx, id, userID)
	log.Printf("!!!!!! DEBUG [ReviseDraft]: GetByID returned -> Config: %+v, Error: %v", config, err) // <-- DEBUG LOG
	if err != nil {
		return fmt.Errorf("ошибка получения драфта для ревизии: %w", err)
	}

	// 2. Проверяем статус
	if config.Status != models.StatusDraft && config.Status != models.StatusError {
		log.Printf("[UserID: %d][StoryID: %s] Попытка ревизии в недопустимом статусе: %s", userID, id, config.Status)
		return sharedModels.ErrCannotRevise
	}

	// Проверяем количество активных генераций для этого userID
	activeCount, err := s.repo.CountActiveGenerations(ctx, userID)
	if err != nil {
		log.Printf("[GameplayService] Ошибка подсчета активных генераций для UserID %d перед ревизией ConfigID %s: %v", userID, id, err)
		return fmt.Errorf("ошибка проверки статуса генерации: %w", err)
	}
	// TODO: Сделать лимит настраиваемым
	generationLimit := 1
	if activeCount >= generationLimit {
		log.Printf("[GameplayService] Пользователь UserID %d достиг лимита активных генераций (%d), ревизия ConfigID %s отклонена.", userID, generationLimit, id)
		return sharedModels.ErrUserHasActiveGeneration
	}

	// 3. Обновляем историю UserInput
	var userInputs []string
	if config.UserInput != nil {
		if err := json.Unmarshal(config.UserInput, &userInputs); err != nil {
			log.Printf("[GameplayService] Ошибка десериализации UserInput для ConfigID %s: %v. Создаем новый массив.", config.ID, err)
			userInputs = make([]string, 0)
		}
	}
	userInputs = append(userInputs, revisionPrompt)
	updatedUserInputJSON, err := json.Marshal(userInputs)
	if err != nil {
		log.Printf("[GameplayService] Ошибка маршалинга обновленного UserInput для ConfigID %s: %v", config.ID, err)
		return fmt.Errorf("ошибка подготовки данных для БД: %w", err)
	}
	config.UserInput = updatedUserInputJSON

	// 4. Обновляем статус на 'generating' и сохраняем ИЗМЕНЕННЫЙ UserInput
	config.Status = models.StatusGenerating
	config.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, config); err != nil {
		return fmt.Errorf("ошибка обновления статуса/UserInput перед ревизией: %w", err)
	}

	// 5. Формируем payload для задачи ревизии
	taskID := uuid.New().String()
	generationPayload := sharedMessaging.GenerationTaskPayload{
		TaskID:        taskID,
		UserID:        strconv.FormatUint(config.UserID, 10),
		PromptType:    sharedMessaging.PromptTypeNarrator,
		InputData:     map[string]interface{}{"current_config": string(config.Config)}, // Передаем текущий JSON из поля Config
		UserInput:     revisionPrompt,                                                  // Передаем только последнюю правку
		StoryConfigID: config.ID.String(),
	}

	// 6. Публикуем задачу в очередь
	if err := s.publisher.PublishGenerationTask(ctx, generationPayload); err != nil {
		log.Printf("[GameplayService] Ошибка публикации задачи ревизии для ConfigID=%s, TaskID=%s: %v. Попытка откатить статус...", config.ID, taskID, err)
		// Пытаемся откатить статус обратно на предыдущий (Draft или Error)
		if len(userInputs) > 1 { // Если это была ревизия, а не первая генерация после ошибки
			config.Status = models.StatusDraft
		} else {
			config.Status = models.StatusError // Если первая попытка после ошибки не удалась
		}
		// Убираем последний UserInput, так как ревизия не удалась
		config.UserInput, _ = json.Marshal(userInputs[:len(userInputs)-1])
		config.UpdatedAt = time.Now().UTC()
		if rollbackErr := s.repo.Update(context.Background(), config); rollbackErr != nil {
			log.Printf("[GameplayService] КРИТИЧЕСКАЯ ОШИБКА: Не удалось откатить статус/UserInput для ConfigID=%s после ошибки публикации ревизии: %v", config.ID, rollbackErr)
		}
		return fmt.Errorf("ошибка публикации задачи ревизии: %w", err)
	}

	log.Printf("[GameplayService] Задача ревизии успешно отправлена: ConfigID=%s, TaskID=%s", config.ID, taskID)
	return nil // Успех, возвращаем только nil
}

// GetStoryConfig получает конфиг истории
// TODO: Рефакторинг на userID uuid.UUID
func (s *gameplayServiceImpl) GetStoryConfig(ctx context.Context, id uuid.UUID, userID uint64) (*models.StoryConfig, error) {
	config, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		// Обработка ошибок (включая NotFound) происходит в репозитории
		return nil, fmt.Errorf("ошибка получения StoryConfig в сервисе: %w", err)
	}
	return config, nil
}

// PublishDraft публикует черновик, удаляет его и создает PublishedStory.
// TODO: Рефакторинг на userID uuid.UUID
func (s *gameplayServiceImpl) PublishDraft(ctx context.Context, draftID uuid.UUID, userID uint64) (publishedStoryID uuid.UUID, err error) {
	// Начало транзакции
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("Failed to begin transaction for publishing draft", zap.String("draftID", draftID.String()), zap.Error(err))
		return uuid.Nil, fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("Panic recovered during PublishDraft, rolling back transaction", zap.Any("panic", r))
			_ = tx.Rollback(context.Background()) // Ignore rollback error after panic
			err = fmt.Errorf("panic during publish: %v", r)
		} else if err != nil {
			s.logger.Warn("Rolling back transaction due to error", zap.String("draftID", draftID.String()), zap.Error(err))
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
				s.logger.Error("Failed to rollback transaction", zap.String("draftID", draftID.String()), zap.Error(rollbackErr))
			}
		} else {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				s.logger.Error("Failed to commit transaction", zap.String("draftID", draftID.String()), zap.Error(commitErr))
				err = fmt.Errorf("ошибка подтверждения транзакции: %w", commitErr)
			}
		}
	}()

	// Используем транзакцию для репозиториев
	repoTx := repository.NewPgStoryConfigRepository(tx, s.logger)
	publishedRepoTx := database.NewPgPublishedStoryRepository(tx, s.logger)

	// 1. Получаем черновик в транзакции
	draft, err := repoTx.GetByID(ctx, draftID, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, sharedModels.ErrNotFound // Используем стандартную ошибку
		}
		return uuid.Nil, fmt.Errorf("ошибка получения черновика: %w", err)
	}

	// 2. Проверяем статус и наличие Config
	if draft.Status != models.StatusDraft && draft.Status != models.StatusError {
		return uuid.Nil, ErrCannotPublish // Используем локальную ошибку
	}
	if draft.Config == nil || len(draft.Config) == 0 {
		s.logger.Warn("Attempt to publish draft without generated config", zap.String("draftID", draftID.String()))
		return uuid.Nil, ErrCannotPublishNoConfig // Используем локальную ошибку
	}

	// 3. Извлекаем необходимые поля из draft.Config
	var tempConfig struct {
		IsAdultContent bool `json:"ac"`
	}
	if err = json.Unmarshal(draft.Config, &tempConfig); err != nil {
		s.logger.Error("Failed to unmarshal draft config to extract adult content flag", zap.String("draftID", draftID.String()), zap.Error(err))
		return uuid.Nil, fmt.Errorf("ошибка чтения конфигурации черновика: %w", err)
	}

	// 4. Создаем PublishedStory в транзакции
	newPublishedStory := &sharedModels.PublishedStory{
		ID:             uuid.New(),
		UserID:         userID, // TODO: Сменить UserID на uuid.UUID когда будет рефакторинг
		Config:         draft.Config,
		Setup:          nil, // Будет сгенерировано позже
		Status:         sharedModels.StatusSetupPending,
		IsPublic:       false, // По умолчанию приватная
		IsAdultContent: tempConfig.IsAdultContent,
		Title:          &draft.Title,       // <<< Исправлено: передаем указатель
		Description:    &draft.Description, // <<< Исправлено: передаем указатель
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}

	if err = publishedRepoTx.Create(ctx, newPublishedStory); err != nil {
		return uuid.Nil, fmt.Errorf("ошибка создания опубликованной истории: %w", err)
	}
	s.logger.Info("Published story created in DB", zap.String("publishedStoryID", newPublishedStory.ID.String()))

	// 5. Удаляем черновик в транзакции
	if err = repoTx.Delete(ctx, draftID, userID); err != nil {
		return uuid.Nil, fmt.Errorf("ошибка удаления черновика: %w", err)
	}
	s.logger.Info("Draft deleted from DB", zap.String("draftID", draftID.String()))

	// 6. Отправляем задачу на генерацию Setup
	taskID := uuid.New().String()
	setupPayload := sharedMessaging.GenerationTaskPayload{
		TaskID:           taskID,
		UserID:           strconv.FormatUint(newPublishedStory.UserID, 10), // TODO: Поменять UserID на uuid.UUID
		PromptType:       sharedMessaging.PromptTypeNovelSetup,
		InputData:        map[string]interface{}{"config": string(newPublishedStory.Config)}, // Передаем JSON конфиг
		PublishedStoryID: newPublishedStory.ID.String(),                                      // Связь с опубликованной историей
	}

	// Публикация задачи ВНЕ транзакции, после того как она почти наверняка будет закоммичена
	go func(payload sharedMessaging.GenerationTaskPayload) {
		publishCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := s.publisher.PublishGenerationTask(publishCtx, payload); err != nil {
			// Ошибка публикации Setup задачи - критично, так как транзакция уже закоммичена.
			// Статус останется SetupPending, но задача не уйдет.
			// TODO: Нужна система ретраев или мониторинг для таких случаев.
			s.logger.Error("CRITICAL: Failed to publish setup generation task after DB commit",
				zap.String("publishedStoryID", payload.PublishedStoryID),
				zap.String("taskID", payload.TaskID),
				zap.Error(err))
		} else {
			s.logger.Info("Setup generation task published successfully",
				zap.String("publishedStoryID", payload.PublishedStoryID),
				zap.String("taskID", payload.TaskID))
		}
	}(setupPayload)

	// Если дошли сюда без ошибок, defer tx.Commit() сработает при выходе
	publishedStoryID = newPublishedStory.ID
	return publishedStoryID, nil
}

// ListMyDrafts возвращает список черновиков пользователя.
func (s *gameplayServiceImpl) ListMyDrafts(ctx context.Context, userID uint64, limit int, cursor string) ([]models.StoryConfig, string, error) {
	// Валидация limit (можно вынести в хендлер)
	if limit <= 0 || limit > 100 { // Примерный максимальный лимит
		s.logger.Warn("Invalid limit requested for ListMyDrafts", zap.Int("limit", limit), zap.Uint64("userID", userID))
		limit = 20 // Возвращаем значение по умолчанию или ошибку?
		// return nil, "", ErrInvalidLimit // Или устанавливаем дефолтное
	}

	s.logger.Debug("Calling repo.ListByUser for drafts", zap.Uint64("userID", userID), zap.Int("limit", limit), zap.String("cursor", cursor))
	configs, nextCursor, err := s.repo.ListByUser(ctx, userID, limit, cursor)
	if err != nil {
		// Обработка ошибок репозитория (включая неверный курсор)
		s.logger.Error("Failed to list user drafts from repository", zap.Uint64("userID", userID), zap.Error(err))
		if strings.Contains(err.Error(), "неверный формат курсора") { // Проверяем текст ошибки, т.к. decodeCursor не возвращает ErrInvalidCursor
			return nil, "", ErrInvalidCursor
		}
		return nil, "", fmt.Errorf("ошибка получения списка черновиков: %w", err)
	}

	s.logger.Info("User drafts listed successfully", zap.Uint64("userID", userID), zap.Int("count", len(configs)))
	return configs, nextCursor, nil
}

// ListMyPublishedStories возвращает список опубликованных историй пользователя.
func (s *gameplayServiceImpl) ListMyPublishedStories(ctx context.Context, userID uint64, limit, offset int) ([]*sharedModels.PublishedStory, error) {
	// Валидация limit и offset (можно вынести в хендлер)
	if limit <= 0 || limit > 100 {
		s.logger.Warn("Invalid limit requested for ListMyPublishedStories", zap.Int("limit", limit), zap.Uint64("userID", userID))
		limit = 20 // Default
		// return nil, ErrInvalidLimit
	}
	if offset < 0 {
		s.logger.Warn("Invalid offset requested for ListMyPublishedStories", zap.Int("offset", offset), zap.Uint64("userID", userID))
		offset = 0 // Default
		// return nil, ErrInvalidOffset
	}

	s.logger.Debug("Calling publishedRepo.ListByUser", zap.Uint64("userID", userID), zap.Int("limit", limit), zap.Int("offset", offset))
	stories, err := s.publishedRepo.ListByUser(ctx, userID, limit, offset)
	if err != nil {
		s.logger.Error("Failed to list user published stories from repository", zap.Uint64("userID", userID), zap.Error(err))
		return nil, fmt.Errorf("ошибка получения списка опубликованных историй пользователя: %w", err)
	}

	s.logger.Info("User published stories listed successfully", zap.Uint64("userID", userID), zap.Int("count", len(stories)))
	return stories, nil
}

// ListPublicStories возвращает список публичных опубликованных историй.
func (s *gameplayServiceImpl) ListPublicStories(ctx context.Context, limit, offset int) ([]*sharedModels.PublishedStory, error) {
	// Валидация limit и offset
	if limit <= 0 || limit > 100 {
		s.logger.Warn("Invalid limit requested for ListPublicStories", zap.Int("limit", limit))
		limit = 20
		// return nil, ErrInvalidLimit
	}
	if offset < 0 {
		s.logger.Warn("Invalid offset requested for ListPublicStories", zap.Int("offset", offset))
		offset = 0
		// return nil, ErrInvalidOffset
	}

	s.logger.Debug("Calling publishedRepo.ListPublic", zap.Int("limit", limit), zap.Int("offset", offset))
	stories, err := s.publishedRepo.ListPublic(ctx, limit, offset)
	if err != nil {
		s.logger.Error("Failed to list public stories from repository", zap.Error(err))
		return nil, fmt.Errorf("ошибка получения списка публичных историй: %w", err)
	}

	s.logger.Info("Public stories listed successfully", zap.Int("count", len(stories)))
	return stories, nil
}

// --- Методы игрового цикла (используют userID uuid.UUID) ---

// GetStoryScene получает текущую сцену для игрока.
func (s *gameplayServiceImpl) GetStoryScene(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (*sharedModels.StoryScene, error) {
	s.logger.Info("GetStoryScene called", zap.String("userID", userID.String()), zap.String("publishedStoryID", publishedStoryID.String()))

	// 1. Получаем опубликованную историю, чтобы проверить статус и UserID
	publishedStory, err := s.publishedRepo.GetByID(ctx, publishedStoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, sharedModels.ErrNotFound
		}
		return nil, fmt.Errorf("ошибка получения опубликованной истории: %w", err)
	}

	// 2. Проверяем, принадлежит ли история пользователю
	// TODO: Сменить publishedStory.UserID на UUID, когда будет рефакторинг
	/*
	   if publishedStory.UserID != userID {
	       return nil, sharedModels.ErrForbidden
	   }
	*/

	// 3. Проверяем статус истории
	if publishedStory.Status == sharedModels.StatusSetupPending || publishedStory.Status == sharedModels.StatusFirstScenePending {
		return nil, sharedModels.ErrStoryNotReadyYet
	}
	if publishedStory.Status != sharedModels.StatusReady && publishedStory.Status != sharedModels.StatusGeneratingScene {
		s.logger.Warn("Attempt to get scene for story in invalid state",
			zap.String("publishedStoryID", publishedStoryID.String()),
			zap.String("status", string(publishedStory.Status)))
		// TODO: Какую ошибку возвращать? Пока возвращаем общую ошибку.
		return nil, fmt.Errorf("история находится в неиграбельном статусе: %s", publishedStory.Status)
	}

	// 4. Получаем прогресс игрока или создаем начальный
	playerProgress, err := s.playerProgressRepo.GetByUserIDAndStoryID(ctx, userID, publishedStoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Прогресс не найден, создаем начальный
			s.logger.Info("Player progress not found, creating initial progress", zap.String("userID", userID.String()), zap.String("publishedStoryID", publishedStoryID.String()))
			playerProgress = &sharedModels.PlayerProgress{
				UserID:           userID,
				PublishedStoryID: publishedStoryID,
				CurrentStateHash: sharedModels.InitialStateHash, // Используем константу
				CoreStats:        make(map[string]int),          // Будут заполнены из Setup при первом MakeChoice?
				StoryVariables:   make(map[string]interface{}),
				GlobalFlags:      []string{},
				CreatedAt:        time.Now().UTC(),
				UpdatedAt:        time.Now().UTC(),
			}
			if errCreate := s.playerProgressRepo.CreateOrUpdate(ctx, playerProgress); errCreate != nil {
				return nil, fmt.Errorf("ошибка создания начального прогресса игрока: %w", errCreate)
			}
		} else {
			// Другая ошибка при получении прогресса
			return nil, fmt.Errorf("ошибка получения прогресса игрока: %w", err)
		}
	}

	// 5. Получаем сцену по текущему хешу состояния из прогресса
	scene, err := s.sceneRepo.FindByStoryAndHash(ctx, publishedStoryID, playerProgress.CurrentStateHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Сцена не найдена - это означает, что ее нужно сгенерировать
			s.logger.Info("Scene not found for hash, requires generation",
				zap.String("publishedStoryID", publishedStoryID.String()),
				zap.String("stateHash", playerProgress.CurrentStateHash))
			// TODO: Запустить генерацию сцены (в MakeChoice?)
			return nil, sharedModels.ErrSceneNeedsGeneration
		}
		// Другая ошибка при получении сцены
		return nil, fmt.Errorf("ошибка получения сцены: %w", err)
	}

	// 6. Возвращаем найденную сцену
	s.logger.Info("Scene found and returned", zap.String("sceneID", scene.ID.String()))
	return scene, nil
}

// MakeChoice обрабатывает выбор игрока и возвращает следующую сцену (или ошибку).
func (s *gameplayServiceImpl) MakeChoice(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID, selectedOptionIndex int) error {
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
		zap.Int("selectedOptionIndex", selectedOptionIndex),
	}
	s.logger.Info("MakeChoice called", logFields...)

	// 1. Получаем текущий прогресс игрока
	progress, err := s.playerProgressRepo.GetByUserIDAndStoryID(ctx, userID, publishedStoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.logger.Warn("Player progress not found for MakeChoice", logFields...)
			// Используем новую ошибку
			return ErrPlayerProgressNotFound
		}
		s.logger.Error("Failed to get player progress", append(logFields, zap.Error(err))...)
		return ErrInternal // Используем новую ошибку
	}

	// 2. Получаем опубликованную историю для проверки статуса и получения Setup/Config
	publishedStory, err := s.publishedRepo.GetByID(ctx, publishedStoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.logger.Warn("Published story not found for MakeChoice", logFields...)
			return ErrStoryNotFound // Используем новую ошибку
		}
		s.logger.Error("Failed to get published story", append(logFields, zap.Error(err))...)
		return ErrInternal
	}

	// Проверяем статус опубликованной истории
	if publishedStory.Status != sharedModels.StatusReady && publishedStory.Status != sharedModels.StatusGeneratingScene { // Разрешаем выбор во время генерации?
		s.logger.Warn("Attempt to make choice in non-ready/generating story state", append(logFields, zap.String("status", string(publishedStory.Status)))...)
		return ErrStoryNotReady // Используем новую ошибку
	}

	// 3. Получаем текущую сцену по хешу из прогресса
	currentScene, err := s.sceneRepo.FindByStoryAndHash(ctx, publishedStoryID, progress.CurrentStateHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.logger.Error("CRITICAL: Current scene not found for hash in player progress", append(logFields, zap.String("stateHash", progress.CurrentStateHash))...)
			// Это нештатная ситуация, прогресс указывает на несуществующую сцену
			// TODO: Как обрабатывать? Откатить прогресс? Вернуть ошибку?
			return ErrSceneNotFound // Используем новую ошибку
		}
		s.logger.Error("Failed to get current scene by hash", append(logFields, zap.String("stateHash", progress.CurrentStateHash), zap.Error(err))...)
		return ErrInternal
	}

	// 4. Парсим контент текущей сцены
	var sceneData sceneContentChoices // Используем существующую структуру парсинга
	if err := json.Unmarshal(currentScene.Content, &sceneData); err != nil {
		s.logger.Error("Failed to unmarshal current scene content", append(logFields, zap.String("sceneID", currentScene.ID.String()), zap.Error(err))...)
		return ErrInternal
	}

	if sceneData.Type != "choices" {
		s.logger.Warn("Scene content is not of type 'choices'", append(logFields, zap.String("sceneID", currentScene.ID.String()), zap.String("type", sceneData.Type))...)
		// TODO: Что делать, если тип не 'choices'? Завершение игры?
		return ErrInternal // Неожиданный тип контента
	}

	// 5. Валидируем selectedOptionIndex
	// <<< Упрощенная логика: предполагаем выбор в ПЕРВОМ блоке
	if len(sceneData.Choices) == 0 {
		s.logger.Error("Current scene has no choice blocks", append(logFields, zap.String("sceneID", currentScene.ID.String()))...)
		return ErrNoChoicesAvailable // Используем новую ошибку
	}
	choiceBlock := sceneData.Choices[0] // Берем первый блок
	if selectedOptionIndex < 0 || selectedOptionIndex >= len(choiceBlock.Options) {
		s.logger.Warn("Invalid selected option index for choice block 0",
			append(logFields, zap.Int("optionsAvailable", len(choiceBlock.Options)))...)
		return ErrInvalidChoice // Используем новую ошибку
	}

	// 6. Загружаем NovelSetup (нужен для проверки Game Over условий)
	if publishedStory.Setup == nil {
		// Этого не должно происходить, если сцена существует и статус Ready
		s.logger.Error("CRITICAL: PublishedStory Setup is nil, but scene exists and status is Ready/Generating", append(logFields, zap.String("status", string(publishedStory.Status)))...)
		return ErrInternal // Ошибка данных
	}
	var setupContent sharedModels.NovelSetupContent
	if err := json.Unmarshal(publishedStory.Setup, &setupContent); err != nil {
		s.logger.Error("Failed to unmarshal NovelSetup content", append(logFields, zap.Error(err))...)
		return ErrInternal // Ошибка данных
	}

	// 7. Применяем последствия выбранной опции
	selectedOption := choiceBlock.Options[selectedOptionIndex]
	gameOverStat, isGameOver := applyConsequences(progress, selectedOption.Consequences, &setupContent)

	// 8. Обработка Game Over (логика без изменений, кроме UserID в payload)
	if isGameOver {
		s.logger.Info("Game Over condition met", append(logFields, zap.String("gameOverStat", gameOverStat))...)

		// Меняем статус PublishedStory на GameOverPending
		if err := s.publishedRepo.UpdateStatusDetails(ctx, publishedStoryID, sharedModels.StatusGameOverPending, nil, nil, nil); err != nil {
			s.logger.Error("Failed to update published story status to GameOverPending", append(logFields, zap.Error(err))...)
			// TODO: Как обрабатывать ошибку? Продолжать ли отправку задачи?
		}

		// Сохраняем финальное состояние прогресса
		progress.UpdatedAt = time.Now().UTC()
		if err := s.playerProgressRepo.CreateOrUpdate(ctx, progress); err != nil {
			s.logger.Error("Failed to save final player progress before game over", append(logFields, zap.Error(err))...)
			// TODO: Ошибка сохранения прогресса - что делать?
		}

		// Отправляем задачу на генерацию концовки
		taskID := uuid.New().String()

		// Определяем причину Game Over
		var reasonCondition string
		finalValue := progress.CoreStats[gameOverStat]
		if def, ok := setupContent.CoreStatsDefinition[gameOverStat]; ok {
			if finalValue <= def.Min {
				reasonCondition = "min"
			}
			if finalValue >= def.Max {
				reasonCondition = "max"
			}
		}
		reason := sharedMessaging.GameOverReason{
			StatName:  gameOverStat,
			Condition: reasonCondition,
			Value:     finalValue,
		}

		var novelConfig sharedModels.Config
		if err := json.Unmarshal(publishedStory.Config, &novelConfig); err != nil {
			s.logger.Error("Failed to unmarshal novel config for game over task", append(logFields, zap.Error(err))...)
		}

		gameOverPayload := sharedMessaging.GameOverTaskPayload{
			TaskID:           taskID,
			UserID:           userID.String(), // <<< Используем UUID как строку
			PublishedStoryID: publishedStoryID.String(),
			LastState:        *progress,
			Reason:           reason,
			NovelConfig:      novelConfig,
			NovelSetup:       setupContent,
		}
		if err := s.publisher.PublishGameOverTask(ctx, gameOverPayload); err != nil {
			s.logger.Error("Failed to publish game over generation task", append(logFields, zap.Error(err))...)
			return ErrInternal // Возвращаем внутреннюю ошибку, если не удалось отправить задачу
		}
		s.logger.Info("Game over task published", append(logFields, zap.String("taskID", taskID))...)
		return nil // Игра окончена
	}

	// 9. Расчет нового хеша состояния (если не Game Over)
	// <<< Сохраняем текущий хэш как предыдущий >>>
	previousHash := progress.CurrentStateHash
	newStateHash, err := calculateStateHash(previousHash, progress.CoreStats, progress.StoryVariables, progress.GlobalFlags) // <<< Добавлен previousHash
	if err != nil {
		s.logger.Error("Failed to calculate new state hash", append(logFields, zap.Error(err))...)
		return ErrInternal
	}
	logFields = append(logFields, zap.String("newStateHash", newStateHash))
	s.logger.Debug("New state hash calculated", logFields...)

	// 10. Обновляем хеш в прогрессе и сохраняем
	progress.CurrentStateHash = newStateHash
	progress.UpdatedAt = time.Now().UTC()
	if err := s.playerProgressRepo.CreateOrUpdate(ctx, progress); err != nil {
		s.logger.Error("Failed to update player progress with new hash", append(logFields, zap.Error(err))...)
		return ErrInternal
	}

	// 11. Ищем следующую сцену по новому хешу
	var nextScene *sharedModels.StoryScene // <<< Сохраним найденную сцену
	nextScene, err = s.sceneRepo.FindByStoryAndHash(ctx, publishedStoryID, newStateHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Сцена не найдена - запускаем генерацию
			s.logger.Info("Next scene not found, publishing generation task", logFields...)

			// Меняем статус PublishedStory на GeneratingScene
			if errStatus := s.publishedRepo.UpdateStatusDetails(ctx, publishedStoryID, sharedModels.StatusGeneratingScene, nil, nil, nil); errStatus != nil {
				s.logger.Error("Failed to update published story status to GeneratingScene", append(logFields, zap.Error(errStatus))...)
			}

			// <<< Используем новую функцию createGenerationPayload, передаем progress и previousHash >>>
			generationPayload, errGenPayload := createGenerationPayload(
				publishedStory,
				progress,     // Передаем весь прогресс для извлечения сводок
				previousHash, // <<< Добавлен previousHash
				newStateHash,
				choiceBlock.Description,
				selectedOption.Text,
			)
			if errGenPayload != nil {
				s.logger.Error("Failed to create generation payload", append(logFields, zap.Error(errGenPayload))...)
				return ErrInternal
			}

			if errPub := s.publisher.PublishGenerationTask(ctx, generationPayload); errPub != nil {
				s.logger.Error("Failed to publish next scene generation task", append(logFields, zap.Error(errPub))...)
				return ErrInternal
			}
			s.logger.Info("Next scene generation task published", append(logFields, zap.String("taskID", generationPayload.TaskID))...)

			// <<< Очистка sv и gf >>>
			s.logger.Debug("Clearing StoryVariables and GlobalFlags before saving progress", logFields...)
			progress.StoryVariables = make(map[string]interface{}) // Очищаем!
			progress.GlobalFlags = []string{}                      // Очищаем!

			// <<< Обновляем прогресс с новым хешем и очищенными sv/gf >>>
			progress.CurrentStateHash = newStateHash // Обновляем хэш
			progress.UpdatedAt = time.Now().UTC()
			if err := s.playerProgressRepo.CreateOrUpdate(ctx, progress); err != nil {
				s.logger.Error("Ошибка сохранения обновленного PlayerProgress после запуска генерации", append(logFields, zap.Error(err))...)
				return ErrInternal
			}
			s.logger.Info("PlayerProgress (с очищенными sv/gf) успешно обновлен после запуска генерации", logFields...)

			return nil // Сцена генерируется
		} else {
			// Другая ошибка при поиске сцены
			s.logger.Error("Error searching for next scene", append(logFields, zap.Error(err))...)
			return ErrInternal
		}
	}

	// 12. Следующая сцена найдена (err == nil, nextScene != nil)
	s.logger.Info("Next scene found in DB", logFields...)

	// <<< Парсим найденную сцену, чтобы извлечь sssf, fd, vis >>>
	type SceneOutputFormat struct {
		Sssf string `json:"sssf"`
		Fd   string `json:"fd"`
		Vis  string `json:"vis"`
		// Остальные поля (ch, svd) нам здесь не нужны
	}
	var sceneOutput SceneOutputFormat
	if errUnmarshal := json.Unmarshal(nextScene.Content, &sceneOutput); errUnmarshal != nil {
		s.logger.Error("Failed to unmarshal next scene content to get summaries",
			append(logFields, zap.String("nextSceneID", nextScene.ID.String()), zap.Error(errUnmarshal))...)
		// Не критично для MakeChoice, но нужно залогировать. Не обновляем сводки.
	} else {
		// <<< Обновляем поля сводок в прогрессе перед очисткой sv/gf >>>
		progress.LastStorySummary = sceneOutput.Sssf
		progress.LastFutureDirection = sceneOutput.Fd
		progress.LastVarImpactSummary = sceneOutput.Vis
	}

	// <<< Очистка sv и gf >>>
	s.logger.Debug("Clearing StoryVariables and GlobalFlags before saving progress", logFields...)
	progress.StoryVariables = make(map[string]interface{}) // Очищаем!
	progress.GlobalFlags = []string{}                      // Очищаем!

	// <<< Обновляем прогресс с новым хешем, очищенными sv/gf и НОВЫМИ сводками >>>
	progress.CurrentStateHash = newStateHash // Обновляем хэш
	progress.UpdatedAt = time.Now().UTC()
	if err := s.playerProgressRepo.CreateOrUpdate(ctx, progress); err != nil {
		s.logger.Error("Ошибка сохранения обновленного PlayerProgress после нахождения след. сцены", append(logFields, zap.Error(err))...)
		return ErrInternal
	}
	s.logger.Info("PlayerProgress (с очищенными sv/gf и новыми сводками) успешно обновлен после нахождения след. сцены", logFields...)

	return nil
}

// calculateStateHash вычисляет детерминированный хеш состояния, включая хэш предыдущего состояния.
func calculateStateHash(previousHash string, coreStats map[string]int, storyVars map[string]interface{}, globalFlags []string) (string, error) {
	// 1. Подготовка данных
	stateMap := make(map[string]interface{}) // Используем interface{} для универсальности

	// Добавляем хэш предыдущего состояния
	stateMap["_ph"] = previousHash // Используем префикс для избежания коллизий

	// Добавляем coreStats
	for k, v := range coreStats {
		stateMap["cs_"+k] = v // Префикс для избежания коллизий
	}

	// Добавляем storyVars
	for k, v := range storyVars {
		stateMap["sv_"+k] = v // Префикс
	}

	// Добавляем globalFlags (сортируем для детерминизма)
	sortedFlags := make([]string, len(globalFlags))
	copy(sortedFlags, globalFlags)
	sort.Strings(sortedFlags)
	stateMap["gf"] = sortedFlags // Добавляем отсортированный срез

	// 2. Сериализация в канонический JSON
	// Сортировка ключей карты для канонического представления
	keys := make([]string, 0, len(stateMap))
	for k := range stateMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Собираем JSON вручную или используем библиотеку, гарантирующую канонический вывод
	// Простой ручной способ (может быть неэффективным для сложных структур):
	var sb strings.Builder
	sb.WriteString("{")
	for i, k := range keys {
		valueBytes, err := json.Marshal(stateMap[k])
		if err != nil {
			return "", fmt.Errorf("ошибка сериализации значения для ключа '%s': %w", k, err)
		}
		sb.WriteString(fmt.Sprintf("\"%s\":%s", k, string(valueBytes)))
		if i < len(keys)-1 {
			sb.WriteString(",")
		}
	}
	sb.WriteString("}")
	canonicalJSON := sb.String()

	// 3. Вычисление SHA256
	hasher := sha256.New()
	hasher.Write([]byte(canonicalJSON)) // Ошибки записи в хешер маловероятны
	hashBytes := hasher.Sum(nil)

	// 4. Представление в виде hex-строки
	return hex.EncodeToString(hashBytes), nil
}

// applyConsequences применяет последствия выбора к прогрессу игрока
// и проверяет условия Game Over.
// Возвращает имя стата, вызвавшего Game Over, и флаг Game Over.
func applyConsequences(progress *sharedModels.PlayerProgress, cons sharedModels.Consequences, setup *sharedModels.NovelSetupContent) (gameOverStat string, isGameOver bool) {
	if progress == nil || setup == nil {
		log.Println("ERROR: applyConsequences called with nil progress or setup")
		return "", false // Не можем применить последствия
	}

	// Инициализируем карты/слайсы, если они nil
	if progress.CoreStats == nil {
		progress.CoreStats = make(map[string]int)
	}
	if progress.StoryVariables == nil {
		progress.StoryVariables = make(map[string]interface{})
	}
	if progress.GlobalFlags == nil {
		progress.GlobalFlags = []string{}
	}

	// 1. Применяем изменения Core Stats
	if cons.CoreStatsChange != nil {
		for statName, change := range cons.CoreStatsChange {
			progress.CoreStats[statName] += change
		}
	}

	// 2. Применяем изменения Story Variables
	if cons.StoryVariables != nil {
		for varName, value := range cons.StoryVariables {
			if value == nil {
				// Если значение null, удаляем переменную
				delete(progress.StoryVariables, varName)
			} else {
				progress.StoryVariables[varName] = value
			}
		}
	}

	// 3. Удаляем Global Flags
	if len(cons.GlobalFlagsRemove) > 0 {
		flagsToRemove := make(map[string]struct{})
		for _, flag := range cons.GlobalFlagsRemove {
			flagsToRemove[flag] = struct{}{}
		}
		newFlags := make([]string, 0, len(progress.GlobalFlags))
		for _, flag := range progress.GlobalFlags {
			if _, found := flagsToRemove[flag]; !found {
				newFlags = append(newFlags, flag)
			}
		}
		progress.GlobalFlags = newFlags
	}

	// 4. Добавляем Global Flags (только уникальные)
	if len(cons.GlobalFlags) > 0 {
		existingFlags := make(map[string]struct{}, len(progress.GlobalFlags))
		for _, flag := range progress.GlobalFlags {
			existingFlags[flag] = struct{}{}
		}
		for _, flagToAdd := range cons.GlobalFlags {
			if _, found := existingFlags[flagToAdd]; !found {
				progress.GlobalFlags = append(progress.GlobalFlags, flagToAdd)
				existingFlags[flagToAdd] = struct{}{} // Добавляем в карту, чтобы избежать дублей из cons.GlobalFlags
			}
		}
	}

	// 5. Проверяем условия Game Over после всех изменений
	if setup.CoreStatsDefinition != nil {
		for statName, definition := range setup.CoreStatsDefinition {
			currentValue := progress.CoreStats[statName] // Получаем текущее значение
			if currentValue <= definition.Min {
				return statName, true // Game Over по минимальному значению
			}
			if currentValue >= definition.Max {
				return statName, true // Game Over по максимальному значению
			}
		}
	}

	return "", false // Не Game Over
}

// DeletePlayerProgress удаляет прогресс игрока для указанной истории.
func (s *gameplayServiceImpl) DeletePlayerProgress(ctx context.Context, userID, publishedStoryID uuid.UUID) error {
	s.logger.Info("Deleting player progress",
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()))

	// Проверяем, существует ли опубликованная история (опционально, но хорошо для валидации)
	_, err := s.publishedRepo.GetByID(ctx, publishedStoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sharedModels.ErrNotFound // История не найдена
		}
		return fmt.Errorf("ошибка проверки опубликованной истории: %w", err)
	}

	// TODO: Проверка прав? Должен ли пользователь удалять только свой прогресс?
	// Пока предполагаем, что userID из контекста уже проверен middleware

	// Удаляем прогресс
	err = s.playerProgressRepo.Delete(ctx, userID, publishedStoryID)
	if err != nil {
		// Логирование ошибки происходит внутри репозитория
		return fmt.Errorf("ошибка удаления прогресса игрока: %w", err)
	}

	// Успех (даже если прогресса не было, репозиторий возвращает nil)
	return nil
}

// createGenerationPayload создает payload для задачи генерации следующей сцены,
// используя сжатые ключи и сводки из предыдущего шага.
func createGenerationPayload(
	story *sharedModels.PublishedStory,
	progress *sharedModels.PlayerProgress,
	previousHash string,
	nextStateHash string,
	userChoiceDescription string,
	userChoiceText string,
) (sharedMessaging.GenerationTaskPayload, error) {
	// --- Шаг 1: Распарсить Config и Setup --- //
	var configMap map[string]interface{}
	if len(story.Config) > 0 {
		if err := json.Unmarshal(story.Config, &configMap); err != nil {
			log.Printf("WARN: Не удалось распарсить Config JSON для задачи генерации StoryID %s: %v", story.ID, err)
			return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("ошибка парсинга Config JSON: %w", err)
		}
	} else {
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("отсутствует Config в PublishedStory ID %s", story.ID)
	}

	var setupMap map[string]interface{}
	if len(story.Setup) > 0 {
		if err := json.Unmarshal(story.Setup, &setupMap); err != nil {
			log.Printf("WARN: Не удалось распарсить Setup JSON для задачи генерации StoryID %s: %v", story.ID, err)
			return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("ошибка парсинга Setup JSON: %w", err)
		}
	} else {
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("отсутствует Setup в PublishedStory ID %s", story.ID)
	}

	// --- Шаг 2: Собрать данные с сжатыми ключами --- //
	compressedInputData := make(map[string]interface{})

	compressedInputData["cfg"] = configMap // Config
	compressedInputData["stp"] = setupMap  // Setup

	// Добавляем текущие Core Stats
	if progress.CoreStats != nil {
		compressedInputData["cs"] = progress.CoreStats
	}

	// <<< Добавляем СВОДКИ из предыдущего шага >>>
	compressedInputData["pss"] = progress.LastStorySummary
	compressedInputData["pfd"] = progress.LastFutureDirection
	compressedInputData["pvis"] = progress.LastVarImpactSummary

	// <<< ДОБАВЛЯЕМ StoryVariables и GlobalFlags ПОСЛЕДНЕГО ШАГА (для генерации vis) >>>
	if progress.StoryVariables != nil {
		compressedInputData["sv"] = progress.StoryVariables // Передаем текущие (last) sv
	}
	if progress.GlobalFlags != nil {
		// Передаем текущие (last) gf как есть (уже []string)
		// Сортировка не обязательна для передачи AI, но не помешает для консистентности
		sortedFlags := make([]string, len(progress.GlobalFlags))
		copy(sortedFlags, progress.GlobalFlags)
		sort.Strings(sortedFlags)
		compressedInputData["gf"] = sortedFlags // Передаем текущие (last) gf
	} else {
		compressedInputData["gf"] = []string{}
	}

	// Добавляем информацию о выборе пользователя с сжатыми ключами
	type CompressedUserChoiceContext struct {
		Desc string `json:"d"` // description
		Text string `json:"t"` // choice_text
	}
	compressedInputData["uc"] = CompressedUserChoiceContext{
		Desc: userChoiceDescription,
		Text: userChoiceText,
	}

	// TODO: Добавить другие необходимые поля из NovelState, если они нужны промпту
	// Например, язык, текущий этап и т.д. Их можно извлечь из configMap или setupMap
	// compressedInputData["lang"] = configMap["language"] // Пример
	// compressedInputData["stage"] = "choices_ready" // Пример

	// --- Шаг 3: Формируем итоговый payload --- //
	payload := sharedMessaging.GenerationTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           progress.UserID.String(),
		PublishedStoryID: story.ID.String(),
		PromptType:       sharedMessaging.PromptTypeNovelCreator,
		InputData:        compressedInputData,
		StateHash:        nextStateHash, // Хеш состояния, ДЛЯ которого генерируем сцену
		// <<< Возможно, стоит передавать и previousHash в задаче? Пока нет. >>>
	}

	return payload, nil
}
