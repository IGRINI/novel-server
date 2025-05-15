package models

// ScenePlanOutputForStorySetup содержит отфильтрованные данные от ScenePlanner,
// предназначенные для передачи в UserInput для StorySetup.
// Структура NewCardSuggestionFromPlanner (с полями Pr, Ir, Title, Reason) определена
// в файле scene_planner_outcome.go и будет использоваться здесь.
type ScenePlanOutputForStorySetup struct {
	SceneFocus         string                         `json:"scene_focus"`
	NewCardSuggestions []NewCardSuggestionFromPlanner `json:"new_card_suggestions,omitempty"`
}
