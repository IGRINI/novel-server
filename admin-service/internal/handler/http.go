package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"novel-server/admin-service/internal/client"
	"novel-server/admin-service/internal/config"
	sharedModels "novel-server/shared/models"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
)

// <<< Структура для параметров генерации >>>
type generationParams struct {
	Temperature float64
	MaxTokens   int
	TopP        float64
}

// <<< Конец >>>

// <<< Определение и регистрация кастомных метрик админки >>>
var (
	userBansTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "admin_user_bans_total",
		Help: "Total number of successful user bans.",
	})
	userUnbansTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "admin_user_unbans_total",
		Help: "Total number of successful user unbans.",
	})
	passwordResetsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "admin_password_resets_total",
		Help: "Total number of successful password resets.",
	})
	userUpdatesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "admin_user_updates_total",
		Help: "Total number of successful user updates.",
	})
)

// <<< Конец определения >>>

// AdminHandler обрабатывает HTTP запросы для admin-service.
type AdminHandler struct {
	logger *zap.Logger
	// Заменяем зависимость от cfg на конкретный клиент
	// cfg           *config.Config
	authClient     client.AuthServiceHttpClient
	storyGenClient client.StoryGeneratorClient
	// Добавьте зависимости на сервисы/репозитории админки, если нужно
}

// NewAdminHandler создает новый AdminHandler.
func NewAdminHandler(cfg *config.Config, logger *zap.Logger, authClient client.AuthServiceHttpClient, storyGenClient client.StoryGeneratorClient) *AdminHandler {
	// Создаем верификатор токенов (ему все еще нужен JWTSecret из cfg)
	// verifier, err := authutils.NewJWTVerifier(cfg.JWTSecret, logger) // <<< Локальный верификатор больше не нужен
	// if err != nil {
	// 	logger.Fatal("Failed to create JWT Verifier", zap.Error(err))
	// }

	return &AdminHandler{
		logger: logger.Named("AdminHandler"),
		// tokenVerifier: verifier, // <<< Убираем локальный верификатор
		authClient:     authClient,     // <<< Клиент уже есть
		storyGenClient: storyGenClient, // <<< Добавлено
	}
}

// RegisterRoutes регистрирует маршруты для admin-service.
func (h *AdminHandler) RegisterRoutes(e *echo.Echo) {
	// Маршруты, не требующие аутентификации (страница входа)
	// Теперь регистрируем их на корневом пути, т.к. Traefik направит /login сюда
	e.GET("/login", h.showLoginPage)
	e.POST("/login", h.handleLogin)

	// Группа для защищенных маршрутов админки
	// Теперь роутер Traefik для /admin/* будет направлять сюда запросы с префиксом /admin
	// А middleware Traefik будет удалять /admin, так что Echo будет видеть /dashboard, /users и т.д.
	// <<< Возвращаем authMiddleware >>>
	adminApiGroup := e.Group("", h.authMiddleware)

	// Защищенные роуты
	adminApiGroup.GET("/dashboard", h.getDashboardData)
	adminApiGroup.GET("/users", h.listUsers)
	adminApiGroup.GET("/logout", h.handleLogout) // Logout тоже должен быть защищен
	// Баны
	adminApiGroup.POST("/users/:user_id/ban", h.handleBanUser)
	adminApiGroup.DELETE("/users/:user_id/ban", h.handleUnbanUser)
	// <<< Редактирование пользователя >>>
	adminApiGroup.GET("/users/:user_id/edit", h.showUserEditPage)
	adminApiGroup.POST("/users/:user_id", h.handleUserUpdate) // Используем POST для обновления через форму
	// <<< Сброс пароля пользователя >>>
	adminApiGroup.POST("/users/:user_id/reset-password", h.handleResetPassword)
	// Добавьте другие роуты админки здесь

	// <<< Добавляем роуты для AI Playground >>>
	adminApiGroup.GET("/ai-playground", h.handleAIPlaygroundPage)
	adminApiGroup.POST("/ai-playground/generate", h.handleAIPlaygroundGenerate)
}

