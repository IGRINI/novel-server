package models

import "encoding/json"

// --- Minimal Structs for AI Input ---

// MinimalCharacterDef contains only the character fields needed for scene generation context.
type MinimalCharacterDef struct {
	Name        string `json:"n"`           // Character Name
	Description string `json:"d"`           // Character Description
	Personality string `json:"p,omitempty"` // Optional personality description
}

// MinimalConfigForScene contains only the config fields needed for scene generation.
type MinimalConfigForScene struct {
	Genre      string `json:"gn"` // Genre
	PlayerName string `json:"pn"` // Player Name
}

// MinimalSetupForScene contains only the setup fields needed for scene generation.
type MinimalSetupForScene struct {
	CoreStatsDefinition map[string]StatDefinition `json:"csd"`             // core_stats_definition
	Characters          []MinimalCharacterDef     `json:"chars,omitempty"` // characters context
}

// PlayerPreferences represents the style and tone preferences.
type PlayerPreferences struct {
	Style string `json:"st,omitempty"` // Style
	Tone  string `json:"tn,omitempty"` // Tone
}

// MinimalConfigForGameOver contains only the config fields needed for game over generation.
type MinimalConfigForGameOver struct {
	Language    string             `json:"ln"`           // Language code (e.g., "en", "ru")
	Genre       string             `json:"gn"`           // Genre
	PlayerPrefs *PlayerPreferences `json:"pp,omitempty"` // Optional player preferences (style, tone)
}

// MinimalCharacterForGameOver contains only the character name for game over context.
type MinimalCharacterForGameOver struct {
	Name string `json:"n"` // Character Name
}

// MinimalSetupForGameOver contains only the setup fields needed for game over context.
type MinimalSetupForGameOver struct {
	Characters []MinimalCharacterForGameOver `json:"chars,omitempty"` // characters context (names only)
}

// Helper function to convert full Config to MinimalConfigForScene
func ToMinimalConfigForScene(fullCfg *Config) MinimalConfigForScene {
	if fullCfg == nil {
		return MinimalConfigForScene{}
	}
	return MinimalConfigForScene{
		Genre:      fullCfg.Genre,
		PlayerName: fullCfg.PlayerName,
	}
}

// Helper function to convert full NovelSetupContent to MinimalSetupForScene
func ToMinimalSetupForScene(fullSetup *NovelSetupContent) MinimalSetupForScene {
	if fullSetup == nil {
		return MinimalSetupForScene{}
	}
	minChars := make([]MinimalCharacterDef, 0, len(fullSetup.Characters))
	for _, char := range fullSetup.Characters {
		minChars = append(minChars, MinimalCharacterDef{
			Name:        char.Name,
			Description: char.Description,
			Personality: char.Personality,
		})
	}
	return MinimalSetupForScene{
		CoreStatsDefinition: fullSetup.CoreStatsDefinition,
		Characters:          minChars,
	}
}

// Helper function to convert full Config to MinimalConfigForGameOver
// Note: Needs access to the original *full* Config JSON to parse pp if present.
func ToMinimalConfigForGameOver(fullCfgJSON json.RawMessage) MinimalConfigForGameOver {
	if len(fullCfgJSON) == 0 || string(fullCfgJSON) == "null" {
		return MinimalConfigForGameOver{}
	}

	// Temporary struct to parse relevant fields including pp
	var tempCfg struct {
		Language    string             `json:"ln"`
		Genre       string             `json:"gn"`
		PlayerPrefs *PlayerPreferences `json:"pp,omitempty"`
	}

	if err := json.Unmarshal(fullCfgJSON, &tempCfg); err != nil {
		// Log error or handle appropriately, return minimal struct for now
		return MinimalConfigForGameOver{} // Or maybe just language/genre if they parsed?
	}

	return MinimalConfigForGameOver{
		Language:    tempCfg.Language,
		Genre:       tempCfg.Genre,
		PlayerPrefs: tempCfg.PlayerPrefs,
	}
}

// Helper function to convert full NovelSetupContent to MinimalSetupForGameOver
func ToMinimalSetupForGameOver(fullSetup *NovelSetupContent) MinimalSetupForGameOver {
	if fullSetup == nil {
		return MinimalSetupForGameOver{}
	}
	minChars := make([]MinimalCharacterForGameOver, 0, len(fullSetup.Characters))
	for _, char := range fullSetup.Characters {
		minChars = append(minChars, MinimalCharacterForGameOver{
			Name: char.Name,
		})
	}
	return MinimalSetupForGameOver{
		Characters: minChars,
	}
}
