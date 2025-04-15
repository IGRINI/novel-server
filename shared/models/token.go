package models

import "github.com/golang-jwt/jwt/v5"

// TokenDetails represents the access and refresh tokens along with their metadata.
type TokenDetails struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccessUUID   string `json:"-"` // Usually not sent to client
	RefreshUUID  string `json:"-"` // Usually not sent to client
	AtExpires    int64  `json:"at_expires"`
	RtExpires    int64  `json:"rt_expires"`
}

// InterServiceClaims defines the custom claims for inter-service JWT tokens.
// It embeds standard RegisteredClaims and adds custom fields.
type InterServiceClaims struct {
	jwt.RegisteredClaims
	RequestingService string `json:"requesting_service,omitempty"`
	// Можно добавить другие кастомные поля при необходимости
}

// Claims represents the JWT claims specific to user authentication.
// This likely belongs ONLY to the auth service's domain, not shared.
// We will keep the Claims struct definition within the auth service.
