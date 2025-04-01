package novel_handlers

import (
	"novel-server/internal/domain"
)

// createSimplifiedResponse преобразует полный ответ в упрощенный для клиента
func createSimplifiedResponse(fullResponse *domain.NovelContentResponse) domain.SimplifiedNovelContentResponse {
	// Получаем текущую сцену
	var backgroundID string
	var characters []domain.SceneCharacter
	var events []domain.Event

	// Получаем текущую сцену из состояния
	if fullResponse.State.CurrentSceneIndex < len(fullResponse.State.Scenes) {
		currentScene := fullResponse.State.Scenes[fullResponse.State.CurrentSceneIndex]
		backgroundID = currentScene.BackgroundID
		events = currentScene.Events

		// Если NewContent содержит SceneContent, используем его
		if sceneContent, ok := fullResponse.NewContent.(*domain.SceneContent); ok {
			characters = sceneContent.Characters
		} else {
			// Создаем список персонажей для сцены из глобального списка
			for _, character := range fullResponse.State.Characters {
				characters = append(characters, domain.SceneCharacter{
					Name:       character.Name,
					Position:   character.Position,
					Expression: character.Expression,
				})
			}
		}
	}

	// Преобразуем события в упрощенные для клиента
	simplifiedEvents := convertToSimplifiedEvents(events)

	// Проверяем, завершена ли история
	isComplete := fullResponse.State.CurrentStage == domain.StageComplete
	isSetup := fullResponse.State.CurrentStage == domain.StageSetup
	var summary string

	// Если история завершена, получаем summary
	if isComplete && fullResponse.NewContent != nil {
		// В финальной стадии summary может быть передан в разных форматах
		// Пробуем извлечь его из структуры или из map
		if completeContent, ok := fullResponse.NewContent.(map[string]interface{}); ok {
			if summaryValue, ok := completeContent["summary"]; ok {
				summary = summaryValue.(string)
			}
		} else if completeStruct, ok := fullResponse.NewContent.(struct{ Summary string }); ok {
			summary = completeStruct.Summary
		}
	}

	// Добавляем информацию о backgrounds и characters, если это этап setup
	var setupBackgrounds []domain.Background
	var setupCharacters []domain.Character

	if isSetup && fullResponse.NewContent != nil {
		if setupContent, ok := fullResponse.NewContent.(*domain.SetupContent); ok {
			setupBackgrounds = setupContent.Backgrounds
			setupCharacters = setupContent.Characters
		}
	}

	// Создаем упрощенный ответ
	return domain.SimplifiedNovelContentResponse{
		CurrentSceneIndex: fullResponse.State.CurrentSceneIndex,
		BackgroundID:      backgroundID,
		Characters:        characters,
		Events:            simplifiedEvents,
		HasNextScene:      fullResponse.State.CurrentSceneIndex < fullResponse.State.SceneCount-1,
		HasPreviousScene:  fullResponse.State.CurrentSceneIndex > 0,
		IsComplete:        isComplete,
		IsSetup:           isSetup,
		Summary:           summary,
		Backgrounds:       setupBackgrounds,
		SetupCharacters:   setupCharacters,
	}
}

// convertToSimplifiedEvents преобразует обычные события в упрощенные для клиента
func convertToSimplifiedEvents(events []domain.Event) []domain.SimplifiedEvent {
	if events == nil {
		return nil
	}

	result := make([]domain.SimplifiedEvent, len(events))

	for i, event := range events {
		simplifiedEvent := domain.SimplifiedEvent{
			EventType:   event.EventType,
			Speaker:     event.Speaker,
			Text:        event.Text,
			Character:   event.Character,
			From:        event.From,
			To:          event.To,
			Description: event.Description,
		}

		// Обрабатываем особые типы событий
		switch event.EventType {
		case "choice":
			// Преобразуем выборы, скрывая последствия
			simplifiedChoices := make([]domain.SimplifiedChoice, len(event.Choices))
			for j, choice := range event.Choices {
				simplifiedChoices[j] = domain.SimplifiedChoice{
					Text: choice.Text,
					// Последствия (consequences) не передаем клиенту
				}
			}
			simplifiedEvent.Choices = simplifiedChoices

		case "inline_choice":
			// В inline_choice есть дополнительные поля
			if event.Data != nil && event.Data["choice_id"] != nil {
				if choiceID, ok := event.Data["choice_id"].(string); ok {
					simplifiedEvent.ChoiceID = choiceID
				}
			}
			simplifiedEvent.Description = event.Description

			// Преобразуем выборы, скрывая последствия
			if event.Data != nil && event.Data["choices"] != nil {
				if choices, ok := event.Data["choices"].([]interface{}); ok {
					simplifiedChoices := make([]domain.SimplifiedChoice, 0, len(choices))
					for _, choiceInterface := range choices {
						if choice, ok := choiceInterface.(map[string]interface{}); ok {
							if textVal, ok := choice["text"]; ok && textVal != nil {
								if text, ok := textVal.(string); ok {
									simplifiedChoices = append(simplifiedChoices, domain.SimplifiedChoice{
										Text: text,
										// Последствия (consequences) не передаем клиенту
									})
								}
							}
						}
					}
					simplifiedEvent.Choices = simplifiedChoices
				}
			}

		case "inline_response":
			// Обработка inline_response
			if event.Data != nil && event.Data["choice_id"] != nil {
				if choiceID, ok := event.Data["choice_id"].(string); ok {
					simplifiedEvent.ChoiceID = choiceID
				}
			}

			if event.Data != nil && event.Data["responses"] != nil {
				if responses, ok := event.Data["responses"].([]interface{}); ok {
					simplifiedResponses := make([]domain.SimplifiedResponse, 0, len(responses))

					for _, respInterface := range responses {
						if resp, ok := respInterface.(map[string]interface{}); ok {
							choiceText := ""
							if textVal, ok := resp["choice_text"]; ok && textVal != nil {
								if text, ok := textVal.(string); ok {
									choiceText = text
								}
							}

							simplifiedResponse := domain.SimplifiedResponse{
								ChoiceText: choiceText,
							}

							// Преобразуем response_events
							if responseEvents, ok := resp["response_events"].([]interface{}); ok && responseEvents != nil {
								respEvents := make([]domain.Event, 0, len(responseEvents))

								for _, eventInterface := range responseEvents {
									if eventMap, ok := eventInterface.(map[string]interface{}); ok {
										// Проверяем event_type
										if eventTypeVal, ok := eventMap["event_type"]; ok && eventTypeVal != nil {
											if eventType, ok := eventTypeVal.(string); ok {
												// Конвертируем из map в Event
												respEvent := domain.Event{
													EventType: eventType,
												}

												if speaker, ok := eventMap["speaker"].(string); ok {
													respEvent.Speaker = speaker
												}
												if text, ok := eventMap["text"].(string); ok {
													respEvent.Text = text
												}
												if expression, ok := eventMap["expression"].(string); ok {
													// Сохраняем выражение для диалогов
													if respEvent.Data == nil {
														respEvent.Data = make(map[string]interface{})
													}
													respEvent.Data["expression"] = expression
												}

												// Добавляем обработку полей character и to для emotion_change
												if eventType == "emotion_change" {
													if character, ok := eventMap["character"].(string); ok {
														respEvent.Character = character
													}
													if to, ok := eventMap["to"].(string); ok {
														respEvent.To = to
													}
												}

												respEvents = append(respEvents, respEvent)
											}
										}
									}
								}

								// Преобразуем вложенные события в упрощенные
								simplifiedResponse.ResponseEvents = convertToSimplifiedEvents(respEvents)
							}

							simplifiedResponses = append(simplifiedResponses, simplifiedResponse)
						}
					}

					simplifiedEvent.Responses = simplifiedResponses
				}
			}
		}

		result[i] = simplifiedEvent
	}

	return result
}

