package utils

import (
	"fmt"
	"novel-server/shared/models"
	"sort"
	"strings"
)

// FormatInputForJsonGeneration форматирует UserInput для задачи структурирования сцены в JSON.
// Принимает Config, типизированный Setup (для персонажей), Setup в виде map (для цели) и текст повествования.
func FormatInputForJsonGeneration(config models.Config, setup models.NovelSetupContent, setupMap map[string]interface{}, narrative string) (string, error) {
	if strings.TrimSpace(narrative) == "" {
		return "", fmt.Errorf("narrative text cannot be empty for JSON generation")
	}

	var sb strings.Builder

	// 1. Имя протагониста
	sb.WriteString(fmt.Sprintf("Protagonist: %s\n", config.ProtagonistName))

	// 2. Цель протагониста (из setupMap)
	protagonistGoal := "(Protagonist goal not found in setup data)"
	if setupMap != nil {
		if goal, ok := setupMap["protagonist_goal"].(string); ok && goal != "" {
			protagonistGoal = goal
		}
	}
	sb.WriteString(fmt.Sprintf("Protagonist Goal: %s\n", protagonistGoal))

	// 3. Персонажи (из setup.Characters)
	sb.WriteString("\nCharacters:\n")
	if len(setup.Characters) > 0 {
		// Сортируем по имени для стабильности
		sortedChars := make([]models.GeneratedCharacter, len(setup.Characters))
		copy(sortedChars, setup.Characters)
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
			// Добавляем другие поля, если нужно для контекста JSON генератора
			// Например, ImageReferenceName
			if char.ImageReferenceName != "" {
				sb.WriteString(fmt.Sprintf("  ImageReferenceName: %s\n", char.ImageReferenceName))
			}
			// Relationship может быть слишком большим, добавляем если необходимо
			// if len(char.Relationship) > 0 { ... }
		}
	} else {
		sb.WriteString("(No characters defined in setup)\n")
	}
	sb.WriteString("\n")

	// 4. Текст повествования для структурирования
	sb.WriteString("Narrative to structure:\n")
	sb.WriteString(narrative)

	// TODO: Адаптировать под актуальный json_generation_prompt.md
	// Убедись, что формат соответствует ожидаемому в prompts/game-prompts/json_generation_prompt.md

	return strings.TrimSpace(sb.String()), nil
}
