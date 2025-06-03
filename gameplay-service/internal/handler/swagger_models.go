package handler

import (
	"time"

	sharedModels "novel-server/shared/models"

	"github.com/google/uuid"
)

// Swagger models for API documentation
// Используем алиасы для shared моделей вместо дублирования

// ErrorResponse alias for shared model
// @Description Error response structure
type ErrorResponse = sharedModels.ErrorResponse

// PaginatedResponseSwagger represents a paginated response
// @Description Paginated response structure
type PaginatedResponseSwagger struct {
	Data       interface{} `json:"data"`
	NextCursor string      `json:"next_cursor,omitempty" example:"eyJpZCI6IjEyMyJ9"`
} // @name PaginatedResponse

// DataResponseSwagger represents a simple data response
// @Description Simple data response structure
type DataResponseSwagger struct {
	Data interface{} `json:"data"`
} // @name DataResponse

// StoryConfigSummary represents a story config summary
// @Description Story configuration summary
type StoryConfigSummarySwagger struct {
	ID          string    `json:"id" example:"550e8400-e29b-41d4-a716-446655440000"`
	Title       string    `json:"title" example:"Приключения в космосе"`
	Description string    `json:"description" example:"Захватывающая история о путешествии к звездам"`
	CreatedAt   time.Time `json:"created_at" example:"2023-01-01T12:00:00Z"`
	Status      string    `json:"status" example:"ready" enums:"draft,generating,ready,error"`
} // @name StoryConfigSummary

// StoryConfigDetail represents detailed story config
// @Description Detailed story configuration
type StoryConfigDetailSwagger struct {
	ID        string                 `json:"id" example:"550e8400-e29b-41d4-a716-446655440000"`
	CreatedAt time.Time              `json:"created_at" example:"2023-01-01T12:00:00Z"`
	Status    string                 `json:"status" example:"ready" enums:"draft,generating,ready,error"`
	Config    map[string]interface{} `json:"config,omitempty"`
} // @name StoryConfigDetail

// PublishedStoryDetail represents detailed published story
// @Description Detailed published story information
type PublishedStoryDetailSwagger struct {
	ID                string                                    `json:"id" example:"550e8400-e29b-41d4-a716-446655440000"`
	Title             string                                    `json:"title" example:"Приключения в космосе"`
	ShortDescription  string                                    `json:"short_description" example:"Захватывающая история"`
	AuthorID          string                                    `json:"author_id" example:"550e8400-e29b-41d4-a716-446655440001"`
	AuthorName        string                                    `json:"author_name" example:"Иван Иванов"`
	PublishedAt       time.Time                                 `json:"published_at" example:"2023-01-01T12:00:00Z"`
	Genre             string                                    `json:"genre" example:"sci-fi"`
	Language          string                                    `json:"language" example:"ru"`
	IsAdultContent    bool                                      `json:"is_adult_content" example:"false"`
	PlayerName        string                                    `json:"player_name" example:"Капитан"`
	PlayerDescription string                                    `json:"player_description" example:"Опытный космический путешественник"`
	WorldContext      string                                    `json:"world_context" example:"Далекое будущее, 2500 год"`
	StorySummary      string                                    `json:"story_summary" example:"История о поиске новых миров"`
	CoreStats         map[string]PublishedCoreStatDetailSwagger `json:"core_stats"`
	LastPlayedAt      *time.Time                                `json:"last_played_at,omitempty" example:"2023-01-02T15:30:00Z"`
	IsAuthor          bool                                      `json:"is_author" example:"true"`
	IsPublic          bool                                      `json:"is_public" example:"true"`
	PlayerGameStatus  string                                    `json:"player_game_status" example:"playing" enums:"not_started,playing,completed,error"`
} // @name PublishedStoryDetail