// authMiddleware - это middleware для проверки наличия валидного токена в cookie
// и роли администратора через auth-service.
func (h *AdminHandler) authMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		cookie, err := c.Cookie("admin_session")
		if err != nil {
			if errors.Is(err, http.ErrNoCookie) {
				return echo.ErrNotFound // Возвращаем 404
			}
			h.logger.Error("Error reading auth cookie", zap.Error(err))
			return echo.NewHTTPError(http.StatusInternalServerError, "Internal server error")
		}

		tokenString := cookie.Value

		// <<< Логирование перед вызовом валидации >>>
		h.logger.Debug("Attempting to validate admin token via auth-service")
		startTime := time.Now()

		// <<< Вызываем auth-service для полной валидации >>>
		claims, err := h.authClient.ValidateAdminToken(c.Request().Context(), tokenString)
		// <<< Конец вызова >>>

		duration := time.Since(startTime)
		h.logger.Debug("Finished validating admin token via auth-service", zap.Duration("duration", duration), zap.Error(err)) // Логируем длительность и ошибку

		if err != nil {
			// Проверяем, не истек ли токен
			if errors.Is(err, sharedModels.ErrTokenExpired) { // <<< Предполагаем наличие этой ошибки
				h.logger.Info("Access token expired, attempting refresh")
				// Пытаемся получить Refresh Token из куки
				refreshCookie, refreshErr := c.Cookie("admin_refresh_session")
				if refreshErr != nil {
					// Если куки рефреш токена нет, то пользователь должен залогиниться
					h.logger.Warn("Refresh token cookie not found after access token expired")
					h.clearAuthCookies(c)
					return echo.ErrNotFound // Перенаправляем на логин
				}

				// Вызываем метод клиента для обновления токена
				newTokens, newClaims, refreshCallErr := h.authClient.RefreshAdminToken(c.Request().Context(), refreshCookie.Value)
				if refreshCallErr != nil {
					// Если обновление не удалось (например, рефреш токен тоже истек или невалиден)
					h.logger.Warn("Failed to refresh admin token via auth-service", zap.Error(refreshCallErr))
					h.clearAuthCookies(c)
					return echo.ErrNotFound // Перенаправляем на логин
				}

				// Обновление токенов успешно!
				h.logger.Info("Admin token refreshed successfully")

				// Устанавливаем новые куки
				accessTokenCookie := new(http.Cookie)
				accessTokenCookie.Name = "admin_session"
				accessTokenCookie.Value = newTokens.AccessToken
				accessTokenCookie.Expires = time.Now().Add(15 * time.Minute) // TODO: Использовать реальное время жизни
				accessTokenCookie.Path = "/"
				accessTokenCookie.HttpOnly = true
				accessTokenCookie.Secure = true                   // <<< Раскомментировано: Только HTTPS
				accessTokenCookie.SameSite = http.SameSiteLaxMode // <<< Раскомментировано: Lax для Access Token
				c.SetCookie(accessTokenCookie)

				refreshTokenCookie := new(http.Cookie)
				refreshTokenCookie.Name = "admin_refresh_session"
				refreshTokenCookie.Value = newTokens.RefreshToken
				refreshTokenCookie.Expires = time.Now().Add(7 * 24 * time.Hour) // TODO: Использовать реальное время жизни
				refreshTokenCookie.Path = "/"
				refreshTokenCookie.HttpOnly = true
				refreshTokenCookie.Secure = true                      // <<< Раскомментировано: Только HTTPS
				refreshTokenCookie.SameSite = http.SameSiteStrictMode // <<< Раскомментировано: Strict для Refresh Token
				c.SetCookie(refreshTokenCookie)

				// Теперь у нас есть новые валидные клеймы, используем их
				claims = newClaims
				// И выходим из блока обработки ошибок, чтобы продолжить выполнение middleware

			} else {
				// Если ошибка валидации - не истечение срока, значит токен невалиден по другой причине
				h.logger.Warn("Token validation failed via auth-service (not expired)", zap.Error(err))
				h.clearAuthCookies(c)   // Удаляем невалидные куки
				return echo.ErrNotFound // Возвращаем 404 при любой ошибке валидации
			}
		}

		// Проверяем роль администратора (полученную от auth-service или после рефреша)
		if !sharedModels.HasRole(claims.Roles, sharedModels.RoleAdmin) {
			h.logger.Warn("User without admin role tried to access admin area", zap.Uint64("userID", claims.UserID), zap.Strings("roles", claims.Roles))
			h.clearAuthCookies(c)
			return echo.ErrNotFound // Возвращаем 404
		}

		// Сохраняем claims в контекст для использования в обработчиках
		ctx := context.WithValue(c.Request().Context(), sharedModels.UserContextKey, claims.UserID)
		ctx = context.WithValue(ctx, sharedModels.RolesContextKey, claims.Roles)
		c.SetRequest(c.Request().WithContext(ctx))

		// Передаем управление следующему обработчику
		return next(c)
	}
}

// showLoginPage рендерит HTML-страницу для входа.
func (h *AdminHandler) showLoginPage(c echo.Context) error {
	// <<< Возвращаем проверку куки и вызов ValidateAdminToken >>>

	// Проверяем, есть ли уже валидная сессия
	cookie, err := c.Cookie("admin_session")
	if err == nil && cookie != nil && cookie.Value != "" {
		// Если есть валидная кука, пытаемся верифицировать токен
		h.logger.Debug("Attempting to validate admin token via auth-service for login page check") // Добавим лог
		startTime := time.Now()
		_, verifyErr := h.authClient.ValidateAdminToken(c.Request().Context(), cookie.Value)
		duration := time.Since(startTime)
		h.logger.Debug("Finished validating admin token for login page check", zap.Duration("duration", duration), zap.Error(verifyErr))

		if verifyErr == nil {
			// Токен валиден, редирект на дашборд
			return c.Redirect(http.StatusSeeOther, "/admin/dashboard")
		}
		// Если токен невалиден (например, просрочен), очищаем куку
		h.clearAuthCookies(c)
	}

	// <<< Конец возвращения >>>

	// Если сессии нет или она невалидна, показываем страницу входа
	data := map[string]interface{}{
		"IsLoginPage": true,                  // <<< Добавляем флаг
		"Error":       c.QueryParam("error"), // Опционально, для показа ошибок после редиректа
	}
	return c.Render(http.StatusOK, "login.html", data)
}

