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
