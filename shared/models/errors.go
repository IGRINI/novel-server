package models

import "errors"

// Application-wide standard errors
var (
	// Common Resource/DB Errors
	ErrNotFound            = errors.New("resource not found") // General not found
	ErrStoryConfigNotFound = errors.New("story config not found")

	// User & Authentication Errors
	ErrUserNotFound       = errors.New("user not found")
	ErrUserAlreadyExists  = errors.New("user with this username already exists")
	ErrEmailAlreadyExists = errors.New("user with this email already exists")
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUnauthorized       = errors.New("unauthorized") // Authentication required or failed
	ErrForbidden          = errors.New("forbidden")    // Authenticated, but lacks permission
	ErrUserBanned         = errors.New("user is banned")

	// Token Errors
	ErrTokenInvalid   = errors.New("token is invalid")
	ErrTokenMalformed = errors.New("token is malformed")
	ErrTokenExpired   = errors.New("token has expired")
	ErrTokenNotFound  = errors.New("token not found in storage")

	// Story Generation & Publishing Errors
	ErrUserHasActiveGeneration = errors.New("user already has an active generation task")
	ErrCannotRevise            = errors.New("story config cannot be revised in its current state")
	ErrGenerationInProgress    = errors.New("generation is already in progress for this story")

	// Gameplay & Scene Errors
	ErrStoryNotReadyYet     = errors.New("story content is not ready yet")
	ErrSceneNeedsGeneration = errors.New("requested scene needs to be generated")

	// General Request/Server Errors
	ErrInternalServer = errors.New("internal server error")
	ErrBadRequest     = errors.New("bad request")
	ErrInvalidInput   = errors.New("invalid input data")

	// Gameplay / Publishing Specific Errors (previously in gameplay-service)
	ErrInvalidOperation           = errors.New("invalid operation")
	ErrInvalidLimit               = errors.New("invalid limit value")
	ErrInvalidOffset              = errors.New("invalid offset value")
	ErrChoiceNotFound             = errors.New("choice or scene not found")
	ErrInvalidChoiceIndex         = errors.New("invalid choice index")
	ErrCannotPublish              = errors.New("story cannot be published in its current status")
	ErrCannotPublishNoConfig      = errors.New("cannot publish without a generated config")
	ErrStoryNotFound              = errors.New("published story not found") // Note: Consider merging with ErrNotFound if applicable
	ErrSceneNotFound              = errors.New("current scene not found")
	ErrPlayerProgressNotFound     = errors.New("player progress not found")
	ErrStoryNotReady              = errors.New("story is not ready for gameplay yet")
	ErrInvalidChoice              = errors.New("invalid choice")
	ErrNoChoicesAvailable         = errors.New("no choices available in the current scene")
	ErrAlreadyLiked               = errors.New("story already liked by this user")
	ErrNotLikedYet                = errors.New("story not liked by this user yet")
	ErrStoryNotReadyForPublishing = errors.New("story is not ready for publishing (must be in Ready status)")
	ErrAdultContentCannotBePublic = errors.New("adult content cannot be made public")
	ErrCannotRetry                = errors.New("cannot retry generation for this story")

	// Errors moved from gameplay-service/internal/service/errors.go
	ErrCannotUpdateSubmittedStory    = errors.New("cannot update story config that is not in draft status")
	ErrStoryAlreadyGeneratingOrReady = errors.New("story generation already in progress or completed")

	// <<< НОВЫЕ ОШИБКИ ДЛЯ GAME LOOP >>>
	ErrGameOverPending         = errors.New("game over sequence is pending generation")
	ErrGameCompleted           = errors.New("game has already been completed by the player")
	ErrPlayerStateInError      = errors.New("player game state is in an error status")
	ErrPlayerGameStateNotFound = errors.New("player game state not found") // Добавлено для случая, когда стейт не найден

	// Add other specific errors as needed
)