// processUserChoice обрабатывает выбор пользователя и применяет последствия к состоянию
func processUserChoice(state *domain.NovelState, scene domain.Scene, choiceText string) {
	// Ищем событие с выбором в сцене
	var choiceEvent *domain.Event
	for i, event := range scene.Events {
		if event.EventType == "choice" && len(event.Choices) > 0 {
			choiceEvent = &scene.Events[i]
			break
		}
	}

	if choiceEvent == nil {
		return // Нет события с выбором
	}

	// Ищем выбор по тексту
	var selectedChoice *domain.Choice
	for i, choice := range choiceEvent.Choices {
		if choice.Text == choiceText {
			selectedChoice = &choiceEvent.Choices[i]
			break
		}
	}

	if selectedChoice == nil {
		return // Выбор не найден
	}

	// Сохраняем выбор в истории
	state.PreviousChoices = append(state.PreviousChoices, choiceText)

	// Если у выбора есть последствия, применяем их к состоянию
	if selectedChoice.Consequences != nil {
		// Обновляем отношения с персонажами
		if relationshipChanges, ok := selectedChoice.Consequences["relationship"].(map[string]interface{}); ok {
			for character, valueInterface := range relationshipChanges {
				if value, ok := valueInterface.(float64); ok {
					// Убедимся, что map существует
					if state.Relationship == nil {
						state.Relationship = make(map[string]int)
					}
					// Обновляем значение отношения
					state.Relationship[character] += int(value)
				}
			}
		}

		// Добавляем глобальные флаги
		if flagsToAdd, ok := selectedChoice.Consequences["add_global_flags"].([]interface{}); ok {
			for _, flagInterface := range flagsToAdd {
				if flag, ok := flagInterface.(string); ok {
					// Проверяем, существует ли уже такой флаг
					flagExists := false
					for _, existingFlag := range state.GlobalFlags {
						if existingFlag == flag {
							flagExists = true
							break
						}
					}
					if !flagExists {
						state.GlobalFlags = append(state.GlobalFlags, flag)
					}
				}
			}
		}

		// Удаляем глобальные флаги
		if flagsToRemove, ok := selectedChoice.Consequences["remove_global_flags"].([]interface{}); ok {
			for _, flagInterface := range flagsToRemove {
				if flag, ok := flagInterface.(string); ok {
					// Ищем и удаляем флаг
					for i, existingFlag := range state.GlobalFlags {
						if existingFlag == flag {
							// Удаляем флаг, сохраняя порядок других элементов
							state.GlobalFlags = append(state.GlobalFlags[:i], state.GlobalFlags[i+1:]...)
							break
						}
					}
				}
			}
		}

		// Обновляем переменные истории
		if storyVariables, ok := selectedChoice.Consequences["story_variables"].(map[string]interface{}); ok {
			// Убедимся, что map существует
			if state.StoryVariables == nil {
				state.StoryVariables = make(map[string]interface{})
			}
			// Обновляем переменные
			for key, value := range storyVariables {
				state.StoryVariables[key] = value
			}
		}

		// Добавляем ветки истории
		if branches, ok := selectedChoice.Consequences["story_branches"].([]interface{}); ok {
			for _, branchInterface := range branches {
				if branch, ok := branchInterface.(string); ok {
					// Проверяем, существует ли уже такая ветка
					branchExists := false
					for _, existingBranch := range state.StoryBranches {
						if existingBranch == branch {
							branchExists = true
							break
						}
					}
					if !branchExists {
						state.StoryBranches = append(state.StoryBranches, branch)
					}
				}
			}
		}
	}
}
