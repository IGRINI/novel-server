package service

import (
	"log"
	"strings"
)

// FixJSON проверяет и исправляет потенциально некорректный JSON
// В частности, решает проблему незакрытых скобок в конце JSON
func FixJSON(jsonStr string) string {
	if jsonStr == "" {
		return jsonStr
	}

	// Подсчитываем открывающие и закрывающие скобки
	counts := map[rune]int{
		'{': 0,
		'}': 0,
		'[': 0,
		']': 0,
	}

	// Счетчик для поиска в строках (не считаем скобки внутри строк)
	inString := false
	escaped := false

	for _, char := range jsonStr {
		if char == '"' && !escaped {
			inString = !inString
		}

		if !inString {
			if count, exists := counts[char]; exists {
				counts[char] = count + 1
			}
		}

		// Отслеживаем экранирование для корректного определения строк
		if char == '\\' && !escaped {
			escaped = true
		} else {
			escaped = false
		}
	}

	// Проверяем и исправляем баланс
	fixedJSON := jsonStr
	imbalance := counts['{'] - counts['}']
	if imbalance > 0 {
		log.Printf("[FixJSON] Fixing unbalanced curly braces. Missing closing braces: %d", imbalance)
		fixedJSON += strings.Repeat("}", imbalance)
	}

	imbalance = counts['['] - counts[']']
	if imbalance > 0 {
		log.Printf("[FixJSON] Fixing unbalanced square brackets. Missing closing brackets: %d", imbalance)
		fixedJSON += strings.Repeat("]", imbalance)
	}

	if fixedJSON != jsonStr {
		log.Printf("[FixJSON] JSON was fixed. Original length: %d, Fixed length: %d",
			len(jsonStr), len(fixedJSON))
	}

	return fixedJSON
}
