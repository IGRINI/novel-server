package service

import "novel-server/shared/models"

// --- Structs specific to Game Loop logic ---

// sceneContentChoices represents the expected structure for scene content of type "choices".
type sceneContentChoices struct {
	Choices []sceneChoice `json:"ch"`
}

// sceneChoice represents a block of choices within a scene.
type sceneChoice struct {
	Description string        `json:"desc"`
	Options     []sceneOption `json:"opts"` // Expecting exactly 2 options
	Char        string        `json:"char"`
}

// sceneOption represents a single option within a choice block.
type sceneOption struct {
	Text         string              `json:"txt"`
	Consequences models.Consequences `json:"cons"`
}
