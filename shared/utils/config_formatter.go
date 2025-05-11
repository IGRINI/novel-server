package utils

import (
	"fmt"
	"strings"

	"novel-server/shared/models"
	"sort"
)

// FormatConfigToString преобразует структуру models.Config в читаемую многострочную строку.
// Поля sssf, fd и cs не включены, так как они не хранятся напрямую в models.Config.
// Принимает необязательный параметр userRevision, который будет добавлен в конец вывода, если предоставлен.
func FormatConfigToString(config models.Config, userRevision string) string {
	var sb strings.Builder

	// Обязательные строковые поля (печатаются всегда)
	sb.WriteString(fmt.Sprintf("Title: %s\n", config.Title))
	sb.WriteString(fmt.Sprintf("Short Description: %s\n", config.ShortDescription))
	sb.WriteString(fmt.Sprintf("Genre: %s\n", config.Genre))
	sb.WriteString(fmt.Sprintf("Protagonist Name: %s\n", config.ProtagonistName))
	sb.WriteString(fmt.Sprintf("Protagonist Description: %s\n", config.ProtagonistDescription))
	sb.WriteString(fmt.Sprintf("World Context: %s\n", config.WorldContext))
	sb.WriteString(fmt.Sprintf("Story Summary: %s\n", config.StorySummary))

	// Опциональное строковое поле
	if config.Franchise != "" {
		sb.WriteString(fmt.Sprintf("Franchise: %s\n", config.Franchise))
	}

	// Булево поле
	sb.WriteString(fmt.Sprintf("Adult Content: %t\n", config.IsAdultContent))

	// Core Stats
	if len(config.CoreStats) > 0 {
		sb.WriteString("Core Stats:\n")
		for name, description := range config.CoreStats {
			sb.WriteString(fmt.Sprintf("  %s: %s\n", name, description))
		}
	}

	// Player Preferences (если есть хотя бы одно поле для вывода)
	hasPlayerPrefs := false
	if len(config.PlayerPrefs.Themes) > 0 {
		hasPlayerPrefs = true
	}
	if config.PlayerPrefs.Style != "" {
		hasPlayerPrefs = true
	}
	if len(config.PlayerPrefs.WorldLore) > 0 {
		hasPlayerPrefs = true
	}
	if config.PlayerPrefs.PlayerDescription != "" {
		hasPlayerPrefs = true
	}
	if len(config.PlayerPrefs.DesiredLocations) > 0 {
		hasPlayerPrefs = true
	}
	if len(config.PlayerPrefs.DesiredCharacters) > 0 {
		hasPlayerPrefs = true
	}

	if hasPlayerPrefs {
		sb.WriteString("Player Preferences:\n")
		if len(config.PlayerPrefs.Themes) > 0 {
			sb.WriteString(fmt.Sprintf("  Tags for Story: %s\n", strings.Join(config.PlayerPrefs.Themes, ", ")))
		}
		if config.PlayerPrefs.Style != "" {
			sb.WriteString(fmt.Sprintf("  Visual Style: %s\n", config.PlayerPrefs.Style))
		}
		if config.PlayerPrefs.WorldLore != "" {
			sb.WriteString(fmt.Sprintf("  World Lore: %s\n", config.PlayerPrefs.WorldLore))
		}
		if config.PlayerPrefs.PlayerDescription != "" {
			sb.WriteString(fmt.Sprintf("  Extra Player Details: %s\n", config.PlayerPrefs.PlayerDescription))
		}
		if len(config.PlayerPrefs.DesiredLocations) > 0 {
			sb.WriteString(fmt.Sprintf("  Desired Locations: %s\n", config.PlayerPrefs.DesiredLocations))
		}
		if len(config.PlayerPrefs.DesiredCharacters) > 0 {
			sb.WriteString(fmt.Sprintf("  Desired Characters: %s\n", config.PlayerPrefs.DesiredCharacters))
		}
	}

	// Добавляем User Revision, если он предоставлен
	if userRevision != "" {
		sb.WriteString("\n\n**User Revision:**\n")
		sb.WriteString(userRevision)
	}

	return strings.TrimRight(sb.String(), "\n")
}

