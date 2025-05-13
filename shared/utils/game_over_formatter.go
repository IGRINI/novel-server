package utils

import (
	"novel-server/shared/models"
	// "strings"
	// "fmt"
)

// FormatInputForGameOver форматирует полное состояние игры для промпта генерации Game Over.
// Внутренне вызывает FormatFullGameStateToString.
func FormatInputForGameOver(
	config models.Config,
	setup models.NovelSetupContent,
	currentCS map[string]int,
	previousChoices []models.UserChoiceInfo,
	previousSSS string,
	previousFD string,
	previousVIS string,
	encounteredChars []string,
	isAdultContent bool,
) (string, error) {
	// На данный момент просто оборачиваем вызов FormatFullGameStateToString.
	// В будущем здесь может быть добавлена специфичная логика для Game Over.
	formattedString := FormatFullGameStateToString(
		config,
		setup,
		currentCS,
		previousChoices,
		previousSSS,
		previousFD,
		previousVIS,
		encounteredChars,
		isAdultContent,
	)
	// TODO: Адаптировать под актуальный novel_gameover_creator_prompt.md
	// Убедись, что формат соответствует ожидаемому в prompts/game-prompts/novel_gameover_creator_prompt.md
	// FormatFullGameStateToString возвращает довольно много информации, возможно, не вся нужна или нужен другой акцент.
	return formattedString, nil
}
