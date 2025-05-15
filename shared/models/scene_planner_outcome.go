package models

// Структуры, связанные с результатом работы ScenePlanner

// NewCharacterSuggestionFromPlanner определяет структуру для предложений по новым персонажам от ScenePlanner.
// Это соответствует элементам в "ncs" в JSON.
type NewCharacterSuggestionFromPlanner struct {
	Role   string `json:"role"`
	Reason string `json:"reason"`
}

// NewCardSuggestionFromPlanner определяет структуру для предложений по новым карточкам от ScenePlanner.
// Эта структура соответствует элементам в "ncds" в JSON.
// Поля Pr и Ir заменяют ранее использовавшиеся ImagePromptDescriptor и ImageReferenceName.
type NewCardSuggestionFromPlanner struct {
	Pr     string `json:"pr"`
	Ir     string `json:"ir"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// CharacterUpdateFromPlanner определяет структуру для обновлений существующих персонажей.
// Соответствует элементам "cus" в JSON.
type CharacterUpdateFromPlanner struct {
	ID                 string            `json:"id"`
	MemoryUpdate       string            `json:"mu,omitempty"`
	RelationshipUpdate map[string]string `json:"ru,omitempty"`
}

// CharacterToRemoveFromPlanner определяет структуру для удаления персонажей.
// Соответствует элементам "crs" в JSON.
type CharacterToRemoveFromPlanner struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// CardToRemoveFromPlanner определяет структуру для удаления карточек.
// Соответствует элементам "cdrs" в JSON.
type CardToRemoveFromPlanner struct {
	RefName string `json:"ref_name"`
	Reason  string `json:"reason"`
}

// InitialScenePlannerOutcome - ожидаемая структура JSON ответа от AI для планировщика ПЕРВОЙ сцены.
// Соответствует JSON-схеме из scene_planner_prompt.md. Поля переименованы для краткости (nnc, ncs, ncds, cus, crs, cdrs, sf).
type InitialScenePlannerOutcome struct {
	NeedNewCharacter   bool                                `json:"nnc"`
	Ncs                []NewCharacterSuggestionFromPlanner `json:"ncs,omitempty"`
	Ncds               []NewCardSuggestionFromPlanner      `json:"ncds,omitempty"`
	CharacterUpdates   []CharacterUpdateFromPlanner        `json:"cus,omitempty"`
	CharactersToRemove []CharacterToRemoveFromPlanner      `json:"crs,omitempty"`
	CardsToRemove      []CardToRemoveFromPlanner           `json:"cdrs,omitempty"`
	Sf                 string                              `json:"sf"`
}
