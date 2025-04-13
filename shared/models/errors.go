package models

import "errors"

// Common Authentication Errors
var (
	ErrUserNotFound       = errors.New("user not found")           // User doesn't exist in the database
	ErrUserAlreadyExists  = errors.New("user already exists")      // Username/ID conflict
	ErrEmailAlreadyExists = errors.New("email already exists")     // Email conflict
	ErrInvalidCredentials = errors.New("invalid credentials")      // Password mismatch or similar
	ErrTokenInvalid       = errors.New("token is invalid")         // Token failed validation (wrong signature, etc.)
	ErrTokenMalformed     = errors.New("token malformed")          // Token structure is wrong
	ErrTokenExpired       = errors.New("token expired")            // Token is valid but past expiry
	ErrTokenNotFound      = errors.New("token not found in store") // Token doesn't exist in persistent store (e.g., Redis)
	// Common database/model level errors
	ErrNotFound = errors.New("запись не найдена")
	// Add other common errors if needed
)

// Specific errors related to story generation/publishing
var (
	ErrUserHasActiveGeneration = errors.New("у пользователя уже есть активная генерация")
	ErrCannotRevise            = errors.New("нельзя редактировать историю в текущем статусе")
)

// Specific errors related to gameplay loop/scenes
var (
	ErrStoryNotReadyYet     = errors.New("история или ее первая сцена еще не готовы")
	ErrSceneNeedsGeneration = errors.New("следующая сцена для данного состояния еще не сгенерирована")
)
