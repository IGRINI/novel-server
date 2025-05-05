package models

import "errors"

// Application-wide standard errors
var (
	// Common Resource/DB Errors
	ErrNotFound                = errors.New("resource not found") // General not found
	ErrStoryConfigNotFound     = errors.New("story config not found")
	ErrStoryNotFound           = errors.New("published story not found")
	ErrSceneNotFound           = errors.New("current scene not found")
	ErrPlayerProgressNotFound  = errors.New("player progress not found")
	ErrPlayerGameStateNotFound = errors.New("player game state not found")
	ErrTokenNotFound           = errors.New("token not found in storage")
	ErrChoiceNotFound          = errors.New("choice or scene not found")
	ErrDraftNotFound           = errors.New("draft not found")

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

	// Story Generation, Revision & Publishing Errors
	ErrUserHasActiveGeneration       = errors.New("user already has an active generation task")
	ErrCannotRevise                  = errors.New("story config cannot be revised in its current state")
	ErrGenerationInProgress          = errors.New("generation is already in progress for this story")
	ErrCannotPublish                 = errors.New("story cannot be published in its current status")
	ErrCannotPublishNoConfig         = errors.New("cannot publish without a generated config")
	ErrStoryNotReadyForPublishing    = errors.New("story is not ready for publishing (must be in Ready status)")
	ErrAdultContentCannotBePublic    = errors.New("adult content cannot be made public")
	ErrCannotRetry                   = errors.New("cannot retry generation for this story")
	ErrCannotUpdateSubmittedStory    = errors.New("cannot update story config that is not in draft status")
	ErrStoryAlreadyGeneratingOrReady = errors.New("story generation already in progress or completed")

	// Gameplay & Scene State Errors
	ErrStoryNotReadyYet     = errors.New("story content is not ready yet")
	ErrSceneNeedsGeneration = errors.New("requested scene needs to be generated")
	ErrStoryNotReady        = errors.New("story is not ready for gameplay yet")
	ErrInvalidOperation     = errors.New("invalid operation")
	ErrInvalidLimit         = errors.New("invalid limit value")
	ErrInvalidOffset        = errors.New("invalid offset value")
	ErrInvalidChoiceIndex   = errors.New("invalid choice index")
	ErrInvalidChoice        = errors.New("invalid choice")
	ErrNoChoicesAvailable   = errors.New("no choices available in the current scene")
	ErrGameOverPending      = errors.New("game over sequence is pending generation")
	ErrGameCompleted        = errors.New("game has already been completed by the player")
	ErrPlayerStateInError   = errors.New("player game state is in an error status")
	ErrSaveSlotExists       = errors.New("save slot already exists for this story")

	// Like Errors
	ErrAlreadyLiked      = errors.New("story already liked by this user")
	ErrNotLikedYet       = errors.New("story not liked by this user yet")
	ErrAlreadyExists     = errors.New("record already exists")
	ErrLikeAlreadyExists = errors.New("like already exists")
	ErrLikeNotFound      = errors.New("like not found")

	// General Request/Server Errors
	ErrInternalServer = errors.New("internal server error")
	ErrBadRequest     = errors.New("bad request")
	ErrInvalidInput   = errors.New("invalid input data")
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
	ErrCodeBadRequest                    = "BAD_REQUEST"
	ErrCodeUnauthorized                  = "UNAUTHORIZED"
	ErrCodeForbidden                     = "FORBIDDEN"
	ErrCodeNotFound                      = "NOT_FOUND"
	ErrCodeDuplicate                     = "DUPLICATE_ENTRY"
	ErrCodeTokenInvalid                  = "TOKEN_INVALID"
	ErrCodeTokenExpired                  = "TOKEN_EXPIRED"
	ErrCodeValidation                    = "VALIDATION_ERROR"
	ErrCodeUserBanned                    = "USER_BANNED"
	ErrCodeWrongCredentials              = "WRONG_CREDENTIALS"
	ErrCodeUserNotFound                  = "USER_NOT_FOUND"
	ErrCodeDuplicateUser                 = "DUPLICATE_USER"
	ErrCodeDuplicateEmail                = "DUPLICATE_EMAIL"
	ErrCodeStoryConfigNotFound           = "STORY_CONFIG_NOT_FOUND"
	ErrCodeUserHasActiveGeneration       = "USER_HAS_ACTIVE_GENERATION"
	ErrCodeCannotRevise                  = "CANNOT_REVISE"
	ErrCodeGenerationInProgress          = "GENERATION_IN_PROGRESS"
	ErrCodeStoryNotReadyYet              = "STORY_NOT_READY_YET"
	ErrCodeSceneNeedsGeneration          = "SCENE_NEEDS_GENERATION"
	ErrCodeStoryNotReadyForPublishing    = "STORY_NOT_READY_FOR_PUBLISHING"
	ErrCodeAdultContentCannotBePublic    = "ADULT_CONTENT_CANNOT_BE_PUBLIC"
	ErrCodeCannotRetry                   = "CANNOT_RETRY_GENERATION"
	ErrCodeCannotUpdateSubmittedStory    = "CANNOT_UPDATE_SUBMITTED_STORY"
	ErrCodeStoryAlreadyGeneratingOrReady = "STORY_ALREADY_GENERATING_OR_READY"
	ErrCodeSaveSlotExists                = "SAVE_SLOT_EXISTS"

	// Gameplay / Publishing Specific Error Codes
	ErrCodeInvalidOperation        = "INVALID_OPERATION"
	ErrCodeInvalidLimit            = "INVALID_LIMIT"
	ErrCodeInvalidOffset           = "INVALID_OFFSET"
	ErrCodeChoiceNotFound          = "CHOICE_NOT_FOUND"
	ErrCodeInvalidChoiceIndex      = "INVALID_CHOICE_INDEX"
	ErrCodeCannotPublish           = "CANNOT_PUBLISH"
	ErrCodeCannotPublishNoConfig   = "CANNOT_PUBLISH_NO_CONFIG"
	ErrCodeStoryNotFound           = "STORY_NOT_FOUND"
	ErrCodeSceneNotFound           = "SCENE_NOT_FOUND"
	ErrCodePlayerProgressNotFound  = "PLAYER_PROGRESS_NOT_FOUND"
	ErrCodeStoryNotReady           = "STORY_NOT_READY"
	ErrCodeInvalidChoice           = "INVALID_CHOICE"
	ErrCodeNoChoicesAvailable      = "NO_CHOICES_AVAILABLE"
	ErrCodeAlreadyLiked            = "ALREADY_LIKED"
	ErrCodeNotLikedYet             = "NOT_LIKED_YET"
	ErrCodeGameOverPending         = "GAME_OVER_PENDING"
	ErrCodeGameCompleted           = "GAME_COMPLETED"
	ErrCodePlayerStateInError      = "PLAYER_STATE_ERROR"
	ErrCodePlayerGameStateNotFound = "PLAYER_GAME_STATE_NOT_FOUND"

	// 5xx Server Errors
	ErrCodeInternal      = "INTERNAL_ERROR"
	ErrCodeDatabaseError = "DATABASE_ERROR"
	ErrCodeRedisError    = "REDIS_ERROR"
	ErrCodePasswordHash  = "PASSWORD_HASH_ERROR"
)
