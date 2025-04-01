package novel_handlers

import (
	"log"
	"net/http"
	"novel-server/internal/domain"
	"strconv"

	"github.com/google/uuid"
)

// ListNovels обрабатывает запрос на получение списка всех новелл с пагинацией
func (h *NovelHandler) ListNovels(w http.ResponseWriter, r *http.Request) {
	// Проверяем метод запроса
	if r.Method != http.MethodGet {
		respondWithError(w, http.StatusMethodNotAllowed, "Only GET method is allowed")
		return
	}

	log.Println("[API] ListNovels: Handling request")

	// Получаем параметры из URL
	query := r.URL.Query()

	// Парсим параметр limit
	limit := 10 // По умолчанию
	if limitStr := query.Get("limit"); limitStr != "" {
		if limitVal, err := strconv.Atoi(limitStr); err == nil && limitVal > 0 {
			limit = limitVal
		}
	}

	// Парсим параметр cursor (ID последней новеллы из предыдущей страницы)
	var cursor *uuid.UUID
	if cursorStr := query.Get("cursor"); cursorStr != "" {
		if cursorID, err := uuid.Parse(cursorStr); err == nil {
			cursor = &cursorID
		} else {
			log.Printf("[API] ListNovels: Invalid cursor format: %s", cursorStr)
		}
	}

	// Формируем запрос
	request := domain.ListNovelsRequest{
		Limit:  limit,
		Cursor: cursor,
	}

	// Получаем список новелл
	response, err := h.novelService.ListNovels(r.Context(), request)
	if err != nil {
		log.Printf("[API] ListNovels: Error listing novels: %v", err)
		respondWithError(w, http.StatusInternalServerError, "Failed to retrieve novels list")
		return
	}

	// Отправляем ответ
	respondWithJSON(w, http.StatusOK, response)
}

// GetNovelDetails обрабатывает запрос на получение детальной информации о новелле
func (h *NovelHandler) GetNovelDetails(w http.ResponseWriter, r *http.Request) {
	// Проверяем метод запроса
	if r.Method != http.MethodGet {
		respondWithError(w, http.StatusMethodNotAllowed, "Only GET method is allowed")
		return
	}

	log.Println("[API] GetNovelDetails: Handling request")

	// Получаем ID новеллы из URL
	novelIDStr := r.URL.Query().Get("novel_id")
	if novelIDStr == "" {
		respondWithError(w, http.StatusBadRequest, "novel_id parameter is required")
		return
	}

	// Парсим UUID
	novelID, err := uuid.Parse(novelIDStr)
	if err != nil {
		log.Printf("[API] GetNovelDetails: Invalid novel_id format: %s", novelIDStr)
		respondWithError(w, http.StatusBadRequest, "Invalid novel_id format")
		return
	}

	// Получаем детальную информацию о новелле
	details, err := h.novelService.GetNovelDetails(r.Context(), novelID)
	if err != nil {
		log.Printf("[API] GetNovelDetails: Error getting novel details: %v", err)

		// Обрабатываем различные ошибки
		if err.Error() == "novel not found" {
			respondWithError(w, http.StatusNotFound, "Novel not found")
			return
		}

		if err.Error() == "novel not setuped" {
			respondWithError(w, http.StatusNotFound, "Novel not setuped")
			return
		}

		respondWithError(w, http.StatusInternalServerError, "Failed to retrieve novel details")
		return
	}

	// Отправляем ответ
	respondWithJSON(w, http.StatusOK, details)
}
