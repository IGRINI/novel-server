package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"novel-server/admin-service/internal/config"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"
)

// PromptDataForTemplate используется для передачи данных в шаблон prompt_edit.html
// Содержит только необходимые для шаблона поля.
type PromptDataForTemplate struct {
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PromptService определяет методы для бизнес-логики работы с промптами.
type PromptService interface {
	// ... (остальные методы интерфейса)
	GetPrompt(ctx context.Context, key, language string) (*models.Prompt, error)
	GetPromptsByKey(ctx context.Context, key string) (map[string]PromptDataForTemplate, error)
	UpsertPrompt(ctx context.Context, key, language, content string) (*models.Prompt, error)
	DeletePromptByKeyAndLang(ctx context.Context, key, language string) error
	DeletePromptsByKey(ctx context.Context, key string) error
	ListPromptKeys(ctx context.Context) ([]string, error)
	CreatePromptKey(ctx context.Context, key string) error
}

type PromptServiceImpl struct {
	repo               interfaces.PromptRepository
	publisher          interfaces.PromptEventPublisher
	supportedLanguages []string
}

func NewPromptService(
	cfg *config.Config,
	repo interfaces.PromptRepository,
	publisher interfaces.PromptEventPublisher,
) *PromptServiceImpl {
	if repo == nil {
		log.Fatal().Msg("PromptRepository is nil for PromptService")
	}
	if publisher == nil {
		log.Fatal().Msg("PromptEventPublisher is nil for PromptService")
	}
	if cfg == nil {
		log.Fatal().Msg("Config is nil for PromptService")
	}
	if len(cfg.SupportedLanguages) == 0 {
		log.Warn().Msg("SupportedLanguages list is empty in config for PromptService")
		// Можно установить значение по умолчанию или оставить пустым, если это допустимо
	}
	return &PromptServiceImpl{
		repo:               repo,
		publisher:          publisher,
		supportedLanguages: cfg.SupportedLanguages,
	}
}

// ListPromptKeys возвращает список уникальных ключей промптов.
func (s *PromptServiceImpl) ListPromptKeys(ctx context.Context) ([]string, error) {
	keys, err := s.repo.ListKeys(ctx)
	if err != nil {
		// Логирование ошибки происходит в репозитории
		return nil, fmt.Errorf("failed to list prompt keys from repo: %w", err)
	}
	return keys, nil
}

// GetPromptsByKey возвращает все языковые версии промпта для одного ключа.
// <<< ИЗМЕНЕНО: Возвращает map[string]PromptDataForTemplate >>>
func (s *PromptServiceImpl) GetPromptsByKey(ctx context.Context, key string) (map[string]PromptDataForTemplate, error) {
	prompts, err := s.repo.GetAllPromptsByKey(ctx, key)
	if err != nil {
		// Ошибка уже залогирована репозиторием
		return nil, err // Возвращаем ошибку репозитория
	}

	// Конвертируем в мапу для шаблона
	promptsMap := make(map[string]PromptDataForTemplate, len(prompts))
	for _, p := range prompts {
		if p != nil {
			promptsMap[p.Language] = PromptDataForTemplate{
				Content:   p.Content,
				UpdatedAt: p.UpdatedAt,
			}
		}
	}

	log.Debug().Str("key", key).Int("language_count", len(promptsMap)).Msg("Successfully retrieved prompts by key")
	return promptsMap, nil
}

// GetPrompt получает конкретную языковую версию промпта.
func (s *PromptServiceImpl) GetPrompt(ctx context.Context, key, language string) (*models.Prompt, error) {
	prompt, err := s.repo.GetByKeyAndLanguage(ctx, key, language)
	if err != nil {
		return nil, fmt.Errorf("failed to get prompt from repo: %w", err)
	}
	return prompt, nil
}

// GetPromptByID получает промпт по его уникальному ID.
func (s *PromptServiceImpl) GetPromptByID(ctx context.Context, id int64) (*models.Prompt, error) {
	prompt, err := s.repo.GetByID(ctx, id) // Предполагаем, что репозиторий имеет этот метод
	if err != nil {
		// Обработка ошибки "не найдено" может быть в репозитории или здесь
		if errors.Is(err, models.ErrNotFound) { // Пример обработки ошибки NotFound
			log.Warn().Int64("id", id).Msg("Prompt not found by ID")
			return nil, fmt.Errorf("prompt with ID %d not found: %w", id, err)
		}
		log.Error().Err(err).Int64("id", id).Msg("Failed to get prompt by ID from repo")
		return nil, fmt.Errorf("failed to get prompt by ID %d from repo: %w", id, err)
	}
	return prompt, nil
}

// GetAllPrompts возвращает список всех промптов из репозитория.
func (s *PromptServiceImpl) GetAllPrompts(ctx context.Context) ([]*models.Prompt, error) {
	prompts, err := s.repo.GetAll(ctx) // Используем существующий метод репозитория
	if err != nil {
		log.Error().Err(err).Msg("Failed to get all prompts from repo")
		return nil, fmt.Errorf("failed to get all prompts from repo: %w", err)
	}
	return prompts, nil
}

// CreatePromptKey создает записи для нового ключа промпта для всех поддерживаемых языков.
func (s *PromptServiceImpl) CreatePromptKey(ctx context.Context, key string) error {
	if len(s.supportedLanguages) == 0 {
		log.Warn().Str("key", key).Msg("Cannot create prompt key, no supported languages configured")
		return fmt.Errorf("no supported languages configured to create prompt key")
	}

	promptsToCreate := make([]*models.Prompt, 0, len(s.supportedLanguages))
	for _, lang := range s.supportedLanguages {
		promptsToCreate = append(promptsToCreate, &models.Prompt{
			Key:      key,
			Language: lang,
			Content:  "",
		})
	}

	err := s.repo.CreateBatch(ctx, promptsToCreate)
	if err != nil {
		// Ошибка ErrPromptKeyAlreadyExists обрабатывается в репозитории и логируется как Warn
		if errors.Is(err, models.ErrAlreadyExists) {
			return fmt.Errorf("prompt key '%s' already exists: %w", key, err)
		}
		return fmt.Errorf("failed to create prompt key '%s' in repo: %w", key, err)
	}

	// Публикуем событие для КАЖДОГО созданного языка?
	// Или одно событие "KeyCreated"?
	// Пока пропустим публикацию здесь, возможно, она нужна в Upsert.
	log.Info().Str("key", key).Strs("languages", s.supportedLanguages).Msg("Prompt key created with empty languages")
	return nil
}

// UpsertPrompt создает или обновляет промпт.
func (s *PromptServiceImpl) UpsertPrompt(ctx context.Context, key, language, content string) (*models.Prompt, error) {
	// Создаем логгер с контекстом
	ctxLog := log.With().Str("key", key).Str("language", language).Logger()
	// TODO: Добавить валидацию языка по списку SupportedLanguages из конфига?

	prompt := &models.Prompt{
		Key:      key,
		Language: language,
		Content:  content,
		// Comment, IsActive, Version - не управляются этим методом
	}

	// Перед вызовом Upsert проверяем, существует ли уже запись (для определения типа события)
	_, getErr := s.repo.GetByKeyAndLanguage(ctx, key, language)
	isUpdate := getErr == nil

	err := s.repo.Upsert(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert prompt in repo: %w", err)
	}

	// Публикуем событие
	eventType := interfaces.PromptEventTypeCreated
	if isUpdate {
		eventType = interfaces.PromptEventTypeUpdated
	}
	event := interfaces.PromptEvent{
		EventType: eventType,
		Key:       prompt.Key,
		Language:  prompt.Language,
		Content:   prompt.Content,
		ID:        prompt.ID,
	}
	if pubErr := s.publisher.PublishPromptEvent(ctx, event); pubErr != nil {
		// Используем логгер с контекстом
		ctxLog.Error().Err(pubErr).Interface("event", event).Msgf("Failed to publish prompt %s event", strings.ToLower(string(eventType)))
	}

	// Используем логгер с контекстом
	ctxLog.Info().Msg("Prompt upserted successfully")
	// Возвращаем обновленный промпт (с ID и UpdatedAt)
	return prompt, nil
}

// DeletePromptsByKey удаляет все языковые версии для ключа.
func (s *PromptServiceImpl) DeletePromptsByKey(ctx context.Context, key string) error {
	err := s.repo.DeleteByKey(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to delete prompts by key '%s' from repo: %w", key, err)
	}

	// Публикуем событие "KeyDeleted"?
	// Или события для каждого языка? Текущий формат события не очень подходит для ключа.
	// Пока не публикуем событие для удаления ключа.
	log.Info().Str("key", key).Msg("Prompts deleted by key")
	return nil
}

// DeletePromptByID удаляет конкретную языковую версию промпта по ID.
func (s *PromptServiceImpl) DeletePromptByID(ctx context.Context, id int64) error {
	// TODO: Решить, нужно ли публиковать событие при удалении по ID.
	// Если да, нужно получить данные промпта *перед* удалением.
	log.Debug().Int64("id", id).Msg("Attempting to delete prompt by ID")

	err := s.repo.DeleteByID(ctx, id)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) { // Пример обработки ошибки NotFound
			log.Warn().Int64("id", id).Msg("Prompt not found for deletion by ID")
			return fmt.Errorf("prompt with ID %d not found for deletion: %w", id, err)
		}
		log.Error().Err(err).Int64("id", id).Msg("Failed to delete prompt by ID from repo")
		return fmt.Errorf("failed to delete prompt by ID %d from repo: %w", id, err)
	}

	log.Info().Int64("id", id).Msg("Prompt deleted by ID")
	return nil
}

