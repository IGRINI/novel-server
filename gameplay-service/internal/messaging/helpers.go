package messaging

import (
	"regexp"
	"strings"
)

// <<< Регулярное выражение для извлечения JSON из ```json ... ``` блока >>>
// (?s) - флаг: '.' совпадает с символом новой строки
// \x60\x60\x60 - открывающие ```
// (?:\w+)? - опциональный идентификатор языка (json, yaml и т.д.), незахватываемый
// \s* - пробелы
// (.*?) - НЕЖАДНАЯ захватывающая группа 1: любой текст (минимально возможный)
// \s* - пробелы
// \x60\x60\x60 - закрывающие ```
var jsonBlockRegex = regexp.MustCompile(`(?s)` + "```" + `(?:\w+)?\s*(.*?)\s*` + "```")

// castToStringSlice пытается преобразовать []interface{} в []string.
func castToStringSlice(slice []interface{}) []string {
	if slice == nil {
		return nil
	}
	strSlice := make([]string, 0, len(slice))
	for _, item := range slice {
		if str, ok := item.(string); ok {
			strSlice = append(strSlice, str)
		}
	}
	return strSlice
}

// extractJsonContent извлекает JSON из блока ```json ... ``` или возвращает исходный текст.
func extractJsonContent(rawText string) string {
	matches := jsonBlockRegex.FindStringSubmatch(rawText)
	if len(matches) > 1 {
		// Group 1 contains the content inside ```...```
		return strings.TrimSpace(matches[1])
	}
	// If no block found, return the original text trimmed
	return strings.TrimSpace(rawText)
}

// stringShort обрезает строку до maxLen символов.
func stringShort(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
