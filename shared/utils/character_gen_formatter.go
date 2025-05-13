package utils

import (
	"fmt"
	"novel-server/shared/models"
	"strings"
	// "go.uber.org/zap" // unused
	// "sort" // unused
)

// FormatInputForCharacterGen форматирует UserInput для генерации персонажей.
// Добавляет цель протагониста и фокус сцены для контекста.
func FormatInputForCharacterGen(config models.Config, setupMap map[string]interface{}, isAdult bool) (string, error) {
	if setupMap == nil {
		return "", fmt.Errorf("setupMap cannot be nil for character generation")
	}

	var sb strings.Builder

	// 1. Форматируем конфиг
	configStr := FormatConfigToString(config, isAdult)
	sb.WriteString("Story Config:\n")
	sb.WriteString(configStr)
	sb.WriteString("\n")

	// 2. Добавляем цель протагониста
	protagonistGoal := "(Protagonist goal not provided)"
	if goal, ok := setupMap["protagonist_goal"].(string); ok && goal != "" {
		protagonistGoal = goal
	}
	sb.WriteString("\nProtagonist Goal:\n")
	sb.WriteString(protagonistGoal)
	sb.WriteString("\n")

	// 3. Добавляем фокус сцены (из плана)
	sceneFocus := "(Scene focus not provided)"
	if scenePlanRaw, planOk := setupMap["full_initial_scene_plan"]; planOk {
		if scenePlanMap, mapOk := scenePlanRaw.(map[string]interface{}); mapOk {
			if focus, focusOk := scenePlanMap["scene_focus"].(string); focusOk && focus != "" {
				sceneFocus = focus
			}
		}
	}
	sb.WriteString("\nUpcoming Scene Focus:\n")
	sb.WriteString(sceneFocus)
	sb.WriteString("\n")

	// 4. Форматируем список предложений для генерации
	charListRaw, ok := setupMap["characters_to_generate_list"]
	if !ok {
		return "", fmt.Errorf("'characters_to_generate_list' missing in setupMap for character generation")
	}
	// Убираем Marshal, форматируем список в текст
	var charListBuilder strings.Builder
	if suggestionsList, listOk := charListRaw.([]interface{}); listOk {
		for i, sugInterface := range suggestionsList {
			if sugMap, sugMapOk := sugInterface.(map[string]interface{}); sugMapOk {
				charListBuilder.WriteString(fmt.Sprintf("%d:\n", i+1))
				if role, roleOk := sugMap["role"].(string); roleOk && role != "" {
					charListBuilder.WriteString(fmt.Sprintf("  Role: %s\n", role))
				}
				if reason, reasonOk := sugMap["reason"].(string); reasonOk && reason != "" {
					charListBuilder.WriteString(fmt.Sprintf("  Reason: %s\n", reason))
				}
			} else {
				charListBuilder.WriteString(fmt.Sprintf("%d: Error - unexpected item format\n", i+1))
			}
		}
	} else {
		// Если формат не []interface{}, пытаемся просто отобразить как есть (менее надежно)
		charListBuilder.WriteString(fmt.Sprintf("%v", charListRaw))
	}

	// configStr := FormatConfigToString(config, isAdult) // Вызываем FormatConfigToString из config_formatter.go -- ПЕРЕМЕЩЕНО ВЫШЕ
	// TODO: Адаптировать под актуальный character_generation_prompt.md
	// Убедись, что формат соответствует ожидаемому в prompts/game-prompts/character_generation_prompt.md
	// input := fmt.Sprintf("Story Config:\n%s\n\nGenerate characters based on these suggestions:\n%s", configStr, charListBuilder.String()) // Старый формат
	sb.WriteString("\nGenerate characters based on these suggestions:\n")
	sb.WriteString(charListBuilder.String())

	return strings.TrimSpace(sb.String()), nil
}
