package ai

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"novel-server/internal/model"
)

// Логгер и константы теперь используются из client.go, т.к. файлы в одном пакете
// var log = zerolog.New(zerolog.NewConsoleWriter()).With().Timestamp().Logger()
// const (
// 	storyVarDefinitionsMarker = "Story Variable Definitions:"
// 	choiceMarker              = "Choice:"
// 	coreStatsResetMarker      = "Core Stats Reset:"
// )

// ParseNovelContentResponse парсит текстовый ответ AI в структуру ParsedNovelContent.
func ParseNovelContentResponse(responseText string) (*model.ParsedNovelContent, error) {
	// ... (код функции ParseNovelContentResponse из client.go) ...
	if responseText == "" {
		return nil, errors.New("пустой ответ для парсинга")
	}

	scanner := bufio.NewScanner(strings.NewReader(responseText))
	parsed := &model.ParsedNovelContent{
		StoryVariableDefinitions: make(map[string]string),
		Choices:                  make([]model.ChoiceEvent, 0),
	}
	lineNumber := 0
	parsingState := "start" // "start", "summary", "direction", "check_after_direction", "definitions", "choices", "game_over_continuation_desc", "game_over_continuation_stats", "game_over_continuation_ending", "check_after_continuation"

	var currentChoice *model.ChoiceEvent
	choiceLineCounter := 0

	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())

		if line == "" {
			continue // Пропускаем пустые строки
		}

		// --- Обработка Choice маркера ---
		if strings.HasPrefix(line, choiceMarker) {
			if currentChoice != nil && choiceLineCounter != 3 {
				return nil, fmt.Errorf("ошибка формата: незавершенный блок Choice перед строкой %d (%s)", lineNumber, line)
			}
			currentChoice = &model.ChoiceEvent{
				Choices: make([]model.ChoiceOption, 2),
			}
			choiceLineCounter = 0
			shuffleableStr := strings.TrimSpace(strings.TrimPrefix(line, choiceMarker))
			if shuffleableStr == "" || shuffleableStr == "1" {
				currentChoice.Shuffleable = true
			} else if shuffleableStr == "0" {
				currentChoice.Shuffleable = false
			} else {
				return nil, fmt.Errorf("ошибка формата на строке %d: неверный флаг shuffleable '%s'", lineNumber, shuffleableStr)
			}
			parsingState = "choices"
			continue
		}
		// --- Конец обработки Choice маркера ---

		switch parsingState {
		case "start":
			parsed.StorySummarySoFar = line
			parsingState = "summary"
		case "summary":
			parsed.FutureDirection = line
			parsingState = "direction"
		case "direction":
			if line == storyVarDefinitionsMarker {
				parsingState = "definitions"
			} else if strings.HasPrefix(line, coreStatsResetMarker) {
				parsed.NewPlayerDescription = parsed.FutureDirection
				parsed.FutureDirection = parsed.StorySummarySoFar
				statsJSON := strings.TrimSpace(strings.TrimPrefix(line, coreStatsResetMarker))
				if statsJSON == "" {
					return nil, fmt.Errorf("ошибка формата на строке %d: пустой JSON после '%s'", lineNumber, coreStatsResetMarker)
				}
				parsed.CoreStatsReset = statsJSON
				parsingState = "game_over_continuation_stats"
			} else {
				return nil, fmt.Errorf("ошибка формата на строке %d: ожидался маркер '%s' или '%s', получено: '%s'", lineNumber, storyVarDefinitionsMarker, coreStatsResetMarker, line)
			}
		case "game_over_continuation_stats":
			parsed.EndingTextPrevious = line
			parsingState = "game_over_continuation_ending"

		case "game_over_continuation_ending":
			if line == storyVarDefinitionsMarker {
				parsingState = "definitions"
			} else {
				return nil, fmt.Errorf("ошибка формата на строке %d после блока continuation: ожидался '%s', получено: '%s'", lineNumber, storyVarDefinitionsMarker, line)
			}

		case "definitions":
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				varName := strings.TrimSpace(parts[0])
				varDesc := strings.TrimSpace(parts[1])
				if varName != "" {
					parsed.StoryVariableDefinitions[varName] = varDesc
				}
			} else {
				log.Warn().Int("line", lineNumber).Str("content", line).Msg("Неверный формат строки в блоке определений переменных, игнорируется")
			}

		case "choices":
			if currentChoice == nil {
				return nil, fmt.Errorf("внутренняя ошибка парсера: состояние 'choices' без currentChoice на строке %d", lineNumber)
			}
			choiceLineCounter++
			switch choiceLineCounter {
			case 1:
				currentChoice.Description = line
			case 2:
				text, consequences, err := parseChoiceLine(line)
				if err != nil {
					return nil, fmt.Errorf("ошибка парсинга варианта 1 на строке %d: %w", lineNumber, err)
				}
				var parsedConsequences model.Consequences
				if err := json.Unmarshal([]byte(consequences), &parsedConsequences); err != nil {
					return nil, fmt.Errorf("ошибка парсинга JSON последствий варианта 1 на строке %d: %w", lineNumber, err)
				}
				currentChoice.Choices[0] = model.ChoiceOption{Text: text, Consequences: parsedConsequences}
			case 3:
				text, consequences, err := parseChoiceLine(line)
				if err != nil {
					return nil, fmt.Errorf("ошибка парсинга варианта 2 на строке %d: %w", lineNumber, err)
				}
				var parsedConsequences model.Consequences
				if err := json.Unmarshal([]byte(consequences), &parsedConsequences); err != nil {
					return nil, fmt.Errorf("ошибка парсинга JSON последствий варианта 2 на строке %d: %w", lineNumber, err)
				}
				currentChoice.Choices[1] = model.ChoiceOption{Text: text, Consequences: parsedConsequences}
				parsed.Choices = append(parsed.Choices, *currentChoice)
				currentChoice = nil
				choiceLineCounter = 0
			default:
				return nil, fmt.Errorf("ошибка формата: неожиданная строка (%d) '%s' в блоке Choice на строке %d", choiceLineCounter, line, lineNumber)
			}
		default:
			return nil, fmt.Errorf("неизвестное состояние парсера: %s", parsingState)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("ошибка чтения ответа: %w", err)
	}

	if currentChoice != nil && choiceLineCounter != 3 {
		return nil, fmt.Errorf("ошибка формата: ответ закончился незавершенным блоком Choice")
	}

	if lineNumber <= 1 {
		parsed.EndingText = parsed.StorySummarySoFar
		parsed.StorySummarySoFar = ""
		parsed.FutureDirection = ""
		return parsed, nil
	}

	if parsed.CoreStatsReset != "" && parsed.EndingTextPrevious == "" {
		return nil, errors.New("ошибка формата: блок continuation не содержит текст концовки предыдущего персонажа")
	}

	if parsed.CoreStatsReset == "" && parsed.EndingText == "" && len(parsed.Choices) == 0 && len(parsed.StoryVariableDefinitions) == 0 {
		log.Warn().Msg("Парсер завершился без ошибок, но не найдено ни выборов, ни определений переменных. Ответ может быть неполным.")
	}

	return parsed, nil
}

