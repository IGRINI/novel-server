package handler

import (
	"errors"
	"fmt"
	"net/http"
	"novel-server/admin-service/internal/config"
	"novel-server/admin-service/internal/service"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"novel-server/shared/database"
)

// PromptHandler обрабатывает HTTP-запросы, связанные с промптами.
type PromptHandler struct {
	promptService service.PromptService // Интерфейс сервиса промптов
	configService service.ConfigService // <<< ДОБАВЛЕНО: Интерфейс сервиса конфигураций
	cfg           *config.Config
	logger        *zap.Logger
}

// NewPromptHandler создает новый экземпляр PromptHandler.
func NewPromptHandler(promptService service.PromptService, configService service.ConfigService, cfg *config.Config, logger *zap.Logger) *PromptHandler {
	return &PromptHandler{
		promptService: promptService,
		configService: configService, // <<< ДОБАВЛЕНО
		cfg:           cfg,
		logger:        logger.Named("PromptHandler"),
	}
}

// ShowPrompts отображает страницу со списком ключей промптов.
func (h *PromptHandler) ShowPrompts(c *gin.Context) {
	flashType, flashMessage, flashExists := getFlashMsg(c, h.cfg)

	// Получаем список ключей промптов из сервиса
	promptKeys, err := h.promptService.ListPromptKeys(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to get prompt keys from service", zap.Error(err))
		_ = setFlashMsg(c, "error", "Could not load prompt keys: "+err.Error(), h.cfg)
		promptKeys = []string{} // Отправляем пустой список
		// Редирект на себя, чтобы показать flash
		c.Redirect(http.StatusFound, "/admin/prompts")
		return
	}

	renderData := gin.H{
		"title":      "Manage Prompts",
		"promptKeys": promptKeys, // Передаем список ключей
	}
	if flashExists {
		renderData["flashType"] = flashType
		renderData["flashMessage"] = flashMessage
	}

	c.HTML(http.StatusOK, "prompts.html", renderData)
}

// ShowCreatePromptForm отображает форму для создания нового КЛЮЧА промпта.
// TODO: Убедиться, что этот метод и шаблон prompt_new_key.html делают то, что нужно.
// Возможно, его тоже нужно переделать или использовать ShowEditPromptForm?
// Пока оставляем как есть.
func (h *PromptHandler) ShowCreatePromptForm(c *gin.Context) {
	// <<< Получаем Flash сообщение >>>
	flashType, flashMessage, flashExists := getFlashMsg(c, h.cfg)

	// Раньше этот метод рендерил prompt_edit.html для *создания*.
	// Если теперь /new отвечает за создание ключа, возможно, нужен другой шаблон?
	// Например, prompt_new_key.html
	renderData := gin.H{
		"title":       "Create New Prompt Key",
		"form_action": "/admin/prompts", // POST на /admin/prompts
		// Возможно, нужно передавать сюда список языков, если создание ключа
		// сразу создает пустые записи для всех языков?
	}
	if flashExists {
		renderData["flashType"] = flashType
		renderData["flashMessage"] = flashMessage
	}

	// Используем старый шаблон? Или нужен новый prompt_new_key.html?
	c.HTML(http.StatusOK, "prompt_new_key.html", renderData) // !!! ИЗМЕНЕНО НА prompt_new_key.html
}

// CreatePrompt обрабатывает отправку формы создания НОВОГО КЛЮЧА промпта.
// TODO: Убедиться, что этот метод правильно обрабатывает создание ключа.
// Возможно, он должен принимать только Key и вызывать promptService.CreatePromptKey?
// Пока оставляем старую логику, которая ожидает Key, Language, Content,
// но она не соответствует идее создания КЛЮЧА.
func (h *PromptHandler) CreatePrompt(c *gin.Context) {
	// Эта логика, вероятно, неверна для создания КЛЮЧА.
	// Она ожидает один промпт, а нужно создать ключ.
	var form struct {
		Key string `form:"key" binding:"required"`
	}
	if err := c.ShouldBind(&form); err != nil {
		h.logger.Error("Failed to bind new prompt key data", zap.Error(err))
		_ = setFlashMsg(c, "error", "Invalid data submitted. "+err.Error(), h.cfg)
		c.Redirect(http.StatusSeeOther, "/admin/prompts/new")
		return
	}

	// Вызываем сервис для создания ключа
	err := h.promptService.CreatePromptKey(c.Request.Context(), form.Key)
	if err != nil {
		h.logger.Error("Failed to create prompt key via service", zap.Error(err), zap.String("key", form.Key))
		errMsg := fmt.Sprintf("Failed to create prompt key: %v", err)
		if errors.Is(err, database.ErrPromptKeyAlreadyExists) {
			errMsg = fmt.Sprintf("Prompt key '%s' already exists.", form.Key)
		}
		_ = setFlashMsg(c, "error", errMsg, h.cfg)
		c.Redirect(http.StatusSeeOther, "/admin/prompts/new")
		return
	}

	h.logger.Info("Prompt key created successfully", zap.String("key", form.Key))
	_ = setFlashMsg(c, "success", fmt.Sprintf("Prompt key '%s' created successfully!", form.Key), h.cfg)
	// Редирект на страницу редактирования нового ключа?
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/admin/prompts/key/%s/edit", form.Key))
	// Или на список ключей?
	// c.Redirect(http.StatusSeeOther, "/admin/prompts")
}

