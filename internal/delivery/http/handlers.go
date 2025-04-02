package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"novel-server/internal/delivery/http/middleware"
	"novel-server/internal/model"
	"novel-server/internal/service"

	"github.com/rs/zerolog/log"
)

// Handler представляет HTTP обработчик
type Handler struct {
	novelService *service.NovelService
}

// New создает новый экземпляр обработчика
func New(novelService *service.NovelService) *Handler {
	return &Handler{
		novelService: novelService,
	}
}

// RegisterRoutes регистрирует маршруты API
func (h *Handler) RegisterRoutes(router *mux.Router) {
	// Маршруты для работы с новеллами (относительно /api)
	router.HandleFunc("/novels", h.ListPublicNovels).Methods("GET") // Публичный?
	router.HandleFunc("/novels", h.CreateNovel).Methods("POST")
	router.HandleFunc("/novels/{id}", h.GetNovel).Methods("GET")
	router.HandleFunc("/novels/{id}", h.UpdateNovel).Methods("PUT")
	router.HandleFunc("/novels/{id}", h.DeleteNovel).Methods("DELETE")
	router.HandleFunc("/novels/{id}/scenes", h.GetScenes).Methods("GET")
	router.HandleFunc("/novels/{id}/publish", h.PublishNovel).Methods("POST")
	router.HandleFunc("/novels/{id}/state", h.GetNovelState).Methods("GET")
	router.HandleFunc("/novels/{id}/state", h.SaveNovelState).Methods("POST")
	router.HandleFunc("/novels/{id}/state", h.DeleteNovelState).Methods("DELETE")

	// Маршруты для генерации новелл (относительно /api)
	router.HandleFunc("/generate/draft", h.GenerateNovelDraft).Methods("POST")
	router.HandleFunc("/generate/setup", h.SetupNovel).Methods("POST")
	router.HandleFunc("/generate/content", h.GenerateNovelContent).Methods("POST")

	// Маршрут для проверки статуса задачи (относительно /api)
	router.HandleFunc("/tasks/{id}", h.GetTaskStatus).Methods("GET")

	// Маршрут для модификации драфта (относительно /api)
	router.HandleFunc("/generate/draft/{id}/modify", h.ModifyNovelDraft).Methods("POST")
}

// ListPublicNovels возвращает список публичных новелл
func (h *Handler) ListPublicNovels(w http.ResponseWriter, r *http.Request) {
	// Получаем параметры пагинации из запроса
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 10 // Значение по умолчанию
	}

	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil || offset < 0 {
		offset = 0 // Значение по умолчанию
	}

	// Получаем список новелл
	novels, err := h.novelService.ListPublicNovels(r.Context(), limit, offset)
	if err != nil {
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при получении списка новелл: %v", err))
		return
	}

	// Возвращаем список новелл
	RespondWithJSON(w, http.StatusOK, novels)
}

// CreateNovel создает новую новеллу
func (h *Handler) CreateNovel(w http.ResponseWriter, r *http.Request) {
	var novel model.Novel
	if err := json.NewDecoder(r.Body).Decode(&novel); err != nil {
		RespondWithError(w, http.StatusBadRequest, fmt.Sprintf("неверный формат запроса: %v", err))
		return
	}

	// Получаем userID из контекста с помощью middleware
	userIDStr, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		log.Error().Msg("Не удалось извлечь userID из контекста в CreateNovel")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: не удалось получить ID пользователя")
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Error().Err(err).Str("userIDStr", userIDStr).Msg("Не удалось преобразовать userID из строки в UUID в CreateNovel")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: неверный формат ID пользователя")
		return
	}

	novel.AuthorID = userID // Устанавливаем автора

	// Создаем новеллу
	createdNovel, err := h.novelService.CreateNovel(r.Context(), novel)
	if err != nil {
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при создании новеллы: %v", err))
		return
	}

	RespondWithJSON(w, http.StatusCreated, createdNovel)
}

