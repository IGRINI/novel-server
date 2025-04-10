package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

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
	router.HandleFunc("/novels", h.ListPublicNovels).Methods("GET")
	router.HandleFunc("/novels/{id}", h.GetNovel).Methods("GET")

	router.HandleFunc("/novels/{id}/publish", h.PublishNovel).Methods("POST")
	router.HandleFunc("/novels/{id}/gameover", h.HandleGameOverNotification).Methods("POST")

	// Маршруты для лайков
	router.HandleFunc("/novels/{id}/like", h.LikeNovel).Methods("POST")
	router.HandleFunc("/novels/{id}/unlike", h.UnlikeNovel).Methods("POST")
	router.HandleFunc("/novels/liked", h.GetLikedNovels).Methods("GET")

	// Новый маршрут для получения новелл пользователя
	router.HandleFunc("/my-novels", h.GetUserNovels).Methods("GET")

	// Маршруты для генерации новелл (относительно /api)
	router.HandleFunc("/generate/draft", h.GenerateNovelDraft).Methods("POST")
	router.HandleFunc("/generate/setup", h.SetupNovel).Methods("POST")
	router.HandleFunc("/generate/content", h.GenerateNovelContent).Methods("POST")
	router.HandleFunc("/generate/drafts", h.GetUserDrafts).Methods("GET")
	router.HandleFunc("/generate/drafts/{id}", h.GetDraftDetails).Methods("GET")

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

// GetNovel возвращает новеллу по ID
func (h *Handler) GetNovel(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		RespondWithError(w, http.StatusBadRequest, "неверный формат ID")
		return
	}

	// Получаем ID текущего пользователя, если он аутентифицирован
	var currentUserID *uuid.UUID
	userIDStr, ok := middleware.GetUserIDFromContext(r.Context())
	if ok {
		userID, err := uuid.Parse(userIDStr)
		if err == nil {
			currentUserID = &userID
		}
	}

	// Получаем новеллу с информацией о лайках
	novel, err := h.novelService.GetNovelByIDWithLikes(r.Context(), id, currentUserID)
	if err != nil {
		RespondWithError(w, http.StatusNotFound, fmt.Sprintf("новелла не найдена: %v", err))
		return
	}

	// Проверяем доступ: если новелла не публичная, то её может просматривать только автор
	if !novel.IsPublic {
		// Проверяем авторизован ли пользователь
		if currentUserID == nil {
			RespondWithError(w, http.StatusForbidden, "доступ запрещен: новелла не является публичной")
			return
		}

		// Проверяем, является ли пользователь автором
		if novel.AuthorID != *currentUserID {
			RespondWithError(w, http.StatusForbidden, "доступ запрещен: вы не являетесь автором этой новеллы")
			return
		}
	}

	// Возвращаем новеллу
	RespondWithJSON(w, http.StatusOK, novel)
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

// GenerateNovelContent генерирует следующий батч контента новеллы
func (h *Handler) GenerateNovelContent(w http.ResponseWriter, r *http.Request) {
	var req model.GenerateNovelContentRequest
	// Парсим тело запроса, которое теперь содержит novel_id и client_state
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

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Error().Err(err).Str("userIDStr", userIDStr).Msg("Не удалось преобразовать userID из строки в UUID в GenerateNovelContent")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: неверный формат ID пользователя")
		return
	}
	req.UserID = userID // Присваиваем преобразованный UUID

	// Проверяем, что novel_id указан
	if req.NovelID == uuid.Nil {
		RespondWithError(w, http.StatusBadRequest, "неверный формат запроса: novel_id обязателен")
		return
	}

	// Проверяем, что client_state получен (хотя бы пустой для первого запроса)
	// if req.ClientState == nil { // ClientState не указатель, проверка на nil не нужна
	// 	RespondWithError(w, http.StatusBadRequest, "неверный формат запроса: client_state обязателен")
	// 	return
	// }

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

	// Проверяем, если задача завершена и имеет результат
	if status.Status == "completed" && status.Result != nil {
		// Если это результат setupNovelTask, обрабатываем его
		// Проверяем, что результат имеет поля NovelID и Setup, характерные для setupNovelTask
		if result, ok := status.Result.(map[string]interface{}); ok {
			if _, hasNovelID := result["novel_id"]; hasNovelID {
				if config, hasConfig := result["config"]; hasConfig {
					// Это результат setupNovelTask, нужно фильтровать поля
					configMap, ok := config.(map[string]interface{})
					if ok {
						// Удаляем конфиденциальные поля
						delete(configMap, "ending_preference")
						delete(configMap, "player_preferences")
						delete(configMap, "story_summary")

						// Если есть initial_state, удаляем его конфиденциальные поля
						if initialState, hasInitialState := configMap["initial_state"]; hasInitialState {
							if initialStateMap, ok := initialState.(map[string]interface{}); ok {
								delete(initialStateMap, "future_direction")
								delete(initialStateMap, "story_summary_so_far")
							}
						}

						// Обновляем поле config в результате
						result["config"] = configMap
					}
				}

				// Обновляем результат в статусе
				status.Result = result
			}
		}
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

// GetUserDrafts возвращает список черновиков пользователя
func (h *Handler) GetUserDrafts(w http.ResponseWriter, r *http.Request) {
	// Получаем userID из контекста (установлен middleware)
	userIDStr, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		log.Error().Msg("Не удалось извлечь userID из контекста в GetUserDrafts")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: не удалось получить ID пользователя")
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Error().Err(err).Str("userIDStr", userIDStr).Msg("Неверный формат userID в GetUserDrafts")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: неверный формат ID пользователя")
		return
	}

	// Получаем черновики пользователя
	drafts, err := h.novelService.GetDraftsByUserID(r.Context(), userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID.String()).Msg("Ошибка при получении черновиков пользователя")
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при получении черновиков: %v", err))
		return
	}

	// Отправляем список черновиков клиенту
	RespondWithJSON(w, http.StatusOK, drafts)
}