// PublishedCoreStatDetail represents core stat details
// @Description Core stat definition and rules
type PublishedCoreStatDetailSwagger struct {
	Description        string                        `json:"description" example:"Уровень здоровья персонажа"`
	InitialValue       int                           `json:"initial_value" example:"100"`
	GameOverConditions []sharedModels.StatDefinition `json:"game_over_conditions"`
} // @name PublishedCoreStatDetail

// GameSceneResponse represents a game scene
// @Description Game scene with choices and current state
type GameSceneResponseSwagger struct {
	ID               uuid.UUID        `json:"id" example:"550e8400-e29b-41d4-a716-446655440000"`
	PublishedStoryID uuid.UUID        `json:"published_story_id" example:"550e8400-e29b-41d4-a716-446655440001"`
	GameStateID      uuid.UUID        `json:"game_state_id" example:"550e8400-e29b-41d4-a716-446655440002"`
	CurrentStats     map[string]int   `json:"current_stats" example:"health:85,mana:60"`
	Choices          []ChoiceBlockDTO `json:"choices,omitempty"`
	EndingText       *string          `json:"ending_text,omitempty" example:"Поздравляем! Вы успешно завершили приключение."`
} // @name GameSceneResponse

// ChoiceBlockDTO represents a choice block
// @Description A block of choices presented to the player
type ChoiceBlockDTOSwagger struct {
	CharacterName string            `json:"character_name" example:"Мудрый старец"`
	Description   string            `json:"description" example:"Старец предлагает вам выбор"`
	Options       []ChoiceOptionDTO `json:"options"`
} // @name ChoiceBlockDTO

// ChoiceOptionDTO represents a single choice option
// @Description A single choice option with consequences
type ChoiceOptionDTOSwagger struct {
	Text         string           `json:"text" example:"Принять предложение"`
	Consequences *ConsequencesDTO `json:"consequences,omitempty"`
} // @name ChoiceOptionDTO

// ConsequencesDTO represents choice consequences
// @Description Consequences of making a choice
type ConsequencesDTOSwagger struct {
	ResponseText *string        `json:"response_text,omitempty" example:"Старец кивает с одобрением"`
	StatChanges  map[string]int `json:"stat_changes,omitempty" example:"health:+10,wisdom:+5"`
} // @name ConsequencesDTO

// PlayerGameState represents player game state
// @Description Player's current game state
type PlayerGameStateSwagger struct {
	ID               uuid.UUID  `json:"id" example:"550e8400-e29b-41d4-a716-446655440000"`
	PlayerID         uuid.UUID  `json:"player_id" example:"550e8400-e29b-41d4-a716-446655440001"`
	PublishedStoryID uuid.UUID  `json:"published_story_id" example:"550e8400-e29b-41d4-a716-446655440002"`
	PlayerProgressID uuid.UUID  `json:"player_progress_id" example:"550e8400-e29b-41d4-a716-446655440003"`
	PlayerStatus     string     `json:"player_status" example:"playing" enums:"not_started,playing,generating_scene,completed,error"`
	CurrentSceneID   *uuid.UUID `json:"current_scene_id,omitempty" example:"550e8400-e29b-41d4-a716-446655440004"`
	ErrorDetails     *string    `json:"error_details,omitempty" example:"Generation failed"`
	CreatedAt        time.Time  `json:"created_at" example:"2023-01-01T12:00:00Z"`
	LastActivityAt   time.Time  `json:"last_activity_at" example:"2023-01-02T15:30:00Z"`
} // @name PlayerGameState

// Request models

