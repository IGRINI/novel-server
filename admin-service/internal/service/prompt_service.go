package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"

	"novel-server/admin-service/internal/config"
	"novel-server/shared/database"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"
)

type PromptService struct {
	repo               interfaces.PromptRepository
	publisher          interfaces.PromptEventPublisher
	supportedLanguages []string
}

func NewPromptService(
	cfg *config.Config,
	repo interfaces.PromptRepository,
	publisher interfaces.PromptEventPublisher,
) *PromptService {
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
	return &PromptService{
		repo:               repo,
		publisher:          publisher,
		supportedLanguages: cfg.SupportedLanguages,
	}
}

// ListPromptKeys возвращает список уникальных ключей промптов.
func (s *PromptService) ListPromptKeys(ctx context.Context) ([]string, error) {
	keys, err := s.repo.ListKeys(ctx)
	if err != nil {
		// Логирование ошибки происходит в репозитории
		return nil, fmt.Errorf("failed to list prompt keys from repo: %w", err)
	}
	return keys, nil
}

// GetPromptsByKey возвращает все языковые версии для ключа в виде map[language]*models.Prompt.
func (s *PromptService) GetPromptsByKey(ctx context.Context, key string) (map[string]*models.Prompt, error) {
	promptsList, err := s.repo.FindByKey(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get prompts by key '%s' from repo: %w", key, err)
	}

	promptsMap := make(map[string]*models.Prompt)
	for _, p := range promptsList {
		promptsMap[p.Language] = p
	}

	return promptsMap, nil
}

// GetPrompt получает конкретную языковую версию промпта.
func (s *PromptService) GetPrompt(ctx context.Context, key, language string) (*models.Prompt, error) {
	prompt, err := s.repo.GetByKeyAndLanguage(ctx, key, language)
	if err != nil {
		return nil, fmt.Errorf("failed to get prompt from repo: %w", err)
	}
	return prompt, nil
}

// GetPromptByID получает промпт по его уникальному ID.
func (s *PromptService) GetPromptByID(ctx context.Context, id int64) (*models.Prompt, error) {
	prompt, err := s.repo.GetByID(ctx, id) // Предполагаем, что репозиторий имеет этот метод
	if err != nil {
		// Обработка ошибки "не найдено" может быть в репозитории или здесь
		if errors.Is(err, database.ErrNotFound) { // Пример обработки ошибки NotFound
			log.Warn().Int64("id", id).Msg("Prompt not found by ID")
			return nil, fmt.Errorf("prompt with ID %d not found: %w", id, err)
		}
		log.Error().Err(err).Int64("id", id).Msg("Failed to get prompt by ID from repo")
		return nil, fmt.Errorf("failed to get prompt by ID %d from repo: %w", id, err)
	}
	return prompt, nil
}

// GetAllPrompts возвращает список всех промптов из репозитория.
func (s *PromptService) GetAllPrompts(ctx context.Context) ([]*models.Prompt, error) {
	prompts, err := s.repo.GetAll(ctx) // Используем существующий метод репозитория
	if err != nil {
		log.Error().Err(err).Msg("Failed to get all prompts from repo")
		return nil, fmt.Errorf("failed to get all prompts from repo: %w", err)
	}
	return prompts, nil
}

// CreatePromptKey создает записи для нового ключа промпта для всех поддерживаемых языков.
func (s *PromptService) CreatePromptKey(ctx context.Context, key string) error {
	if len(s.supportedLanguages) == 0 {
		log.Warn().Str("key", key).Msg("Cannot create prompt key, no supported languages configured")
		return fmt.Errorf("no supported languages configured to create prompt key")
	}

	promptsToCreate := make([]*models.Prompt, 0, len(s.supportedLanguages))
	for _, lang := range s.supportedLanguages {
		promptsToCreate = append(promptsToCreate, &models.Prompt{
			Key:      key,
			Language: lang,
			Content:  "", // Начальное значение - пустая строка
		})
	}

	err := s.repo.CreateBatch(ctx, promptsToCreate)
	if err != nil {
		// Ошибка ErrPromptKeyAlreadyExists обрабатывается в репозитории и логируется как Warn
		if errors.Is(err, database.ErrPromptKeyAlreadyExists) {
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

// UpsertPrompt создает или обновляет конкретную языковую версию промпта.
func (s *PromptService) UpsertPrompt(ctx context.Context, key, language, content string) (*models.Prompt, error) {
	prompt := &models.Prompt{
		Key:      key,
		Language: language,
		Content:  content,
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
		ID:        prompt.ID, // ID и время обновляются в repo.Upsert
	}
	if pubErr := s.publisher.PublishPromptEvent(ctx, event); pubErr != nil {
		log.Error().Err(pubErr).Interface("event", event).Msgf("Failed to publish prompt %s event", strings.ToLower(string(eventType)))
	}

	return prompt, nil
}

// DeletePromptsByKey удаляет все языковые версии для ключа.
func (s *PromptService) DeletePromptsByKey(ctx context.Context, key string) error {
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
func (s *PromptService) DeletePromptByID(ctx context.Context, id int64) error {
	// Возможно, стоит получить промпт перед удалением, чтобы опубликовать событие?
	// prompt, err := s.repo.GetByID(ctx, id)
	// if err != nil { ... }

	err := s.repo.DeleteByID(ctx, id) // Предполагаем, что репозиторий имеет этот метод
	if err != nil {
		if errors.Is(err, database.ErrNotFound) { // Пример обработки ошибки NotFound
			log.Warn().Int64("id", id).Msg("Prompt not found for deletion by ID")
			// Можно вернуть nil, если "не найдено" не считается ошибкой при удалении
			// return nil
			return fmt.Errorf("prompt with ID %d not found for deletion: %w", id, err)
		}
		log.Error().Err(err).Int64("id", id).Msg("Failed to delete prompt by ID from repo")
		return fmt.Errorf("failed to delete prompt by ID %d from repo: %w", id, err)
	}

	// TODO: Решить, нужно ли публиковать событие при удалении по ID.
	// Если да, нужно получить данные промпта *перед* удалением.
	// event := interfaces.PromptEvent{
	// 	EventType: interfaces.PromptEventTypeDeleted,
	// 	ID:        id,
	//  Key:       prompt.Key, // Нужны данные удаленного промпта
	//  Language:  prompt.Language,
	// }
	// if pubErr := s.publisher.PublishPromptEvent(ctx, event); pubErr != nil { ... }

	log.Info().Int64("id", id).Msg("Prompt deleted by ID")
	return nil
}