// loginData содержит данные для рендера страницы логина (например, при ошибке)
type loginPageData struct {
	Username string // Чтобы сохранить введенное имя пользователя
	Error    string
}

// handleLogin обрабатывает POST запрос с данными формы входа.
func (h *AdminHandler) handleLogin(c echo.Context) error {
	username := c.FormValue("username")
	password := c.FormValue("password")

	h.logger.Info("Login attempt", zap.String("username", username))

	// Вызов auth-service через HTTP-клиент
	tokenDetails, authErr := h.authClient.Login(c.Request().Context(), username, password)

	if authErr != nil {
		h.logger.Warn("Login failed via auth-service", zap.String("username", username), zap.Error(authErr))
		// Определяем, какую ошибку показать пользователю
		userFacingError := "Неверный логин или пароль"
		if errors.Is(authErr, context.DeadlineExceeded) || errors.Is(authErr, context.Canceled) {
			userFacingError = "Ошибка соединения с сервисом аутентификации (таймаут)"
		} else if !errors.Is(authErr, sharedModels.ErrInvalidCredentials) {
			// Если это не ошибка неверных данных, а другая (ошибка сети, парсинга и т.д.),
			// покажем более общую ошибку.
			userFacingError = "Произошла внутренняя ошибка при попытке входа"
		}

		// Рендерим страницу логина снова с сообщением об ошибке
		data := map[string]interface{}{
			"IsLoginPage": true, // <<< Добавляем флаг
			"Username":    username,
			"Error":       userFacingError,
		}
		return c.Render(http.StatusOK, "login.html", data)
	}

	// Начало проверки роли администратора
	h.logger.Info("User authenticated by auth-service, checking admin role...", zap.String("username", username))

	// Проверяем полученный access token, чтобы извлечь роли
	claims, verifyErr := h.authClient.ValidateAdminToken(c.Request().Context(), tokenDetails.AccessToken)
	if verifyErr != nil {
		// Если мы не можем верифицировать токен, который только что получили, это внутренняя ошибка
		h.logger.Error("Failed to verify access token immediately after login", zap.String("username", username), zap.Error(verifyErr))
		data := map[string]interface{}{
			"IsLoginPage": true, // <<< Добавляем флаг
			"Username":    username,
			"Error":       "Неверный логин или пароль",
		}
		return c.Render(http.StatusOK, "login.html", data)
	}

	// Проверяем наличие роли администратора
	if !sharedModels.HasRole(claims.Roles, sharedModels.RoleAdmin) {
		h.logger.Warn("Login rejected: user does not have admin role", zap.String("username", username), zap.Uint64("userID", claims.UserID), zap.Strings("roles", claims.Roles))
		data := map[string]interface{}{
			"IsLoginPage": true, // <<< Добавляем флаг
			"Username":    username,
			"Error":       "Неверный логин или пароль",
		}
		// Возвращаем ошибку на страницу входа
		return c.Render(http.StatusOK, "login.html", data)
	}
	// Конец проверки роли администратора

	// Успешный вход АДМИНИСТРАТОРА: устанавливаем cookie
	// Устанавливаем куку для Access Token (короткое время жизни)
	accessTokenCookie := new(http.Cookie)
	accessTokenCookie.Name = "admin_session"
	accessTokenCookie.Value = tokenDetails.AccessToken
	accessTokenCookie.Expires = time.Now().Add(15 * time.Minute) // TODO: Использовать реальное время жизни из токена/конфига
	accessTokenCookie.Path = "/"
	accessTokenCookie.HttpOnly = true
	// accessTokenCookie.Secure = true                   // <<< Раскомментировано: Только HTTPS
	// accessTokenCookie.SameSite = http.SameSiteLaxMode // <<< Раскомментировано: Lax для Access Token
	c.SetCookie(accessTokenCookie)

	// Устанавливаем куку для Refresh Token (длительное время жизни)
	refreshTokenCookie := new(http.Cookie)
	refreshTokenCookie.Name = "admin_refresh_session" // Другое имя для рефреш токена
	refreshTokenCookie.Value = tokenDetails.RefreshToken
	refreshTokenCookie.Expires = time.Now().Add(7 * 24 * time.Hour) // TODO: Использовать реальное время жизни из токена/конфига
	refreshTokenCookie.Path = "/"                                   // Обычно рефреш токен доступен только на эндпоинте обновления, но здесь упрощаем
	refreshTokenCookie.HttpOnly = true
	// refreshTokenCookie.Secure = true                      // <<< Раскомментировано: Только HTTPS
	// refreshTokenCookie.SameSite = http.SameSiteStrictMode // <<< Раскомментировано: Strict для Refresh Token
	c.SetCookie(refreshTokenCookie)

	h.logger.Info("Admin login successful, setting cookies", zap.String("username", username), zap.Uint64("userID", claims.UserID))

	// Отправляем заголовок для HTMX, чтобы он сделал редирект
	// --- ИЗМЕНЕНИЕ: Редирект на /admin/dashboard ---
	c.Response().Header().Set("HX-Redirect", "/admin/dashboard")
	return c.NoContent(http.StatusOK) // HTMX ожидает 2xx для HX-Redirect
}

