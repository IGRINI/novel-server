package handler

import (
	"fmt"
	"net/http"
	sharedModels "novel-server/shared/models"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// getDashboardData извлекает данные для дашборда и рендерит страницу
func (h *AdminHandler) getDashboardData(c *gin.Context) {
	// Получаем данные из контекста Gin (установлены в middleware)
	rawUserID, userOK := c.Get(string(sharedModels.UserContextKey))
	rawRoles, rolesOK := c.Get(string(sharedModels.RolesContextKey))

	var userID uuid.UUID // <<< Изменен тип на uuid.UUID
	var roles []string
	if userOK {
		// Необходимо преобразовать тип, т.к. c.Get возвращает interface{}
		var typeOK bool
		userID, typeOK = rawUserID.(uuid.UUID) // <<< Присваиваем userID после преобразования
		if !typeOK {
			// Логируем ошибку, если тип в контексте не uuid.UUID
			h.logger.Error("Invalid type for user ID in context", zap.Any("rawUserID", rawUserID))
			// Можно прервать выполнение или установить userID в uuid.Nil
			userID = uuid.Nil
			userOK = false // Считаем, что пользователя не получили
		}
	}
	if rolesOK {
		roles = rawRoles.([]string)
	}

	// Используем userID.String() для логирования
	h.logger.Info("Admin dashboard requested", zap.String("adminUserID", userID.String()), zap.Strings("roles", roles))
	h.logger.Debug("Attempting to get user count via auth-service")
	startTime := time.Now()
	// Используем c.Request.Context()
	userCount, err := h.authClient.GetUserCount(c.Request.Context())
	duration := time.Since(startTime)
	h.logger.Debug("Finished getting user count via auth-service", zap.Duration("duration", duration), zap.Error(err))
	if err != nil {
		h.logger.Error("Failed to get user count from auth-service", zap.Error(err))
		userCount = -1 // Используем -1 как индикатор ошибки
	}
	activeStories := 0 // TODO: Получить реальное значение, если нужно
	data := gin.H{     // <<< gin.H
		"PageTitle": "Дашборд",
		// Используем userID.String() в сообщении
		"WelcomeMessage": fmt.Sprintf("Добро пожаловать, User %s!", userID.String()), // TODO: Использовать имя пользователя?
		"UserRoles":      roles,
		"Stats": map[string]int{
			"totalUsers":    userCount,
			"activeStories": activeStories,
		},
		"UserCountError": err != nil,
		"IsLoggedIn":     true, // Предполагаем, что middleware гарантирует это
	}
	// Используем c.HTML
	c.HTML(http.StatusOK, "dashboard.html", data)
}
