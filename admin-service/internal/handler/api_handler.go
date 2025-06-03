package handler

import (
	"errors"
	"net/http"

	"novel-server/admin-service/internal/service"
	"novel-server/shared/models"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"go.uber.org/zap"
)

// ApiHandler обрабатывает запросы к внутреннему API v1.
type ApiHandler struct {
	promptService service.PromptService
	logger        *zap.Logger
}

// NewApiHandler создает новый экземпляр ApiHandler.
func NewApiHandler(promptService service.PromptService, logger *zap.Logger) *ApiHandler {
	if promptService == nil {
		log.Fatal().Msg("PromptService is nil for ApiHandler")
	}
	return &ApiHandler{
		promptService: promptService,
		logger:        logger.Named("ApiHandler"),
	}
}

// RegisterRoutes регистрирует API маршруты для ApiHandler.
func (h *ApiHandler) RegisterRoutes(group *gin.RouterGroup) {
	h.logger.Info("Регистрация API маршрутов для промптов...")
	prompts := group.Group("/prompts")
	{
		prompts.GET("", h.ListPromptsByKey)
		prompts.DELETE("/:key", h.DeletePromptByKey)
	}
	h.logger.Info("API маршруты для промптов зарегистрированы.")
}

// --- Prompt Handlers ---

type UpsertPromptRequest struct {
	Key      string `json:"key" binding:"required"`
	Language string `json:"language" binding:"required"`
	Content  string `json:"content" binding:"required"`
}

// UpsertPrompt создает или обновляет промпт.
// POST /api/v1/prompts
// PUT /api/v1/prompts/:key/:language (для совместимости/альтернативы?)
// <<< Решено использовать POST /api/v1/prompts для Upsert >>>
func (h *ApiHandler) UpsertPrompt(c *gin.Context) {
	var req UpsertPromptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Error("Invalid request body for upsert prompt", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}

	prompt, err := h.promptService.UpsertPrompt(c.Request.Context(), req.Key, req.Language, req.Content)
	if err != nil {
		h.logger.Error("Failed to upsert prompt", zap.Error(err), zap.String("key", req.Key), zap.String("language", req.Language))
		if errors.Is(err, models.ErrAlreadyExists) {
			c.JSON(http.StatusConflict, gin.H{"error": "Conflict during upsert (should not happen)"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save prompt"})
		}
		return
	}

	status := http.StatusOK
	if prompt.CreatedAt.Equal(prompt.UpdatedAt) {
		status = http.StatusCreated
	}

	c.JSON(status, prompt)
}

// @Summary Получение промптов по ключу
// @Description Возвращает все языковые версии для указанного ключа
// @Tags prompts
// @Accept json
// @Produce json
// @Param key query string true "Ключ промпта"
// @Success 200 {object} map[string]interface{} "Список промптов"
// @Failure 400 {object} map[string]interface{} "Неверные параметры"
// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
// @Router /api/v1/prompts [get]
func (h *ApiHandler) ListPromptsByKey(c *gin.Context) {
	key := c.Query("key")

	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Query parameter 'key' is required"})
		return
	}

	promptsMap, err := h.promptService.GetPromptsByKey(c.Request.Context(), key)
	if err != nil {
		h.logger.Error("Failed to list prompts by key", zap.Error(err), zap.String("key", key))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list prompts by key"})
		return
	}

	c.JSON(http.StatusOK, promptsMap)
}

// GetPrompt возвращает промпт по ключу и языку.
// GET /api/v1/prompts/:key/:language
func (h *ApiHandler) GetPrompt(c *gin.Context) {
	key := c.Param("key")
	language := c.Param("language")

	if key == "" || language == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Key and language parameters are required"})
		return
	}

	prompt, err := h.promptService.GetPrompt(c.Request.Context(), key, language)
	if err != nil {
		h.logger.Warn("Failed to get prompt", zap.Error(err), zap.String("key", key), zap.String("language", language))
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get prompt"})
		}
		return
	}

	c.JSON(http.StatusOK, prompt)
}

// @Summary Удаление промпта по ключу
// @Description Удаляет все языковые версии промпта по ключу
// @Tags prompts
// @Accept json
// @Produce json
// @Param key path string true "Ключ промпта"
// @Success 204 "Промпт удален"
// @Failure 400 {object} map[string]interface{} "Неверные параметры"
// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
// @Router /api/v1/prompts/{key} [delete]
func (h *ApiHandler) DeletePromptByKey(c *gin.Context) {
	key := c.Param("key")

	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Key parameter is required"})
		return
	}

	err := h.promptService.DeletePromptsByKey(c.Request.Context(), key)
	if err != nil {
		h.logger.Error("Failed to delete prompts by key", zap.Error(err), zap.String("key", key))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete prompt key"})
		return
	}

	c.Status(http.StatusNoContent)
}

// DeletePromptByLang удаляет конкретную языковую версию промпта.
// DELETE /api/v1/prompts/:key/:language
func (h *ApiHandler) DeletePromptByLang(c *gin.Context) {
	key := c.Param("key")
	language := c.Param("language")

	if key == "" || language == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Key and language parameters are required"})
		return
	}

	err := h.promptService.DeletePromptByKeyAndLang(c.Request.Context(), key, language)
	if err != nil {
		// Ошибка "не найдено" обрабатывается в сервисе как успех (nil), поэтому здесь только 500
		h.logger.Error("Failed to delete prompt by key and language", zap.Error(err), zap.String("key", key), zap.String("language", language))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete prompt language"})
		return
	}

	c.Status(http.StatusNoContent)
}
