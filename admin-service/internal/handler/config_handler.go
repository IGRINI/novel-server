package handler

import (
	"fmt"
	"net/http"
	"net/url"
	"novel-server/admin-service/internal/config"
	"novel-server/admin-service/internal/service" // Сервис для работы с конфигами

	// Модель DynamicConfig
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ConfigHandler обрабатывает HTTP-запросы для динамических настроек.
type ConfigHandler struct {
	configService service.ConfigService // Сервис для работы с конфигами
	cfg           *config.Config        // Общий конфиг админки (для JWT секрета)
	logger        *zap.Logger
}

// NewConfigHandler создает новый экземпляр ConfigHandler.
func NewConfigHandler(configService service.ConfigService, cfg *config.Config, logger *zap.Logger) *ConfigHandler {
	return &ConfigHandler{
		configService: configService,
		cfg:           cfg,
		logger:        logger.Named("ConfigHandler"),
	}
}

// ShowConfigs отображает страницу со списком динамических настроек.
func (h *ConfigHandler) ShowConfigs(c *gin.Context) {
	flashType, flashMessage, flashExists := getFlashMsg(c, h.cfg) // Получаем flash

	configs, err := h.configService.GetAllConfigs(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to get dynamic configs", zap.Error(err))
		// Устанавливаем flash об ошибке и редиректим, чтобы показать его
		_ = setFlashMsg(c, "error", "Could not load dynamic configurations: "+err.Error(), h.cfg)
		c.Redirect(http.StatusFound, "/admin/configs") // Редирект на эту же страницу
		return
	}

	renderData := gin.H{
		"title":   "Dynamic Configuration",
		"configs": configs,
	}
	if flashExists {
		renderData["flashType"] = flashType
		renderData["flashMessage"] = flashMessage
	}

	c.HTML(http.StatusOK, "configs.html", renderData)
}

// ShowEditConfigForm отображает форму редактирования для конкретной настройки.
func (h *ConfigHandler) ShowEditConfigForm(c *gin.Context) {
	flashType, flashMessage, flashExists := getFlashMsg(c, h.cfg) // Получаем flash

	// Получаем ключ из query параметра, декодируем его
	keyEncoded := c.Query("key")
	key, err := url.QueryUnescape(keyEncoded)
	if err != nil || key == "" {
		h.logger.Warn("Invalid or missing key query parameter for edit config", zap.String("keyEncoded", keyEncoded), zap.Error(err))
		_ = setFlashMsg(c, "error", "Invalid or missing configuration key.", h.cfg)
		c.Redirect(http.StatusSeeOther, "/admin/configs")
		return
	}

	config, err := h.configService.GetConfigByKey(c.Request.Context(), key)
	if err != nil {
		h.logger.Error("Failed to get config by key for edit", zap.String("key", key), zap.Error(err))
		_ = setFlashMsg(c, "error", fmt.Sprintf("Configuration key '%s' not found or error occurred: %v", key, err), h.cfg)
		c.Redirect(http.StatusSeeOther, "/admin/configs")
		return
	}

	renderData := gin.H{
		"title":       fmt.Sprintf("Edit Configuration: %s", key),
		"config":      config,
		"form_action": fmt.Sprintf("/admin/configs/edit?key=%s", keyEncoded), // Передаем ключ обратно в action
	}
	if flashExists {
		renderData["flashType"] = flashType
		renderData["flashMessage"] = flashMessage
	}

	c.HTML(http.StatusOK, "config_edit.html", renderData)
}

// UpdateConfig обрабатывает сохранение изменений динамической настройки.
func (h *ConfigHandler) UpdateConfig(c *gin.Context) {
	// Получаем ключ из query параметра (он должен быть в action формы)
	keyEncoded := c.Query("key")
	key, err := url.QueryUnescape(keyEncoded)
	if err != nil || key == "" {
		h.logger.Error("Invalid or missing key query parameter on update config POST", zap.String("keyEncoded", keyEncoded), zap.Error(err))
		_ = setFlashMsg(c, "error", "Invalid or missing configuration key for update.", h.cfg)
		c.Redirect(http.StatusSeeOther, "/admin/configs")
		return
	}

	// Получаем данные из формы
	value := c.PostForm("value")
	// description := c.PostForm("description") // Удалено, так как поле убрано из модели и сервиса

	// Вызываем сервис для обновления
	err = h.configService.UpdateConfig(c.Request.Context(), key, value) // Удален description из вызова
	if err != nil {
		h.logger.Error("Failed to update config via service", zap.String("key", key), zap.Error(err))
		_ = setFlashMsg(c, "error", fmt.Sprintf("Failed to update config '%s': %v", key, err), h.cfg)
		// Редирект обратно на форму редактирования
		c.Redirect(http.StatusSeeOther, fmt.Sprintf("/admin/configs/edit?key=%s", keyEncoded))
		return
	}

	h.logger.Info("Dynamic config updated successfully", zap.String("key", key))
	_ = setFlashMsg(c, "success", fmt.Sprintf("Configuration '%s' updated successfully!", key), h.cfg)
	c.Redirect(http.StatusSeeOther, "/admin/configs") // Редирект на список конфигов
}

// RegisterConfigRoutes регистрирует роуты для управления динамическими настройками.
func (h *ConfigHandler) RegisterConfigRoutes(group *gin.RouterGroup) {
	configsGroup := group.Group("/configs")
	{
		configsGroup.GET("", h.ShowConfigs)
		configsGroup.GET("/edit", h.ShowEditConfigForm) // GET для показа формы
		configsGroup.POST("/edit", h.UpdateConfig)      // POST для сохранения формы
	}
}