// FormatConfigAndSetupToString преобразует структуры models.Config и models.NovelSetupContent
// в читаемую многострочную строку для использования в промптах генераторов контента.
func FormatConfigAndSetupToString(config models.Config, setup models.NovelSetupContent) string {
	var sb strings.Builder

	// Используем части существующего FormatConfigToString для единообразия, но без userRevision
	sb.WriteString(fmt.Sprintf("Title: %s\n", config.Title))
	sb.WriteString(fmt.Sprintf("Short Description: %s\n", config.ShortDescription))
	sb.WriteString(fmt.Sprintf("Genre: %s\n", config.Genre))
	sb.WriteString(fmt.Sprintf("Protagonist Name: %s\n", config.ProtagonistName))
	sb.WriteString(fmt.Sprintf("Protagonist Description: %s\n", config.ProtagonistDescription))
	sb.WriteString(fmt.Sprintf("World Context: %s\n", config.WorldContext))
	sb.WriteString(fmt.Sprintf("Story Summary: %s\n", config.StorySummary))
	if config.Franchise != "" {
		sb.WriteString(fmt.Sprintf("Franchise: %s\n", config.Franchise))
	}
	sb.WriteString(fmt.Sprintf("Adult Content: %t\n", config.IsAdultContent))
	if len(config.CoreStats) > 0 {
		sb.WriteString("Core Stats (from Config):\n") // Уточняем, что это из Config
		for name, description := range config.CoreStats {
			sb.WriteString(fmt.Sprintf("  %s: %s\n", name, description))
		}
	}
	hasPlayerPrefs := false
	if len(config.PlayerPrefs.Themes) > 0 {
		hasPlayerPrefs = true
	}
	if config.PlayerPrefs.Style != "" {
		hasPlayerPrefs = true
	}
	if len(config.PlayerPrefs.WorldLore) > 0 {
		hasPlayerPrefs = true
	}
	if config.PlayerPrefs.PlayerDescription != "" {
		hasPlayerPrefs = true
	}
	if len(config.PlayerPrefs.DesiredLocations) > 0 {
		hasPlayerPrefs = true
	}
	if len(config.PlayerPrefs.DesiredCharacters) > 0 {
		hasPlayerPrefs = true
	}
	if hasPlayerPrefs {
		sb.WriteString("Player Preferences:\n")
		if len(config.PlayerPrefs.Themes) > 0 {
			sb.WriteString(fmt.Sprintf("  Tags for Story: %s\n", strings.Join(config.PlayerPrefs.Themes, ", ")))
		}
		if config.PlayerPrefs.Style != "" {
			sb.WriteString(fmt.Sprintf("  Visual Style: %s\n", config.PlayerPrefs.Style))
		}
		if config.PlayerPrefs.WorldLore != "" {
			sb.WriteString(fmt.Sprintf("  World Lore: %s\n", config.PlayerPrefs.WorldLore))
		}
		if config.PlayerPrefs.PlayerDescription != "" {
			sb.WriteString(fmt.Sprintf("  Extra Player Details: %s\n", config.PlayerPrefs.PlayerDescription))
		}
		if config.PlayerPrefs.DesiredLocations != "" {
			sb.WriteString(fmt.Sprintf("  Desired Locations: %s\n", config.PlayerPrefs.DesiredLocations))
		}
		if config.PlayerPrefs.DesiredCharacters != "" {
			sb.WriteString(fmt.Sprintf("  Desired Characters: %s\n", config.PlayerPrefs.DesiredCharacters))
		}
	}

	// Данные из NovelSetupContent
	if len(setup.CoreStatsDefinition) > 0 {
		sb.WriteString("Core Stats Definition (from Setup):\n")
		for name, statDef := range setup.CoreStatsDefinition {
			sb.WriteString(fmt.Sprintf("  Stat: %s\n", name))
			sb.WriteString(fmt.Sprintf("    Description: %s\n", statDef.Description))
			sb.WriteString(fmt.Sprintf("    Initial Value: %d\n", statDef.Initial))
			sb.WriteString(fmt.Sprintf("    Game Ends on 0: %t\n", statDef.Go.Min))
			sb.WriteString(fmt.Sprintf("    Game Ends on 100: %t\n", statDef.Go.Max))
			sb.WriteString(fmt.Sprintf("    Icon: %s\n", statDef.Icon))
		}
	}
	if len(setup.Characters) > 0 {
		sb.WriteString("Characters:\n")
		for i, char := range setup.Characters {
			sb.WriteString(fmt.Sprintf("  Character %d Name: %s\n", i+1, char.Name))
			sb.WriteString(fmt.Sprintf("    Description: %s\n", char.Description))
			if char.Personality != "" {
				sb.WriteString(fmt.Sprintf("    Personality: %s\n", char.Personality))
			}
			if char.ImageRef != "" {
				sb.WriteString(fmt.Sprintf("    Image Ref: %s\n", char.ImageRef))
			}
		}
	}
	if setup.StoryPreviewImagePrompt != "" {
		sb.WriteString(fmt.Sprintf("Story Preview Image Prompt: %s\n", setup.StoryPreviewImagePrompt))
	}
	if setup.StorySummarySoFar != "" {
		sb.WriteString(fmt.Sprintf("Story Summary So Far (Setup): %s\n", setup.StorySummarySoFar))
	}
	if setup.FutureDirection != "" {
		sb.WriteString(fmt.Sprintf("Future Direction (Setup): %s\n", setup.FutureDirection))
	}

	return strings.TrimRight(sb.String(), "\n")
}

