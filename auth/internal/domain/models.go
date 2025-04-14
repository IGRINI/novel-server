package domain

import "github.com/golang-jwt/jwt/v5"

// Claims represents the JWT claims.
type Claims struct {
	UserID uint64   `json:"user_id"`
	Roles  []string `json:"roles"`
	jwt.RegisteredClaims
}
