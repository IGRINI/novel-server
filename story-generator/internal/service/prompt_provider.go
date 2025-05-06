package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
	"go.uber.org/zap"

	"novel-server/shared/interfaces"
	"novel-server/shared/models"
)

var ErrPromptNotFoundInCache = errors.New("prompt not found in cache")

// PromptProvider provides access to prompt data, caching it locally.
type PromptProvider struct {
	repo              interfaces.PromptRepository        // Repository for initial load
	dynamicConfigRepo interfaces.DynamicConfigRepository // Repository for dynamic configuration
	cacheLock         sync.RWMutex                       // Mutex for map operations (alternative to sync.Map)
	cacheMap          map[string]map[string]string       // Cache: map[language]map[key]content
	logger            *zap.Logger                        // Use *zap.Logger
	db                interfaces.DBTX                    // <<< ДОБАВЛЕНО: Пул соединений БД >>>
}

// NewPromptProvider creates a new PromptProvider.
func NewPromptProvider(repo interfaces.PromptRepository, dynamicConfigRepo interfaces.DynamicConfigRepository, logger *zap.Logger, dbPool interfaces.DBTX) *PromptProvider { // <<< ДОБАВЛЕНО: dbPool interfaces.DBTX >>>
	if repo == nil {
		log.Fatal().Msg("PromptRepository is nil for PromptProvider")
	}
	if dynamicConfigRepo == nil {
		log.Fatal().Msg("DynamicConfigRepository is nil for PromptProvider")
	}
	if logger == nil {
		log.Fatal().Msg("Logger is nil for PromptProvider")
	}
	return &PromptProvider{
		repo:              repo,
		dynamicConfigRepo: dynamicConfigRepo,
		cacheMap:          make(map[string]map[string]string),
		logger:            logger.Named("PromptProvider"),
		db:                dbPool, // <<< ДОБАВЛЕНО: Сохраняем пул >>>
	}
}

// LoadInitialPrompts loads all prompts from the database into the cache.
// This should be called once at startup.
func (p *PromptProvider) LoadInitialPrompts(ctx context.Context) error {
	p.logger.Info("Loading initial prompts into cache...")
	prompts, err := p.repo.GetAll(ctx)
	if err != nil {
		p.logger.Error("Failed to get all prompts from repository", zap.Error(err))
		return fmt.Errorf("failed to get all prompts from repository: %w", err)
	}

	newCache := make(map[string]map[string]string)
	count := 0
	for _, prompt := range prompts {
		if _, ok := newCache[prompt.Language]; !ok {
			newCache[prompt.Language] = make(map[string]string)
		}
		newCache[prompt.Language][prompt.Key] = prompt.Content
		count++
	}

	p.cacheLock.Lock()
	p.cacheMap = newCache
	p.cacheLock.Unlock()

	p.logger.Info("Initial prompts loaded successfully into cache", zap.Int("count", count))
	return nil
}

// UpdateCache updates the cache based on a received PromptEvent.
func (p *PromptProvider) UpdateCache(event interfaces.PromptEvent) {
	p.cacheLock.Lock()
	defer p.cacheLock.Unlock()

	langCache, langExists := p.cacheMap[event.Language]
	sugar := p.logger.Sugar()

	switch event.EventType {
	case interfaces.PromptEventTypeCreated, interfaces.PromptEventTypeUpdated:
		if !langExists {
			langCache = make(map[string]string)
			p.cacheMap[event.Language] = langCache
			sugar.Infof("Added new language '%s' to prompt cache", event.Language)
		}
		langCache[event.Key] = event.Content
		sugar.Infof("Prompt cache updated: %s - %s/%s", event.EventType, event.Language, event.Key)

	case interfaces.PromptEventTypeDeleted:
		if langExists {
			delete(langCache, event.Key)
			if len(langCache) == 0 {
				delete(p.cacheMap, event.Language)
				sugar.Infof("Removed empty language '%s' from prompt cache", event.Language)
			}
			sugar.Infof("Prompt removed from cache: %s - %s/%s", event.EventType, event.Language, event.Key)
		} else {
			sugar.Warnf("Received delete event for non-existent language '%s' in cache (key: %s)", event.Language, event.Key)
		}
	default:
		sugar.Warnf("Received unknown prompt event type: %s", event.EventType)
	}
}

