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

// extractJsonContent извлекает JSON из блока ```json ... ``` или пытается очистить края.
func extractJsonContent(rawText string) string {
	// 1. Сначала обрезаем пробельные символы с краев
	cleaned := strings.TrimSpace(rawText)

	// 2. Пытаемся найти полный блок ```...``` с помощью регулярного выражения
	matches := jsonBlockRegex.FindStringSubmatch(cleaned)
	if len(matches) > 1 {
		// Нашли полный блок. Group 1 содержит содержимое внутри ```...```
		return strings.TrimSpace(matches[1])
	}

	// 3. Полный блок ```...``` не найден regex-ом.
	//    Пытаемся очистить некорректную/неполную обертку.

	//    Сначала проверяем и удаляем суффикс ```
	if strings.HasSuffix(cleaned, "```") {
		cleaned = strings.TrimSuffix(cleaned, "```")
		cleaned = strings.TrimSpace(cleaned) // Убираем пробелы/переносы перед ```
	}

	//    Затем проверяем и удаляем префикс ``` (возможно, с языком)
	if strings.HasPrefix(cleaned, "```") {
		firstNewline := strings.Index(cleaned, "\n")
		if firstNewline != -1 {
			// Нашли ``` и перенос строки после него - берем все после переноса
			cleaned = strings.TrimSpace(cleaned[firstNewline+1:])
		} else {
			// Не нашли переноса строки после ```.
			// Возможно, это ```{} или ``` json {} без переноса.
			// Просто удаляем ```. Если там был язык (```json{}), останется "json{}",
			// что все равно вызовет ошибку парсинга JSON - это приемлемо.
			cleaned = strings.TrimPrefix(cleaned, "```")
			// Дополнительно обрежем пробелы, если было '``` {}'
			cleaned = strings.TrimSpace(cleaned)
		}
	}

	// 4. Возвращаем результат после попыток очистки.
	return cleaned
}

// stringShort обрезает строку до maxLen символов.
func stringShort(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
