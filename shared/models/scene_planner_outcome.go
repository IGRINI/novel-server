package models

// Структуры, связанные с результатом работы ScenePlanner
// Перенесено из gameplay-service/internal/messaging/handle_scene_planner.go

// NewCharacterSuggestionFromPlanner определяет структуру для предложений по новым персонажам от ScenePlanner.
type NewCharacterSuggestionFromPlanner struct {
	Role   string `json:"role"`
	Reason string `json:"reason"`
}

// CharacterUpdateFromPlanner определяет структуру для обновлений существующих персонажей.
type CharacterUpdateFromPlanner struct {
	ID                 string            `json:"id"`
	MemoryUpdate       string            `json:"memory_update,omitempty"`
	RelationshipUpdate map[string]string `json:"relationship_update,omitempty"` // Ключи - ID персонажей, значения - описание отношений
}

// CharacterToRemoveFromPlanner определяет структуру для удаления персонажей.
type CharacterToRemoveFromPlanner struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// CardToRemoveFromPlanner определяет структуру для удаления карточек.
type CardToRemoveFromPlanner struct {
	RefName string `json:"ref_name"`
	Reason  string `json:"reason"`
}

// InitialScenePlannerOutcome - ожидаемая структура JSON ответа от AI для планировщика ПЕРВОЙ сцены.
// Соответствует JSON-схеме из scene_planner_prompt.md
type InitialScenePlannerOutcome struct {
	NeedNewCharacter        bool                                `json:"need_new_character"`
	NewCharacterSuggestions []NewCharacterSuggestionFromPlanner `json:"new_character_suggestions,omitempty"`
	NewCardSuggestions      []NewCardSuggestionFromPlanner      `json:"new_card_suggestions,omitempty"` // Используем существующую models.NewCardSuggestionFromPlanner
	CharacterUpdates        []CharacterUpdateFromPlanner        `json:"character_updates,omitempty"`
	CharactersToRemove      []CharacterToRemoveFromPlanner      `json:"characters_to_remove,omitempty"`
	CardsToRemove           []CardToRemoveFromPlanner           `json:"cards_to_remove,omitempty"`
	SceneFocus              string                              `json:"scene_focus"`
}
