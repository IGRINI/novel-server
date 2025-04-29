package handler

import (
	"fmt"
	"net/http"
	"novel-server/admin-service/internal/config"
	"novel-server/admin-service/internal/service"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	shared_models "novel-server/shared/models"
)

// PromptHandler обрабатывает HTTP-запросы, связанные с промптами.
type PromptHandler struct {
	promptService service.PromptService // Интерфейс сервиса промптов
	cfg           *config.Config
	logger        *zap.Logger
}

// NewPromptHandler создает новый экземпляр PromptHandler.
func NewPromptHandler(promptService service.PromptService, cfg *config.Config, logger *zap.Logger) *PromptHandler {
	return &PromptHandler{
		promptService: promptService,
		cfg:           cfg,
		logger:        logger.Named("PromptHandler"),
	}
}

// ShowPrompts отображает страницу со списком промптов.
func (h *PromptHandler) ShowPrompts(c *gin.Context) {
	// <<< Получаем Flash сообщение >>>
	flashType, flashMessage, flashExists := getFlashMsg(c, h.cfg)

	// Получаем список промптов из сервиса
	prompts, err := h.promptService.GetAllPrompts(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to get prompts from service", zap.Error(err))
		// Отображаем ошибку пользователю через Flash?
		_ = setFlashMsg(c, "error", "Could not load prompts: "+err.Error(), h.cfg)
		// Отправляем пустой список в шаблон или редирект?
		// Пока отправляем пустой список.
		prompts = []*shared_models.Prompt{}
		// Редиректим сами на себя, чтобы показать flash об ошибке загрузки
		c.Redirect(http.StatusFound, "/admin/prompts")
		return
	}

	renderData := gin.H{
		"title":   "Manage Prompts",
		"prompts": prompts,
	}
	// <<< Добавляем Flash в данные для шаблона, если он есть >>>
	if flashExists {
		renderData["flashType"] = flashType
		renderData["flashMessage"] = flashMessage
	}

	c.HTML(http.StatusOK, "prompts.html", renderData)
}

// ShowCreatePromptForm отображает форму для создания нового промпта.
func (h *PromptHandler) ShowCreatePromptForm(c *gin.Context) {
	// <<< Получаем Flash сообщение >>>
	flashType, flashMessage, flashExists := getFlashMsg(c, h.cfg)

	renderData := gin.H{
		"title":       "Create New Prompt",
		"prompt":      shared_models.Prompt{}, // Пустой промпт для формы
		"form_action": "/admin/prompts",
		"languages":   h.cfg.SupportedLanguages,
	}
	// <<< Добавляем Flash в данные для шаблона, если он есть >>>
	if flashExists {
		renderData["flashType"] = flashType
		renderData["flashMessage"] = flashMessage
	}

	c.HTML(http.StatusOK, "prompt_edit.html", renderData)
}

// CreatePrompt обрабатывает отправку формы создания промпта.
func (h *PromptHandler) CreatePrompt(c *gin.Context) {
	var prompt shared_models.Prompt
	if err := c.ShouldBind(&prompt); err != nil {
		h.logger.Error("Failed to bind prompt data", zap.Error(err))
		// <<< Устанавливаем Flash сообщение об ошибке >>>
		_ = setFlashMsg(c, "error", "Invalid data submitted. "+err.Error(), h.cfg)
		c.Redirect(http.StatusSeeOther, "/admin/prompts/new") // Редирект на форму создания
		return
	}

	// Валидация (пример: ключ не должен быть пустым)
	if prompt.Key == "" || prompt.Language == "" || prompt.Content == "" {
		_ = setFlashMsg(c, "error", "Key, Language, and Content cannot be empty.", h.cfg)
		// Можно сохранить введенные данные в куку или передать обратно в шаблон,
		// но для простоты просто редиректим на пустую форму.
		c.Redirect(http.StatusSeeOther, "/admin/prompts/new")
		return
	}

	// Вызываем сервис для создания/обновления (Upsert)
	_, createErr := h.promptService.UpsertPrompt(c.Request.Context(), prompt.Key, prompt.Language, prompt.Content)
	if createErr != nil {
		h.logger.Error("Failed to create prompt via service", zap.Error(createErr), zap.Any("prompt", prompt))
		// <<< Устанавливаем Flash сообщение об ошибке >>>
		_ = setFlashMsg(c, "error", fmt.Sprintf("Failed to create prompt: %v", createErr), h.cfg)
		// Редирект на форму создания, возможно, стоит передать введенные данные обратно
		c.Redirect(http.StatusSeeOther, "/admin/prompts/new")
		return
	}

	h.logger.Info("Prompt created successfully", zap.String("key", prompt.Key), zap.String("lang", prompt.Language))
	// <<< Устанавливаем Flash сообщение об успехе >>>
	_ = setFlashMsg(c, "success", "Prompt created successfully!", h.cfg)
	c.Redirect(http.StatusSeeOther, "/admin/prompts") // Редирект на список
}

