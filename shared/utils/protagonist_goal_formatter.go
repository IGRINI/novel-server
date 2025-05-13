package utils

import (
	"novel-server/shared/models"
	"strings" // Возвращаем импорт
)

// FormatConfigForGoalPrompt форматирует данные конфигурации для использования
// в качестве входных данных для PromptTypeProtagonistGoal (генератора цели протагониста).
func FormatConfigForGoalPrompt(config models.Config, isAdultContent bool) string {
	var sb strings.Builder // Возвращаем strings.Builder

	// Используем общую функцию для форматирования базовой информации о конфигурации.
	AppendBasicConfigInfo(&sb, config) // Возвращаем эту часть

	// Добавляем явную инструкцию на английском языке
	sb.WriteString("\n\n--- Generation Guidelines ---\n")
	if isAdultContent {
		sb.WriteString("You are allowed to generate adult content themes and scenarios.\n")
	} else {
		sb.WriteString("CRITICAL RULE: Do NOT generate sexual or erotic content. Maintain a non-adult theme.\n")
	}

	return strings.TrimSpace(sb.String()) // Возвращаем правильный return
}