// handleLogout обрабатывает выход пользователя (удаляет cookie).
func (h *AdminHandler) handleLogout(c echo.Context) error {
	h.clearAuthCookies(c)
	h.logger.Info("User logged out")
	// Редирект на страницу входа
	// --- ИЗМЕНЕНИЕ: Редирект на /login ---
	return c.Redirect(http.StatusSeeOther, "/login")
}

// clearAuthCookies удаляет обе сессионные куки.
func (h *AdminHandler) clearAuthCookies(c echo.Context) {
	// Удаляем Access Token cookie
	accessCookie := new(http.Cookie)
	accessCookie.Name = "admin_session"
	accessCookie.Value = ""
	accessCookie.Expires = time.Unix(0, 0) // Прошедшее время
	accessCookie.Path = "/"
	accessCookie.HttpOnly = true
	accessCookie.Secure = true                   // <<< Раскомментировано
	accessCookie.SameSite = http.SameSiteLaxMode // <<< Раскомментировано
	c.SetCookie(accessCookie)

	// Удаляем Refresh Token cookie
	refreshCookie := new(http.Cookie)
	refreshCookie.Name = "admin_refresh_session"
	refreshCookie.Value = ""
	refreshCookie.Expires = time.Unix(0, 0) // Прошедшее время
	refreshCookie.Path = "/"
	refreshCookie.HttpOnly = true
	refreshCookie.Secure = true                      // <<< Раскомментировано
	refreshCookie.SameSite = http.SameSiteStrictMode // <<< Раскомментировано
	c.SetCookie(refreshCookie)
}

// getDashboardData - пример обработчика для получения данных дашборда.
func (h *AdminHandler) getDashboardData(c echo.Context) error {
	// Извлекаем ID и роли из контекста (установлены middleware)
	userID, _ := sharedModels.GetUserIDFromContext(c.Request().Context())
	roles, _ := sharedModels.GetRolesFromContext(c.Request().Context())

	h.logger.Info("Admin dashboard requested", zap.Uint64("adminUserID", userID), zap.Strings("roles", roles))

	// --- Получаем данные через клиент auth-service ---
	// <<< Логирование перед вызовом GetUserCount >>>
	h.logger.Debug("Attempting to get user count via auth-service")
	startTime := time.Now()

	userCount, err := h.authClient.GetUserCount(c.Request().Context())

	duration := time.Since(startTime)
	h.logger.Debug("Finished getting user count via auth-service", zap.Duration("duration", duration), zap.Error(err)) // Логируем длительность и ошибку

	if err != nil {
		// Если не удалось получить данные, логируем и показываем дашборд с ошибкой или 0
		h.logger.Error("Failed to get user count from auth-service", zap.Error(err))
		// Можно передать ошибку в шаблон или установить значения по умолчанию
		userCount = -1 // Индикатор ошибки
	}
	// TODO: Добавить получение количества активных историй (потребует еще одного клиента или эндпоинта)
	activeStories := 0 // Placeholder

	data := map[string]interface{}{
		"PageTitle":      "Дашборд",
		"WelcomeMessage": fmt.Sprintf("Добро пожаловать, User %d!", userID),
		"UserRoles":      roles,
		"Stats": map[string]int{
			"totalUsers":    userCount, // Используем полученное значение
			"activeStories": activeStories,
		},
		"UserCountError": err != nil, // Флаг для шаблона, если нужно показать ошибку
		"IsLoggedIn":     true,       // <<< Устанавливаем флаг
	}
	// return c.JSON(http.StatusOK, data) // Раньше возвращали JSON
	// Теперь рендерим HTML шаблон (нужно создать dashboard.html)
	return c.Render(http.StatusOK, "dashboard.html", data) // <<< Нужно создать dashboard.html
}

// listUsers - пример обработчика для получения списка пользователей.
func (h *AdminHandler) listUsers(c echo.Context) error {
	userID, _ := sharedModels.GetUserIDFromContext(c.Request().Context())
	log := h.logger.With(zap.Uint64("adminUserID", userID))
	log.Info("Admin requested user list")

	// Получаем параметры пагинации из query
	limitStr := c.QueryParam("limit")
	afterCursor := c.QueryParam("after")

	// Устанавливаем лимит по умолчанию
	limit := 20 // Или другое значение по умолчанию
	var err error
	if limitStr != "" {
		limit, err = strconv.Atoi(limitStr)
		if err != nil || limit <= 0 {
			log.Warn("Invalid limit parameter, using default", zap.String("limit", limitStr))
			limit = 20
		}
	}

	log = log.With(zap.Int("limit", limit), zap.String("after", afterCursor))
	log.Debug("Fetching user list with pagination")

	// --- Получаем список пользователей через клиент auth-service ---
	users, nextCursor, err := h.authClient.ListUsers(c.Request().Context(), limit, afterCursor)
	if err != nil {
		log.Error("Failed to get user list from auth-service", zap.Error(err))
		users = []sharedModels.User{} // Пустой список при ошибке
		data := map[string]interface{}{
			"PageTitle":  "Управление пользователями",
			"Users":      users,
			"Error":      "Не удалось загрузить список пользователей: " + err.Error(),
			"IsLoggedIn": true,
			"NextCursor": "",    // Нет следующего курсора при ошибке
			"Limit":      limit, // Передаем текущий лимит для ссылок
		}
		return c.Render(http.StatusOK, "users.html", data)
	}

	// Передаем данные в шаблон
	data := map[string]interface{}{
		"PageTitle":  "Управление пользователями",
		"Users":      users,      // Передаем полученных пользователей
		"IsLoggedIn": true,       // <<< Устанавливаем флаг
		"NextCursor": nextCursor, // Передаем курсор для следующей страницы
		"Limit":      limit,      // Передаем текущий лимит для ссылки "Далее"
	}
	return c.Render(http.StatusOK, "users.html", data)
}

