package utils

import (
	"novel-server/shared/models"
	//"strings" // Не используется пока
)

// FormatInputForModeration возвращает UserInput для задачи модерации.
// Теперь возвращает форматированный текст конфига.
func FormatInputForModeration(config models.Config) (string, error) {
	// Используем FormatConfigToString для получения текстового представления
	// Передаем false для isAdultContent, так как сама модерация должна определить это.
	// Если флаг isAdultContent важен для самой модерации, его нужно получить и передать.
	// Пока предполагаем, что модерация анализирует сам текст конфига.
	formattedConfig := FormatConfigToString(config, false) // Использует AppendBasicConfigInfo
	return formattedConfig, nil
}
