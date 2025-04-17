package models

import (
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims представляет стандартные поля JWT и пользовательские данные,
// которые мы хотим включить в токен.
type Claims struct {
	UserID               uuid.UUID `json:"user_id"`
	Roles                []string  `json:"roles"`
	jwt.RegisteredClaims           // Встраиваем стандартные поля: Issuer, Subject, Audience, ExpiresAt, NotBefore, IssuedAt, ID (JTI)
}
