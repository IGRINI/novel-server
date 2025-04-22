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
	Email       *string  `json:"email,omitempty"`
	DisplayName *string  `json:"display_name,omitempty"`
	Roles       []string `json:"roles,omitempty"`
	IsBanned    *bool    `json:"is_banned,omitempty"`
}

type updatePasswordRequest struct {
	NewPassword string `json:"new_password" binding:"required"`
}

// --- Структуры для эндпоинта /internal/users/batch-info ---

// BatchGetUsersInfoRequest - структура запроса для пакетного получения информации о пользователях.
type BatchGetUsersInfoRequest struct {
	UserIDs []string `json:"userIds" binding:"required,dive,uuid"`
}

// UserInfoForBatch - структура с информацией о пользователе для ответа batch-info.
// (Используем meResponse, так как она содержит нужные поля)
type UserInfoForBatch struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName *string  `json:"displayName"`
	Email       *string  `json:"email"`
	Roles       []string `json:"roles"`
	IsBanned    bool     `json:"isBanned"`
}

// BatchGetUsersInfoResponse - структура ответа для пакетного получения информации о пользователях.
type BatchGetUsersInfoResponse struct {
	Data []UserInfoForBatch `json:"data"`
}