// GetUserNovels возвращает список новелл пользователя
func (h *Handler) GetUserNovels(w http.ResponseWriter, r *http.Request) {
	// Получаем userID из контекста (установлен middleware)
	userIDStr, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		log.Error().Msg("Не удалось извлечь userID из контекста в GetUserNovels")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: не удалось получить ID пользователя")
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Error().Err(err).Str("userIDStr", userIDStr).Msg("Неверный формат userID в GetUserNovels")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: неверный формат ID пользователя")
		return
	}

	// Получаем новеллы пользователя
	novels, err := h.novelService.GetNovelsByAuthorID(r.Context(), userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID.String()).Msg("Ошибка при получении новелл пользователя")
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при получении новелл: %v", err))
		return
	}

	// Отправляем список новелл клиенту
	RespondWithJSON(w, http.StatusOK, novels)
}

// GetDraftDetails возвращает детальную информацию о черновике
func (h *Handler) GetDraftDetails(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	draftID, err := uuid.Parse(vars["id"]) // Получаем ID драфта из URL
	if err != nil {
		RespondWithError(w, http.StatusBadRequest, "неверный формат ID драфта")
		return
	}

	// Получаем userID из контекста (установлен middleware)
	userIDStr, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		log.Error().Msg("Не удалось извлечь userID из контекста в GetDraftDetails")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: не удалось получить ID пользователя")
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Error().Err(err).Str("userIDStr", userIDStr).Msg("Неверный формат userID в GetDraftDetails")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: неверный формат ID пользователя")
		return
	}

	// Получаем детали черновика из сервиса
	draftView, err := h.novelService.GetDraftViewByID(r.Context(), draftID, userID)
	if err != nil {
		// Обрабатываем специфичную ошибку доступа или не найденного драфта
		if strings.Contains(err.Error(), "доступ запрещен") {
			RespondWithError(w, http.StatusForbidden, err.Error())
		} else if strings.Contains(err.Error(), "не найден") {
			RespondWithError(w, http.StatusNotFound, err.Error())
		} else {
			log.Error().Err(err).Str("draftID", draftID.String()).Str("userID", userID.String()).Msg("Ошибка при получении деталей черновика")
			RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при получении деталей черновика: %v", err))
		}
		return
	}

	// Отправляем детали черновика клиенту
	RespondWithJSON(w, http.StatusOK, draftView)
}

