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

// ExtractJsonContent пытается извлечь первый валидный JSON блок (объект {} или массив [])
// из строки, игнорируя возможный текст до и после.
// Использует регулярное выражение для поиска.
var jsonRegex = regexp.MustCompile(`(?s)\{.*?\}|(?s)\[.*?\]`)

func ExtractJsonContent(rawText string) string {
	matches := jsonRegex.FindAllString(rawText, -1)
	if len(matches) == 0 {
		return "" // Ничего похожего на JSON не найдено
	}

	// Пытаемся найти первый валидный JSON
	for _, match := range matches {
		var js json.RawMessage
		if err := json.Unmarshal([]byte(match), &js); err == nil {
			// Проверяем, что это действительно объект или массив
			trimmed := strings.TrimSpace(match)
			if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
				(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
				return match // Возвращаем первый валидный JSON
			}
		}
	}

	// Если валидный JSON не найден среди совпадений, возвращаем пустую строку
	// или, возможно, первое совпадение, если оно единственное?
	// Пока возвращаем пустую строку для надежности.
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
