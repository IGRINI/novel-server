package service

import (
	"encoding/json"
	"fmt"
	"log"
	"novel-server/internal/domain"
	"strings"
	"time"
)

// processModelResponse обрабатывает JSON-ответ от модели и обновляет состояние
func (s *NovelContentService) processModelResponse(jsonStr string, currentState *domain.NovelState) (*domain.NovelContentResponse, error) {
	log.Printf("[processModelResponse] Processing response. CurrentState Stage: %s, SceneIndex: %d", currentState.CurrentStage, currentState.CurrentSceneIndex)

	// Проверяем и исправляем JSON перед десериализацией
	fixedJsonStr := FixJSON(jsonStr)

	var data map[string]interface{}
	err := json.Unmarshal([]byte(fixedJsonStr), &data)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal model response: %w\nResponse string: %s", err, fixedJsonStr)
	}

	// Определяем текущий этап из ответа модели
	currentStage := "" // Инициализируем пустой строкой
	if stageVal, ok := data["current_stage"]; ok {
		if stageStr, ok := stageVal.(string); ok {
			currentStage = stageStr
		} else {
			return nil, fmt.Errorf("invalid type for current_stage in model response: expected string, got %T", stageVal)
		}
	} else {
		// Если current_stage нет, предполагаем, что это setup или ошибка
		// Для setup проверим наличие характерных полей setup
		if _, hasBG := data["backgrounds"]; hasBG {
			currentStage = domain.StageSetup
		} else {
			return nil, fmt.Errorf("current_stage field is missing in model response")
		}
	}

	updatedState := *currentState // Создаем копию для обновления
	log.Printf("[processModelResponse] State *before* update: Stage=%s, Index=%d, BGs=%d, Chars=%d",
		updatedState.CurrentStage, updatedState.CurrentSceneIndex, len(updatedState.Backgrounds), len(updatedState.Characters))

	// Если модель вернула "scene_X_ready", устанавливаем универсальный StageSceneReady
	if strings.HasPrefix(currentStage, "scene_") && strings.HasSuffix(currentStage, "_ready") {
		log.Printf("[processModelResponse] Received scene ready stage from model (%s), setting state stage to StageSceneReady ('%s')", currentStage, domain.StageSceneReady)
		updatedState.CurrentStage = domain.StageSceneReady
	} else {
		// В остальных случаях (StageSetup, StageComplete, или др.) используем значение от модели
		updatedState.CurrentStage = currentStage
	}

	// Обновляем общие поля состояния, если они есть в ответе
	updateStateField(&updatedState.SceneCount, data["scene_count"])
	// НЕ обновляем CurrentSceneIndex здесь - он обновляется в processSceneResponse или prepareContinuationRequest
	// updateStateField(&updatedState.CurrentSceneIndex, data["current_scene_index"])
	updateStateField(&updatedState.Language, data["language"])
	updateStateField(&updatedState.PlayerName, data["player_name"])
	updateStateField(&updatedState.PlayerGender, data["player_gender"])
	updateStateField(&updatedState.EndingPreference, data["ending_preference"])
	updateStateField(&updatedState.WorldContext, data["world_context"])
	updateStateField(&updatedState.StorySummary, data["story_summary"])
	updateStateField(&updatedState.GlobalFlags, data["global_flags"])
	updateStateField(&updatedState.Relationship, data["relationship"])
	updateStateField(&updatedState.StoryVariables, data["story_variables"])
	updateStateField(&updatedState.PreviousChoices, data["previous_choices"])
	updateStateField(&updatedState.StorySummarySoFar, data["story_summary_so_far"])
	updateStateField(&updatedState.FutureDirection, data["future_direction"])

	// Обрабатываем специфичный контент в зависимости от этапа
	var newContent interface{}

	// Используем strings.HasPrefix для обработки "scene_X_ready"
	if currentStage == domain.StageSetup {
		setupContent := domain.SetupContent{}
		err = s.processSetupResponse(data, &updatedState, &setupContent)
		if err != nil {
			return nil, fmt.Errorf("failed to process setup response: %w", err)
		}
		newContent = setupContent
	} else if strings.HasPrefix(currentStage, "scene_") && strings.HasSuffix(currentStage, "_ready") {
		log.Printf("[processModelResponse] Detected scene ready stage: %s", currentStage)
		sceneContent, err := s.processSceneResponse(data, &updatedState)
		if err != nil {
			return nil, fmt.Errorf("failed to process scene response: %w", err)
		}
		newContent = sceneContent
	} else if currentStage == domain.StageComplete {
		// Никакого нового контента на этапе завершения
	} else {
		return nil, fmt.Errorf("unknown current_stage in model response: %s", currentStage)
	}

	log.Printf("[processModelResponse] State *after* update: Stage=%s, Index=%d, BGs=%d, Chars=%d, Scenes=%d",
		updatedState.CurrentStage, updatedState.CurrentSceneIndex, len(updatedState.Backgrounds), len(updatedState.Characters), len(updatedState.Scenes))

	response := &domain.NovelContentResponse{
		State:      updatedState,
		NewContent: newContent,
	}

	return response, nil
}