// parseChoiceLine разделяет строку варианта выбора на текст и JSON последствий.
// Улучшенная версия: ищет первую '{}' и соответствующую ей '}'.
func parseChoiceLine(line string) (string, model.ChoiceConsequences, error) {
	// ... (код функции parseChoiceLine из client.go) ...
	jsonStart := strings.Index(line, "{") // Ищем *первую* '{'
	if jsonStart == -1 {
		// Если нет '{', возможно, это выбор без последствий вообще
		textPart := strings.TrimSpace(line)
		if textPart == "" {
			return "", "", errors.New("пустая строка варианта выбора")
		}
		log.Trace().Str("line", line).Msg("Не найден символ '{' для JSON последствий, возвращаем пустой JSON.")
		return textPart, model.ChoiceConsequences("{}"), nil // Возвращаем пустой JSON
	}

	// Текст - это всё до первой '{'
	textPart := strings.TrimSpace(line[:jsonStart])
	if textPart == "" {
		// Это может быть нормально, если JSON идет сразу
		log.Trace().Str("line", line).Msg("Текст варианта выбора пуст (JSON начинается сразу)")
		// return "", "", errors.New("пустой текст варианта выбора")
	}

	// Потенциальная JSON часть, начиная с первой '{'
	potentialJsonPart := line[jsonStart:]

	// Ищем соответствующую закрывающую скобку '}'
	braceLevel := 0
	jsonEnd := -1
	inString := false
	var prevChar rune
	for i, r := range potentialJsonPart {
		switch r {
		case '"':
			if prevChar != '\\' {
				inString = !inString
			}
		case '{':
			if !inString {
				braceLevel++
			}
		case '}':
			if !inString {
				braceLevel--
				if braceLevel == 0 {
					jsonEnd = i + 1 // Позиция *после* закрывающей скобки
					goto FoundBraceParse
				}
				if braceLevel < 0 { // Нашли лишнюю закрывающую скобку до того, как открыли
					return "", "", fmt.Errorf("нарушен баланс скобок (} перед {) в JSON последствий: %s", potentialJsonPart)
				}
			}
		}
		prevChar = r
	}
FoundBraceParse:

	if jsonEnd == -1 || braceLevel != 0 {
		return "", "", fmt.Errorf("не найдена соответствующая '}' или нарушен баланс скобок для JSON последствий: %s", potentialJsonPart)
	}

	// Извлекаем точную часть JSON
	jsonPart := potentialJsonPart[:jsonEnd]

	// Проверяем валидность JSON (можно сделать более строгим)
	var consequences map[string]interface{}
	if err := json.Unmarshal([]byte(jsonPart), &consequences); err != nil {
		log.Warn().Str("potentialJson", potentialJsonPart).Str("extractedJson", jsonPart).Err(err).Msg("Ошибка проверки валидности JSON в parseChoiceLine")
		return "", "", fmt.Errorf("некорректный JSON последствий (проверка): %w. Извлечено: %s", err, jsonPart)
	}

	// Проверка на core_stats_change больше не нужна, т.к. может отсутствовать

	return textPart, model.ChoiceConsequences(jsonPart), nil
}