// handleBanUser обрабатывает запрос на бан пользователя.
func (h *AdminHandler) handleBanUser(c echo.Context) error {
	userIDStr := c.Param("user_id")
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		// TODO: Вернуть ошибку в формате, понятном HTMX (например, через HX-Retarget)
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid user ID format")
	}

	adminUserID, _ := sharedModels.GetUserIDFromContext(c.Request().Context())
	h.logger.Info("Admin attempting to ban user", zap.Uint64("adminUserID", adminUserID), zap.Uint64("targetUserID", userID))

	err = h.authClient.BanUser(c.Request().Context(), userID)
	if err != nil {
		h.logger.Error("Failed to ban user via auth client", zap.Uint64("targetUserID", userID), zap.Error(err))
		// TODO: Вернуть ошибку в формате, понятном HTMX
		userFacingError := "Не удалось забанить пользователя."
		if errors.Is(err, sharedModels.ErrUserNotFound) {
			userFacingError = "Пользователь не найден."
			return echo.NewHTTPError(http.StatusNotFound, userFacingError)
		}
		return echo.NewHTTPError(http.StatusInternalServerError, userFacingError)
	}

	// <<< Инкремент счетчика банов >>>
	userBansTotal.Inc()

	// --- Перезапрашиваем и рендерим всю таблицу ---
	// Используем лимит по умолчанию и пустой курсор для первой страницы
	users, _, listErr := h.authClient.ListUsers(c.Request().Context(), 20, "") // <<< Исправлен вызов
	if listErr != nil {
		h.logger.Error("Failed to reload user list after ban", zap.Uint64("bannedUserID", userID), zap.Error(listErr))
		c.Response().Header().Set("HX-Refresh", "true")
		return c.NoContent(http.StatusOK)
	}
	// Рендерим только содержимое tbody таблицы (нужно будет создать частичный шаблон)
	// Пока для простоты рендерим всю страницу users.html (это не HTMX-way)
	// return c.Render(http.StatusOK, "users.html", data)

	// --- ИЛИ: Возвращаем только обновленную строку (HTMX-way, требует рефакторинга) ---
	// Найдем пользователя в списке
	var updatedUser *sharedModels.User
	for i := range users {
		if users[i].ID == userID {
			updatedUser = &users[i]
			break
		}
	}
	if updatedUser == nil {
		// Не нашли пользователя после бана? Странно. Обновим всю страницу.
		c.Response().Header().Set("HX-Refresh", "true")
		return c.NoContent(http.StatusOK)
	}
	// Рендерим частичный шаблон user_row.html (нужно создать)
	// return c.Render(http.StatusOK, "user_row.html", updatedUser)

	// Пока оставим перезагрузку всей страницы как самый простой вариант
	c.Response().Header().Set("HX-Refresh", "true")
	return c.NoContent(http.StatusOK)
}

// handleUnbanUser обрабатывает запрос на разбан пользователя.
func (h *AdminHandler) handleUnbanUser(c echo.Context) error {
	userIDStr := c.Param("user_id")
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		// TODO: Вернуть ошибку HTMX
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid user ID format")
	}

	adminUserID, _ := sharedModels.GetUserIDFromContext(c.Request().Context())
	h.logger.Info("Admin attempting to unban user", zap.Uint64("adminUserID", adminUserID), zap.Uint64("targetUserID", userID))

	err = h.authClient.UnbanUser(c.Request().Context(), userID)
	if err != nil {
		h.logger.Error("Failed to unban user via auth client", zap.Uint64("targetUserID", userID), zap.Error(err))
		// TODO: Вернуть ошибку HTMX
		userFacingError := "Не удалось разбанить пользователя."
		if errors.Is(err, sharedModels.ErrUserNotFound) {
			userFacingError = "Пользователь не найден."
			return echo.NewHTTPError(http.StatusNotFound, userFacingError)
		}
		return echo.NewHTTPError(http.StatusInternalServerError, userFacingError)
	}

	// <<< Инкремент счетчика разбанов >>>
	userUnbansTotal.Inc()

	// При успехе, также перезагружаем страницу (простой вариант)
	c.Response().Header().Set("HX-Refresh", "true")
	return c.NoContent(http.StatusOK)

	// --- ИЛИ: Возвращаем только обновленную строку (HTMX-way) ---
	// (код аналогичен handleBanUser)
}

// --- Новые обработчики для редактирования ---