// ShowEditPromptForm отображает новую форму для редактирования всех языков по ключу.
func (h *PromptHandler) ShowEditPromptForm(c *gin.Context) {
	flashType, flashMessage, flashExists := getFlashMsg(c, h.cfg)
	key := c.Param("key")
	if key == "" {
		h.logger.Warn("Missing prompt key in request URL")
		_ = setFlashMsg(c, "error", "Prompt key is missing.", h.cfg)
		c.Redirect(http.StatusSeeOther, "/admin/prompts")
		return
	}

	log := h.logger.With(zap.String("promptKey", key))

	// <<< ДОБАВЛЕНО: Получение стоимости токена >>>
	tokenInputCost := -1.0 // Значение по умолчанию, если не найдено или ошибка
	costConfigKey := "generation.token_input_cost"
	costConfig, err := h.configService.GetConfigByKey(c.Request.Context(), costConfigKey)
	if err == nil {
		costFloat, parseErr := strconv.ParseFloat(costConfig.Value, 64)
		if parseErr == nil {
			tokenInputCost = costFloat
			log.Debug("Successfully fetched and parsed token input cost", zap.Float64("cost", tokenInputCost))
		} else {
			log.Error("Failed to parse token input cost value", zap.String("key", costConfigKey), zap.String("value", costConfig.Value), zap.Error(parseErr))
			// Оставляем tokenInputCost = -1.0
		}
	} else {
		log.Warn("Failed to get token input cost config, using default -1.0", zap.String("key", costConfigKey), zap.Error(err))
		// Оставляем tokenInputCost = -1.0
	}
	// <<< КОНЕЦ ДОБАВЛЕНИЯ >>>

	// 4. Готовим данные для рендеринга
	renderData := gin.H{
		"PromptKey":          key,
		"SupportedLanguages": h.cfg.SupportedLanguages,
		"TokenInputCost":     tokenInputCost, // <<< ДОБАВЛЕНО: Передаем стоимость в шаблон
	}
	if flashExists {
		renderData["FlashType"] = flashType
		renderData["FlashMessage"] = flashMessage
	}

	log.Debug("Rendering prompt_edit.html")
	c.HTML(http.StatusOK, "prompt_edit.html", renderData)
}

// GetPromptContent возвращает содержимое промпта для API.
func (h *PromptHandler) GetPromptContent(c *gin.Context) {
	key := c.Param("key")
	language := c.Param("language")

	prompt, err := h.promptService.GetPrompt(c.Request.Context(), key, language)
	if err != nil {
		// Не логируем "не найдено" как ошибку, просто возвращаем 404
		// if errors.Is(err, database.ErrPromptNotFound) {...
		h.logger.Warn("Prompt not found for API get", zap.String("key", key), zap.String("language", language), zap.Error(err))
		c.JSON(http.StatusNotFound, gin.H{"error": "Prompt not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"content": prompt.Content})
}

// RegisterPromptRoutes регистрирует роуты для управления промптами.
func (h *PromptHandler) RegisterPromptRoutes(group *gin.RouterGroup) {
	h.logger.Debug("DEBUG: Вызвана функция RegisterPromptRoutes")

	// Группа для страниц админки (HTML)
	adminPromptsGroup := group.Group("/prompts") // Base is /admin/prompts
	h.logger.Debug("DEBUG: Создана подгруппа /prompts для HTML")
	{
		adminPromptsGroup.GET("", h.ShowPrompts) // Показывает список ключей
		h.logger.Debug("DEBUG: Зарегистрирован GET /prompts")

		// Роуты для создания КЛЮЧА
		adminPromptsGroup.GET("/new", h.ShowCreatePromptForm) // Форма создания ключа
		h.logger.Debug("DEBUG: Зарегистрирован GET /prompts/new (для ключа)")
		adminPromptsGroup.POST("/new", h.CreatePrompt) // Обработка создания ключа
		h.logger.Debug("DEBUG: Зарегистрирован POST /prompts/new (для ключа)")

		// Роут для редактирования по ключу
		adminPromptsGroup.GET("/key/:key/edit", h.ShowEditPromptForm)
		h.logger.Debug("DEBUG: Зарегистрирован GET /prompts/key/:key/edit")
	}

	h.logger.Info("Зарегистрированы роуты для PromptHandler (только HTML)")
}
