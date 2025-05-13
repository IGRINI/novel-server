package models

// NewCardSuggestionFromPlanner используется как часть ScenePlanOutputForStorySetup
// и соответствует части вывода scene_planner_prompt.md.
type NewCardSuggestionFromPlanner struct {
	ImagePromptDescriptor string `json:"image_prompt_descriptor"`
	ImageReferenceName    string `json:"image_reference_name"`
	Title                 string `json:"title"`
	Reason                string `json:"reason"`
}

// ScenePlanOutputForStorySetup содержит отфильтрованные данные от ScenePlanner,
// предназначенные для передачи в UserInput для StorySetup.
type ScenePlanOutputForStorySetup struct {
	SceneFocus         string                         `json:"scene_focus"`
	NewCardSuggestions []NewCardSuggestionFromPlanner `json:"new_card_suggestions,omitempty"`
}
