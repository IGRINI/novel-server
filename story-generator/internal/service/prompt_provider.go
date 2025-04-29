package service

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/rs/zerolog/log"
	"go.uber.org/zap"

	"novel-server/shared/interfaces"
)

var ErrPromptNotFoundInCache = errors.New("prompt not found in cache")

// PromptProvider provides access to prompt data, caching it locally.
type PromptProvider struct {
	repo      interfaces.PromptRepository  // Repository for initial load
	cache     sync.Map                     // Concurrent-safe cache: map[string]*models.Prompt
	cacheLock sync.RWMutex                 // Mutex for map operations (alternative to sync.Map)
	cacheMap  map[string]map[string]string // Cache: map[language]map[key]content
	logger    *zap.Logger                  // Use *zap.Logger
}

// NewPromptProvider creates a new PromptProvider.
func NewPromptProvider(repo interfaces.PromptRepository, logger *zap.Logger) *PromptProvider {
	if repo == nil {
		log.Fatal().Msg("PromptRepository is nil for PromptProvider")
	}
	if logger == nil {
		log.Fatal().Msg("Logger is nil for PromptProvider")
	}
	return &PromptProvider{
		repo:     repo,
		cacheMap: make(map[string]map[string]string),
		logger:   logger.Named("PromptProvider"),
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
func (p *PromptProvider) GetPrompt(ctx context.Context, key string, language string) (string, error) {
	p.cacheLock.RLock()
	defer p.cacheLock.RUnlock()

	if langCache, ok := p.cacheMap[language]; ok {
		if content, ok := langCache[key]; ok {
			return content, nil
		}
	}

	p.logger.Warn("Prompt not found in cache", zap.String("key", key), zap.String("language", language))
	return "", ErrPromptNotFoundInCache
}
