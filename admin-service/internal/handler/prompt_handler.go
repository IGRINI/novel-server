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
	flash, err := getFlashMessage(c, []byte(h.cfg.JWTSecret))
	if err != nil {
		h.logger.Error("Failed to get flash message", zap.Error(err))
		// Не прерываем запрос, просто не будет flash-сообщения
	}

	// Получаем список промптов из сервиса
	prompts, err := h.promptService.GetAllPrompts(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to get prompts from service", zap.Error(err))
		// Отображаем ошибку пользователю через Flash?
		_ = setFlashMessage(c, "error", "Could not load prompts: "+err.Error(), []byte(h.cfg.JWTSecret))
		// Отправляем пустой список в шаблон или редирект?
		// Пока отправляем пустой список.
		prompts = []*shared_models.Prompt{}
	}

	c.HTML(http.StatusOK, "prompts.html", gin.H{
		"title":   "Manage Prompts",
		"prompts": prompts,
		"flash":   flash, // Передаем flash-сообщение в шаблон
	})
}

// ShowCreatePromptForm отображает форму для создания нового промпта.
func (h *PromptHandler) ShowCreatePromptForm(c *gin.Context) {
	flash, err := getFlashMessage(c, []byte(h.cfg.JWTSecret))
	if err != nil {
		h.logger.Error("Failed to get flash message", zap.Error(err))
	}

	c.HTML(http.StatusOK, "prompt_edit.html", gin.H{
		"title":       "Create New Prompt",
		"prompt":      shared_models.Prompt{}, // Пустой промпт для формы
		"form_action": "/admin/prompts",
		"languages":   h.cfg.SupportedLanguages,
		"flash":       flash,
	})
}

// CreatePrompt обрабатывает отправку формы создания промпта.
func (h *PromptHandler) CreatePrompt(c *gin.Context) {
	var prompt shared_models.Prompt
	if err := c.ShouldBind(&prompt); err != nil {
		h.logger.Error("Failed to bind prompt data", zap.Error(err))
		_ = setFlashMessage(c, "error", "Invalid data submitted.", []byte(h.cfg.JWTSecret))
		c.Redirect(http.StatusSeeOther, "/admin/prompts/new")
		return
	}

	// Вызываем сервис для создания/обновления (Upsert)
	_, createErr := h.promptService.UpsertPrompt(c.Request.Context(), prompt.Key, prompt.Language, prompt.Content)
	if createErr != nil {
		h.logger.Error("Failed to create prompt via service", zap.Error(createErr), zap.Any("prompt", prompt))
		_ = setFlashMessage(c, "error", fmt.Sprintf("Failed to create prompt: %v", createErr), []byte(h.cfg.JWTSecret))
		c.Redirect(http.StatusSeeOther, "/admin/prompts/new")
		return
	}

	_ = setFlashMessage(c, "success", "Prompt created successfully!", []byte(h.cfg.JWTSecret))
	c.Redirect(http.StatusSeeOther, "/admin/prompts")
}

// ShowEditPromptForm отображает форму для редактирования существующего промпта.
func (h *PromptHandler) ShowEditPromptForm(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		h.logger.Error("Invalid prompt ID format", zap.String("id", idStr), zap.Error(err))
		_ = setFlashMessage(c, "error", "Invalid prompt ID.", []byte(h.cfg.JWTSecret))
		c.Redirect(http.StatusSeeOther, "/admin/prompts")
		return
	}

	// Получаем промпт по ID из сервиса
	prompt, err := h.promptService.GetPromptByID(c.Request.Context(), int64(id))
	if err != nil {
		h.logger.Error("Failed to get prompt by ID from service", zap.Uint64("id", id), zap.Error(err))
		_ = setFlashMessage(c, "error", fmt.Sprintf("Prompt with ID %d not found or error occurred: %v", id, err), []byte(h.cfg.JWTSecret))
		c.Redirect(http.StatusSeeOther, "/admin/prompts")
		return
	}

	flash, err := getFlashMessage(c, []byte(h.cfg.JWTSecret))
	if err != nil {
		h.logger.Error("Failed to get flash message", zap.Error(err))
	}

	prompt.ID = int64(id) // Устанавливаем ID из URL, исправлен тип

	c.HTML(http.StatusOK, "prompt_edit.html", gin.H{
		"title":       fmt.Sprintf("Edit Prompt #%d", id),
		"prompt":      prompt,
		"form_action": fmt.Sprintf("/admin/prompts/%d", id),
		"languages":   h.cfg.SupportedLanguages,
		"flash":       flash,
	})
}

// UpdatePrompt обрабатывает отправку формы редактирования промпта.
func (h *PromptHandler) UpdatePrompt(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		h.logger.Error("Invalid prompt ID format for update", zap.String("id", idStr), zap.Error(err))
		_ = setFlashMessage(c, "error", "Invalid prompt ID.", []byte(h.cfg.JWTSecret))
		c.Redirect(http.StatusSeeOther, "/admin/prompts")
		return
	}

	var prompt shared_models.Prompt
	if err := c.ShouldBind(&prompt); err != nil {
		h.logger.Error("Failed to bind prompt data for update", zap.Uint64("id", id), zap.Error(err))
		_ = setFlashMessage(c, "error", "Invalid data submitted.", []byte(h.cfg.JWTSecret))
		c.Redirect(http.StatusSeeOther, fmt.Sprintf("/admin/prompts/%d/edit", id))
		return
	}
	prompt.ID = int64(id) // Устанавливаем ID из URL, исправлен тип

	// Вызываем сервис для обновления (Upsert)
	_, updateErr := h.promptService.UpsertPrompt(c.Request.Context(), prompt.Key, prompt.Language, prompt.Content)
	if updateErr != nil {
		h.logger.Error("Failed to update prompt via service", zap.Error(updateErr), zap.Any("prompt", prompt))
		_ = setFlashMessage(c, "error", fmt.Sprintf("Failed to update prompt: %v", updateErr), []byte(h.cfg.JWTSecret))
		c.Redirect(http.StatusSeeOther, fmt.Sprintf("/admin/prompts/%d/edit", id))
		return
	}

	_ = setFlashMessage(c, "success", "Prompt updated successfully!", []byte(h.cfg.JWTSecret))
	c.Redirect(http.StatusSeeOther, "/admin/prompts")
}

// DeletePrompt обрабатывает запрос на удаление промпта.
func (h *PromptHandler) DeletePrompt(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		h.logger.Error("Invalid prompt ID format for delete", zap.String("id", idStr), zap.Error(err))
		_ = setFlashMessage(c, "error", "Invalid prompt ID.", []byte(h.cfg.JWTSecret))
		c.Redirect(http.StatusSeeOther, "/admin/prompts")
		return
	}

	// Вызываем сервис для удаления промпта по ID
	deleteErr := h.promptService.DeletePromptByID(c.Request.Context(), int64(id))
	if deleteErr != nil {
		h.logger.Error("Failed to delete prompt via service", zap.Error(deleteErr), zap.Uint64("id", id))
		_ = setFlashMessage(c, "error", fmt.Sprintf("Failed to delete prompt: %v", deleteErr), []byte(h.cfg.JWTSecret))
	} else {
		_ = setFlashMessage(c, "success", "Prompt deleted successfully!", []byte(h.cfg.JWTSecret))
	}

	c.Redirect(http.StatusSeeOther, "/admin/prompts")
}
