package utils

import (
	"encoding/json"
	"fmt"
	"novel-server/shared/models"
	"sort"
	"strings"
)

// FormatInputForSetupGen форматирует UserInput для генерации Setup (первого повествования).
// Передает конфиг, список персонажей, фокус сцены и карты сцены.
func FormatInputForSetupGen(config models.Config, setupMap map[string]interface{}, isAdult bool) (string, error) {
	if setupMap == nil {
		return "", fmt.Errorf("setupMap cannot be nil for setup generation")
	}

	var sb strings.Builder

	// 1. Форматируем базовый конфиг
	configStr := FormatConfigToString(config, isAdult) // Включает isAdult директиву
	sb.WriteString("Story Config:\n")                  // Новый формат
	sb.WriteString(configStr)
	sb.WriteString("\n")

	// 2. Форматируем список всех сгенерированных персонажей
	sb.WriteString("\nCharacters:\n")      // Новый формат
	charsRaw, charsOk := setupMap["chars"] // Используем "chars" как ключ, судя по retry коду
	if charsOk && charsRaw != nil {
		// Пытаемся преобразовать в []models.GeneratedCharacter
		charsJSON, _ := json.Marshal(charsRaw)
		var chars []models.GeneratedCharacter
		if err := json.Unmarshal(charsJSON, &chars); err == nil && len(chars) > 0 {
			// Сортируем по имени для стабильности
			sort.Slice(chars, func(i, j int) bool {
				return chars[i].Name < chars[j].Name
			})
			for _, char := range chars {
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
				// Добавляем вывод Relationship
				if len(char.Relationship) > 0 {
					sb.WriteString("  Relationships:\n")
					// Сортируем ключи для стабильности вывода
					relKeys := make([]string, 0, len(char.Relationship))
					for k := range char.Relationship {
						relKeys = append(relKeys, k)
					}
					sort.Strings(relKeys)
					for _, targetName := range relKeys {
						sb.WriteString(fmt.Sprintf("    - %s: %s\n", targetName, char.Relationship[targetName]))
					}
				}
				// Добавляем вывод Memories
				if char.Memories != "" {
					sb.WriteString(fmt.Sprintf("  Memories: %s\n", char.Memories))
				}
				// Добавляем вывод PlotHook
				if char.PlotHook != "" {
					sb.WriteString(fmt.Sprintf("  PlotHook: %s\n", char.PlotHook))
				}
				// Можно добавить другие поля при необходимости, например, Relationships
			}
		} else {
			sb.WriteString("(No valid characters found in setup data)\n")
		}
	} else {
		sb.WriteString("(Characters data missing in setup map)\n")
	}
	sb.WriteString("\n")

	// 3. Извлекаем данные из плана сцены (scene_focus, cards)
	sceneFocusStr := "(Scene focus not provided)"
	cardsStr := "(Cards not provided or invalid format)"

	scenePlanRaw, planOk := setupMap["full_initial_scene_plan"]
	if planOk {
		if scenePlanMap, mapOk := scenePlanRaw.(map[string]interface{}); mapOk {
			// 3.1 Извлекаем scene_focus
			if focus, focusOk := scenePlanMap["scene_focus"].(string); focusOk && focus != "" {
				sceneFocusStr = focus
			}

			// 3.2 Извлекаем и форматируем cards
			if cardsRaw, cardsOk := scenePlanMap["cards"]; cardsOk && cardsRaw != nil {
				// ПРЕДПОЛОЖЕНИЕ: cardsRaw это []interface{}, где каждый элемент - map[string]interface{} с полем "name"
				if cardsList, listOk := cardsRaw.([]interface{}); listOk && len(cardsList) > 0 {
					var cardsBuilder strings.Builder
					validCardsFound := false
					for _, cardInterface := range cardsList { // Используем _, чтобы убрать ошибку 'i declared and not used'
						if cardMap, cardMapOk := cardInterface.(map[string]interface{}); cardMapOk {
							if name, nameOk := cardMap["name"].(string); nameOk && name != "" {
								cardsBuilder.WriteString(fmt.Sprintf("  %s\n", name))
								validCardsFound = true
								// Можно добавить вывод других полей карты, если они есть и нужны
							}
						}
					}
					if validCardsFound {
						cardsStr = cardsBuilder.String()
					} else {
						cardsStr = "(No valid cards found in scene plan)"
					}
				} else {
					cardsStr = "(Cards list is empty or not a list in scene plan)"
				}
			} else {
				cardsStr = "(Cards data missing in scene plan)"
			}

		} else {
			// Если full_initial_scene_plan не карта, это ошибка
			return "", fmt.Errorf("invalid format for 'full_initial_scene_plan' in setupMap: expected a map")
		}
	} else {
		// Если плана сцены нет вообще
		return "", fmt.Errorf("'full_initial_scene_plan' missing in setupMap for setup generation")
	}

	// 4. Добавляем scene_focus
	sb.WriteString("\nScene Focus:\n") // Новый формат
	sb.WriteString(sceneFocusStr)
	sb.WriteString("\n")

	// 5. Добавляем карты (персонажей/сущности сцены)
	sb.WriteString("\nScene Characters:\n")     // Новый формат
	sb.WriteString(strings.TrimSpace(cardsStr)) // Убираем лишний перенос строки, если он есть
	sb.WriteString("\n")

	// 6. Добавляем финальную инструкцию
	sb.WriteString("\nTask:\n") // Новый формат
	sb.WriteString("Generate the initial narrative scene based on the story config, the full list of characters, the scene focus, and the scene cards/characters listed above.")

	return strings.TrimSpace(sb.String()), nil
}