// showUserEditPage отображает страницу редактирования пользователя.
func (h *AdminHandler) showUserEditPage(c echo.Context) error {
	userIDStr := c.Param("user_id")
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid user ID format")
	}

	h.logger.Info("Showing edit page for user", zap.Uint64("userID", userID))

	// <<< ЗАГЛУШКА: Нужен метод GetUserDetails в клиенте >>>
	// user, err := h.authClient.GetUserDetails(c.Request().Context(), userID)
	// <<< ВРЕМЕННЫЙ КОД >>>
	// Получим весь список и найдем нужного юзера (неэффективно, но пока нет GetUserDetails)
	// Используем лимит по умолчанию и пустой курсор для первой страницы
	users, _, err := h.authClient.ListUsers(c.Request().Context(), 20, "") // <<< Исправлен вызов
	if err != nil {
		h.logger.Error("Failed to get user list for editing", zap.Uint64("userID", userID), zap.Error(err))
		// Можно редиректнуть на список с ошибкой
		return c.Redirect(http.StatusSeeOther, "/admin/users?error=fetch_failed")
	}
	var user *sharedModels.User
	for i := range users {
		if users[i].ID == userID {
			user = &users[i]
			break
		}
	}
	// <<< КОНЕЦ ВРЕМЕННОГО КОДА >>>

	if err != nil {
		if errors.Is(err, sharedModels.ErrUserNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "Пользователь не найден")
		}
		h.logger.Error("Failed to get user details for edit page", zap.Uint64("userID", userID), zap.Error(err))
		return echo.NewHTTPError(http.StatusInternalServerError, "Не удалось загрузить данные пользователя")
	}
	if user == nil { // Доп. проверка для временного кода
		return echo.NewHTTPError(http.StatusNotFound, "Пользователь не найден")
	}

	// Собираем данные для шаблона
	data := map[string]interface{}{
		"PageTitle": "Редактирование пользователя",
		"User":      user,
		// "RolesString": strings.Join(user.Roles, " "), // Больше не нужно
		"AllRoles":         sharedModels.AllRoles(), // Передаем все возможные роли
		"CurrentUserRoles": user.Roles,              // Передаем текущие роли для отметки в select
		"IsLoggedIn":       true,                    // Для layout.html
	}

	return c.Render(http.StatusOK, "user_edit.html", data)
}

// handleUserUpdate обрабатывает сохранение изменений пользователя.
type userUpdateFormData struct {
	Email    string   `form:"email"`
	Roles    []string `form:"roles"`     // <-- Теперь слайс для мультиселекта
	IsBanned string   `form:"is_banned"` // Читаем как строку, т.к. checkbox шлет "true" или ничего
}

func (h *AdminHandler) handleUserUpdate(c echo.Context) error {
	userIDStr := c.Param("user_id")
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid user ID format")
	}

	var formData userUpdateFormData
	if err := c.Bind(&formData); err != nil {
		// TODO: Вернуть ошибку на страницу редактирования?
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid form data: "+err.Error())
	}

	adminUserID, _ := sharedModels.GetUserIDFromContext(c.Request().Context())
	h.logger.Info("Admin attempting to update user",
		zap.Uint64("adminUserID", adminUserID),
		zap.Uint64("targetUserID", userID),
		zap.String("email", formData.Email),
		zap.Strings("roles", formData.Roles),
		zap.String("is_banned_form", formData.IsBanned),
	)

	// Преобразуем данные формы для отправки в auth-service
	// Email остается как есть
	// Роли парсим из строки <-- Больше не нужно, уже слайс
	// rolesSlice := strings.Fields(formData.Roles) // Разделяем по пробелам
	// TODO: Валидация ролей?
	var rolesSlice []string // Инициализируем слайс
	if formData.Roles != nil {
		// Простая валидация: проверяем, что все переданные роли существуют в AllRoles
		allDefinedRoles := sharedModels.AllRolesMap() // Нужна функция AllRolesMap()
		validRoles := make([]string, 0, len(formData.Roles))
		for _, role := range formData.Roles {
			if _, ok := allDefinedRoles[role]; ok {
				validRoles = append(validRoles, role)
			} else {
				h.logger.Warn("Received invalid role from form during update", zap.String("invalidRole", role), zap.Uint64("userID", userID))
			}
		}
		rolesSlice = validRoles
	} else {
		// Если roles не передано (например, все сняли), отправляем пустой слайс
		rolesSlice = []string{}
	}

	// Статус бана
	isBanned := formData.IsBanned == "true"

	// <<< Вызываем метод клиента auth-service >>>
	updatePayload := client.UserUpdatePayload{
		Email:    &formData.Email,
		Roles:    rolesSlice, // <-- Передаем полученный слайс
		IsBanned: &isBanned,
	}
	// Если email пустой в форме, не отправляем его (сервис может требовать непустой email)
	if formData.Email == "" {
		updatePayload.Email = nil
	}

	err = h.authClient.UpdateUser(c.Request().Context(), userID, updatePayload)
	// <<< Конец вызова >>>

	// После вызова API, нужно перезагрузить данные пользователя, чтобы показать актуальное состояние
	// <<< ВРЕМЕННЫЙ КОД (получаем список и ищем) >>>
	// Используем лимит по умолчанию и пустой курсор для первой страницы
	users, _, listErr := h.authClient.ListUsers(c.Request().Context(), 20, "") // <<< Исправлен вызов
	if listErr != nil {
		h.logger.Error("Failed to reload user list after update attempt", zap.Uint64("userID", userID), zap.Error(listErr))
		// Если не удалось перезагрузить, показываем старые данные с ошибкой или успехом обновления
	}
	var user *sharedModels.User
	if listErr == nil {
		for i := range users {
			if users[i].ID == userID {
				user = &users[i]
				break
			}
		}
	}
	// Если пользователя не нашли (или была ошибка), используем данные из формы как fallback
	if user == nil {
		h.logger.Warn("User not found in list after update attempt or list failed, using form data as fallback for render", zap.Uint64("userID", userID))
		user = &sharedModels.User{ID: userID, Username: "(unknown)", Email: formData.Email, Roles: rolesSlice, IsBanned: isBanned}
	}
	// <<< КОНЕЦ ВРЕМЕННОГО КОДА >>>

	data := map[string]interface{}{
		"PageTitle":   "Редактирование пользователя",
		"User":        user,                          // Передаем актуальные данные
		"RolesString": strings.Join(user.Roles, " "), // Актуальные роли
		"IsLoggedIn":  true,
	}

	if err != nil {
		h.logger.Error("Failed to update user via auth client", zap.Uint64("targetUserID", userID), zap.Error(err))
		data["Error"] = "Не удалось сохранить изменения. " + err.Error() // Показываем ошибку
		return c.Render(http.StatusOK, "user_edit.html", data)           // Рендерим снова с ошибкой
	}

	// <<< Инкремент счетчика обновлений >>>
	userUpdatesTotal.Inc()

	// Успех
	data["Success"] = "Изменения успешно сохранены!"
	// Можно сделать редирект обратно на список: return c.Redirect(http.StatusSeeOther, "/admin/users")
	// Но пока оставим на странице редактирования с сообщением об успехе
	return c.Render(http.StatusOK, "user_edit.html", data)
}

