package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	sharedMessaging "novel-server/shared/messaging"
	"novel-server/shared/models"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// --- Helper Functions ---

// calculateStateHash calculates a deterministic state hash, including the previous state hash.
func calculateStateHash(previousHash string, coreStats map[string]int, storyVars map[string]interface{}, globalFlags []string) (string, error) {
	stateMap := make(map[string]interface{})

	stateMap["_ph"] = previousHash

	for k, v := range coreStats {
		stateMap["cs_"+k] = v
	}

	for k, v := range storyVars {
		if v != nil && !strings.HasPrefix(k, "_") {
			stateMap["sv_"+k] = v
		}
	}

	nonTransientFlags := make([]string, 0, len(globalFlags))
	for _, flag := range globalFlags {
		if !strings.HasPrefix(flag, "_") {
			nonTransientFlags = append(nonTransientFlags, flag)
		}
	}
	sort.Strings(nonTransientFlags)
	stateMap["gf"] = nonTransientFlags

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
	if progress.StoryVariables == nil {
		progress.StoryVariables = make(map[string]interface{})
	}
	if progress.GlobalFlags == nil {
		progress.GlobalFlags = []string{}
	}

	if cons.CoreStatsChange != nil {
		for statName, change := range cons.CoreStatsChange {
			progress.CoreStats[statName] += change
		}
	}

	if cons.StoryVariables != nil {
		for varName, value := range cons.StoryVariables {
			if value == nil {
				delete(progress.StoryVariables, varName)
			} else {
				progress.StoryVariables[varName] = value
			}
		}
	}

	if len(cons.GlobalFlagsRemove) > 0 {
		flagsToRemove := make(map[string]struct{}, len(cons.GlobalFlagsRemove))
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

	if len(cons.GlobalFlags) > 0 {
		existingFlags := make(map[string]struct{}, len(progress.GlobalFlags))
		for _, flag := range progress.GlobalFlags {
			existingFlags[flag] = struct{}{}
		}
		for _, flagToAdd := range cons.GlobalFlags {
			if _, found := existingFlags[flagToAdd]; !found {
				progress.GlobalFlags = append(progress.GlobalFlags, flagToAdd)
				existingFlags[flagToAdd] = struct{}{}
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
) (sharedMessaging.GenerationTaskPayload, error) {

	if story.Config == nil || story.Setup == nil {
		log.Printf("ERROR: Story Config or Setup is nil for StoryID %s", story.ID)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("story config or setup is nil")
	}
	var fullConfig models.Config
	if err := json.Unmarshal(story.Config, &fullConfig); err != nil {
		log.Printf("WARN: Failed to parse Config JSON for generation task StoryID %s: %v", story.ID, err)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error parsing Config JSON: %w", err)
	}
	var fullSetup models.NovelSetupContent
	if err := json.Unmarshal(story.Setup, &fullSetup); err != nil {
		log.Printf("WARN: Failed to parse Setup JSON for generation task StoryID %s: %v", story.ID, err)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error parsing Setup JSON: %w", err)
	}

	minimalConfig := models.ToMinimalConfigForScene(&fullConfig)
	minimalSetup := models.ToMinimalSetupForScene(&fullSetup)

	promptType := models.PromptTypeNovelCreator
	if gameState != nil && gameState.PlayerStatus == models.PlayerStatusGameOverPending {

		log.Printf("WARN: Attempting to create generation payload for StoryID %s while game over is pending. This should not happen.", story.ID)

	}

	compressedInputData := make(map[string]interface{})

	compressedInputData["cfg"] = minimalConfig
	compressedInputData["stp"] = minimalSetup

	if progress.CoreStats != nil {
		compressedInputData["cs"] = progress.CoreStats
	}
	nonTransientFlags := make([]string, 0, len(progress.GlobalFlags))
	for _, flag := range progress.GlobalFlags {
		if !strings.HasPrefix(flag, "_") {
			nonTransientFlags = append(nonTransientFlags, flag)
		}
	}
	sort.Strings(nonTransientFlags)
	compressedInputData["gf"] = nonTransientFlags

	compressedInputData["pss"] = progress.LastStorySummary
	compressedInputData["pfd"] = progress.LastFutureDirection
	compressedInputData["pvis"] = progress.LastVarImpactSummary

	nonTransientVars := make(map[string]interface{})
	if progress.StoryVariables != nil {
		for k, v := range progress.StoryVariables {
			if v != nil && !strings.HasPrefix(k, "_") {
				nonTransientVars[k] = v
			}
		}
	}
	compressedInputData["sv"] = nonTransientVars
	compressedInputData["ec"] = progress.EncounteredCharacters

	userChoiceMap := make(map[string]string)
	if len(madeChoicesInfo) > 0 {

		lastChoice := madeChoicesInfo[len(madeChoicesInfo)-1]
		userChoiceMap["d"] = lastChoice.Desc
		userChoiceMap["t"] = lastChoice.Text
	}
	compressedInputData["uc"] = userChoiceMap

	userInputBytes, errMarshal := json.Marshal(compressedInputData)
	if errMarshal != nil {
		log.Printf("ERROR: Failed to marshal compressedInputData for generation task StoryID %s: %v", story.ID, errMarshal)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error marshaling input data: %w", errMarshal)
	}
	userInputJSON := string(userInputBytes)

	payload := sharedMessaging.GenerationTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           userID.String(),
		PublishedStoryID: story.ID.String(),
		PromptType:       promptType,
		UserInput:        userInputJSON,
		StateHash:        currentStateHash,
		Language:         language,
	}

	return payload, nil
}

// clearTransientFlags removes flags starting with "_" from the slice.
func clearTransientFlags(flags []string) []string {
	if flags == nil {
		return nil
	}
	newFlags := make([]string, 0, len(flags))
	for _, flag := range flags {
		if !strings.HasPrefix(flag, "_") {
			newFlags = append(newFlags, flag)
		}
	}
	return newFlags
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
	if err := json.Unmarshal(story.Config, &fullConfig); err != nil {
		log.Printf("WARN: Failed to parse Config JSON for initial scene task StoryID %s: %v", story.ID, err)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error parsing Config JSON: %w", err)
	}
	var fullSetup models.NovelSetupContent
	if err := json.Unmarshal(story.Setup, &fullSetup); err != nil {
		log.Printf("WARN: Failed to parse Setup JSON for initial scene task StoryID %s: %v", story.ID, err)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error parsing Setup JSON: %w", err)
	}

	minimalConfig := models.ToMinimalConfigForScene(&fullConfig)
	minimalSetup := models.ToMinimalSetupForScene(&fullSetup)

	initialCoreStats := make(map[string]int)
	if fullSetup.CoreStatsDefinition != nil {
		for statName, definition := range fullSetup.CoreStatsDefinition {
			initialCoreStats[statName] = definition.Initial
		}
	}

	compressedInputData := make(map[string]interface{})
	compressedInputData["cfg"] = minimalConfig
	compressedInputData["stp"] = minimalSetup
	compressedInputData["cs"] = initialCoreStats
	compressedInputData["sv"] = make(map[string]interface{})
	compressedInputData["gf"] = []string{}
	compressedInputData["uc"] = make(map[string]string)
	compressedInputData["pss"] = ""
	compressedInputData["pfd"] = ""
	compressedInputData["pvis"] = ""
	compressedInputData["ec"] = []string{}

	userInputBytes, errMarshal := json.Marshal(compressedInputData)
	if errMarshal != nil {
		log.Printf("ERROR: Failed to marshal compressedInputData for initial scene generation task StoryID %s: %v", story.ID, errMarshal)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error marshaling input data: %w", errMarshal)
	}
	userInputJSON := string(userInputBytes)

	payload := sharedMessaging.GenerationTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           userID.String(),
		PublishedStoryID: story.ID.String(),
		PromptType:       models.PromptTypeNovelFirstSceneCreator,
		UserInput:        userInputJSON,
		StateHash:        models.InitialStateHash,
		Language:         language,
	}

	return payload, nil
}
