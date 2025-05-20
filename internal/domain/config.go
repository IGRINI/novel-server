package domain

// NovelGenerationRequest представляет запрос на генерацию новеллы
//
// UserPrompt - текстовое описание новеллы от пользователя.
type NovelGenerationRequest struct {
	UserPrompt string `json:"user_prompt"`
}

// NovelConfig представляет конфигурацию новеллы.
type NovelConfig struct {
	Title             string `json:"title"`
	ShortDescription  string `json:"short_description"`
	Franchise         string `json:"franchise"`
	Genre             string `json:"genre"`
	Language          string `json:"language"`
	IsAdultContent    bool   `json:"is_adult_content"`
	PlayerName        string `json:"player_name"`
	PlayerGender      string `json:"player_gender"`
	EndingPreference  string `json:"ending_preference"`
	WorldContext      string `json:"world_context"`
	StorySummary      string `json:"story_summary"`
	StorySummarySoFar string `json:"story_summary_so_far"`
	FutureDirection   string `json:"future_direction"`
	PlayerPreferences struct {
		Themes            []string `json:"themes"`
		Style             string   `json:"style"`
		Tone              string   `json:"tone"`
		DialogDensity     string   `json:"dialog_density"`
		ChoiceFrequency   string   `json:"choice_frequency"`
		PlayerDescription string   `json:"player_description"`
		WorldLore         []string `json:"world_lore"`
		DesiredLocations  []string `json:"desired_locations"`
		DesiredCharacters []string `json:"desired_characters"`
	} `json:"player_preferences"`
	StoryConfig struct {
		Length           string `json:"length"`
		CharacterCount   int    `json:"character_count"`
		SceneEventTarget int    `json:"scene_event_target"`
	} `json:"story_config"`
	RequiredOutput struct {
		IncludePrompts         bool `json:"include_prompts"`
		IncludeNegativePrompts bool `json:"include_negative_prompts"`
		GenerateBackgrounds    bool `json:"generate_backgrounds"`
		GenerateCharacters     bool `json:"generate_characters"`
		GenerateStartScene     bool `json:"generate_start_scene"`
	} `json:"required_output"`
}

// NovelGenerationResponse представляет ответ от API генерации новеллы
// Config содержит сконфигурированные параметры новеллы.
type NovelGenerationResponse struct {
	Config NovelConfig `json:"config"`
}

// Validate проверяет NovelConfig на наличие обязательных полей.
func (c *NovelConfig) Validate() error {
	if c.Franchise == "" {
		return NewValidationError("franchise is required")
	}
	if c.Genre == "" {
		return NewValidationError("genre is required")
	}
	if c.Language == "" {
		return NewValidationError("language is required")
	}
	if c.PlayerName == "" {
		return NewValidationError("player_name is required")
	}
	if c.PlayerGender == "" {
		return NewValidationError("player_gender is required")
	}
	return nil
}

// ValidationError представляет ошибку валидации
// Message содержит текст ошибки.
type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string { return e.Message }

// NewValidationError создает новую ошибку валидации
func NewValidationError(message string) ValidationError {
	return ValidationError{Message: message}
}
