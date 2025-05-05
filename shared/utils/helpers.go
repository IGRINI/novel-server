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
			if !strings.Contains(balancedText[len(balancedText)-5:], `"`) { // Примерная проверка
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
			if !strings.Contains(balancedText[len(balancedText)-5:], `"`) {
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
			if !strings.Contains(balancedText[len(balancedText)-5:], `"`) {
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
			if !strings.Contains(balancedText[len(balancedText)-5:], `"`) {
				balancedText = balancedText[:len(balancedText)-1]
				balanceCurly++
			} else {
				break
			}
		}
	}

	return balancedText
}

func ExtractJsonContent(rawText string) string {
	rawText = strings.TrimSpace(rawText)

	// 1. Поиск ```json ... ```
	jsonBlockRegex := regexp.MustCompile("(?s)```json\\s*(\\{.*?\\}|\\s*\\[.*?\\])\\s*```")
	matches := jsonBlockRegex.FindStringSubmatch(rawText)
	if len(matches) > 1 {
		content := strings.TrimSpace(matches[1])
		balancedContent := balanceBrackets(content) // Балансировка
		if isValidJson(balancedContent) {
			return balancedContent
		}
		// Если не валидно, продолжаем поиск
	}

	// 2. Поиск ``` ... ``` (если ```json не найден или невалиден)
	anyBlockRegex := regexp.MustCompile("(?s)```\\s*(\\{.*?\\}|\\s*\\[.*?\\])\\s*```")
	matches = anyBlockRegex.FindStringSubmatch(rawText)
	if len(matches) > 1 {
		content := strings.TrimSpace(matches[1])
		balancedContent := balanceBrackets(content) // Балансировка
		if isValidJson(balancedContent) {
			return balancedContent
		}
		// Если не валидно, продолжаем поиск
	}

	// 4. Поиск между первой {/[ и последней }/] - основная логика извлечения
	firstBrace := strings.Index(rawText, "{")
	lastBrace := strings.LastIndex(rawText, "}")
	firstBracket := strings.Index(rawText, "[")
	lastBracket := strings.LastIndex(rawText, "]")

	var potentialJson string

	startIdx := -1
	endIdx := -1

	// Определяем, что идет раньше и где искать конец
	if firstBrace != -1 && (firstBracket == -1 || firstBrace < firstBracket) {
		// Начинается с {
		startIdx = firstBrace
		endIdx = lastBrace // Ищем последнюю }
	} else if firstBracket != -1 {
		// Начинается с [
		startIdx = firstBracket
		endIdx = lastBracket // Ищем последнюю ]
	}

	if startIdx != -1 {
		// Если нашли начало '{' или '['
		if endIdx != -1 && endIdx > startIdx {
			// Если нашли и конец, берем срез до него
			potentialJson = rawText[startIdx : endIdx+1]
		} else {
			// Если конец '}' или ']' НЕ найден, берем все от начала до конца строки
			// Это важно для случаев, когда последняя скобка отсутствует
			potentialJson = rawText[startIdx:]
		}

		// Всегда пытаемся сбалансировать то, что извлекли
		balancedContent := balanceBrackets(potentialJson)
		if isValidJson(balancedContent) {
			// Проверяем, что результат начинается с нужной скобки (доп. проверка)
			trimmedBalanced := strings.TrimSpace(balancedContent)
			if (strings.HasPrefix(trimmedBalanced, "{") && strings.HasSuffix(trimmedBalanced, "}")) ||
				(strings.HasPrefix(trimmedBalanced, "[") && strings.HasSuffix(trimmedBalanced, "]")) {
				return balancedContent
			}
		}
		// Если невалидно или не прошло доп. проверку, продолжаем
	}

	// 5. Fallback с жадными регулярками (если шаг 4 не сработал)
	// Ищем именно объект {}, т.к. наш конфиг - это объект
	objectRegexFallback := regexp.MustCompile(`(?s)(\{.*\})`)
	fallbackMatches := objectRegexFallback.FindStringSubmatch(rawText)
	if len(fallbackMatches) > 1 {
		content := strings.TrimSpace(fallbackMatches[1])
		balancedContent := balanceBrackets(content) // Балансировка
		if isValidJson(balancedContent) {
			return balancedContent
		}
	}

	// Попробуем также для массива
	arrayRegexFallback := regexp.MustCompile(`(?s)(\[.*\])`)
	fallbackMatches = arrayRegexFallback.FindStringSubmatch(rawText)
	if len(fallbackMatches) > 1 {
		content := strings.TrimSpace(fallbackMatches[1])
		balancedContent := balanceBrackets(content) // Балансировка
		if isValidJson(balancedContent) {
			return balancedContent
		}
	}

	// 6. Если ничего не помогло, возвращаем как есть (но обрезанное)
	// Это крайний случай, если JSON внутри текста без маркеров
	if firstBrace != -1 { // Если нашли хоть какую-то фигурную скобку
		return strings.TrimSpace(rawText[firstBrace:])
	}
	if firstBracket != -1 { // Если нашли хоть какую-то квадратную скобку
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
