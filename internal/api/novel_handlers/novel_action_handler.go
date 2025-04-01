package novel_handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"novel-server/internal/auth"
	"novel-server/internal/domain"

	"github.com/google/uuid"
)

// HandleNovelAction обрабатывает различные действия с новеллой (перезапуск, просмотр конкретной сцены)
func (h *NovelHandler) HandleNovelAction(w http.ResponseWriter, r *http.Request) {
	// Проверяем метод запроса
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "Only POST method is allowed")
		return
	}

	// Получаем user_id из контекста
	userID, ok := r.Context().Value(auth.UserIDKey).(string)
	if !ok || userID == "" {
		log.Printf("[API] HandleNovelAction - Error: userID not found in context or empty")
		respondWithError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}

	// Декодируем запрос
	var request struct {
		NovelID    uuid.UUID          `json:"novel_id"`              // ID новеллы
		Action     string             `json:"action"`                // Тип действия: "restart", "get_scene", и т.д.
		SceneIndex *int               `json:"scene_index"`           // Индекс сцены (для restart, get_scene)
		UserChoice *domain.UserChoice `json:"user_choice,omitempty"` // Выбор пользователя (опционально)
	}

	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	defer r.Body.Close()

	// Проверяем наличие обязательных полей
	if request.NovelID == uuid.Nil {
		respondWithError(w, http.StatusBadRequest, "novel_id is required")
		return
	}

	if request.Action == "" {
		respondWithError(w, http.StatusBadRequest, "action is required")
		return
	}

	// Обрабатываем действие
	if request.Action == "restart" {
		// Проверяем обязательные параметры
		if request.SceneIndex == nil {
			respondWithError(w, http.StatusBadRequest, "scene_index is required for restart action")
			return
		}

		sceneIndex := *request.SceneIndex
		if sceneIndex < 0 {
			respondWithError(w, http.StatusBadRequest, "Invalid scene_index value")
			return
		}

		// Создаем упрощенный запрос для генерации контента с указанной сценой
		simplifiedContentRequest := domain.SimplifiedNovelContentRequest{
			NovelID:               request.NovelID,
			RestartFromSceneIndex: &sceneIndex,
		}

		// Преобразуем в полный запрос для сервиса
		fullContentRequest := domain.NovelContentRequest{
			NovelID:               simplifiedContentRequest.NovelID,
			UserID:                userID,
			RestartFromSceneIndex: simplifiedContentRequest.RestartFromSceneIndex,
		}

		// Генерируем контент с перезапуском
		fullResponse, err := h.novelContentService.GenerateNovelContent(r.Context(), fullContentRequest)
		if err != nil {
			log.Printf("[API] HandleNovelAction - Error restarting novel: %v", err)
			respondWithError(w, http.StatusInternalServerError, "Failed to restart novel")
			return
		}

		// Преобразуем полный ответ в упрощенный
		simplifiedResponse := createSimplifiedResponse(fullResponse)

		// Отправляем упрощенный ответ
		respondWithJSON(w, http.StatusOK, simplifiedResponse)
	} else if request.Action == "get_scene" {
		// TODO: Имплементация для получения информации о конкретной сцене
		respondWithError(w, http.StatusNotImplemented, "get_scene action not implemented yet")
	} else {
		respondWithError(w, http.StatusBadRequest, "Unknown action")
	}
}