// GetPrompt retrieves a prompt content from the cache by key and language.
// It also replaces placeholders like {{NPC_COUNT}} with values from dynamic config.
// If the prompt is not found for the requested language, it tries to fall back to English ('en').
func (p *PromptProvider) GetPrompt(ctx context.Context, key string, language string) (string, error) {
	const fallbackLanguage = "en" // Define fallback language

	p.logger.Debug("GetPrompt requested", zap.String("key", key), zap.String("language", language))

	p.cacheLock.RLock()
	langCache, langOk := p.cacheMap[language]
	var content string
	var keyOk bool
	if langOk {
		content, keyOk = langCache[key]
	}
	p.cacheLock.RUnlock()

	// If not found in the requested language, try fallback language
	if (!langOk || !keyOk) && language != fallbackLanguage {
		p.logger.Warn("Prompt not found in requested language, trying fallback",
			zap.String("key", key),
			zap.String("requested_language", language),
			zap.String("fallback_language", fallbackLanguage))

		p.cacheLock.RLock()
		langCache, langOk = p.cacheMap[fallbackLanguage]
		if langOk {
			content, keyOk = langCache[key]
		}
		p.cacheLock.RUnlock()

		if langOk && keyOk {
			p.logger.Info("Using fallback language prompt",
				zap.String("key", key),
				zap.String("language_used", fallbackLanguage))
			language = fallbackLanguage // Update language variable to reflect the actual language used for placeholder replacement later
		}
	}

	if !langOk || !keyOk {
		p.logger.Error("Prompt not found in cache, including fallback", zap.String("key", key), zap.String("requested_language", language)) // Changed Warn to Error as it's a definitive failure now
		return "", fmt.Errorf("%w: key='%s', lang='%s'", ErrPromptNotFoundInCache, key, language)                                           // Return specific error with key/lang
	}

	if strings.Contains(content, "{{NPC_COUNT}}") {
		npcCount := 10
		configKey := "generation.npc_count"
		dynConf, err := p.dynamicConfigRepo.GetByKey(ctx, p.db, configKey)
		if err != nil {
			if !errors.Is(err, models.ErrNotFound) {
				p.logger.Error("Failed to get dynamic config for NPC count, using default",
					zap.String("key", configKey),
					zap.Error(err),
				)
			} else {
				p.logger.Warn("Dynamic config for NPC count not found, using default",
					zap.String("key", configKey),
					zap.Int("default_value", npcCount),
				)
			}
		} else if dynConf != nil && dynConf.Value != "" {
			if parsedCount, convErr := strconv.Atoi(dynConf.Value); convErr == nil && parsedCount > 0 {
				npcCount = parsedCount
				p.logger.Debug("Using dynamic config for NPC count", zap.String("key", configKey), zap.Int("value", npcCount))
			} else {
				p.logger.Error("Failed to parse dynamic config value for NPC count as positive integer, using default",
					zap.String("key", configKey),
					zap.String("value", dynConf.Value),
					zap.Error(convErr),
				)
			}
		}

		content = strings.ReplaceAll(content, "{{NPC_COUNT}}", strconv.Itoa(npcCount))
	}

	if strings.Contains(content, "{{CHOICE_COUNT}}") {
		choiceCount := 10
		configKey := "generation.choice_count"
		dynConf, err := p.dynamicConfigRepo.GetByKey(ctx, p.db, configKey)
		if err != nil {
			if !errors.Is(err, models.ErrNotFound) {
				p.logger.Error("Failed to get dynamic config for CHOICE count, using default",
					zap.String("key", configKey),
					zap.Error(err),
				)
			} else {
				p.logger.Warn("Dynamic config for CHOICE count not found, using default",
					zap.String("key", configKey),
					zap.Int("default_value", choiceCount),
				)
			}
		} else if dynConf != nil && dynConf.Value != "" {
			if parsedCount, convErr := strconv.Atoi(dynConf.Value); convErr == nil && parsedCount > 0 {
				choiceCount = parsedCount
				p.logger.Debug("Using dynamic config for CHOICE count", zap.String("key", configKey), zap.Int("value", choiceCount))
			} else {
				p.logger.Error("Failed to parse dynamic config value for CHOICE count as positive integer, using default",
					zap.String("key", configKey),
					zap.String("value", dynConf.Value),
					zap.Error(convErr),
				)
			}
		}
		content = strings.ReplaceAll(content, "{{CHOICE_COUNT}}", strconv.Itoa(choiceCount))
	}

	return content, nil
}
