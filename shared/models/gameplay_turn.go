package models

// GameplayChoiceConsequence описывает последствия выбора опции.
type GameplayChoiceConsequence struct {
	CoreStats  map[string]int `json:"cs"` // Ключ - индекс стата (строка), значение - изменение
	ResultText string         `json:"rt,omitempty"`
}

// GameplayChoiceOption представляет одну из опций выбора.
type GameplayChoiceOption struct {
	Text         string                    `json:"txt"`
	Consequences GameplayChoiceConsequence `json:"cons"`
}

// GameplayChoiceBlock представляет один блок выбора в игровой сцене.
type GameplayChoiceBlock struct {
	Scene   string                 `json:"scene"` // Общее описание/ключ для картинки сцены
	Name    string                 `json:"name"`  // Название карточки выбора
	Desc    string                 `json:"desc"`  // Описание ситуации выбора
	Options []GameplayChoiceOption `json:"opts"`  // Массив из двух опций
}

// GameplayTurnOutput представляет структурированный вывод от json_generation_prompt.md,
// описывающий текущую сцену и доступные выборы.
type GameplayTurnOutput struct {
	Location string                `json:"location"` // Описание текущей локации/сцены
	Choices  []GameplayChoiceBlock `json:"ch"`       // Список блоков выбора
}