// ShowEditPromptForm отображает форму для редактирования существующего промпта.
func (h *PromptHandler) ShowEditPromptForm(c *gin.Context) {
	// <<< Получаем Flash сообщение >>>
	flashType, flashMessage, flashExists := getFlashMsg(c, h.cfg)

	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		h.logger.Error("Invalid prompt ID format", zap.String("id", idStr), zap.Error(err))
		_ = setFlashMsg(c, "error", "Invalid prompt ID.", h.cfg)
		c.Redirect(http.StatusSeeOther, "/admin/prompts")
		return
	}

	// Получаем промпт по ID из сервиса
	prompt, err := h.promptService.GetPromptByID(c.Request.Context(), int64(id))
	if err != nil {
		h.logger.Error("Failed to get prompt by ID from service", zap.Uint64("id", id), zap.Error(err))
		_ = setFlashMsg(c, "error", fmt.Sprintf("Prompt with ID %d not found or error occurred: %v", id, err), h.cfg)
		c.Redirect(http.StatusSeeOther, "/admin/prompts")
		return
	}
	// ID уже есть в prompt после GetPromptByID

	renderData := gin.H{
		"title":       fmt.Sprintf("Edit Prompt #%d", id),
		"prompt":      prompt,
		"form_action": fmt.Sprintf("/admin/prompts/%d", id),
		"languages":   h.cfg.SupportedLanguages,
	}
	// <<< Добавляем Flash в данные для шаблона, если он есть >>>
	if flashExists {
		renderData["flashType"] = flashType
		renderData["flashMessage"] = flashMessage
	}

	c.HTML(http.StatusOK, "prompt_edit.html", renderData)
}

// UpdatePrompt обрабатывает отправку формы редактирования промпта.
func (h *PromptHandler) UpdatePrompt(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		h.logger.Error("Invalid prompt ID format for update", zap.String("id", idStr), zap.Error(err))
		_ = setFlashMsg(c, "error", "Invalid prompt ID.", h.cfg)
		c.Redirect(http.StatusSeeOther, "/admin/prompts")
		return
	}

	var prompt shared_models.Prompt
	if err := c.ShouldBind(&prompt); err != nil {
		h.logger.Error("Failed to bind prompt data for update", zap.Uint64("id", id), zap.Error(err))
		_ = setFlashMsg(c, "error", "Invalid data submitted. "+err.Error(), h.cfg)
		c.Redirect(http.StatusSeeOther, fmt.Sprintf("/admin/prompts/%d/edit", id))
		return
	}
	prompt.ID = int64(id) // Устанавливаем ID из URL

	// Валидация (пример: ключ не должен быть пустым)
	if prompt.Key == "" || prompt.Language == "" || prompt.Content == "" {
		_ = setFlashMsg(c, "error", "Key, Language, and Content cannot be empty.", h.cfg)
		// Редирект обратно на форму редактирования
		c.Redirect(http.StatusSeeOther, fmt.Sprintf("/admin/prompts/%d/edit", id))
		return
	}

	// Вызываем сервис для обновления (Upsert)
	_, updateErr := h.promptService.UpsertPrompt(c.Request.Context(), prompt.Key, prompt.Language, prompt.Content)
	if updateErr != nil {
		h.logger.Error("Failed to update prompt via service", zap.Error(updateErr), zap.Any("prompt", prompt))
		_ = setFlashMsg(c, "error", fmt.Sprintf("Failed to update prompt: %v", updateErr), h.cfg)
		c.Redirect(http.StatusSeeOther, fmt.Sprintf("/admin/prompts/%d/edit", id)) // Редирект на форму редактирования
		return
	}

	h.logger.Info("Prompt updated successfully", zap.Int64("id", prompt.ID), zap.String("key", prompt.Key), zap.String("lang", prompt.Language))
	// <<< Устанавливаем Flash сообщение об успехе >>>
	_ = setFlashMsg(c, "success", "Prompt updated successfully!", h.cfg)
	c.Redirect(http.StatusSeeOther, "/admin/prompts") // Редирект на список
}

// DeletePrompt обрабатывает запрос на удаление промпта.
func (h *PromptHandler) DeletePrompt(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		h.logger.Error("Invalid prompt ID format for delete", zap.String("id", idStr), zap.Error(err))
		// <<< Устанавливаем Flash сообщение об ошибке >>>
		_ = setFlashMsg(c, "error", "Invalid prompt ID.", h.cfg)
		c.Redirect(http.StatusSeeOther, "/admin/prompts")
		return
	}

	// Вызываем сервис для удаления промпта по ID
	deleteErr := h.promptService.DeletePromptByID(c.Request.Context(), int64(id))
	if deleteErr != nil {
		h.logger.Error("Failed to delete prompt via service", zap.Error(deleteErr), zap.Uint64("id", id))
		// <<< Устанавливаем Flash сообщение об ошибке >>>
		_ = setFlashMsg(c, "error", fmt.Sprintf("Failed to delete prompt: %v", deleteErr), h.cfg)
	} else {
		h.logger.Info("Prompt deleted successfully", zap.Uint64("id", id))
		// <<< Устанавливаем Flash сообщение об успехе >>>
		_ = setFlashMsg(c, "success", "Prompt deleted successfully!", h.cfg)
	}

	c.Redirect(http.StatusSeeOther, "/admin/prompts") // Редирект на список
}

// RegisterPromptRoutes регистрирует роуты для управления промптами.
func (h *PromptHandler) RegisterPromptRoutes(group *gin.RouterGroup) {
	promptsGroup := group.Group("/prompts")
	{
		promptsGroup.GET("", h.ShowPrompts)                 // GET /admin/prompts
		promptsGroup.GET("/new", h.ShowCreatePromptForm)    // GET /admin/prompts/new
		promptsGroup.POST("", h.CreatePrompt)               // POST /admin/prompts
		promptsGroup.GET("/:id/edit", h.ShowEditPromptForm) // GET /admin/prompts/:id/edit
		// Используем POST для обновления и удаления, т.к. формы HTML отправляют POST
		promptsGroup.POST("/:id", h.UpdatePrompt)        // POST /admin/prompts/:id (Update)
		promptsGroup.POST("/:id/delete", h.DeletePrompt) // POST /admin/prompts/:id/delete (Delete)
	}
	h.logger.Info("Зарегистрированы роуты для PromptHandler в группе /admin/prompts")
}
