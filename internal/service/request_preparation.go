package service

import (
	"encoding/json"
	"log"
	"novel-server/internal/domain"
)

// prepareInitialRequest формирует JSON для первоначального запроса
func (s *NovelContentService) prepareInitialRequest(config domain.NovelConfig) ([]byte, error) {
	initialRequest := map[string]interface{}{
		"franchise":          config.Franchise,
		"genre":              config.Genre,
		"language":           config.Language,
		"player_name":        config.PlayerName,
		"player_gender":      config.PlayerGender,
		"player_description": config.PlayerPreferences.PlayerDescription,
		"ending_preference":  config.EndingPreference,
		"world_context":      config.WorldContext,
		"request_summary":    config.StorySummary,
		"player_preferences": config.PlayerPreferences,
		"story_config":       config.StoryConfig,
		"required_output":    config.RequiredOutput,
		"is_adult_content":   config.IsAdultContent,
	}

	return json.Marshal(initialRequest)
}

// prepareContinuationRequest формирует JSON для продолжения новеллы
func (s *NovelContentService) prepareContinuationRequest(state *domain.NovelState, userChoice *domain.UserChoice) ([]byte, error) {
	// Если передан выбор пользователя, просто добавляем его в историю.
	// Увеличение индекса сцены теперь происходит в GenerateNovelContent или processSceneResponse.
	if userChoice != nil {
		log.Printf("[prepareContinuationRequest] Adding user choice to history: %s", userChoice.ChoiceText)
		state.PreviousChoices = append(state.PreviousChoices, userChoice.ChoiceText)
		// Убираем обработку последствий и увеличение индекса отсюда
	}

	// Если состояние - 'setup', убеждаемся, что индекс для запроса к ИИ равен 0
	if state.CurrentStage == domain.StageSetup {
		log.Printf("[prepareContinuationRequest] State stage is 'setup'. Ensuring scene index is 0 for the AI request.")
		state.CurrentSceneIndex = 0
	}

	log.Printf("[prepareContinuationRequest] Preparing JSON. Final SceneIndex to send to AI: %d", state.CurrentSceneIndex)
	// Формируем запрос с текущим состоянием
	continuationRequest := map[string]interface{}{
		"current_stage":        state.CurrentStage,
		"scene_count":          state.SceneCount,
		"current_scene_index":  state.CurrentSceneIndex,
		"language":             state.Language,
		"player_name":          state.PlayerName,
		"player_gender":        state.PlayerGender,
		"ending_preference":    state.EndingPreference,
		"world_context":        state.WorldContext,
		"story_summary":        state.StorySummary,
		"global_flags":         state.GlobalFlags,
		"relationship":         state.Relationship,
		"story_variables":      state.StoryVariables,
		"previous_choices":     state.PreviousChoices,
		"story_summary_so_far": state.StorySummarySoFar,
		"future_direction":     state.FutureDirection,
		"backgrounds":          state.Backgrounds,
		"characters":           state.Characters,
		"scenes":               state.Scenes,
		"is_adult_content":     state.IsAdultContent,
	}

	return json.Marshal(continuationRequest)
}
