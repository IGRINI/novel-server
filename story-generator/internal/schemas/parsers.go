package schemas

import (
	"encoding/json"
	"fmt"
	"novel-server/shared/models"
)

// ParseNarratorConfig parses JSON from the narrator prompt into a StoryConfig.
func ParseNarratorConfig(data []byte) (*models.StoryConfig, error) {
	var aux struct {
		T  string `json:"t"`
		Sd string `json:"sd"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return nil, fmt.Errorf("failed to parse narrator config: %w", err)
	}
	return &models.StoryConfig{
		Title:       aux.T,
		Description: aux.Sd,
		Config:      json.RawMessage(data),
	}, nil
}

// ParseNarratorRevision parses JSON from the narrator reviser prompt into a StoryConfig.
func ParseNarratorRevision(data []byte) (*models.StoryConfig, error) {
	return ParseNarratorConfig(data)
}

// ParseNovelSetup parses JSON from the novel setup prompt into NovelSetupContent.
func ParseNovelSetup(data []byte) (*models.NovelSetupContent, error) {
	var content models.NovelSetupContent
	if err := json.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("failed to parse novel setup: %w", err)
	}
	return &content, nil
}

// ParseFirstScene parses JSON from the novel first scene prompt into SceneContent.
func ParseFirstScene(data []byte) (*models.SceneContent, error) {
	var content models.SceneContent
	if err := json.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("failed to parse first scene: %w", err)
	}
	return &content, nil
}

// ParseNovelCreator parses JSON from the ongoing gameplay prompt into SceneContent.
func ParseNovelCreator(data []byte) (*models.SceneContent, error) {
	var content models.SceneContent
	if err := json.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("failed to parse novel creator scene: %w", err)
	}
	return &content, nil
}

// ParseGameOver parses JSON from the game over prompt and returns the ending text.
func ParseGameOver(data []byte) (string, error) {
	var aux struct {
		Et string `json:"et"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return "", fmt.Errorf("failed to parse game over text: %w", err)
	}
	return aux.Et, nil
}
