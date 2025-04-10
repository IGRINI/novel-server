package models

// TokenDetails holds the details of the JWT token.
// This might be shared if other services need to understand token structure,
// otherwise, it could potentially stay within the auth service's domain.
// For now, placing it in shared models.
type TokenDetails struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccessUUID   string `json:"-"` // Usually not exposed
	RefreshUUID  string `json:"-"` // Usually not exposed
	AtExpires    int64  `json:"at_expires"`
	RtExpires    int64  `json:"rt_expires"`
}

// Claims represents the JWT claims specific to user authentication.
// This likely belongs ONLY to the auth service's domain, not shared.
// We will keep the Claims struct definition within the auth service.