// processSetupResponse обрабатывает ответ модели для этапа setup
func (s *NovelContentService) processSetupResponse(data map[string]interface{}, state *domain.NovelState, setupContent *domain.SetupContent) error {
	// Извлекаем и присваиваем поля для SetupContent
	updateStateField(&setupContent.StorySummary, data["story_summary"])

	// Обработка backgrounds
	if bgData, ok := data["backgrounds"].([]interface{}); ok {
		var backgrounds []domain.Background
		for _, item := range bgData {
			if bgMap, ok := item.(map[string]interface{}); ok {
				bg := domain.Background{}
				updateStateField(&bg.ID, bgMap["id"])
				updateStateField(&bg.Name, bgMap["name"])
				updateStateField(&bg.Description, bgMap["description"])
				updateStateField(&bg.Prompt, bgMap["prompt"])
				updateStateField(&bg.NegativePrompt, bgMap["negative_prompt"])
				backgrounds = append(backgrounds, bg)
			}
		}
		setupContent.Backgrounds = backgrounds
		state.Backgrounds = backgrounds // Также сохраняем в общем состоянии для последующих запросов
	} else {
		// Можно добавить предупреждение или ошибку, если backgrounds ожидаются
		// log.Println("Warning: 'backgrounds' field missing or not an array in setup response")
	}

	// Обработка characters
	if charData, ok := data["characters"].([]interface{}); ok {
		var characters []domain.Character
		initialRelationship := make(map[string]int)
		for _, item := range charData {
			if charMap, ok := item.(map[string]interface{}); ok {
				char := domain.Character{}
				updateStateField(&char.Name, charMap["name"])
				updateStateField(&char.Description, charMap["description"])
				updateStateField(&char.VisualTags, charMap["visual_tags"])
				updateStateField(&char.Personality, charMap["personality"])
				updateStateField(&char.Position, charMap["position"])
				updateStateField(&char.Expression, charMap["expression"])
				updateStateField(&char.Prompt, charMap["prompt"])
				updateStateField(&char.NegativePrompt, charMap["negative_prompt"])
				characters = append(characters, char)
				// Инициализируем отношения, если имени нет в state.Relationship
				if _, exists := state.Relationship[char.Name]; !exists && char.Name != "" {
					initialRelationship[char.Name] = 0
				}
			}
		}
		setupContent.Characters = characters
		state.Characters = characters // Также сохраняем в общем состоянии
		// Применяем начальные отношения, только если state.Relationship пуст
		if len(state.Relationship) == 0 {
			state.Relationship = initialRelationship
		}
	} else {
		// log.Println("Warning: 'characters' field missing or not an array in setup response")
	}

	// Устанавливаем relationship в setupContent из state (они должны быть одинаковы на этом этапе)
	setupContent.Relationship = state.Relationship

	// Можно добавить обработку других полей setup, если они есть в ответе модели

	return nil
}