// handleResetPassword обрабатывает запрос на сброс пароля пользователя.
func (h *AdminHandler) handleResetPassword(c echo.Context) error {
	userIDStr := c.Param("user_id")
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		// TODO: Вернуть ошибку HTMX?
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid user ID format")
	}

	adminUserID, _ := sharedModels.GetUserIDFromContext(c.Request().Context())
	h.logger.Info("Admin attempting to reset password for user",
		zap.Uint64("adminUserID", adminUserID),
		zap.Uint64("targetUserID", userID),
	)

	// Вызываем метод клиента для сброса пароля
	newPassword, err := h.authClient.ResetPassword(c.Request().Context(), userID)
	if err != nil {
		h.logger.Error("Failed to reset password via auth client", zap.Uint64("targetUserID", userID), zap.Error(err))
		// Возвращаем ошибку для HTMX
		// Можно вернуть просто текст ошибки или отрендерить частичный шаблон с ошибкой.
		// Установим заголовок, чтобы HTMX заменил содержимое specific div
		errorMessage := "Не удалось сбросить пароль."
		if errors.Is(err, sharedModels.ErrUserNotFound) {
			errorMessage = "Пользователь не найден."
		}
		c.Response().Header().Set("HX-Retarget", "#password-reset-status")
		c.Response().Header().Set("HX-Reswap", "innerHTML")
		return c.String(http.StatusInternalServerError, fmt.Sprintf("<article aria-invalid='true'>%s</article>", errorMessage))
	}

	// <<< Инкремент счетчика сброса паролей >>>
	passwordResetsTotal.Inc()

	// Успех: Возвращаем HTML с новым паролем для отображения через HTMX
	h.logger.Info("Password reset successful for user", zap.Uint64("targetUserID", userID))
	c.Response().Header().Set("HX-Retarget", "#password-reset-status")
	c.Response().Header().Set("HX-Reswap", "innerHTML")
	// Важно: Отображаем пароль только один раз!
	// Используем <pre><code> для сохранения форматирования и легкого копирования.
	// Добавляем немного поясняющего текста и кнопку копирования (если хотим JS)
	responseHTML := fmt.Sprintf(
		`<article style="background-color: var(--pico-color-green-150); border-color: var(--pico-color-green-400);">
			Пароль успешно сброшен. Новый временный пароль:
			<pre><code>%s</code></pre>
			<small>Пожалуйста, скопируйте этот пароль и передайте пользователю. После первого входа ему следует сменить пароль.</small>
		</article>`,
		newPassword,
	)
	return c.HTML(http.StatusOK, responseHTML)
}

// <<< Добавляем обработчики для AI Playground >>>

// handleAIPlaygroundPage рендерит страницу AI Playground.
func (h *AdminHandler) handleAIPlaygroundPage(c echo.Context) error {
	data := map[string]interface{}{
		"PageTitle":  "AI Playground",
		"IsLoggedIn": true, // Предполагаем, что middleware отработал
	}
	return c.Render(http.StatusOK, "ai_playground.html", data)
}