// DeletePromptByKeyAndLang удаляет конкретную языковую версию промпта по ключу и языку.
func (s *PromptServiceImpl) DeletePromptByKeyAndLang(ctx context.Context, key, language string) error {
	// Создаем логгер с контекстом
	ctxLog := log.With().Str("key", key).Str("language", language).Logger()
	ctxLog.Debug().Msg("Attempting to delete prompt by key and language")

	// Получаем промпт перед удалением, чтобы иметь данные для события
	prompt, getErr := s.repo.GetByKeyAndLanguage(ctx, key, language)
	if getErr != nil {
		if errors.Is(getErr, models.ErrNotFound) {
			ctxLog.Warn().Msg("Prompt not found for deletion by key and language") // Контекст уже добавлен в ctxLog
			// Если не найдено, считаем удаление успешным (или возвращаем ошибку, если это важно)
			return nil // Или: return fmt.Errorf("prompt %s/%s not found: %w", key, language, getErr)
		}
		ctxLog.Error().Err(getErr).Msg("Failed to get prompt before deletion") // Контекст уже добавлен в ctxLog
		// Продолжаем попытку удаления, даже если не смогли получить для события
	}

	err := s.repo.DeleteByKeyAndLanguage(ctx, key, language)
	if err != nil {
		// Ошибку ErrNotFound мы уже обработали выше при попытке получения
		ctxLog.Error().Err(err).Msg("Failed to delete prompt by key and language from repo") // Контекст уже добавлен в ctxLog
		return fmt.Errorf("failed to delete prompt %s/%s from repo: %w", key, language, err)
	}

	ctxLog.Info().Msg("Prompt deleted by key and language") // Контекст уже добавлен в ctxLog

	// Публикуем событие, если удалось получить промпт ранее
	if getErr == nil && prompt != nil {
		event := interfaces.PromptEvent{
			EventType: interfaces.PromptEventTypeDeleted,
			ID:        prompt.ID, // Используем ID удаленного промпта
			Key:       prompt.Key,
			Language:  prompt.Language,
			// Content можно не передавать при удалении (остается пустым)
		}
		if pubErr := s.publisher.PublishPromptEvent(ctx, event); pubErr != nil {
			ctxLog.Error().Err(pubErr).Interface("event", event).Msg("Failed to publish prompt deleted event") // Контекст уже добавлен в ctxLog
			// Не возвращаем ошибку публикации, т.к. основная операция (удаление) прошла успешно
		}
	}

	return nil
}
