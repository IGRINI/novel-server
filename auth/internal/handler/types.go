package handler

type registerRequest struct {
	Username string `json:"username" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type tokenVerifyRequest struct {
	Token string `json:"token" binding:"required"`
}

type generateInterServiceTokenRequest struct {
	ServiceName string `json:"service_name" binding:"required"`
}

type meResponse struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Email       string   `json:"email"`
	Roles       []string `json:"roles,omitempty"`
	IsBanned    bool     `json:"isBanned"`
}

type updateUserRequest struct {
	Email    *string  `json:"email,omitempty"`
	Roles    []string `json:"roles,omitempty"`
	IsBanned *bool    `json:"is_banned,omitempty"`
}

type updatePasswordRequest struct {
	NewPassword string `json:"new_password" binding:"required"`
}
