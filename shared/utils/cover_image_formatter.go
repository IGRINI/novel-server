package utils

import (
	"fmt"
	"strings"
	//"novel-server/shared/models" // Не используется пока
)

// FormatInputForCoverImage возвращает UserInput (промпт) для задачи генерации обложки.
func FormatInputForCoverImage(basePrompt, style, suffix string) (string, error) {
	if strings.TrimSpace(basePrompt) == "" {
		return "", fmt.Errorf("base prompt for cover image cannot be empty")
	}
	if style != "" {
		style = ", " + style
	}
	if suffix != "" && !strings.HasPrefix(suffix, " ") && !strings.HasPrefix(suffix, ",") {
		suffix = " " + suffix
	}
	return basePrompt + style + suffix, nil
}
