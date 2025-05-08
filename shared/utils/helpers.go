package utils

import (
	"encoding/json"
	"regexp"
	"strings"
)

// CastToStringSlice преобразует срез interface{} в срез string.
// Элементы, которые не являются строками, игнорируются.
func CastToStringSlice(slice []interface{}) []string {
	stringSlice := make([]string, 0, len(slice))
	for _, v := range slice {
		if s, ok := v.(string); ok {
			stringSlice = append(stringSlice, s)
		}
	}
	return stringSlice
}

// jsonRegex используется как fallback
// var jsonRegex = regexp.MustCompile(`(?s)(\{.*\})|(?s)(\[.*\])`) // Захватываем жадно, так как это fallback

// isValidJson проверяет, можно ли распарсить строку как JSON
func isValidJson(s string) bool {
	var js json.RawMessage
	return json.Unmarshal([]byte(s), &js) == nil
}

// balanceBrackets пытается добавить/удалить закрывающие скобки/квадратные скобки в конце строки.
// Улучшенная версия, игнорирующая скобки внутри строк.
func balanceBrackets(text string) string {
	balanceCurly := 0
	balanceSquare := 0
	inString := false
	escape := false

	// Сначала подсчитываем баланс скобок, игнорируя строки
	for _, r := range text {
		if escape {
			escape = false
			continue
		}
		if r == '\\' {
			escape = true
			continue
		}
		// Важно: Проверяем кавычку ДО проверки скобок
		if r == '"' {
			inString = !inString
		}
		// Считаем скобки только если мы НЕ внутри строки
		if !inString {
			switch r {
			case '{':
				balanceCurly++
			case '}':
				balanceCurly--
			case '[':
				balanceSquare++
			case ']':
				balanceSquare--
			}
		}
	}

	// Теперь применяем балансировку
	balancedText := text
	trimmed := strings.TrimSpace(text)

	if strings.HasPrefix(trimmed, "{") {
		// Ожидаем объект на верхнем уровне
		for balanceCurly > 0 {
			balancedText += "}"
			balanceCurly--
		}
		// Удаляем лишние } только если они точно в конце и баланс отрицательный
		for balanceCurly < 0 && strings.HasSuffix(balancedText, "}") {
			// Перед удалением убедимся, что скобка не часть строки (упрощенная проверка)
			if !strings.Contains(balancedText[len(balancedText)-5:], "\"") { // Примерная проверка
				balancedText = balancedText[:len(balancedText)-1]
				balanceCurly++
			} else {
				break // Не рискуем удалять, если рядом кавычки
			}
		}
		// После добавления/удаления фигурных, проверим квадратные (менее вероятно, но возможно)
		for balanceSquare > 0 && strings.HasSuffix(balancedText, "}") { // Если закончили на }, добавляем ]
			balancedText += "]"
			balanceSquare--
		}
		for balanceSquare < 0 && strings.HasSuffix(balancedText, "]") { // Если закончили на ]
			if !strings.Contains(balancedText[len(balancedText)-5:], "\"") {
				balancedText = balancedText[:len(balancedText)-1]
				balanceSquare++
			} else {
				break
			}
		}

	} else if strings.HasPrefix(trimmed, "[") {
		// Ожидаем массив на верхнем уровне
		for balanceSquare > 0 {
			balancedText += "]"
			balanceSquare--
		}
		for balanceSquare < 0 && strings.HasSuffix(balancedText, "]") {
			if !strings.Contains(balancedText[len(balancedText)-5:], "\"") {
				balancedText = balancedText[:len(balancedText)-1]
				balanceSquare++
			} else {
				break
			}
		}
		// Проверим фигурные
		for balanceCurly > 0 && strings.HasSuffix(balancedText, "]") {
			balancedText += "}"
			balanceCurly--
		}
		for balanceCurly < 0 && strings.HasSuffix(balancedText, "}") {
			if !strings.Contains(balancedText[len(balancedText)-5:], "\"") {
				balancedText = balancedText[:len(balancedText)-1]
				balanceCurly++
			} else {
				break
			}
		}
	}

	return balancedText
}

// processPotentialJson пытается привести строку к валидному JSON (trim, балансировка скобок)
func processPotentialJson(content string) string {
	trimmed := strings.TrimSpace(content)
	if isValidJson(trimmed) {
		return trimmed
	}
	balanced := balanceBrackets(trimmed)
	if isValidJson(balanced) {
		return balanced
	}
	return ""
}

func ExtractJsonContent(rawText string) string {
	rawText = strings.TrimSpace(rawText)

	// 1. Поиск ```json ... ```
	jsonBlockRegex := regexp.MustCompile("(?s)```json\\s*([\\s\\S]*?)\\s*```")
	matches := jsonBlockRegex.FindStringSubmatch(rawText)
	if len(matches) > 1 {
		if result := processPotentialJson(matches[1]); result != "" {
			return result
		}
	}

	// 2. Поиск ``` ... ``` (если ```json не найден или невалиден)
	anyBlockRegex := regexp.MustCompile("(?s)```\\s*([\\s\\S]*?)\\s*```")
	matches = anyBlockRegex.FindStringSubmatch(rawText)
	if len(matches) > 1 {
		if result := processPotentialJson(matches[1]); result != "" {
			return result
		}
	}

	// 4. Поиск между первой {/[ и последней }/]
	firstBrace := strings.Index(rawText, "{")
	lastBrace := strings.LastIndex(rawText, "}")
	firstBracket := strings.Index(rawText, "[")
	lastBracket := strings.LastIndex(rawText, "]")

	var potentialJson string
	startIdx := -1
	endIdx := -1

	if firstBrace != -1 && (firstBracket == -1 || firstBrace < firstBracket) {
		startIdx = firstBrace
		endIdx = lastBrace
	} else if firstBracket != -1 {
		startIdx = firstBracket
		endIdx = lastBracket
	}

	if startIdx != -1 {
		if endIdx != -1 && endIdx > startIdx {
			potentialJson = rawText[startIdx : endIdx+1]
		} else {
			potentialJson = rawText[startIdx:]
		}
		if result := processPotentialJson(potentialJson); result != "" {
			return result
		}
	}

	// 5. Fallback с жадными регулярками (если шаг 4 не сработал)
	objectRegexFallback := regexp.MustCompile(`(?s)({[\s\S]*})`)
	fallbackMatches := objectRegexFallback.FindStringSubmatch(rawText)
	if len(fallbackMatches) > 1 {
		if result := processPotentialJson(fallbackMatches[1]); result != "" {
			return result
		}
	}

	arrayRegexFallback := regexp.MustCompile(`(?s)(\[[\s\S]*\])`)
	fallbackMatches = arrayRegexFallback.FindStringSubmatch(rawText)
	if len(fallbackMatches) > 1 {
		if result := processPotentialJson(fallbackMatches[1]); result != "" {
			return result
		}
	}

	// 6. Если ничего не помогло, возвращаем как есть (но обрезанное)
	if firstBrace != -1 {
		return strings.TrimSpace(rawText[firstBrace:])
	}
	if firstBracket != -1 {
		return strings.TrimSpace(rawText[firstBracket:])
	}

	// 7. Возврат пустой строки, если вообще ничего похожего на JSON не найдено
	return ""
}

// StringShort обрезает строку до указанной максимальной длины,
// добавляя многоточие, если строка была обрезана.
func StringShort(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return "..."
	}
	return s[:maxLen-3] + "..."
}

// GetMapKeys возвращает срез ключей из map[string]interface{}.
func GetMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
