package utils

import (
	"fmt"
	"strings"

	"bytes"
	"encoding/json"
	"novel-server/shared/models"
	"sort"
)

// FormatConfigToString преобразует структуру models.Config в читаемую многострочную строку.
// Поля sssf, fd и cs не включены, так как они не хранятся напрямую в models.Config.
// ПРИМЕЧАНИЕ: IsAdultContent не является частью models.Config, но часто передается вместе.
// Для совместимости с appendBasicConfigInfo, isAdultContent здесь добавлен как параметр.
func FormatConfigToString(config models.Config, isAdultContent bool) string {
	var sb strings.Builder

	// Используем общую функцию для базовой информации о конфигурации
	AppendBasicConfigInfo(&sb, config)

	// Добавляем явную инструкцию о взрослом контенте
	sb.WriteString("\nGeneration Guidelines:\n")
	if isAdultContent {
		sb.WriteString("You are allowed to generate adult content themes and scenarios.\n")
	} else {
		sb.WriteString("CRITICAL RULE: Do NOT generate sexual or erotic content. Maintain a non-adult theme.\n")
	}

	// User Revision больше не добавляется согласно требованию.

	return strings.TrimRight(sb.String(), "\n")
}

// FormatConfigAndSetupToString преобразует структуры models.Config и models.NovelSetupContent
// в читаемую многострочную строку для использования в промптах генераторов контента.
// ПРИМЕЧАНИЕ: IsAdultContent не является частью models.Config, поэтому его вывод здесь может быть некорректным или должен быть удален.
// Эта версия функции не используется в текущем потоке, но исправляем для консистентности.
func FormatConfigAndSetupToString(config models.Config, setup models.NovelSetupContent /*, isAdultContent bool */) string {
	var sb strings.Builder

	// Используем общую функцию для базовой информации о конфигурации
	// Поскольку isAdultContent закомментирован в этой функции, передаем false или реальное значение, если оно будет добавлено.
	// Для примера, если бы isAdultContent было параметром, было бы: appendBasicConfigInfo(&sb, config, isAdultContent)
	// Так как его нет, а appendBasicConfigInfo его ожидает, нужно будет его либо добавить в параметры этой функции,
	// либо передавать некое значение по умолчанию, если isAdultContent всегда false для этого конкретного вызова.
	// Предположим, что для этого конкретного FormatConfigAndSetupToString isAdultContent не используется и можно передать false.
	AppendBasicConfigInfo(&sb, config) // Имя с большой буквы, передаем false как плейсхолдер для isAdultContent

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
		sb.WriteString("Characters (from Setup):\n") // Уточняем, что из Setup
		for i, char := range setup.Characters {      // char теперь GeneratedCharacter
			sb.WriteString(fmt.Sprintf("  Character %d Name: %s\n", i+1, char.Name))
			if char.Role != "" {
				sb.WriteString(fmt.Sprintf("    Role: %s\n", char.Role))
			}
			if char.Traits != "" {
				sb.WriteString(fmt.Sprintf("    Traits: %s\n", char.Traits))
			}
			// Отношения могут быть слишком объемными для простого вывода, можно добавить при необходимости
			// if len(char.Relationship) > 0 {
			// 	sb.WriteString(fmt.Sprintf("    Relationships: ...\n"))
			// }geReferenceName != "" {
			sb.WriteString(fmt.Sprintf("    Image Ref Name: %s\n", char.ImageReferenceName))
			// ImagePromptDescriptor тоже может быть слишком длинным, выводим если нужно
			// if char.ImagePromptDescriptor != "" {
			// 	sb.WriteString(fmt.Sprintf("    Image Prompt Desc: %s\n", char.ImagePromptDescriptor))
			// }
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

// FormatConfigAndSetupDataToString объединяет models.Config и данные из частично заполненного Setup
// (в виде map[string]interface{}) в единую строку для AI.
// ПРИНИМАЕТ isAdultContent ОТДЕЛЬНЫМ ПАРАМЕТРОМ
func FormatConfigAndSetupDataToString(config models.Config, setupData map[string]interface{}, isAdultContent bool) (string, error) {
	var sb strings.Builder

	// Используем новую общую функцию для форматирования базовой информации о конфигурации
	AppendBasicConfigInfo(&sb, config)

	// --- PROTAGONIST GOAL (FROM SETUP DATA) ---
	if goal, ok := setupData["protagonist_goal"].(string); ok && goal != "" {
		sb.WriteString(fmt.Sprintf("Protagonist Goal: %s\n\n", goal))
	}

	// --- INITIAL SETUP ELEMENTS FROM GOAL (FROM SETUP DATA) ---
	if initSetupRaw, ok := setupData["initial_setup_elements_from_goal"]; ok && initSetupRaw != nil {
		var initSetupBytes []byte
		var err error
		switch v := initSetupRaw.(type) {
		case []byte:
			initSetupBytes = v
		case string:
			initSetupBytes = []byte(v)
		default:
			initSetupBytes, err = json.Marshal(initSetupRaw)
			if err != nil {
				sb.WriteString(fmt.Sprintf("Error marshalling initial_setup_elements_from_goal: %v\n", err))
				initSetupBytes = nil
			}
		}

		if len(initSetupBytes) > 0 && string(initSetupBytes) != "null" && string(initSetupBytes) != "{}" {
			sb.WriteString("Initial Setup Elements from Goal (Optional):\n") // Оставим этот подзаголовок
			var prettyJSONBuffer bytes.Buffer
			if errIndent := json.Indent(&prettyJSONBuffer, initSetupBytes, "", "  "); errIndent == nil {
				sb.WriteString(prettyJSONBuffer.String() + "\n")
			} else {
				sb.WriteString(string(initSetupBytes) + "\n")
			}
			sb.WriteString("\n")
		}
	}

	// --- INITIAL SCENE PLAN (FROM SETUP DATA, ORIGINALLY FROM SCENE PLANNER) ---
	if initialScenePlanRaw, ok := setupData["initial_scene_plan"]; ok && initialScenePlanRaw != nil {
		var planBytes []byte
		var errPlanBytes error

		switch v := initialScenePlanRaw.(type) {
		case []byte:
			planBytes = v
		case string:
			planBytes = []byte(v)
		case map[string]interface{}:
			planBytes, errPlanBytes = json.Marshal(v)
		case json.RawMessage:
			planBytes = v
		default:
			planBytes, errPlanBytes = json.Marshal(v)
		}

		if errPlanBytes != nil {
			sb.WriteString(fmt.Sprintf("Error processing initial_scene_plan for scene_focus: %v\n", errPlanBytes))
		} else if len(planBytes) > 0 && string(planBytes) != "null" && string(planBytes) != "{}" {
			// Десериализуем, чтобы извлечь только scene_focus
			var scenePlanData map[string]interface{}
			if errUnmarshal := json.Unmarshal(planBytes, &scenePlanData); errUnmarshal == nil {
				if sceneFocus, focusOk := scenePlanData["scene_focus"].(string); focusOk && sceneFocus != "" {
					sb.WriteString(fmt.Sprintf("Initial Scene Focus: %s\n\n", sceneFocus))
				} else {
					// sb.WriteString("(Scene focus not found or empty in initial_scene_plan)\n\n")
				}
			} else {
				sb.WriteString(fmt.Sprintf("Error unmarshalling initial_scene_plan to extract scene_focus: %v\n\n", errUnmarshal))
			}
		} else {
			// sb.WriteString("(No specific output from initial scene planner or output was empty for scene_focus)\n\n")
		}
	}

	// --- CHARACTER TO GENERATE DETAILS (FROM SETUP DATA, if present) ---
	if charDetailsInterface, ok := setupData["character_to_generate_details"]; ok {
		if charDetailsMap, mapOk := charDetailsInterface.(map[string]string); mapOk {
			role, roleExists := charDetailsMap["role"]
			reason, reasonExists := charDetailsMap["reason"]

			if roleExists || reasonExists { // Only print the section if there's something to show
				sb.WriteString("Character to Generate Details:\n") // Заголовок секции
				if roleExists && role != "" {
					sb.WriteString(fmt.Sprintf("  Role: %s\n", role))
				}
				if reasonExists && reason != "" {
					sb.WriteString(fmt.Sprintf("  Reason: %s\n", reason))
				}
				sb.WriteString("\n")
			}
		} else if charDetailsInterface != nil {
			// Если charDetailsInterface не nil, но не map[string]string, логируем или обрабатываем как ошибку формата
			// В данном случае, если это не ожидаемый map, мы можем просто проигнорировать или залогировать.
			// Для простоты пока игнорируем, если формат неожиданный, но можно добавить логирование.
		}
	}

	// --- CHARACTERS TO GENERATE LIST (FROM SETUP DATA, if present) ---
	if charListInterface, ok := setupData["characters_to_generate_list"]; ok && charListInterface != nil {
		if suggestionsList, listOk := charListInterface.([]interface{}); listOk {
			if len(suggestionsList) > 0 {
				sb.WriteString("Create characters:\n")
				for i, sugInterface := range suggestionsList {
					if sugMap, sugMapOk := sugInterface.(map[string]interface{}); sugMapOk {
						sb.WriteString(fmt.Sprintf("%d:\n", i+1))
						if role, roleOk := sugMap["role"].(string); roleOk && role != "" {
							sb.WriteString(fmt.Sprintf("  Role: %s\n", role))
						}
						if reason, reasonOk := sugMap["reason"].(string); reasonOk && reason != "" {
							sb.WriteString(fmt.Sprintf("  Reason: %s\n", reason))
						}
					} else {
						// Элемент списка не ожидаемого типа map[string]interface{}, можно залогировать
						sb.WriteString(fmt.Sprintf("%d: Error - unexpected item format in characters_to_generate_list\n", i+1))
					}
				}
				sb.WriteString("\n")
			}
		}
	}

	// --- GENERATED CHARACTERS (FROM SETUP DATA, if present) ---
	if charactersRaw, ok := setupData["characters"]; ok && charactersRaw != nil {
		var charactersBytes []byte
		var errCharacters error

		switch v := charactersRaw.(type) {
		case []byte:
			charactersBytes = v
		case string:
			charactersBytes = []byte(v)
		case []models.GeneratedCharacter: // Если это уже нужный тип
			// Проверяем, что слайс не пустой
			if len(v) > 0 {
				charactersBytes, errCharacters = json.Marshal(v)
			}
		case []*models.GeneratedCharacter: // Если это слайс указателей
			if len(v) > 0 {
				charactersBytes, errCharacters = json.Marshal(v)
			}
		default:
			// Попробуем через рефлексию определить, можно ли это маршализовать как список персонажей,
			// или просто маршализовать как есть, если это уже JSON-совместимый тип.
			// Для простоты, если это не []byte, string, или известный слайс персонажей, просто маршалим.
			charactersBytes, errCharacters = json.Marshal(charactersRaw)
		}

		if errCharacters != nil {
			sb.WriteString(fmt.Sprintf("Error marshalling characters from setupData: %v\n", errCharacters))
		} else if len(charactersBytes) > 0 && string(charactersBytes) != "null" && string(charactersBytes) != "[]" && string(charactersBytes) != "{}" {
			sb.WriteString("Generated Characters:\n") // Оставим этот подзаголовок
			var prettyCharactersBuffer bytes.Buffer
			if errIndent := json.Indent(&prettyCharactersBuffer, charactersBytes, "", "  "); errIndent == nil {
				sb.WriteString(prettyCharactersBuffer.String() + "\n")
			} else {
				sb.WriteString(string(charactersBytes) + "\n")
			}
			sb.WriteString("\n")
		}
	}

	return strings.TrimSpace(sb.String()), nil
}

// appendBasicConfigInfo добавляет базовую информацию о конфигурации в strings.Builder.
// Это экспортируемая функция, используемая внутри пакета utils.
func AppendBasicConfigInfo(sb *strings.Builder, config models.Config) {
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

	// Player Preferences из Config
	hasPlayerPrefs := len(config.PlayerPrefs.Themes) > 0 ||
		config.PlayerPrefs.Style != "" ||
		config.PlayerPrefs.WorldLore != "" ||
		config.PlayerPrefs.PlayerDescription != "" ||
		config.PlayerPrefs.DesiredLocations != "" ||
		config.PlayerPrefs.DesiredCharacters != ""

	if hasPlayerPrefs {
		sb.WriteString("Player Preferences:\n") // Оставим этот подзаголовок для ясности группы полей
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
	sb.WriteString("\n") // Пустая строка для разделения

	// --- Core Stats (from Config) ---
	if len(config.CoreStats) > 0 {
		sb.WriteString("Core Stats:\n")
		// Сортируем ключи для консистентного вывода
		keys := make([]string, 0, len(config.CoreStats))
		for k := range config.CoreStats {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, name := range keys {
			stat := config.CoreStats[name]
			// Выводим имя стата (ключ) и его поля из NarratorCsStat
			sb.WriteString(fmt.Sprintf("  Stat [%s]: %s\n", name, stat.Name))
			sb.WriteString(fmt.Sprintf("    Description: %s\n", stat.Description))
			// Заменяем вывод 'min'/'max'/'both' на понятные строки
			switch stat.Go {
			case "min":
				sb.WriteString("    Game Over: Ends at 0\n")
			case "max":
				sb.WriteString("    Game Over: Ends at 100\n")
			case "both":
				sb.WriteString("    Game Over: Ends at 0 or 100\n")
			default:
				// Если значение не 'min', 'max' или 'both', выводим как есть или указываем, что не стандартное
				sb.WriteString(fmt.Sprintf("    Game Over: %s (Unknown condition)\n", stat.Go))
			}
		}
	}

	sb.WriteString("\n") // Пустая строка для разделения
}

// FormatFullGameStateToString преобразует полное состояние игры (статическое и динамическое)
// в единую читаемую многострочную строку для использования в промптах генераторов контента.
// ПРИМЕЧАНИЕ: IsAdultContent не является частью models.Config.
// Эта функция будет использовать isAdultContent из PublishedStory, переданный отдельно.
func FormatFullGameStateToString(
	config models.Config,
	setup models.NovelSetupContent,
	currentCS map[string]int, // Текущие статы
	previousChoices []models.UserChoiceInfo, // Предыдущие выборы пользователя
	previousSSS string, // Предыдущий Story Summary So Far
	previousFD string, // Предыдущий Future Direction
	previousVIS string, // Предыдущий Variable Impact Summary
	encounteredChars []string, // Встреченные персонажи
	isAdultContent bool, // <<< ДОБАВЛЕН ПАРАМЕТР
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
	sb.WriteString(fmt.Sprintf("Adult Content: %t\n", isAdultContent)) // <<< ИСПОЛЬЗУЕМ ПЕРЕДАННЫЙ ПАРАМЕТР
	// Удаляем вывод CoreStats из Config
	/*
		if len(config.CoreStats) > 0 {
			sb.WriteString("Core Stats (from Config):\n")
			// Если config.CoreStats это json.RawMessage, его нужно сначала анмаршалить
			var actualCoreStats map[string]string
			if err := json.Unmarshal(config.CoreStats, &actualCoreStats); err == nil {
				keysCS := make([]string, 0, len(actualCoreStats))
				for k := range actualCoreStats {
					keysCS = append(keysCS, k)
				}
				sort.Strings(keysCS)
				for _, name := range keysCS {
					sb.WriteString(fmt.Sprintf("  %s: %s\n", name, actualCoreStats[name]))
				}
			} else {
				sb.WriteString(fmt.Sprintf("  Error unmarshalling CoreStats from Config: %v\n", err))
			}
		}
	*/

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
		sb.WriteString("Characters (from Setup):\n") // Уточняем, что из Setup
		for i, char := range setup.Characters {      // char теперь GeneratedCharacter
			sb.WriteString(fmt.Sprintf("  Character Index %d: %s\n", i, char.Name))
			if char.Role != "" {
				sb.WriteString(fmt.Sprintf("    Role: %s\n", char.Role))
			}
			if char.Traits != "" {
				sb.WriteString(fmt.Sprintf("    Traits: %s\n", char.Traits))
			}
			// Вывод VisualTags и Personality удален, так как их нет в GeneratedCharacter
			// if char.VisualTags != "" { ... }
			// if char.Personality != "" { ... }
			if char.ImageReferenceName != "" { // Используем ImageReferenceName вместо ImageRef
				sb.WriteString(fmt.Sprintf("    Image Ref Name: %s\n", char.ImageReferenceName))
			}
			// Дополнительные поля из GeneratedCharacter можно добавить сюда при необходимости
			// Например, PlotHook или Memories, если они важны для этого контекста.
		}
	}
	// Удаляем вывод StoryPreviewImagePrompt
	// if setup.StoryPreviewImagePrompt != "" {
	// 	sb.WriteString(fmt.Sprintf("Story Preview Image Prompt: %s\n", setup.StoryPreviewImagePrompt))
	// }
	// SSSovF и FD из сетапа здесь не выводим, т.к. они относятся только к первой сцене

	// --- Динамическая часть ---
	sb.WriteString("\nCurrent Game State:\n\n")

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
		sb.WriteString("\nPrevious Turn Summary:\n\n")
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
