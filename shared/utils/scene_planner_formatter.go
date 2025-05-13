package utils

import (
	"fmt"
	"novel-server/shared/models"
	"strings"
)

// FormatConfigAndGoalForScenePlanner форматирует данные конфигурации и цель протагониста
// для использования в качестве входных данных для PromptTypeScenePlanner.
func FormatConfigAndGoalForScenePlanner(config models.Config, protagonistGoal string, isAdultContent bool) (string, error) {
	var sb strings.Builder

	// Используем общую функцию для форматирования базовой информации о конфигурации
	configStr := FormatConfigToString(config, isAdultContent)
	sb.WriteString("Story Config:\n") // Новый формат
	sb.WriteString(configStr)
	sb.WriteString("\n") // Разделитель

	// --- PROTAGONIST GOAL ---
	if protagonistGoal != "" {
		sb.WriteString("\nProtagonist Goal:\n") // Новый формат
		sb.WriteString(protagonistGoal)
	} else {
		// Цель протагониста важна для этого форматтера
		return "", fmt.Errorf("protagonistGoal cannot be empty for ScenePlanner prompt")
	}

	// TODO: Адаптировать под актуальный scene_planner_prompt.md
	// Убедись, что формат соответствует ожидаемому в prompts/game-prompts/scene_planner_prompt.md

	return strings.TrimSpace(sb.String()), nil
}
