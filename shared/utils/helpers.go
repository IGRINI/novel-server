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

func ExtractJsonContent(rawText string) string {
	rawText = strings.TrimSpace(rawText)

	// 1. Поиск ```json ... ```
	jsonBlockRegex := regexp.MustCompile("(?s)```json\\s*(\\{.*?\\}|\\s*\\[.*?\\])\\s*```")
	matches := jsonBlockRegex.FindStringSubmatch(rawText)
	if len(matches) > 1 {
		content := strings.TrimSpace(matches[1])
		if isValidJson(content) {
			return content
		}
	}

	// 2. Поиск ``` ... ``` (если ```json не найден или невалиден)
	anyBlockRegex := regexp.MustCompile("(?s)```\\s*(\\{.*?\\}|\\s*\\[.*?\\])\\s*```")
	matches = anyBlockRegex.FindStringSubmatch(rawText)
	if len(matches) > 1 {
		content := strings.TrimSpace(matches[1])
		if isValidJson(content) {
			return content
		}
	}

	// 3. Очистка от ``` по краям (если не найдены блоки или их содержимое невалидно)
	cleanedText := rawText
	if strings.HasPrefix(cleanedText, "```json") {
		cleanedText = strings.TrimPrefix(cleanedText, "```json")
	} else if strings.HasPrefix(cleanedText, "```") {
		cleanedText = strings.TrimPrefix(cleanedText, "```")
	}
	cleanedText = strings.TrimSuffix(cleanedText, "```")
	cleanedText = strings.TrimSpace(cleanedText)
	if isValidJson(cleanedText) && (strings.HasPrefix(cleanedText, "{") || strings.HasPrefix(cleanedText, "[")) {
		return cleanedText
	}

	// 4. Поиск между первой { и последней }
	firstBrace := strings.Index(rawText, "{")
	lastBrace := strings.LastIndex(rawText, "}")
	firstBracket := strings.Index(rawText, "[")
	lastBracket := strings.LastIndex(rawText, "]")

	var potentialJson string

	// Определяем, что идет раньше: { или [
	startBrace := -1
	endBrace := -1
	isObject := false

	if firstBrace != -1 && (firstBracket == -1 || firstBrace < firstBracket) {
		// Начинается с {
		startBrace = firstBrace
		endBrace = lastBrace
		isObject = true
	} else if firstBracket != -1 {
		// Начинается с [ (или { не найдена)
		startBrace = firstBracket
		endBrace = lastBracket
	}

	if startBrace != -1 && endBrace != -1 && endBrace > startBrace {
		potentialJson = rawText[startBrace : endBrace+1]
		if isValidJson(potentialJson) {
			// Доп. проверка, что это действительно объект/массив, а не просто текст со скобками
			trimmedPotential := strings.TrimSpace(potentialJson)
			if (isObject && strings.HasPrefix(trimmedPotential, "{") && strings.HasSuffix(trimmedPotential, "}")) ||
				(!isObject && strings.HasPrefix(trimmedPotential, "[") && strings.HasSuffix(trimmedPotential, "]")) {
				return potentialJson
			}
		}
	}

	// 6. Финальная попытка с оригинальной (но жадной) регуляркой
	// Ищем именно объект {}, т.к. конфиг - это объект
	objectRegexFallback := regexp.MustCompile(`(?s)(\{.*\})`)
	fallbackMatches := objectRegexFallback.FindStringSubmatch(rawText)
	if len(fallbackMatches) > 1 {
		content := strings.TrimSpace(fallbackMatches[1])
		if isValidJson(content) {
			return content
		}
	}

	// 7. Возврат пустой строки
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
