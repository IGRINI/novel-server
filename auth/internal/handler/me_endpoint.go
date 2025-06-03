package handler

import (
	"errors"
	"net/http"
	"novel-server/shared/models"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// @Summary Получение информации о текущем пользователе
// @Description Возвращает информацию о пользователе по токену
// @Tags user
// @Accept json
// @Produce json
// @Success 200 {object} meResponse "Информация о пользователе"
// @Failure 401 {object} ErrorResponse "Неавторизован"
// @Failure 404 {object} ErrorResponse "Пользователь не найден"
// @Security BearerAuth
// @Router /me [get]
func (h *AuthHandler) getMe(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return
	}

	zap.L().Debug("Handling /me request", zap.String("userID", userID.String()))

	user, err := h.userRepo.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, models.ErrUserNotFound) {
			zap.L().Warn("User not found for ID from token during /me request", zap.String("userID", userID.String()))
			handleServiceError(c, models.ErrUserNotFound)
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
