package handler

import (
	"errors"
	"net/http"
	"novel-server/shared/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func (h *AuthHandler) getMe(c *gin.Context) {
	userIDStr := c.GetString("user_id")
	if userIDStr == "" {
		zap.L().Error("User ID missing in context for /me endpoint")
		c.AbortWithStatusJSON(http.StatusInternalServerError, ErrorResponse{
			Code:    ErrCodeInternalError,
			Message: "Internal server error: User context missing",
		})
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		zap.L().Error("Invalid User ID (UUID) format in context for /me endpoint", zap.String("userIDStr", userIDStr), zap.Error(err))
		c.AbortWithStatusJSON(http.StatusInternalServerError, ErrorResponse{
			Code:    ErrCodeInternalError,
			Message: "Internal server error: Invalid user ID format",
		})
		return
	}

	zap.L().Debug("Handling /me request", zap.String("userID", userID.String()))

	user, err := h.userRepo.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, models.ErrUserNotFound) {
			zap.L().Warn("User not found for ID from token during /me request", zap.String("userID", userID.String()))
			c.AbortWithStatusJSON(http.StatusNotFound, ErrorResponse{
				Code:    ErrCodeUserNotFound,
				Message: "User associated with token not found",
			})
			return
		}
		zap.L().Error("Error fetching user details for /me from repository", zap.String("userID", userID.String()), zap.Error(err))
		handleServiceError(c, err)
		return
	}

	zap.L().Info("User details retrieved successfully for /me", zap.String("userID", user.ID.String()), zap.String("username", user.Username))

	resp := meResponse{
		ID:          user.ID.String(),
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Email:       user.Email,
		Roles:       user.Roles,
		IsBanned:    user.IsBanned,
	}

	c.JSON(http.StatusOK, resp)
}