// HandleGameOverNotification обрабатывает уведомление от клиента о Game Over по статам
func (h *Handler) HandleGameOverNotification(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	novelID, err := uuid.Parse(vars["id"])
	if err != nil {
		RespondWithError(w, http.StatusBadRequest, "неверный формат ID новеллы")
		return
	}

	// Получаем userID из контекста
	userIDStr, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		log.Error().Msg("Не удалось извлечь userID из контекста в HandleGameOverNotification")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: не удалось получить ID пользователя")
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Error().Err(err).Str("userIDStr", userIDStr).Msg("Не удалось преобразовать userID из строки в UUID в HandleGameOverNotification")
		RespondWithError(w, http.StatusUnauthorized, "ошибка аутентификации: неверный формат ID пользователя")
		return
	}

	// Парсим тело запроса
	var req model.GameOverNotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondWithError(w, http.StatusBadRequest, fmt.Sprintf("неверный формат запроса: %v", err))
		return
	}

	// Валидация причины (базовая)
	if req.Reason.StatName == "" || req.Reason.Condition == "" {
		RespondWithError(w, http.StatusBadRequest, "неверный формат запроса: не указана причина game over (stat_name, condition)")
		return
	}

	log.Info().Str("novelID", novelID.String()).Str("userID", userID.String()).Interface("reason", req.Reason).Msg("Получено уведомление о Game Over")

	// Вызываем сервис для генерации концовки и возможного продолжения
	gameOverResult, err := h.novelService.HandleGameOver(r.Context(), userID, novelID, req.Reason, req.UserChoices)
	if err != nil {
		// Обрабатываем специфичные ошибки
		log.Error().Err(err).Str("novelID", novelID.String()).Str("userID", userID.String()).Msg("Ошибка при обработке Game Over")
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при обработке Game Over: %v", err))
		return
	}

	// Формируем ответ клиенту на основе полного результата
	responsePayload := model.ClientGameplayPayload{
		EndingText:     &gameOverResult.EndingText,
		IsGameOver:     true,
		CanContinue:    gameOverResult.CanContinue,
		NewCharacter:   gameOverResult.NewCharacter,
		NewCoreStats:   gameOverResult.NewCoreStats,
		InitialChoices: gameOverResult.InitialChoices,
	}

	RespondWithJSON(w, http.StatusOK, responsePayload)
}

// LikeNovel добавляет лайк к новелле
func (h *Handler) LikeNovel(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	novelID, err := uuid.Parse(vars["id"])
	if err != nil {
		RespondWithError(w, http.StatusBadRequest, "неверный формат ID новеллы")
		return
	}

	// Получаем ID пользователя из контекста
	userIDStr, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		RespondWithError(w, http.StatusUnauthorized, "требуется авторизация")
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		RespondWithError(w, http.StatusUnauthorized, "неверный формат ID пользователя")
		return
	}

	// Добавляем лайк
	err = h.novelService.LikeNovel(r.Context(), userID, novelID)
	if err != nil {
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при добавлении лайка: %v", err))
		return
	}

	RespondWithJSON(w, http.StatusOK, map[string]string{"message": "лайк успешно добавлен"})
}

// UnlikeNovel удаляет лайк с новеллы
func (h *Handler) UnlikeNovel(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	novelID, err := uuid.Parse(vars["id"])
	if err != nil {
		RespondWithError(w, http.StatusBadRequest, "неверный формат ID новеллы")
		return
	}

	// Получаем ID пользователя из контекста
	userIDStr, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		RespondWithError(w, http.StatusUnauthorized, "требуется авторизация")
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		RespondWithError(w, http.StatusUnauthorized, "неверный формат ID пользователя")
		return
	}

	// Удаляем лайк
	err = h.novelService.UnlikeNovel(r.Context(), userID, novelID)
	if err != nil {
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при удалении лайка: %v", err))
		return
	}

	RespondWithJSON(w, http.StatusOK, map[string]string{"message": "лайк успешно удален"})
}

// GetLikedNovels возвращает список новелл, лайкнутых пользователем
func (h *Handler) GetLikedNovels(w http.ResponseWriter, r *http.Request) {
	// Получаем ID пользователя из контекста
	userIDStr, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		RespondWithError(w, http.StatusUnauthorized, "требуется авторизация")
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		RespondWithError(w, http.StatusUnauthorized, "неверный формат ID пользователя")
		return
	}

	// Получаем параметры пагинации из запроса
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 10 // Значение по умолчанию
	}

	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil || offset < 0 {
		offset = 0 // Значение по умолчанию
	}

	// Получаем список лайкнутых новелл
	novels, err := h.novelService.GetLikedNovelsByUser(r.Context(), userID, limit, offset)
	if err != nil {
		RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка при получении списка лайкнутых новелл: %v", err))
		return
	}

	// Возвращаем список новелл
	RespondWithJSON(w, http.StatusOK, novels)
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
