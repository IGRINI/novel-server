package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	sharedMessaging "novel-server/shared/messaging"
	"novel-server/shared/models"
	"sort"
	"strconv"
	"strings"

	"novel-server/shared/utils"

	"github.com/google/uuid"
)

// --- Helper Functions ---

// calculateStateHash calculates a deterministic state hash, including the previous state hash.
func calculateStateHash(previousHash string, coreStats map[string]int) (string, error) {
	stateMap := make(map[string]interface{})

	stateMap["_ph"] = previousHash

	for k, v := range coreStats {
		stateMap["cs_"+k] = v
	}

	keys := make([]string, 0, len(stateMap))
	for k := range stateMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString("{")
	for i, k := range keys {
		valueBytes, err := json.Marshal(stateMap[k])
		if err != nil {
			log.Printf("ERROR calculating state hash: failed to marshal value for key '%s': %v", k, err)
			return "", fmt.Errorf("error serializing value for key '%s': %w", k, err)
		}
		sb.WriteString(fmt.Sprintf("\"%s\":%s", k, string(valueBytes)))
		if i < len(keys)-1 {
			sb.WriteString(",")
		}
	}
	sb.WriteString("}")
	canonicalJSON := sb.String()

	hasher := sha256.New()
	hasher.Write([]byte(canonicalJSON))
	hashBytes := hasher.Sum(nil)

	return hex.EncodeToString(hashBytes), nil
}

// applyConsequences applies consequences of choice to player progress
// and checks Game Over conditions.
// Returns stat name causing Game Over and Game Over flag.
func applyConsequences(progress *models.PlayerProgress, cons models.Consequences, setup *models.NovelSetupContent) (gameOverStat string, isGameOver bool) {
	if progress == nil || setup == nil {
		log.Println("ERROR: applyConsequences called with nil progress or setup")
		return "", false
	}

	if progress.CoreStats == nil {
		progress.CoreStats = make(map[string]int)
	}

	if cons.CoreStatsChange != nil {
		// build sorted stat keys from setup definition
		statKeys := make([]string, 0, len(setup.CoreStatsDefinition))
		for name := range setup.CoreStatsDefinition {
			statKeys = append(statKeys, name)
		}
		sort.Strings(statKeys)
		for key, change := range cons.CoreStatsChange {
			if idx, err := strconv.Atoi(key); err == nil && idx >= 0 && idx < len(statKeys) {
				statName := statKeys[idx]
				progress.CoreStats[statName] += change
			} else {
				// fallback: treat key as stat name
				progress.CoreStats[key] += change
			}
		}
	}

	if setup.CoreStatsDefinition != nil {
		for statName, definition := range setup.CoreStatsDefinition {
			currentValue, exists := progress.CoreStats[statName]
			if !exists {
				currentValue = 0
			}

			if definition.Go.Min && currentValue <= 0 {
				return statName, true
			}

			if definition.Go.Max && currentValue >= 100 {
				return statName, true
			}
		}
	}

	return "", false
}

// createGenerationPayload creates the payload for the next scene generation task,
// using compressed keys and summaries from the previous step.
func createGenerationPayload(
	userID uuid.UUID,
	story *models.PublishedStory,
	progress *models.PlayerProgress,
	gameState *models.PlayerGameState,
	madeChoicesInfo []models.UserChoiceInfo,
	currentStateHash string,
	language string,
	promptType models.PromptType,
) (sharedMessaging.GenerationTaskPayload, error) {

	if story.Config == nil || story.Setup == nil {
		log.Printf("ERROR: Story Config or Setup is nil for StoryID %s", story.ID)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("story config or setup is nil")
	}
	var fullConfig models.Config
	if err := DecodeStrictJSON(story.Config, &fullConfig); err != nil {
		log.Printf("WARN: Failed to parse Config JSON for generation task StoryID %s: %v", story.ID, err)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error parsing Config JSON: %w", err)
	}
	var fullSetup models.NovelSetupContent
	if err := DecodeStrictJSON(story.Setup, &fullSetup); err != nil {
		log.Printf("WARN: Failed to parse Setup JSON for generation task StoryID %s: %v", story.ID, err)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error parsing Setup JSON: %w", err)
	}

	// Используем универсальный форматтер для динамического состояния
	userInputString := utils.FormatFullGameStateToString(
		fullConfig,
		fullSetup,
		progress.CoreStats,
		madeChoicesInfo,
		derefStringPtr(progress.LastStorySummary),
		derefStringPtr(progress.LastFutureDirection),
		derefStringPtr(progress.LastVarImpactSummary),
		progress.EncounteredCharacters,
		story.IsAdultContent,
	)

	payload := sharedMessaging.GenerationTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           userID.String(),
		PublishedStoryID: story.ID.String(),
		PromptType:       promptType,
		UserInput:        userInputString,
		StateHash:        currentStateHash,
		Language:         language,
	}

	return payload, nil
}

// derefStringPtr безопасное разыменовывание *string
func derefStringPtr(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}

// createInitialSceneGenerationPayload создает payload для генерации *первой* сцены истории.
// Она использует только Config и Setup истории, без PlayerProgress.
func createInitialSceneGenerationPayload(
	userID uuid.UUID,
	story *models.PublishedStory,
	language string,
) (sharedMessaging.GenerationTaskPayload, error) {

	if story.Config == nil || story.Setup == nil {
		log.Printf("ERROR: Story Config or Setup is nil for initial scene generation, StoryID %s", story.ID)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("story config or setup is nil")
	}
	var fullConfig models.Config
	if err := DecodeStrictJSON(story.Config, &fullConfig); err != nil {
		log.Printf("WARN: Failed to parse Config JSON for initial scene task StoryID %s: %v", story.ID, err)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error parsing Config JSON: %w", err)
	}
	var fullSetup models.NovelSetupContent
	if err := DecodeStrictJSON(story.Setup, &fullSetup); err != nil {
		log.Printf("WARN: Failed to parse Setup JSON for initial scene task StoryID %s: %v", story.ID, err)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error parsing Setup JSON: %w", err)
	}

	// Формируем UserInput для начальной сцены из Config и Setup
	userInputString := utils.FormatConfigAndSetupToString(fullConfig, fullSetup)

	payload := sharedMessaging.GenerationTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           userID.String(),
		PublishedStoryID: story.ID.String(),
		PromptType:       models.PromptTypeStoryContinuation,
		UserInput:        userInputString,
		StateHash:        models.InitialStateHash,
		Language:         language,
	}

	return payload, nil
}

// fetchAndAuthorizeGameState retrieves a PlayerGameState by ID, handles errors, and verifies ownership.
func (s *gameLoopServiceImpl) fetchAndAuthorizeGameState(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) (*models.PlayerGameState, error) {
	gameState, err := s.playerGameStateRepo.GetByID(ctx, s.pool, gameStateID)
	if wrapErr := WrapRepoError(s.logger, err, "PlayerGameState"); wrapErr != nil {
		if errors.Is(wrapErr, models.ErrNotFound) {
			return nil, models.ErrPlayerGameStateNotFound
		}
		return nil, wrapErr
	}
	if gameState.PlayerID != userID {
		return nil, models.ErrForbidden
	}
	return gameState, nil
}