// ErrorResponse defines the structure for API error responses.
// It's placed here to be shared across services if needed.
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// --- API Error Codes ---
// These are string codes used in the ErrorResponse struct.
const (
	// 4xx Client Errors
	ErrCodeBadRequest                    = "BAD_REQUEST"                // Generic Bad Request (maps to models.ErrBadRequest)
	ErrCodeUnauthorized                  = "UNAUTHORIZED"               // Authentication required or invalid (maps to models.ErrUnauthorized)
	ErrCodeForbidden                     = "FORBIDDEN"                  // Authenticated but lacks permission (maps to models.ErrForbidden)
	ErrCodeNotFound                      = "NOT_FOUND"                  // General resource not found (maps to models.ErrNotFound)
	ErrCodeDuplicate                     = "DUPLICATE_ENTRY"            // General duplicate error
	ErrCodeTokenInvalid                  = "TOKEN_INVALID"              // Invalid format, not found in storage, malformed (maps to models.ErrTokenInvalid, models.ErrTokenNotFound, models.ErrTokenMalformed)
	ErrCodeTokenExpired                  = "TOKEN_EXPIRED"              // maps to models.ErrTokenExpired
	ErrCodeValidation                    = "VALIDATION_ERROR"           // Failed validation (maps to models.ErrInvalidInput)
	ErrCodeUserBanned                    = "USER_BANNED"                // maps to models.ErrUserBanned
	ErrCodeWrongCredentials              = "WRONG_CREDENTIALS"          // maps to models.ErrInvalidCredentials
	ErrCodeUserNotFound                  = "USER_NOT_FOUND"             // maps to models.ErrUserNotFound
	ErrCodeDuplicateUser                 = "DUPLICATE_USER"             // maps to models.ErrUserAlreadyExists (username)
	ErrCodeDuplicateEmail                = "DUPLICATE_EMAIL"            // maps to models.ErrEmailAlreadyExists
	ErrCodeStoryConfigNotFound           = "STORY_CONFIG_NOT_FOUND"     // maps to models.ErrStoryConfigNotFound
	ErrCodeUserHasActiveGeneration       = "USER_HAS_ACTIVE_GENERATION" // maps to models.ErrUserHasActiveGeneration
	ErrCodeCannotRevise                  = "CANNOT_REVISE"              // maps to models.ErrCannotRevise
	ErrCodeGenerationInProgress          = "GENERATION_IN_PROGRESS"     // maps to models.ErrGenerationInProgress
	ErrCodeStoryNotReadyYet              = "STORY_NOT_READY_YET"        // maps to models.ErrStoryNotReadyYet
	ErrCodeSceneNeedsGeneration          = "SCENE_NEEDS_GENERATION"     // maps to models.ErrSceneNeedsGeneration
	ErrCodeStoryNotReadyForPublishing    = "STORY_NOT_READY_FOR_PUBLISHING"
	ErrCodeAdultContentCannotBePublic    = "ADULT_CONTENT_CANNOT_BE_PUBLIC"
	ErrCodeCannotRetry                   = "CANNOT_RETRY_GENERATION"
	ErrCodeCannotUpdateSubmittedStory    = "CANNOT_UPDATE_SUBMITTED_STORY"
	ErrCodeStoryAlreadyGeneratingOrReady = "STORY_ALREADY_GENERATING_OR_READY"

	// Gameplay / Publishing Specific Error Codes
	ErrCodeInvalidOperation       = "INVALID_OPERATION"
	ErrCodeInvalidLimit           = "INVALID_LIMIT"
	ErrCodeInvalidOffset          = "INVALID_OFFSET"
	ErrCodeChoiceNotFound         = "CHOICE_NOT_FOUND"
	ErrCodeInvalidChoiceIndex     = "INVALID_CHOICE_INDEX"
	ErrCodeCannotPublish          = "CANNOT_PUBLISH"
	ErrCodeCannotPublishNoConfig  = "CANNOT_PUBLISH_NO_CONFIG"
	ErrCodeStoryNotFound          = "STORY_NOT_FOUND" // Note: Consider merging with ErrCodeNotFound
	ErrCodeSceneNotFound          = "SCENE_NOT_FOUND"
	ErrCodePlayerProgressNotFound = "PLAYER_PROGRESS_NOT_FOUND"
	ErrCodeStoryNotReady          = "STORY_NOT_READY" // Note: Consider merging with ErrCodeStoryNotReadyYet
	ErrCodeInvalidChoice          = "INVALID_CHOICE"
	ErrCodeNoChoicesAvailable     = "NO_CHOICES_AVAILABLE"
	ErrCodeAlreadyLiked           = "ALREADY_LIKED"
	ErrCodeNotLikedYet            = "NOT_LIKED_YET"

	// 5xx Server Errors
	ErrCodeInternal      = "INTERNAL_ERROR" // Generic Internal Server Error (maps to models.ErrInternalServer)
	ErrCodeDatabaseError = "DATABASE_ERROR"
	ErrCodeRedisError    = "REDIS_ERROR"
	ErrCodePasswordHash  = "PASSWORD_HASH_ERROR"

	// <<< НОВЫЕ КОДЫ ОШИБОК ДЛЯ GAME LOOP >>>
	ErrCodeGameOverPending         = "GAME_OVER_PENDING"           // maps to models.ErrGameOverPending
	ErrCodeGameCompleted           = "GAME_COMPLETED"              // maps to models.ErrGameCompleted
	ErrCodePlayerStateInError      = "PLAYER_STATE_ERROR"          // maps to models.ErrPlayerStateInError
	ErrCodePlayerGameStateNotFound = "PLAYER_GAME_STATE_NOT_FOUND" // maps to models.ErrPlayerGameStateNotFound
)
