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
	ErrCodeBadRequest              = "BAD_REQUEST"                // Generic Bad Request (maps to models.ErrBadRequest)
	ErrCodeUnauthorized            = "UNAUTHORIZED"               // Authentication required or invalid (maps to models.ErrUnauthorized)
	ErrCodeForbidden               = "FORBIDDEN"                  // Authenticated but lacks permission (maps to models.ErrForbidden)
	ErrCodeNotFound                = "NOT_FOUND"                  // General resource not found (maps to models.ErrNotFound)
	ErrCodeDuplicate               = "DUPLICATE_ENTRY"            // General duplicate error
	ErrCodeTokenInvalid            = "TOKEN_INVALID"              // Invalid format, not found in storage, malformed (maps to models.ErrTokenInvalid, models.ErrTokenNotFound, models.ErrTokenMalformed)
	ErrCodeTokenExpired            = "TOKEN_EXPIRED"              // maps to models.ErrTokenExpired
	ErrCodeValidation              = "VALIDATION_ERROR"           // Failed validation (maps to models.ErrInvalidInput)
	ErrCodeUserBanned              = "USER_BANNED"                // maps to models.ErrUserBanned
	ErrCodeWrongCredentials        = "WRONG_CREDENTIALS"          // maps to models.ErrInvalidCredentials
	ErrCodeUserNotFound            = "USER_NOT_FOUND"             // maps to models.ErrUserNotFound
	ErrCodeDuplicateUser           = "DUPLICATE_USER"             // maps to models.ErrUserAlreadyExists (username)
	ErrCodeDuplicateEmail          = "DUPLICATE_EMAIL"            // maps to models.ErrEmailAlreadyExists
	ErrCodeStoryConfigNotFound     = "STORY_CONFIG_NOT_FOUND"     // maps to models.ErrStoryConfigNotFound
	ErrCodeUserHasActiveGeneration = "USER_HAS_ACTIVE_GENERATION" // maps to models.ErrUserHasActiveGeneration
	ErrCodeCannotRevise            = "CANNOT_REVISE"              // maps to models.ErrCannotRevise
	ErrCodeGenerationInProgress    = "GENERATION_IN_PROGRESS"     // maps to models.ErrGenerationInProgress
	ErrCodeStoryNotReadyYet        = "STORY_NOT_READY_YET"        // maps to models.ErrStoryNotReadyYet
	ErrCodeSceneNeedsGeneration    = "SCENE_NEEDS_GENERATION"     // maps to models.ErrSceneNeedsGeneration

	// 5xx Server Errors
	ErrCodeInternal      = "INTERNAL_ERROR" // Generic Internal Server Error (maps to models.ErrInternalServer)
	ErrCodeDatabaseError = "DATABASE_ERROR"
	ErrCodeRedisError    = "REDIS_ERROR"
	ErrCodePasswordHash  = "PASSWORD_HASH_ERROR"
)