// GetNovel возвращает новеллу по ID
func (h *Handler) GetNovel(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		RespondWithError(w, http.StatusBadRequest, "неверный формат ID")
		return
	}

	// Получаем новеллу
	novel, err := h.novelService.GetNovelByID(r.Context(), id)
	if err != nil {
		RespondWithError(w, http.StatusNotFound, fmt.Sprintf("новелла не найдена: %v", err))
		return
	}

	RespondWithJSON(w, http.StatusOK, novel)
}

// UpdateNovel обновляет новеллу
func (h *Handler) UpdateNovel(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		RespondWithError(w, http.StatusBadRequest, "неверный формат ID")
		return
	}

	// Получаем userID из контекста
	userIDStr, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: не удалось получить ID пользователя")
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: неверный формат ID пользователя")
		return
	}

	// Разбираем тело запроса
	var novel model.Novel
	if err := json.NewDecoder(r.Body).Decode(&novel); err != nil {
		RespondWithError(w, http.StatusBadRequest, fmt.Sprintf("неверный формат запроса: %v", err))
		return
	}

	// Устанавливаем ID и AuthorID (для проверки в сервисе)
	novel.ID = id
	novel.AuthorID = userID

	// Обновляем новеллу
	updatedNovel, err := h.novelService.UpdateNovel(r.Context(), novel)
	if err != nil {
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при обновлении новеллы: %v", err))
		return
	}

	RespondWithJSON(w, http.StatusOK, updatedNovel)
}

// DeleteNovel удаляет новеллу
func (h *Handler) DeleteNovel(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		RespondWithError(w, http.StatusBadRequest, "неверный формат ID")
		return
	}

	// Удаляем новеллу (сервис должен проверить, что userID == authorID новеллы)
	if err := h.novelService.DeleteNovel(r.Context(), id); err != nil {
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при удалении новеллы: %v", err))
		return
	}

	RespondWithJSON(w, http.StatusOK, map[string]string{"message": "новелла успешно удалена"})
}

// GetScenes возвращает список сцен новеллы
func (h *Handler) GetScenes(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		RespondWithError(w, http.StatusBadRequest, "неверный формат ID")
		return
	}

	// Получаем сцены новеллы
	scenes, err := h.novelService.GetScenesByNovelID(r.Context(), id)
	if err != nil {
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при получении сцен: %v", err))
		return
	}

	RespondWithJSON(w, http.StatusOK, scenes)
}

// GenerateNovelDraft генерирует драфт новеллы через нарратор
func (h *Handler) GenerateNovelDraft(w http.ResponseWriter, r *http.Request) {
	var req model.NarratorPromptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondWithError(w, http.StatusBadRequest, fmt.Sprintf("неверный формат запроса: %v", err))
		return
	}

	// Используем middleware для получения userID
	userIDStr, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		log.Error().Msg("Не удалось извлечь userID из контекста в GenerateNovelDraft")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: не удалось получить ID пользователя")
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Error().Err(err).Str("userIDStr", userIDStr).Msg("Не удалось преобразовать userID из строки в UUID в GenerateNovelDraft")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: неверный формат ID пользователя")
		return
	}

	// Запускаем асинхронную генерацию драфта
	taskID, err := h.novelService.GenerateNovelDraftAsync(r.Context(), userID, req)
	if err != nil {
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при запуске генерации драфта: %v", err))
		return
	}

	// Возвращаем ID задачи
	RespondWithJSON(w, http.StatusAccepted, map[string]string{
		"task_id": taskID.String(),
		"message": "генерация драфта запущена",
	})
}

// SetupNovel создает новеллу из драфта через сетап
func (h *Handler) SetupNovel(w http.ResponseWriter, r *http.Request) {
	var req model.SetupNovelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondWithError(w, http.StatusBadRequest, fmt.Sprintf("неверный формат запроса: %v", err))
		return
	}

	// Проверяем, что DraftID получен
	if req.DraftID == uuid.Nil {
		RespondWithError(w, http.StatusBadRequest, "неверный формат запроса: draft_id обязателен")
		return
	}

	// Используем middleware для получения userID (он же authorID)
	authorIDStr, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		log.Error().Msg("Не удалось извлечь userID из контекста в SetupNovel")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: не удалось получить ID пользователя")
		return
	}
	authorID, err := uuid.Parse(authorIDStr)
	if err != nil {
		log.Error().Err(err).Str("authorIDStr", authorIDStr).Msg("Не удалось преобразовать userID из строки в UUID в SetupNovel")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: неверный формат ID пользователя")
		return
	}

	// Запускаем асинхронную настройку новеллы
	// Передаем только req.DraftID и authorID
	taskID, err := h.novelService.SetupNovelAsync(r.Context(), req.DraftID, authorID)
	if err != nil {
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при запуске настройки новеллы: %v", err))
		return
	}

	// Возвращаем ID задачи
	RespondWithJSON(w, http.StatusAccepted, map[string]string{
		"task_id": taskID.String(),
		"message": "настройка новеллы запущена",
	})
}

