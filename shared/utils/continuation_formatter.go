package utils

import (
	"fmt"
	"novel-server/shared/models"
	"sort"
	"strings"
)

// FormatInputForContinuation formats the full game state for the continuation scene prompt.
func FormatInputForContinuation(
	config models.Config,
	initialSetup models.NovelSetupContent, // Initial setup (character master list, protagonist goal)
	currentCoreStats map[string]int,
	encounteredCharacters []string,
	previousPlayerChoices []models.UserChoiceInfo,
	nextScenePlanData map[string]interface{}, // Plan for the *upcoming* scene from ScenePlanner
	isAdultContent bool,
) (string, error) {
	var sb strings.Builder

	// 1. Story Config
	configStr := FormatConfigToString(config, isAdultContent)
	sb.WriteString("Story Config:\n")
	sb.WriteString(configStr)
	sb.WriteString("\n")

	// 2. Characters (Master List from initialSetup)
	sb.WriteString("\nCharacters:\n")
	if len(initialSetup.Characters) > 0 {
		// Сортируем по имени для стабильности
		sortedChars := make([]models.GeneratedCharacter, len(initialSetup.Characters))
		copy(sortedChars, initialSetup.Characters)
		sort.Slice(sortedChars, func(i, j int) bool {
			return sortedChars[i].Name < sortedChars[j].Name
		})
		for _, char := range sortedChars {
			sb.WriteString(fmt.Sprintf("  Name: %s\n", char.Name))
			if char.Role != "" {
				sb.WriteString(fmt.Sprintf("  Role: %s\n", char.Role))
			}
			if char.Traits != "" {
				sb.WriteString(fmt.Sprintf("  Traits: %s\n", char.Traits))
			}
			if char.ImageReferenceName != "" {
				sb.WriteString(fmt.Sprintf("  ImageReferenceName: %s\n", char.ImageReferenceName))
			}
			if len(char.Relationship) > 0 {
				sb.WriteString("  Relationships:\n")
				relKeys := make([]string, 0, len(char.Relationship))
				for k := range char.Relationship {
					relKeys = append(relKeys, k)
				}
				sort.Strings(relKeys)
				for _, targetName := range relKeys {
					sb.WriteString(fmt.Sprintf("    - %s: %s\n", targetName, char.Relationship[targetName]))
				}
			}
			if char.Memories != "" {
				sb.WriteString(fmt.Sprintf("  Memories: %s\n", char.Memories))
			}
			if char.PlotHook != "" {
				sb.WriteString(fmt.Sprintf("  PlotHook: %s\n", char.PlotHook))
			}
		}
	} else {
		sb.WriteString("(No characters defined in initial setup)\n")
	}
	sb.WriteString("\n")

	// 3. Protagonist Goal (Извлекаем из nextScenePlanData, где ожидается поле protagonist_goal)
	protagonistGoal := "(Protagonist goal not found in scene plan data)"
	if nextScenePlanData != nil {
		if goal, ok := nextScenePlanData["protagonist_goal"].(string); ok && goal != "" {
			protagonistGoal = goal
		}
	}
	// Старый плейсхолдер и комментарий удалены
	// protagonistGoal := "(Protagonist goal not explicitly provided for this formatter)"
	// Пример: if initialSetup.ProtagonistGoalField != "" { protagonistGoal = initialSetup.ProtagonistGoalField }
	sb.WriteString("\nProtagonist Goal:\n")
	sb.WriteString(protagonistGoal)
	sb.WriteString("\n")

	// 4. Current Game State
	sb.WriteString("\nCurrent Game State:\n")
	if len(currentCoreStats) > 0 {
		sb.WriteString("  Current Core Stats:\n")
		statKeys := make([]string, 0, len(currentCoreStats))
		for k := range currentCoreStats {
			statKeys = append(statKeys, k)
		}
		sort.Strings(statKeys)
		for _, key := range statKeys {
			sb.WriteString(fmt.Sprintf("    %s: %d\n", key, currentCoreStats[key]))
		}
	} else {
		sb.WriteString("  Current Core Stats: (None)\n")
	}
	if len(encounteredCharacters) > 0 {
		sb.WriteString(fmt.Sprintf("  Encountered Characters: %s\n", strings.Join(encounteredCharacters, ", ")))
	} else {
		sb.WriteString("  Encountered Characters: (None)\n")
	}
	sb.WriteString("\n")

	// 6. Previous Player Choices
	sb.WriteString("\nPrevious Player Choices:\n")
	if len(previousPlayerChoices) > 0 {
		for i, choice := range previousPlayerChoices {
			sb.WriteString(fmt.Sprintf("  Choice %d:\n", i+1))
			sb.WriteString(fmt.Sprintf("    Description: %s\n", choice.Desc))
			sb.WriteString(fmt.Sprintf("    Selected: %s\n", choice.Text))
			if choice.ResponseText != nil && *choice.ResponseText != "" {
				sb.WriteString(fmt.Sprintf("    Outcome: %s\n", *choice.ResponseText))
			}
		}
	} else {
		sb.WriteString("(No choices made in the previous scene)\n")
	}
	sb.WriteString("\n")

	// 7. Upcoming Scene Plan (из nextScenePlanData)
	upcomingFocus := "(Upcoming scene focus not provided)"
	upcomingCharsStr := "(Upcoming scene characters not provided)"
	if nextScenePlanData != nil {
		if focus, ok := nextScenePlanData["scene_focus"].(string); ok && focus != "" {
			upcomingFocus = focus
		}
		if cardsRaw, cardsOk := nextScenePlanData["cards"]; cardsOk && cardsRaw != nil {
			// Это предполагает, что "cards" - это список объектов, у каждого из которых есть "name" или "title"
			if cardsList, listOk := cardsRaw.([]interface{}); listOk && len(cardsList) > 0 {
				var cardsBuilder strings.Builder
				found := false
				for _, cardInterface := range cardsList {
					if cardMap, mapOk := cardInterface.(map[string]interface{}); mapOk {
						name := ""
						if n, ok := cardMap["name"].(string); ok {
							name = n
						}
						if name == "" {
							if t, ok := cardMap["title"].(string); ok {
								name = t
							}
						}

						if name != "" {
							cardsBuilder.WriteString(fmt.Sprintf("  %s\n", name))
							found = true
						}
					}
				}
				if found {
					upcomingCharsStr = cardsBuilder.String()
				}
			}
		}
	}
	sb.WriteString("\nUpcoming Scene Focus:\n")
	sb.WriteString(upcomingFocus)
	sb.WriteString("\n")
	sb.WriteString("\nUpcoming Scene Characters:\n")
	sb.WriteString(strings.TrimSpace(upcomingCharsStr))
	sb.WriteString("\n")

	// 8. Task
	sb.WriteString("\nTask:\n")
	sb.WriteString("Generate the next narrative scene based on all the provided context, including the story config, characters, current game state, previous player choices, and the plan for the upcoming scene.")

	return strings.TrimSpace(sb.String()), nil
}
