package domain

import (
	"github.com/google/uuid"
	"time"
)

// NovelState представляет текущее состояние новеллы
// и используется при продолжении истории.
type NovelState struct {
	StateHash            string                 `json:"-"`
	SceneCount           int                    `json:"scene_count"`
	CurrentSceneIndex    int                    `json:"current_scene_index"`
	WorldContext         string                 `json:"world_context,omitempty"`
	OriginalStorySummary string                 `json:"-"`
	StorySummary         string                 `json:"story_summary,omitempty"`
	Language             string                 `json:"language"`
	PlayerName           string                 `json:"player_name"`
	PlayerGender         string                 `json:"player_gender"`
	EndingPreference     string                 `json:"ending_preference"`
	CurrentStage         string                 `json:"current_stage"`
	Backgrounds          []Background           `json:"backgrounds"`
	Characters           []Character            `json:"characters"`
	Scenes               []Scene                `json:"scenes,omitempty"`
	GlobalFlags          []string               `json:"global_flags"`
	Relationship         map[string]int         `json:"relationship"`
	StoryVariables       map[string]interface{} `json:"story_variables"`
	PreviousChoices      []string               `json:"previous_choices"`
	StoryBranches        []string               `json:"story_branches,omitempty"`
	StorySummarySoFar    string                 `json:"story_summary_so_far,omitempty"`
	FutureDirection      string                 `json:"future_direction,omitempty"`
	IsAdultContent       bool                   `json:"is_adult_content"`
}

// NovelMetadata представляет краткую информацию о новелле
// для отображения в списках.
type NovelMetadata struct {
	NovelID          uuid.UUID `json:"novel_id"`
	UserID           string    `json:"user_id"`
	Title            string    `json:"title"`
	ShortDescription string    `json:"short_description"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ListNovelsRequest представляет запрос на получение списка новелл
// с поддержкой пагинации.
type ListNovelsRequest struct {
	Limit  int        `json:"limit,omitempty"`
	Cursor *uuid.UUID `json:"cursor,omitempty"`
}

type ListNovelsResponse struct {
	Novels       []NovelListItem `json:"novels"`
	HasMore      bool            `json:"has_more"`
	NextCursor   *uuid.UUID      `json:"next_cursor"`
	TotalResults int             `json:"total_results"`
}

type NovelListItem struct {
	NovelID               uuid.UUID `json:"novel_id"`
	Title                 string    `json:"title"`
	ShortDescription      string    `json:"short_description"`
	IsAdultContent        bool      `json:"is_adult_content"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
	IsSetuped             bool      `json:"is_setuped"`
	IsStartedByUser       bool      `json:"is_started_by_user"`
	CurrentUserSceneIndex *int      `json:"current_user_scene_index,omitempty"`
	TotalScenesCount      int       `json:"total_scenes_count"`
}

type NovelDetailsResponse struct {
	NovelID          uuid.UUID   `json:"novel_id"`
	Title            string      `json:"title"`
	ShortDescription string      `json:"short_description"`
	Genre            string      `json:"genre"`
	Language         string      `json:"language"`
	WorldContext     string      `json:"world_context"`
	EndingPreference string      `json:"ending_preference"`
	PlayerName       string      `json:"player_name"`
	PlayerGender     string      `json:"player_gender"`
	PlayerDesc       string      `json:"player_desc"`
	Style            string      `json:"style"`
	Tone             string      `json:"tone"`
	Characters       []Character `json:"characters"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
	ScenesCount      int         `json:"scenes_count"`
}

type UserStoryProgress struct {
	NovelID           uuid.UUID              `json:"novel_id"`
	UserID            string                 `json:"user_id"`
	SceneIndex        int                    `json:"scene_index"`
	GlobalFlags       []string               `json:"global_flags"`
	Relationship      map[string]int         `json:"relationship"`
	StoryVariables    map[string]interface{} `json:"story_variables"`
	PreviousChoices   []string               `json:"previous_choices"`
	StorySummarySoFar string                 `json:"story_summary_so_far"`
	FutureDirection   string                 `json:"future_direction"`
	StateHash         string                 `json:"state_hash"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}