// handleAIPlaygroundGenerate обрабатывает запрос на генерацию текста и стримит ответ.
func (h *AdminHandler) handleAIPlaygroundGenerate(c echo.Context) error {
	systemPrompt := c.FormValue("system_prompt")
	userPrompt := c.FormValue("user_prompt")

	// <<< Читаем и парсим параметры генерации из query >>>
	tempStr := c.QueryParam("temperature")
	maxTokensStr := c.QueryParam("max_tokens")
	topPStr := c.QueryParam("top_p")

	params := generationParams{
		Temperature: 0.7, // Значения по умолчанию
		MaxTokens:   512,
		TopP:        1.0,
	}
	var parseErrors []string

	if tempStr != "" {
		if t, err := strconv.ParseFloat(tempStr, 64); err == nil {
			if t >= 0 && t <= 2.0 {
				params.Temperature = t
			} else {
				parseErrors = append(parseErrors, "Temperature must be between 0.0 and 2.0")
			}
		} else {
			parseErrors = append(parseErrors, "Invalid Temperature format")
		}
	}
	if maxTokensStr != "" {
		if mt, err := strconv.Atoi(maxTokensStr); err == nil {
			if mt > 0 {
				params.MaxTokens = mt
			} else {
				parseErrors = append(parseErrors, "Max Tokens must be positive")
			}
		} else {
			parseErrors = append(parseErrors, "Invalid Max Tokens format")
		}
	}
	if topPStr != "" {
		if tp, err := strconv.ParseFloat(topPStr, 64); err == nil {
			if tp >= 0 && tp <= 1.0 {
				params.TopP = tp
			} else {
				parseErrors = append(parseErrors, "Top P must be between 0.0 and 1.0")
			}
		} else {
			parseErrors = append(parseErrors, "Invalid Top P format")
		}
	}

	if len(parseErrors) > 0 {
		// TODO: Вернуть ошибку клиенту в понятном виде?
		errMsg := "Invalid generation parameters: " + strings.Join(parseErrors, ", ")
		h.logger.Warn(errMsg, zap.String("handler", "handleAIPlaygroundGenerate"))
		return echo.NewHTTPError(http.StatusBadRequest, errMsg)
	}
	// <<< Конец парсинга >>>

	log := h.logger.With(
		zap.String("handler", "handleAIPlaygroundGenerate"),
		zap.Int("systemPromptLen", len(systemPrompt)),
		zap.Int("userPromptLen", len(userPrompt)),
		zap.Float64("temperature", params.Temperature),
		zap.Int("max_tokens", params.MaxTokens),
		zap.Float64("top_p", params.TopP),
	)

	log.Info("Received AI generation request (non-streaming)") // <<< Обновлено сообщение

	// <<< Вызываем НОВЫЙ не-стриминговый метод клиента (нужно будет его создать) >>>
	generationResult, err := h.storyGenClient.GenerateText(c.Request().Context(), systemPrompt, userPrompt, client.GenerationParams{
		Temperature: &params.Temperature,
		MaxTokens:   &params.MaxTokens,
		TopP:        &params.TopP,
	})
	if err != nil {
		log.Error("Failed to call story generator text API", zap.Error(err))
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Error calling generator: %v", err))
	}

	// <<< Возвращаем полный текст как простой текст >>>
	return c.String(http.StatusOK, generationResult)

	// <<< Удаляем всю старую логику стриминга >>>
	/*
		defer streamBody.Close() // Важно закрыть тело ответа
		...
		return nil // Успешное завершение стриминга
	*/
}

// CustomHTTPErrorHandler обрабатывает ошибки HTTP и возвращает кастомные страницы/ответы
func CustomHTTPErrorHandler(err error, c echo.Context) {
	code := http.StatusInternalServerError

	if he, ok := err.(*echo.HTTPError); ok {
		code = he.Code
		// Используем внутреннее сообщение ошибки, если оно есть и это не стандартная 404
		if code == http.StatusNotFound {
			// --- ИЗМЕНЕНИЕ: Читаем и возвращаем HTML файл для 404 ---
			filePath := "/app/static/404.html" // Путь внутри контейнера
			content, readErr := os.ReadFile(filePath)
			if readErr != nil {
				// Если файл не найден или не читается, возвращаем простой текст
				c.Logger().Error("Could not read custom 404 page", zap.Error(readErr), zap.String("path", filePath))
				c.String(http.StatusNotFound, "404 Not Found") // Простой текст как fallback
			} else {
				// Отдаем содержимое файла
				c.HTMLBlob(http.StatusNotFound, content)
			}
			return // Важно выйти здесь
		}
	}

	// Логируем только серверные ошибки (5xx)
	if code >= 500 {
		c.Logger().Error(err) // Используем логгер Echo для простоты
	}

	// Для всех остальных ошибок (включая 5xx) возвращаем стандартный JSON или HTML
	// В данном случае, чтобы не усложнять, вернем простой текст
	if !c.Response().Committed {
		if c.Request().Method == http.MethodHead { // Issue #608
			err = c.NoContent(code)
		} else {
			// Вернем простой текст для других ошибок, чтобы не усложнять
			err = c.String(code, http.StatusText(code))
		}
		if err != nil {
			c.Logger().Error(err)
		}
	}
}