// GenerateStoryRequest represents story generation request
// @Description Request to generate a new story
type GenerateStoryRequestSwagger struct {
	Title             string                           `json:"title" binding:"required,min=1,max=100" example:"Приключения в космосе"`
	ShortDescription  string                           `json:"short_description" binding:"required,min=1,max=500" example:"Захватывающая история о путешествии к звездам"`
	Franchise         string                           `json:"franchise" binding:"max=100" example:"Star Trek"`
	Genre             string                           `json:"genre" binding:"required,min=1,max=50" example:"sci-fi"`
	Language          string                           `json:"language" binding:"required,len=2" example:"ru"`
	IsAdultContent    bool                             `json:"is_adult_content" example:"false"`
	PlayerName        string                           `json:"player_name" binding:"required,min=1,max=50" example:"Капитан"`
	PlayerDescription string                           `json:"player_description" binding:"required,min=1,max=500" example:"Опытный космический путешественник"`
	WorldContext      string                           `json:"world_context" binding:"required,min=1,max=1000" example:"Далекое будущее, 2500 год"`
	StorySummary      string                           `json:"story_summary" binding:"required,min=1,max=1000" example:"История о поиске новых миров"`
	CoreStats         map[string]CoreStatConfigSwagger `json:"core_stats" binding:"required"`
	PlayerPrefs       PlayerPrefsConfigSwagger         `json:"player_prefs"`
} // @name GenerateStoryRequest

// CoreStatConfig represents core stat configuration
// @Description Configuration for a core stat
type CoreStatConfigSwagger struct {
	Description        string                        `json:"description" binding:"required,min=1,max=200" example:"Уровень здоровья персонажа"`
	InitialValue       int                           `json:"initial_value" binding:"required,min=1,max=1000" example:"100"`
	GameOverConditions []sharedModels.StatDefinition `json:"game_over_conditions" binding:"required"`
} // @name CoreStatConfig

// PlayerPrefsConfig represents player preferences
// @Description Player preferences configuration
type PlayerPrefsConfigSwagger struct {
	PreferredGenres    []string `json:"preferred_genres" example:"sci-fi,fantasy"`
	ContentPreferences []string `json:"content_preferences" example:"action,mystery"`
	DifficultyLevel    string   `json:"difficulty_level" example:"medium" enums:"easy,medium,hard"`
} // @name PlayerPrefsConfig

// MakeChoicesRequest represents choice making request
// @Description Request to make choices in the game
type MakeChoicesRequestSwagger struct {
	SelectedOptionIndices []int `json:"selected_option_indices" binding:"required,dive,min=0,max=1" example:"0,1"`
} // @name MakeChoicesRequest

// SetStoryVisibilityRequest represents visibility change request
// @Description Request to change story visibility
type SetStoryVisibilityRequestSwagger struct {
	IsPublic bool `json:"is_public" example:"true"`
} // @name SetStoryVisibilityRequest

// PublishedStorySummaryWithProgress represents published story with progress
// @Description Published story summary with player progress information
type PublishedStorySummaryWithProgressSwagger struct {
	ID               uuid.UUID  `json:"id" example:"550e8400-e29b-41d4-a716-446655440000"`
	Title            string     `json:"title" example:"Приключения в космосе"`
	ShortDescription string     `json:"short_description" example:"Захватывающая история"`
	AuthorID         uuid.UUID  `json:"author_id" example:"550e8400-e29b-41d4-a716-446655440001"`
	AuthorName       string     `json:"author_name" example:"Иван Иванов"`
	PublishedAt      time.Time  `json:"published_at" example:"2023-01-01T12:00:00Z"`
	Genre            string     `json:"genre" example:"sci-fi"`
	Language         string     `json:"language" example:"ru"`
	IsAdultContent   bool       `json:"is_adult_content" example:"false"`
	IsPublic         bool       `json:"is_public" example:"true"`
	IsLiked          bool       `json:"is_liked" example:"true"`
	LikesCount       int        `json:"likes_count" example:"42"`
	PlayersCount     int        `json:"players_count" example:"156"`
	LastPlayedAt     *time.Time `json:"last_played_at,omitempty" example:"2023-01-02T15:30:00Z"`
	PlayerGameStatus string     `json:"player_game_status" example:"playing" enums:"not_started,playing,completed,error"`
} // @name PublishedStorySummaryWithProgress