// GenerateNovelContent генерирует контент новеллы через создателя
func (h *Handler) GenerateNovelContent(w http.ResponseWriter, r *http.Request) {
	var req model.GenerateNovelContentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondWithError(w, http.StatusBadRequest, fmt.Sprintf("неверный формат запроса: %v", err))
		return
	}

	// Используем middleware для получения userID
	userIDStr, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		log.Error().Msg("Не удалось извлечь userID из контекста в GenerateNovelContent")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: не удалось получить ID пользователя")
		return
	}

	// Проверяем, что novel_id указан
	if req.NovelID == uuid.Nil {
		RespondWithError(w, http.StatusBadRequest, "неверный формат запроса: novel_id обязателен")
		return
	}

	// Проверяем, совпадает ли userID из контекста с userID в теле запроса, если он там есть
	// Это может быть полезно для отладки, но полагаться нужно на userID из токена
	if req.UserID != "" && req.UserID != userIDStr {
		log.Warn().Str("contextUserID", userIDStr).Str("bodyUserID", req.UserID).Msg("UserID в теле запроса не совпадает с userID из контекста (используем из контекста)")
	}
	req.UserID = userIDStr // Всегда используем userID из контекста (токена)

	// Запускаем асинхронную генерацию контента
	taskID, err := h.novelService.GenerateNovelContentAsync(r.Context(), req)
	if err != nil {
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при запуске генерации контента: %v", err))
		return
	}

	// Возвращаем ID задачи
	RespondWithJSON(w, http.StatusAccepted, map[string]string{
		"task_id": taskID.String(),
		"message": "генерация контента запущена",
	})
}

// GetTaskStatus возвращает статус задачи
func (h *Handler) GetTaskStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		RespondWithError(w, http.StatusBadRequest, "неверный формат ID задачи")
		return
	}

	// Получаем статус задачи
	status, err := h.novelService.GetTaskStatus(r.Context(), id)
	if err != nil {
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при получении статуса задачи: %v", err))
		return
	}

	RespondWithJSON(w, http.StatusOK, status)
}

// PublishNovel публикует новеллу, делая ее доступной для других игроков
func (h *Handler) PublishNovel(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		RespondWithError(w, http.StatusBadRequest, "неверный формат ID")
		return
	}

	// Публикуем новеллу (сервис должен проверить, что userID == authorID новеллы)
	if err := h.novelService.PublishNovel(r.Context(), id); err != nil {
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при публикации новеллы: %v", err))
		return
	}

	RespondWithJSON(w, http.StatusOK, map[string]string{"message": "новелла успешно опубликована"})
}

// GetNovelState возвращает состояние новеллы для пользователя
func (h *Handler) GetNovelState(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	novelID, err := uuid.Parse(vars["id"])
	if err != nil {
		RespondWithError(w, http.StatusBadRequest, "неверный формат ID новеллы")
		return
	}

	// Используем middleware для получения userID
	userIDStr, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		log.Error().Msg("Не удалось извлечь userID из контекста в GetNovelState")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: не удалось получить ID пользователя")
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Error().Err(err).Str("userIDStr", userIDStr).Msg("Не удалось преобразовать userID из строки в UUID в GetNovelState")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: неверный формат ID пользователя")
		return
	}

	// Получаем состояние новеллы
	state, err := h.novelService.GetNovelState(r.Context(), userID, novelID)
	if err != nil {
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при получении состояния новеллы: %v", err))
		return
	}

	RespondWithJSON(w, http.StatusOK, state)
}

