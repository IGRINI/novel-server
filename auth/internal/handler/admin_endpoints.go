package handler

import (
	"net/http"
	"novel-server/shared/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func (h *AuthHandler) getUserCount(c *gin.Context) {
	count, err := h.userRepo.GetUserCount(c.Request.Context())
	if err != nil {
		zap.L().Error("Failed to get user count from repository", zap.Error(err))
		handleServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": count})
}

func (h *AuthHandler) listUsers(c *gin.Context) {
	users, err := h.userRepo.ListUsers(c.Request.Context())
	if err != nil {
		zap.L().Error("Failed to list users from repository", zap.Error(err))
		handleServiceError(c, err)
		return
	}
	userDTOs := make([]meResponse, 0, len(users))
	for _, u := range users {
		userDTOs = append(userDTOs, meResponse{
			ID:          u.ID.String(),
			Username:    u.Username,
			DisplayName: u.DisplayName,
			Email:       u.Email,
			Roles:       u.Roles,
			IsBanned:    u.IsBanned,
		})
	}
	c.JSON(http.StatusOK, userDTOs)
}

func (h *AuthHandler) banUser(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		zap.L().Warn("Invalid user ID (UUID) format for ban request", zap.String("userID", userIDStr), zap.Error(err))
		c.AbortWithStatusJSON(http.StatusBadRequest, models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Invalid user ID format"})
		return
	}
	err = h.authService.BanUser(c.Request.Context(), userID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AuthHandler) unbanUser(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		zap.L().Warn("Invalid user ID (UUID) format for unban request", zap.String("userID", userIDStr), zap.Error(err))
		c.AbortWithStatusJSON(http.StatusBadRequest, models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Invalid user ID format"})
		return
	}
	err = h.authService.UnbanUser(c.Request.Context(), userID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type validateTokenRequest struct {
	Token string `json:"token" binding:"required"`
}

func (h *AuthHandler) validateToken(c *gin.Context) {
	var req validateTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}
	claims, err := h.authService.ValidateAndGetClaims(c.Request.Context(), req.Token)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, claims)
}

func (h *AuthHandler) getUserDetails(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Invalid user ID format"})
		return
	}
	user, err := h.userRepo.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	userDTO := meResponse{
		ID:          user.ID.String(),
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Email:       user.Email,
		Roles:       user.Roles,
		IsBanned:    user.IsBanned,
	}
	c.JSON(http.StatusOK, userDTO)
}

func (h *AuthHandler) updateUser(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Invalid user ID format"})
		return
	}
	var req updateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}
	err = h.authService.UpdateUser(c.Request.Context(), userID, req.Email, req.Roles, req.IsBanned)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AuthHandler) updatePassword(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Invalid user ID format"})
		return
	}
	var req updatePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}
	err = h.authService.UpdatePassword(c.Request.Context(), userID, req.NewPassword)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type refreshAdminTokenResponse struct {
	Tokens models.TokenDetails `json:"tokens"`
	Claims models.Claims       `json:"claims"`
}

func (h *AuthHandler) refreshAdminToken(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}
	newTokens, newClaims, err := h.authService.RefreshAdminToken(c.Request.Context(), req.RefreshToken)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	refreshesTotal.Inc()
	resp := refreshAdminTokenResponse{
		Tokens: *newTokens,
		Claims: *newClaims,
	}
	c.JSON(http.StatusOK, resp)
}
