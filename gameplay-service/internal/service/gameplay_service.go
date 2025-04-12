package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"novel-server/gameplay-service/internal/messaging"
	"novel-server/gameplay-service/internal/models"
	"novel-server/gameplay-service/internal/repository"
	sharedMessaging "novel-server/shared/messaging"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// GameplayService определяет интерфейс для бизнес-логики gameplay.
type GameplayService interface {
	GenerateInitialStory(ctx context.Context, userID uint64, initialPrompt string) (*models.StoryConfig, error)
	ReviseDraft(ctx context.Context, id uuid.UUID, userID uint64, revisionPrompt string) error // Возвращает только ошибку
	GetStoryConfig(ctx context.Context, id uuid.UUID, userID uint64) (*models.StoryConfig, error)
	// TODO: Добавить проверку на наличие активной генерации для UserID?
	// TODO: Добавить методы для обработки игрового цикла (получение карточек, выбор и т.д.)
}

type gameplayServiceImpl struct {
	repo      repository.StoryConfigRepository
	publisher messaging.TaskPublisher
	// TODO: Добавить зависимость для проверки активных задач генерации (если нужно)
}

func NewGameplayService(repo repository.StoryConfigRepository, publisher messaging.TaskPublisher) GameplayService {
	return &gameplayServiceImpl{repo: repo, publisher: publisher}
}

// GenerateInitialStory создает новую запись StoryConfig и отправляет задачу на генерацию.
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
		return nil, ErrUserHasActiveGeneration // Используем ту же ошибку
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
		return ErrCannotRevise
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
		return ErrUserHasActiveGeneration
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

func (s *gameplayServiceImpl) GetStoryConfig(ctx context.Context, id uuid.UUID, userID uint64) (*models.StoryConfig, error) {
	config, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		// Обработка ошибок (включая NotFound) происходит в репозитории
		return nil, fmt.Errorf("ошибка получения StoryConfig в сервисе: %w", err)
	}
	return config, nil
}

// TODO: Добавить реализацию hasActiveGeneration(ctx, userID), если нужно
// func (s *gameplayServiceImpl) hasActiveGeneration(ctx context.Context, userID uint64) bool {
// 	 // Логика проверки статусов в БД или через запрос к другому сервису/кэшу
// 	 return false
// }