// FormatFullGameStateToString преобразует полное состояние игры (статическое и динамическое)
// в единую читаемую многострочную строку для использования в промптах генераторов контента.
func FormatFullGameStateToString(
	config models.Config,
	setup models.NovelSetupContent,
	currentCS map[string]int, // Текущие статы
	previousChoices []models.UserChoiceInfo, // Предыдущие выборы пользователя
	previousSSS string, // Предыдущий Story Summary So Far
	previousFD string, // Предыдущий Future Direction
	previousVIS string, // Предыдущий Variable Impact Summary
	encounteredChars []string, // Встреченные персонажи
) string {
	var sb strings.Builder
	// Конфиг (как в FormatConfigAndSetupToString, но без заголовка)
	sb.WriteString(fmt.Sprintf("Title: %s\n", config.Title))
	sb.WriteString(fmt.Sprintf("Short Description: %s\n", config.ShortDescription))
	sb.WriteString(fmt.Sprintf("Genre: %s\n", config.Genre))
	sb.WriteString(fmt.Sprintf("Protagonist Name: %s\n", config.ProtagonistName))
	sb.WriteString(fmt.Sprintf("Protagonist Description: %s\n", config.ProtagonistDescription))
	sb.WriteString(fmt.Sprintf("World Context: %s\n", config.WorldContext))
	sb.WriteString(fmt.Sprintf("Story Summary: %s\n", config.StorySummary))
	if config.Franchise != "" {
		sb.WriteString(fmt.Sprintf("Franchise: %s\n", config.Franchise))
	}
	sb.WriteString(fmt.Sprintf("Adult Content: %t\n", config.IsAdultContent))
	// Удаляем вывод CoreStats из Config
	// if len(config.CoreStats) > 0 {
	// 	sb.WriteString("Core Stats (from Config):\n")
	// 	// Если config.CoreStats это json.RawMessage, его нужно сначала анмаршалить
	// 	var actualCoreStats map[string]string
	// 	if err := json.Unmarshal(config.CoreStats, &actualCoreStats); err == nil {
	// 		keysCS := make([]string, 0, len(actualCoreStats))
	// 		for k := range actualCoreStats {
	// 			keysCS = append(keysCS, k)
	// 		}
	// 		sort.Strings(keysCS)
	// 		for _, name := range keysCS {
	// 			sb.WriteString(fmt.Sprintf("  %s: %s\n", name, actualCoreStats[name]))
	// 		}
	// 	} else {
	// 		sb.WriteString(fmt.Sprintf("  Error unmarshalling CoreStats from Config: %v\n", err))
	// 	}
	// }

	// Player Preferences
	prefsFields := []string{}
	if len(config.PlayerPrefs.Themes) > 0 {
		prefsFields = append(prefsFields, fmt.Sprintf("  Tags for Story: %s", strings.Join(config.PlayerPrefs.Themes, ", ")))
	}
	if config.PlayerPrefs.Style != "" {
		prefsFields = append(prefsFields, fmt.Sprintf("  Visual Style: %s", config.PlayerPrefs.Style))
	}
	if config.PlayerPrefs.WorldLore != "" {
		prefsFields = append(prefsFields, fmt.Sprintf("  World Lore: %s", config.PlayerPrefs.WorldLore))
	}
	if config.PlayerPrefs.PlayerDescription != "" {
		prefsFields = append(prefsFields, fmt.Sprintf("  Extra Player Details: %s", config.PlayerPrefs.PlayerDescription))
	}
	if config.PlayerPrefs.DesiredLocations != "" {
		prefsFields = append(prefsFields, fmt.Sprintf("  Desired Locations: %s", config.PlayerPrefs.DesiredLocations))
	}
	if config.PlayerPrefs.DesiredCharacters != "" {
		prefsFields = append(prefsFields, fmt.Sprintf("  Desired Characters: %s", config.PlayerPrefs.DesiredCharacters))
	}
	if len(prefsFields) > 0 {
		sb.WriteString("Player Preferences:\n")
		for _, field := range prefsFields {
			sb.WriteString(field + "\n")
		}
	}

	sb.WriteString("\n") // Разделитель между Config и Setup

	// Сетап (как в FormatConfigAndSetupToString, но без заголовка)
	if len(setup.CoreStatsDefinition) > 0 {
		sb.WriteString("Core Stats Definition (from Setup):\n")
		keysCSD := make([]string, 0, len(setup.CoreStatsDefinition))
		for k := range setup.CoreStatsDefinition {
			keysCSD = append(keysCSD, k)
		}
		sort.Strings(keysCSD)
		for i, name := range keysCSD {
			statDef := setup.CoreStatsDefinition[name]
			sb.WriteString(fmt.Sprintf("  Stat Index %d: %s\n", i, name))
			sb.WriteString(fmt.Sprintf("    Description: %s\n", statDef.Description))
			sb.WriteString(fmt.Sprintf("    Initial Value: %d\n", statDef.Initial))
			sb.WriteString(fmt.Sprintf("    Game Over on Min: %t\n", statDef.Go.Min))
			sb.WriteString(fmt.Sprintf("    Game Over on Max: %t\n", statDef.Go.Max))
			sb.WriteString(fmt.Sprintf("    Icon: %s\n", statDef.Icon))
		}
	}
	if len(setup.Characters) > 0 {
		sb.WriteString("Characters:\n")
		for i, char := range setup.Characters {
			sb.WriteString(fmt.Sprintf("  Character Index %d: %s\n", i, char.Name))
			sb.WriteString(fmt.Sprintf("    Description: %s\n", char.Description))
			if char.VisualTags != "" {
				sb.WriteString(fmt.Sprintf("    Visual Tags: %s\n", char.VisualTags))
			}
			if char.Personality != "" {
				sb.WriteString(fmt.Sprintf("    Personality: %s\n", char.Personality))
			}
		}
	}
	// Удаляем вывод StoryPreviewImagePrompt
	// if setup.StoryPreviewImagePrompt != "" {
	// 	sb.WriteString(fmt.Sprintf("Story Preview Image Prompt: %s\n", setup.StoryPreviewImagePrompt))
	// }
	// SSSovF и FD из сетапа здесь не выводим, т.к. они относятся только к первой сцене

	// --- Динамическая часть ---
	sb.WriteString("\n### Current Game State ###\n\n")

	// Текущие статы
	if len(currentCS) > 0 {
		sb.WriteString("Current Core Stats:\n")
		keysCSCurr := make([]string, 0, len(currentCS))
		for k := range currentCS {
			keysCSCurr = append(keysCSCurr, k)
		}
		sort.Strings(keysCSCurr)
		for i, name := range keysCSCurr {
			sb.WriteString(fmt.Sprintf("  Stat Index %d: %s = %d\n", i, name, currentCS[name]))
		}
	} else {
		sb.WriteString("Current Core Stats: (None)\n")
	}

	// Встреченные персонажи
	if len(encounteredChars) > 0 {
		sb.WriteString(fmt.Sprintf("Encountered Characters: %s\n", strings.Join(encounteredChars, ", ")))
	} else {
		sb.WriteString("Encountered Characters: (None)\n")
	}

	// --- Предыдущий ход ---
	// Выводим эту секцию только если есть хотя бы одно из полей
	hasPreviousTurnInfo := previousSSS != "" || previousFD != "" || previousVIS != "" || len(previousChoices) > 0
	if hasPreviousTurnInfo {
		sb.WriteString("\n### Previous Turn Summary ###\n\n")
		if previousSSS != "" {
			sb.WriteString(fmt.Sprintf("Previous Summary So Far: %s\n", previousSSS))
		}
		if previousFD != "" {
			sb.WriteString(fmt.Sprintf("Previous Future Direction: %s\n", previousFD))
		}
		if previousVIS != "" {
			sb.WriteString(fmt.Sprintf("Previous Variable Impact Summary: %s\n", previousVIS))
		}
		if len(previousChoices) > 0 {
			sb.WriteString("Last Choices Made:\n")
			for i, choice := range previousChoices {
				sb.WriteString(fmt.Sprintf("  Choice %d:\n", i+1))
				sb.WriteString(fmt.Sprintf("    Description: %s\n", choice.Desc))
				sb.WriteString(fmt.Sprintf("    Chosen Option: %s\n", choice.Text))
				if choice.ResponseText != nil && *choice.ResponseText != "" {
					sb.WriteString(fmt.Sprintf("    Result Text: %s\n", *choice.ResponseText))
				}
			}
		}
	}

	return strings.TrimSpace(sb.String()) // Используем TrimSpace для удаления лишних пробелов/переносов строк в начале/конце
}
