package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"novel-server/admin-service/internal/config"
	"novel-server/admin-service/internal/service" // Сервис для работы с конфигами

	// Модель DynamicConfig
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	formDataFlashCookieName = "form_data_flash" // <<< НОВЫЙ КЛЮЧ КУКИ (оставляем только нужный) >>>
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

// ShowNewConfigForm отображает форму для создания новой динамической настройки.
func (h *ConfigHandler) ShowNewConfigForm(c *gin.Context) {
	flashType, flashMessage, flashExists := getFlashMsg(c, h.cfg) // Получаем flash
	formData := getFormDataFlash(c, h.cfg)                        // <<< ПОЛУЧАЕМ ДАННЫЕ ФОРМЫ >>>

	renderData := gin.H{
		"title":    "Create New Dynamic Configuration",
		"formData": formData, // <<< ПЕРЕДАЕМ ДАННЫЕ ФОРМЫ В ШАБЛОН >>>
	}
	if flashExists {
		renderData["flashType"] = flashType
		renderData["flashMessage"] = flashMessage
	}

	c.HTML(http.StatusOK, "config_new.html", renderData)
}

// CreateConfig обрабатывает создание новой динамической настройки.
func (h *ConfigHandler) CreateConfig(c *gin.Context) {
	// Получаем данные из формы
	key := c.PostForm("key")
	value := c.PostForm("value")
	// description := c.PostForm("description") // <<< УБРАНО: Description больше не используется >>>

	if key == "" || value == "" { // Простая валидация: ключ и значение не должны быть пустыми
		h.logger.Warn("Attempted to create config with empty key or value", zap.String("key", key), zap.String("value", value))
		_ = setFlashMsg(c, "error", "Key and Value cannot be empty.", h.cfg)
		// Редирект обратно на форму создания, чтобы показать ошибку
		// <<< ДОБАВЛЕНО: Сохранение введенных данных для формы >>>
		_ = setFormDataFlash(c, map[string]string{"key": key, "value": value}, h.cfg)
		c.Redirect(http.StatusSeeOther, "/admin/configs/new")
		return
	}

	// Вызываем сервис для создания
	// <<< ИЗМЕНЕНО: Убран description из вызова >>>
	err := h.configService.CreateConfig(c.Request.Context(), key, value)
	if err != nil {
		h.logger.Error("Failed to create config via service", zap.String("key", key), zap.Error(err))
		_ = setFlashMsg(c, "error", fmt.Sprintf("Failed to create config '%s': %v", key, err), h.cfg)
		// Редирект обратно на форму создания
		// <<< ДОБАВЛЕНО: Сохранение введенных данных для формы >>>
		_ = setFormDataFlash(c, map[string]string{"key": key, "value": value}, h.cfg)
		c.Redirect(http.StatusSeeOther, "/admin/configs/new")
		return
	}

	h.logger.Info("Dynamic config created successfully", zap.String("key", key))
	_ = setFlashMsg(c, "success", fmt.Sprintf("Configuration '%s' created successfully!", key), h.cfg)
	c.Redirect(http.StatusSeeOther, "/admin/configs") // Редирект на список конфигов
}

// <<< НОВАЯ ФУНКЦИЯ: Устанавливает flash-куку с данными формы >>>
func setFormDataFlash(c *gin.Context, data map[string]string, cfg *config.Config) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		// Логируем ошибку, но не прерываем основной поток
		log.Printf("ERROR: Failed to marshal form data for flash: %v", err)
		return err // Возвращаем ошибку, чтобы вызывающий мог ее обработать (хотя в текущем коде игнорируется)
	}

	// Кодируем в Base64 для безопасности в куках
	encodedData := base64.StdEncoding.EncodeToString(jsonData)

	secure := cfg.Env != "development" // Secure flag в зависимости от окружения
	samesite := http.SameSiteStrictMode
	if secure {
		samesite = http.SameSiteStrictMode
	}

	// <<< ПОВТОРНОЕ ИЗМЕНЕНИЕ: Используем http.SetCookie для установки SameSite >>>
	cookie := http.Cookie{
		Name:     formDataFlashCookieName,
		Value:    encodedData,
		MaxAge:   60 * 5, // 5 минут
		Path:     "/admin",
		Domain:   "", // Пусто - текущий домен
		Secure:   secure,
		HttpOnly: true,
		SameSite: samesite,
	}
	http.SetCookie(c.Writer, &cookie)

	return nil
}

// <<< НОВАЯ ФУНКЦИЯ: Получает и удаляет flash-куку с данными формы >>>
func getFormDataFlash(c *gin.Context, cfg *config.Config) map[string]string {
	cookieValue, err := c.Cookie(formDataFlashCookieName)
	if err != nil || cookieValue == "" {
		return make(map[string]string) // Возвращаем пустую карту, если куки нет
	}

	// Удаляем куку после прочтения
	secure := cfg.Env != "development"
	c.SetCookie(formDataFlashCookieName, "", -1, "/admin", "", secure, true)

	// Декодируем из Base64
	jsonData, err := base64.StdEncoding.DecodeString(cookieValue)
	if err != nil {
		log.Printf("ERROR: Failed to decode form data flash cookie: %v", err)
		return make(map[string]string)
	}

	// Распарсиваем JSON
	var formData map[string]string
	if err := json.Unmarshal(jsonData, &formData); err != nil {
		log.Printf("ERROR: Failed to unmarshal form data flash cookie: %v", err)
		return make(map[string]string)
	}

	return formData
}

// RegisterConfigRoutes регистрирует роуты для управления динамическими настройками.
func (h *ConfigHandler) RegisterConfigRoutes(group *gin.RouterGroup) {
	configsGroup := group.Group("/configs")
	{
		configsGroup.GET("", h.ShowConfigs)
		configsGroup.GET("/new", h.ShowNewConfigForm)   // GET для показа формы создания
		configsGroup.POST("/new", h.CreateConfig)       // POST для создания новой записи
		configsGroup.GET("/edit", h.ShowEditConfigForm) // GET для показа формы редактирования
		configsGroup.POST("/edit", h.UpdateConfig)      // POST для сохранения формы редактирования
	}
}