// processSceneResponse обрабатывает ответ с новой сценой и возвращает SceneContent
func (s *NovelContentService) processSceneResponse(data map[string]interface{}, state *domain.NovelState) (*domain.SceneContent, error) {
	// Ожидаем, что данные сцены находятся внутри ключа "scene", как в примере
	sceneData, ok := data["scene"].(map[string]interface{})
	if !ok {
		// Попробуем поискать ключ "new_content", если "scene" нет (на всякий случай, для гибкости)
		sceneData, ok = data["new_content"].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("neither 'scene' nor 'new_content' found or is not a map in scene response")
		}
	}

	sceneContent := domain.SceneContent{}

	// Обрабатываем ID фона
	updateStateField(&sceneContent.BackgroundID, sceneData["background_id"])

	// Обрабатываем персонажей в сцене
	if charactersData, ok := sceneData["characters"].([]interface{}); ok {
		for _, charItem := range charactersData {
			if charMap, ok := charItem.(map[string]interface{}); ok {
				sceneChar := domain.SceneCharacter{}
				updateStateField(&sceneChar.Name, charMap["name"])
				updateStateField(&sceneChar.Position, charMap["position"])
				updateStateField(&sceneChar.Expression, charMap["expression"])
				if sceneChar.Name != "" { // Добавляем только если есть имя
					sceneContent.Characters = append(sceneContent.Characters, sceneChar)
				}
			}
		}
	}

	// Обрабатываем события в сцене
	if eventsData, ok := sceneData["events"].([]interface{}); ok {
		var events []domain.Event
		for _, evtItem := range eventsData {
			if evtMap, ok := evtItem.(map[string]interface{}); ok {
				event := domain.Event{}
				updateStateField(&event.EventType, evtMap["event_type"])
				updateStateField(&event.Speaker, evtMap["speaker"])
				updateStateField(&event.Text, evtMap["text"])
				updateStateField(&event.Character, evtMap["character"])
				updateStateField(&event.From, evtMap["from"])
				updateStateField(&event.To, evtMap["to"])
				updateStateField(&event.Description, evtMap["description"])

				// Обрабатываем специальные поля для разных типов событий
				if event.EventType == "choice" {
					event.Choices = processChoices(evtMap)
				} else if event.EventType == "inline_choice" {
					event.Choices = processChoices(evtMap)
					// Добавляем Data с choice_id для inline_choice
					if event.Data == nil {
						event.Data = make(map[string]interface{})
					}
					if choiceID, ok := evtMap["choice_id"].(string); ok {
						event.Data["choice_id"] = choiceID
					} else {
						// Генерируем уникальный ID для inline_choice, если его нет
						event.Data["choice_id"] = fmt.Sprintf("choice_%d", time.Now().UnixNano())
					}
					// Обрабатываем responses для inline_choice
					if responses, ok := evtMap["responses"].([]interface{}); ok {
						event.Data["responses"] = responses
					}
				} else if event.EventType == "inline_response" {
					// Для inline_response у нас могут быть особые поля
					if event.Data == nil {
						event.Data = make(map[string]interface{})
					}
					// Добавляем choice_id из запроса
					if choiceID, ok := evtMap["choice_id"].(string); ok {
						event.Data["choice_id"] = choiceID
					}
					// Обрабатываем responses для inline_response
					if responses, ok := evtMap["responses"].([]interface{}); ok {
						event.Data["responses"] = responses
					}
				}

				// Обрабатываем дополнительные данные
				if eventData, ok := evtMap["data"].(map[string]interface{}); ok && event.Data == nil {
					event.Data = eventData
				}

				events = append(events, event)
			}
		}
		sceneContent.Events = events
	}

	// Создаем объект сцены для сохранения в состоянии
	scene := domain.Scene{
		BackgroundID: sceneContent.BackgroundID,
		Events:       sceneContent.Events,
	}

	// Обновляем массив сцен в состоянии
	// Если сцена с данным индексом уже существует, заменяем ее
	sceneFound := false
	for i, s := range state.Scenes {
		if len(s.Events) > 0 && len(scene.Events) > 0 &&
			s.Events[0].EventType == scene.Events[0].EventType {
			state.Scenes[i] = scene
			sceneFound = true
			break
		}
	}

	if !sceneFound {
		state.Scenes = append(state.Scenes, scene)
	}

	// Если в ответе есть новый индекс сцены, обновляем его в состоянии
	if sceneIndexVal, ok := data["current_scene_index"]; ok {
		if sceneIndex, ok := sceneIndexVal.(float64); ok {
			state.CurrentSceneIndex = int(sceneIndex)
		}
	} else {
		// ВАЖНО: Если пользователь сделал финальный выбор, увеличиваем индекс сцены
		// Определяем, был ли финальный выбор, по наличию choice события в конце сцены
		if len(sceneContent.Events) > 0 {
			lastEvent := sceneContent.Events[len(sceneContent.Events)-1]
			if lastEvent.EventType == "choice" && len(lastEvent.Choices) > 0 {
				// Это финал сцены с выбором - переходим к следующей сцене
				state.CurrentSceneIndex++
				log.Printf("[processSceneResponse] Final choice detected, incrementing scene index to %d", state.CurrentSceneIndex)
			}
		}
	}

	return &sceneContent, nil
}

// processChoices обрабатывает варианты выбора из события
func processChoices(evtMap map[string]interface{}) []domain.Choice {
	var choices []domain.Choice
	if choicesData, ok := evtMap["choices"].([]interface{}); ok {
		for _, choiceItem := range choicesData {
			if choiceMap, ok := choiceItem.(map[string]interface{}); ok {
				choice := domain.Choice{
					Consequences: make(map[string]interface{}),
				}
				if text, ok := choiceMap["text"].(string); ok {
					choice.Text = text
				}
				if consData, ok := choiceMap["consequences"].(map[string]interface{}); ok {
					choice.Consequences = consData
				}
				choices = append(choices, choice)
			}
		}
	}
	return choices
}
