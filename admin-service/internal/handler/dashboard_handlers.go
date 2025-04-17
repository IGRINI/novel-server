package handler

import (
	"fmt"
	"net/http"
	sharedModels "novel-server/shared/models"
	"time"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

func (h *AdminHandler) getDashboardData(c echo.Context) error {
	userID, _ := sharedModels.GetUserIDFromContext(c.Request().Context())
	roles, _ := sharedModels.GetRolesFromContext(c.Request().Context())

	h.logger.Info("Admin dashboard requested", zap.Uint64("adminUserID", userID), zap.Strings("roles", roles))
	h.logger.Debug("Attempting to get user count via auth-service")
	startTime := time.Now()
	userCount, err := h.authClient.GetUserCount(c.Request().Context())
	duration := time.Since(startTime)
	h.logger.Debug("Finished getting user count via auth-service", zap.Duration("duration", duration), zap.Error(err))
	if err != nil {
		h.logger.Error("Failed to get user count from auth-service", zap.Error(err))
		userCount = -1
	}
	activeStories := 0
	data := map[string]interface{}{
		"PageTitle":      "Дашборд",
		"WelcomeMessage": fmt.Sprintf("Добро пожаловать, User %d!", userID),
		"UserRoles":      roles,
		"Stats": map[string]int{
			"totalUsers":    userCount,
			"activeStories": activeStories,
		},
		"UserCountError": err != nil,
		"IsLoggedIn":     true,
	}
	return c.Render(http.StatusOK, "dashboard.html", data)
}
