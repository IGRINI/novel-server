package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	sharedInterfaces "novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// APIError используется для ответа об ошибках в этом внутреннем хендлере
// (Возможно, стоит перенести в shared/models, если будет переиспользоваться)
// type APIError struct {
//  Message string `json:"message"`
// }

// listUserDraftsInternal возвращает список черновиков для указанного пользователя (для админки).
func (h *GameplayHandler) listUserDraftsInternal(c *gin.Context) {
	log := h.logger.With(zap.String("handler", "listUserDraftsInternal"))

	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Warn("Invalid user ID format", zap.String("user_id", userIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, sharedModels.ErrorResponse{Message: "Invalid user ID format"})
		return
	}

	// Используем новую вспомогательную функцию
	limit, cursor, ok := parsePaginationParams(c, 20, 100, h.logger) // Установим maxLimit=100
	if !ok {
		return // Ошибка уже обработана
	}

	log = log.With(zap.Stringer("userID", userID), zap.Int("limit", limit), zap.String("cursor", cursor))
	log.Info("Internal request for user drafts")

	drafts, nextCursor, err := h.service.ListUserDrafts(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		if errors.Is(err, sharedInterfaces.ErrInvalidCursor) {
			log.Warn("Invalid cursor provided", zap.Error(err))
			c.JSON(http.StatusBadRequest, sharedModels.ErrorResponse{Message: "Invalid cursor format"})
			return
		}
		log.Error("Failed to list user drafts internally", zap.Error(err))
		c.JSON(http.StatusInternalServerError, sharedModels.ErrorResponse{Message: "Failed to retrieve drafts"})
		return
	}

	// Используем ту же DTO PaginatedResponse, что и для публичного API
	response := PaginatedResponse{
		Data:       drafts, // Сервис уже возвращает []*StoryConfigSummary
		NextCursor: nextCursor,
	}
	c.JSON(http.StatusOK, response)
}

// -- Internal Handlers --

