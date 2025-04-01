package service

import (
	"log"
	"novel-server/internal/domain"
)

// processUserChoice обрабатывает последствия выбора пользователя
func processUserChoice(state *domain.NovelState, scene domain.Scene, choiceText string) {
	log.Printf("[processUserChoice] Processing choice: %s", choiceText)

	// Сначала проверяем event типа choice (в конце сцены)
	for _, event := range scene.Events {
		if event.EventType == "choice" && event.Choices != nil {
			// Проверяем каждый выбор
			for _, choice := range event.Choices {
				if choice.Text == choiceText {
					log.Printf("[processUserChoice] Found matching 'choice': %s", choiceText)
					processChoiceConsequences(state, choice.Consequences)
					return
				}
			}
		}
	}

	// Если не нашли в обычных выборах, проверяем inline_choice и inline_response
	processInlineChoice(state, scene, choiceText)
}

// processInlineChoice обрабатывает inline_choice и inline_response события
func processInlineChoice(state *domain.NovelState, scene domain.Scene, choiceText string) {
	var inlineChoiceId string

	// Ищем событие типа inline_choice
	for _, event := range scene.Events {
		if event.EventType == "inline_choice" && event.Data != nil {
			if choiceId, ok := event.Data["choice_id"].(string); ok {
				inlineChoiceId = choiceId
				break
			}
		}
	}

	// Если нашли ID выбора, ищем соответствующий inline_response
	if inlineChoiceId != "" {
		for _, event := range scene.Events {
			if event.EventType == "inline_response" && event.Data != nil {
				dataChoiceId, hasChoiceId := event.Data["choice_id"].(string)
				responses, hasResponses := event.Data["responses"].([]interface{})

				if hasChoiceId && hasResponses && dataChoiceId == inlineChoiceId {
					// Перебираем все возможные ответы
					for _, respItem := range responses {
						response, ok := respItem.(map[string]interface{})
						if !ok {
							continue
						}

						respChoiceText, hasText := response["choice_text"].(string)
						if !hasText || respChoiceText != choiceText {
							continue
						}

						log.Printf("[processInlineChoice] Found matching inline choice: %s", choiceText)

						// Обрабатываем последствия inline-выбора
						// В примере inline_response не содержит consequences, но в будущем может содержать
						if consequences, hasConsequences := response["consequences"].(map[string]interface{}); hasConsequences {
							processChoiceConsequences(state, consequences)
						}

						return
					}
				}
			}
		}
	}

	log.Printf("[processInlineChoice] No matching inline choice found for: %s", choiceText)
}

// processChoiceConsequences обрабатывает последствия выбора
func processChoiceConsequences(state *domain.NovelState, consequences map[string]interface{}) {
	if consequences == nil {
		return
	}

	// Обработка флагов
	if flags, ok := consequences["global_flags"].([]interface{}); ok {
		log.Printf("[processChoiceConsequences] Processing global flags")
		for _, flag := range flags {
			if flagStr, ok := flag.(string); ok {
				state.GlobalFlags = append(state.GlobalFlags, flagStr)
			}
		}
	}

	// Обработка отношений
	if relationships, ok := consequences["relationship"].(map[string]interface{}); ok {
		log.Printf("[processChoiceConsequences] Processing relationships")
		for character, value := range relationships {
			if intValue, ok := value.(float64); ok {
				current, exists := state.Relationship[character]
				if exists {
					state.Relationship[character] = current + int(intValue)
				} else {
					state.Relationship[character] = int(intValue)
				}
			}
		}
	}

	// Обработка переменных истории
	if variables, ok := consequences["story_variables"].(map[string]interface{}); ok {
		log.Printf("[processChoiceConsequences] Processing story variables")
		for key, value := range variables {
			state.StoryVariables[key] = value
		}
	}
}