// SaveNovelState сохраняет состояние новеллы для пользователя
func (h *Handler) SaveNovelState(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	novelID, err := uuid.Parse(vars["id"])
	if err != nil {
		RespondWithError(w, http.StatusBadRequest, "неверный формат ID новеллы")
		return
	}

	// Используем middleware для получения userID
	userIDStr, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		log.Error().Msg("Не удалось извлечь userID из контекста в SaveNovelState")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: не удалось получить ID пользователя")
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Error().Err(err).Str("userIDStr", userIDStr).Msg("Не удалось преобразовать userID из строки в UUID в SaveNovelState")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: неверный формат ID пользователя")
		return
	}

	// Разбираем тело запроса
	var state model.NovelState
	if err := json.NewDecoder(r.Body).Decode(&state); err != nil {
		RespondWithError(w, http.StatusBadRequest, fmt.Sprintf("неверный формат запроса: %v", err))
		return
	}

	// Устанавливаем ID новеллы и пользователя (из контекста!)
	state.NovelID = novelID
	state.UserID = userID // Используем userID из контекста

	// Сохраняем состояние новеллы
	savedState, err := h.novelService.SaveNovelState(r.Context(), state)
	if err != nil {
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при сохранении состояния новеллы: %v", err))
		return
	}

	RespondWithJSON(w, http.StatusOK, savedState)
}

// DeleteNovelState удаляет состояние новеллы (сохранение) для пользователя
func (h *Handler) DeleteNovelState(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	novelID, err := uuid.Parse(vars["id"])
	if err != nil {
		RespondWithError(w, http.StatusBadRequest, "неверный формат ID новеллы")
		return
	}

	// Используем middleware для получения userID
	userIDStr, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		log.Error().Msg("Не удалось извлечь userID из контекста в DeleteNovelState")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: не удалось получить ID пользователя")
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Error().Err(err).Str("userIDStr", userIDStr).Msg("Не удалось преобразовать userID из строки в UUID в DeleteNovelState")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: неверный формат ID пользователя")
		return
	}

	// Удаляем состояние новеллы
	if err := h.novelService.DeleteNovelState(r.Context(), userID, novelID); err != nil {
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при удалении состояния новеллы: %v", err))
		return
	}

	RespondWithJSON(w, http.StatusOK, map[string]string{"message": "состояние новеллы успешно удалено"})
}

// ModifyNovelDraft запускает асинхронную модификацию существующего драфта новеллы
func (h *Handler) ModifyNovelDraft(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	draftID, err := uuid.Parse(vars["id"]) // Получаем ID драфта из URL
	if err != nil {
		RespondWithError(w, http.StatusBadRequest, "неверный формат ID драфта")
		return
	}

	// Парсим тело запроса для получения текста модификации
	var req model.ModifyNovelDraftRequest // Предполагаем, что такая модель существует
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondWithError(w, http.StatusBadRequest, fmt.Sprintf("неверный формат запроса: %v", err))
		return
	}

	// Используем middleware для получения userID
	userIDStr, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		log.Error().Msg("Не удалось извлечь userID из контекста в ModifyNovelDraft")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: не удалось получить ID пользователя")
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Error().Err(err).Str("userIDStr", userIDStr).Msg("Не удалось преобразовать userID из строки в UUID в ModifyNovelDraft")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: неверный формат ID пользователя")
		return
	}

	// Запускаем асинхронную модификацию драфта
	taskID, err := h.novelService.ModifyNovelDraftAsync(r.Context(), draftID, userID, req.ModificationPrompt)
	if err != nil {
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при запуске модификации драфта: %v", err))
		return
	}

	// Возвращаем ID задачи
	RespondWithJSON(w, http.StatusAccepted, map[string]string{
		"task_id": taskID.String(),
		"message": "модификация драфта запущена",
	})
}

// RespondWithError отправляет ошибку в формате JSON
func RespondWithError(w http.ResponseWriter, code int, message string) {
	RespondWithJSON(w, code, map[string]string{"error": message})
}

// RespondWithJSON отправляет ответ в формате JSON
func RespondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	response, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(response)
}