// listUserStoriesInternal обрабатывает внутренний запрос на получение списка историй пользователя.
// <<< ИЗМЕНЕНО: Использует курсорную пагинацию >>>
func (h *GameplayHandler) listUserStoriesInternal(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.logger.Warn("Invalid userID in listUserStoriesInternal", zap.String("userID", userIDStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid user ID", sharedModels.ErrBadRequest), h.logger)
		return
	}

	// Используем новую вспомогательную функцию
	limit, cursor, ok := parsePaginationParams(c, 10, 100, h.logger)
	if !ok {
		return // Ошибка уже обработана
	}

	// <<< ИЗМЕНЕНО: Вызываем обновленный метод сервиса >>>
	stories, nextCursor, err := h.service.ListUserPublishedStories(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		h.logger.Error("Error listing user stories internally", zap.String("userID", userID.String()), zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	// <<< ИЗМЕНЕНО: Используем PaginatedResponse >>>
	resp := PaginatedResponse{
		Data:       stories,
		NextCursor: nextCursor,
	}
	c.JSON(http.StatusOK, resp)
}

// <<< ДОБАВЛЕНО: Обработчик для обновления черновика >>>
func (h *GameplayHandler) updateDraftInternal(c *gin.Context) {
	log := h.logger.With(zap.String("handler", "updateDraftInternal"))

	// Используем новую вспомогательную функцию
	draftID, ok := parseUUIDParam(c, "draft_id", h.logger)
	if !ok {
		return // Ошибка уже обработана
	}
	// Парсим userID из URL, хотя он может не использоваться напрямую
	userIDStr := c.Param("user_id")
	log = log.With(zap.String("draftID", draftID.String()), zap.String("userIDParam", userIDStr))

	// Парсим тело запроса
	type updateRequest struct {
		ConfigJson    string                   `json:"configJson"`
		UserInputJson string                   `json:"userInputJson"`
		Status        sharedModels.StoryStatus `json:"status"`
	}
	var req updateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Warn("Invalid request body for draft update", zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid request body: %v", sharedModels.ErrBadRequest, err), h.logger)
		return
	}

	log.Info("Handling internal draft update request", zap.String("newStatus", string(req.Status)))

	// Вызываем сервис
	err := h.service.UpdateDraftInternal(c.Request.Context(), draftID, req.ConfigJson, req.UserInputJson, req.Status)
	if err != nil {
		log.Error("Error updating draft internally", zap.Error(err))
		handleServiceError(c, err, h.logger) // Передаем ошибку для стандартизированной обработки
		return
	}

	log.Info("Internal draft update successful")
	c.Status(http.StatusNoContent) // Успех
}

// <<< ДОБАВЛЕНО: Обработчик для обновления истории >>>
func (h *GameplayHandler) updateStoryInternal(c *gin.Context) {
	log := h.logger.With(zap.String("handler", "updateStoryInternal"))

	// Используем новую вспомогательную функцию
	storyID, ok := parseUUIDParam(c, "story_id", h.logger)
	if !ok {
		return // Ошибка уже обработана
	}
	// Парсим userID из URL, хотя он может не использоваться напрямую
	userIDStr := c.Param("user_id")
	log = log.With(zap.String("storyID", storyID.String()), zap.String("userIDParam", userIDStr))

	// Парсим тело запроса
	type updateRequest struct {
		ConfigJson json.RawMessage          `json:"configJson"`
		SetupJson  json.RawMessage          `json:"setupJson"`
		Status     sharedModels.StoryStatus `json:"status"`
	}
	var req updateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Warn("Invalid request body for story update", zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid request body: %v", sharedModels.ErrBadRequest, err), h.logger)
		return
	}

	log.Info("Handling internal story update request", zap.String("newStatus", string(req.Status)))

	// Вызываем сервис, передавая json.RawMessage напрямую
	err := h.service.UpdateStoryInternal(c.Request.Context(), storyID, req.ConfigJson, req.SetupJson, req.Status)
	if err != nil {
		log.Error("Error updating story internally", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	log.Info("Internal story update successful")
	c.Status(http.StatusNoContent)
}

// <<< ДОБАВЛЕНО: Обработчик для обновления сцены >>>
func (h *GameplayHandler) updateSceneInternal(c *gin.Context) {
	log := h.logger.With(zap.String("handler", "updateSceneInternal"))

	// Используем новую вспомогательную функцию
	sceneID, ok := parseUUIDParam(c, "scene_id", h.logger)
	if !ok {
		return // Ошибка уже обработана
	}
	// Парсим остальные ID из URL, хотя они могут не использоваться напрямую
	storyIDStr := c.Param("story_id")
	userIDStr := c.Param("user_id")
	log = log.With(zap.String("sceneID", sceneID.String()), zap.String("storyIDParam", storyIDStr), zap.String("userIDParam", userIDStr))

	// Парсим тело запроса
	type updateRequest struct {
		ContentJson string `json:"contentJson" binding:"required"`
	}
	var req updateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Warn("Invalid request body for scene update", zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid request body: %v", sharedModels.ErrBadRequest, err), h.logger)
		return
	}

	log.Info("Handling internal scene update request")

	// Вызываем сервис
	err := h.service.UpdateSceneInternal(c.Request.Context(), sceneID, req.ContentJson)
	if err != nil {
		log.Error("Error updating scene internally", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	log.Info("Internal scene update successful")
	c.JSON(http.StatusOK, gin.H{"message": "Scene updated successfully"}) // Отвечаем JSON для AJAX запроса
}

// <<< Добавлено: Обработчик для удаления сцены >>>
func (h *GameplayHandler) deleteSceneInternal(c *gin.Context) {
	log := h.logger.With(zap.String("handler", "deleteSceneInternal"))

	// Используем новую вспомогательную функцию
	sceneID, ok := parseUUIDParam(c, "scene_id", h.logger)
	if !ok {
		return // Ошибка уже обработана
	}
	log = log.With(zap.String("sceneID", sceneID.String()))

	log.Info("Handling internal scene delete request")

	// Вызываем метод сервиса (предполагаем, что он существует)
	err := h.service.DeleteSceneInternal(c.Request.Context(), sceneID)
	if err != nil {
		log.Error("Error deleting scene internally", zap.Error(err))
		handleServiceError(c, err, h.logger) // Обрабатываем стандартные ошибки (NotFound и др.)
		return
	}

	log.Info("Internal scene delete successful")
	c.Status(http.StatusNoContent) // Успешное удаление
}

// <<< Добавлено: Обработчик для получения списка состояний игроков >>>
func (h *GameplayHandler) listStoryPlayersInternal(c *gin.Context) {
	log := h.logger.With(zap.String("handler", "listStoryPlayersInternal"))

	// Используем новую вспомогательную функцию
	storyID, ok := parseUUIDParam(c, "story_id", h.logger)
	if !ok {
		return // Ошибка уже обработана
	}
	log = log.With(zap.String("storyID", storyID.String()))

	log.Info("Handling internal list story players request")

	// Вызываем метод сервиса (предполагаем, что он существует)
	playerStates, err := h.service.ListStoryPlayersInternal(c.Request.Context(), storyID)
	if err != nil {
		log.Error("Error listing story players internally", zap.Error(err))
		handleServiceError(c, err, h.logger) // Обрабатываем ошибки (NotFound, Internal и т.д.)
		return
	}

	// Если сервис вернул nil вместо пустого среза, инициализируем его
	if playerStates == nil {
		playerStates = make([]sharedModels.PlayerGameState, 0)
	}

	log.Info("Internal list story players successful", zap.Int("count", len(playerStates)))
	c.JSON(http.StatusOK, playerStates)
}
