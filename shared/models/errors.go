package models

import "errors"

// Custom domain errors related to models or common operations
var (
	ErrUserNotFound       = errors.New("user not found")
	ErrUserAlreadyExists  = errors.New("user already exists")
	ErrEmailAlreadyExists = errors.New("email already exists")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid token")            // General invalid token
	ErrTokenExpired       = errors.New("token expired")            // Token expired (JWT validation)
	ErrTokenMalformed     = errors.New("token malformed")          // Token malformed (JWT validation)
	ErrTokenNotFound      = errors.New("token not found in store") // Token doesn't exist in persistent store (e.g., Redis)
)
