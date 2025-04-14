package models

import "github.com/golang-jwt/jwt/v5"

// Claims представляет стандартные поля JWT и пользовательские данные,
// которые мы хотим включить в токен.
type Claims struct {
	UserID               uint64   `json:"user_id"`
	Roles                []string `json:"roles"`
	jwt.RegisteredClaims          // Встраиваем стандартные поля: Issuer, Subject, Audience, ExpiresAt, NotBefore, IssuedAt, ID (JTI)
}
