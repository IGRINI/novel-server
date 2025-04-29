package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"novel-server/admin-service/internal/client"
	"novel-server/admin-service/internal/config"
	"novel-server/admin-service/internal/handler"
	"novel-server/admin-service/internal/messaging"
	"novel-server/admin-service/internal/service"
	"novel-server/shared/database"
	"novel-server/shared/interfaces"
	sharedInterfaces "novel-server/shared/interfaces"
	sharedLogger "novel-server/shared/logger"
	sharedMessaging "novel-server/shared/messaging"
	middleware "novel-server/shared/middleware"
	"novel-server/shared/models"
	"os"
	"os/signal"
	"syscall"
	"time"

	"html/template"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"

	// Добавляем импорт для Prometheus
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// <<< Начинаем определение кастомного рендерера >>>
// multiTemplateRenderer управляет отдельными экземплярами шаблонов для каждой страницы.
type multiTemplateRenderer struct {
	templates map[string]*template.Template
	logger    *zap.Logger // Добавляем логгер для отладки
}

// NewMultiTemplateRenderer создает новый рендерер.
func NewMultiTemplateRenderer(templatesDir string, funcMap template.FuncMap, logger *zap.Logger) *multiTemplateRenderer {
	r := &multiTemplateRenderer{
		templates: make(map[string]*template.Template),
		logger:    logger.Named("MultiTemplateRenderer"),
	}

	// Сначала загружаем layout отдельно, он будет основой
	layoutPath := fmt.Sprintf("%s/layout.html", templatesDir)
	layoutTmpl, err := template.New("layout.html").Funcs(funcMap).ParseFiles(layoutPath)
	if err != nil {
		logger.Fatal("Не удалось загрузить layout.html", zap.String("path", layoutPath), zap.Error(err))
	}

	// Находим все остальные *.html файлы (кроме layout)
	pageFiles, err := os.ReadDir(templatesDir)
	if err != nil {
		logger.Fatal("Не удалось прочитать директорию шаблонов", zap.String("dir", templatesDir), zap.Error(err))
	}

	for _, file := range pageFiles {
		fileName := file.Name()
		// Пропускаем layout и не-html файлы
		// Используем проверку расширения файла напрямую
		isHTML := strings.HasSuffix(fileName, ".html") || strings.HasSuffix(fileName, ".tmpl") || strings.HasSuffix(fileName, ".gohtml")
		if file.IsDir() || fileName == "layout.html" || !isHTML {
			continue
		}

		// Для каждого файла страницы: клонируем layout и парсим файл страницы в него
		pagePath := fmt.Sprintf("%s/%s", templatesDir, fileName)
		tmplClone, err := layoutTmpl.Clone()
		if err != nil {
			logger.Fatal("Не удалось клонировать layout для страницы", zap.String("page", fileName), zap.Error(err))
		}

		// Парсим файл страницы в склонированный шаблон
		_, err = tmplClone.ParseFiles(pagePath)
		if err != nil {
			logger.Fatal("Не удалось загрузить шаблон страницы", zap.String("page", fileName), zap.String("path", pagePath), zap.Error(err))
		}

		// Сохраняем готовый шаблон под именем файла страницы
		r.templates[fileName] = tmplClone
		r.logger.Debug("Загружен и ассоциирован шаблон", zap.String("name", fileName))
	}

	return r
}

// Instance возвращает render.Render для указанного имени шаблона.
func (r *multiTemplateRenderer) Instance(name string, data interface{}) render.Render {
	tmpl, ok := r.templates[name]
	if !ok {
		// Если шаблон не найден, логируем ошибку и возвращаем ошибку рендеринга
		r.logger.Error("Шаблон не найден в рендерере", zap.String("name", name))
		// Возвращаем пустой рендер или рендер ошибки
		// Здесь можно вернуть, например, рендер текста с ошибкой
		return render.Data{
			ContentType: "text/plain; charset=utf-8",
			Data:        []byte(fmt.Sprintf("Template '%s' not found", name)),
		}
	}
	// Возвращаем HTML рендер, указывая конкретный экземпляр шаблона
	// и имя основного шаблона для выполнения (обычно это layout).
	return render.HTML{
		Template: tmpl,
		Name:     "layout.html", // <<< Мы исполняем layout, который внутри найдет правильный `define` блока
		Data:     data,
	}
}

// <<< Заканчиваем определение кастомного рендерера >>>

// <<< Функция для конвертации в JSON для шаблонов >>>
func toJson(v interface{}) template.JS {
	b, err := json.Marshal(v)
	if err != nil {
		// В случае ошибки возвращаем пустой JS объект или null
		// или можно логировать ошибку
		log.Printf("[ERROR] Failed to marshal value to JSON in template function: %v", err)
		return template.JS("null") // Или "{}" или "'Error marshalling JSON'"
	}
	return template.JS(b)
}

// <<< Конец функции >>>

func main() {
	log.Println("Запуск Admin Service...")

	// --- Предварительная загрузка минимума для логгера ---
	// Загружаем переменные из .env, если есть
	_ = godotenv.Load()
	// Читаем уровень логгирования из ENV, по умолчанию "info"
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	// --- Инициализация логгера (Используем shared/logger) ---
	// Используем предварительно загруженный logLevel
	logger, err := sharedLogger.New(sharedLogger.Config{
		Level:      logLevel,
		Encoding:   "json", // Или читаем из ENV, если нужно
		OutputPath: "",     // stdout по умолчанию
	})
	if err != nil {
		log.Fatalf("Не удалось инициализировать логгер: %v", err)
	}
	defer logger.Sync()
	sugar := logger.Sugar()
	sugar.Info("Логгер инициализирован", zap.String("logLevel", logLevel))

	// --- Загрузка конфигурации ---
	// Теперь передаем созданный логгер
	cfg, err := config.LoadConfig(logger)
	if err != nil {
		sugar.Fatalf("Ошибка загрузки конфигурации: %v", err)
	}
	sugar.Info("Конфигурация загружена")

	// --- Проверка и обновление уровня логгера (если он изменился в конфиге) ---
	if cfg.LogLevel != logLevel {
		sugar.Warnf("Уровень логгирования изменен с %s на %s после загрузки конфига", logLevel, cfg.LogLevel)
		// Если требуется динамическое изменение уровня, можно пересоздать логгер
		// или использовать zap.AtomicLevel, но для простоты пока оставляем так.
	}

	// --- Подключение к PostgreSQL ---
	var dbPool *pgxpool.Pool
	if cfg.PostgresDSN == "" {
		sugar.Warn("Postgres DSN не указан (DATABASE_URL или POSTGRES_DSN). Функционал, требующий БД, будет недоступен.")
	} else {
		dbPool, err = pgxpool.New(context.Background(), cfg.PostgresDSN)
		if err != nil {
			sugar.Fatalf("Не удалось подключиться к PostgreSQL: %v", err)
		}
		defer dbPool.Close()
		sugar.Info("Успешно подключено к PostgreSQL")

		// Проверка соединения
		if err = dbPool.Ping(context.Background()); err != nil {
			sugar.Fatalf("Не удалось проверить соединение с PostgreSQL: %v", err)
		}
		sugar.Info("Проверка соединения с PostgreSQL прошла успешно")
	}

	// --- Подключение к RabbitMQ ---
	rabbitConn, err := connectRabbitMQ(cfg.RabbitMQ.URL, logger) // Передаем созданный логгер
	if err != nil {
		sugar.Fatalf("Не удалось подключиться к RabbitMQ: %v", err)
	}
	defer rabbitConn.Close()
	sugar.Info("Успешно подключено к RabbitMQ")

	// --- Инициализация репозиториев ---
	var promptRepo sharedInterfaces.PromptRepository
	if dbPool != nil {
		promptRepo = database.NewPgPromptRepository(dbPool)
		sugar.Info("PromptRepository инициализирован")
	} else {
		sugar.Warn("PromptRepository не инициализирован из-за отсутствия подключения к БД")
	}

	// <<< Инициализация репозитория динамических конфигов >>>
	var dynamicConfigRepo sharedInterfaces.DynamicConfigRepository
	if dbPool != nil {
		dynamicConfigRepo = database.NewPgDynamicConfigRepository(dbPool, logger)
		sugar.Info("DynamicConfigRepository инициализирован")
	} else {
		sugar.Warn("DynamicConfigRepository не инициализирован из-за отсутствия подключения к БД")
	}

	// --- Инициализация издателей событий ---
	var configUpdatePublisher sharedMessaging.Publisher
	// Используем реальный ConfigUpdatePublisher
	configUpdatePublisher, err = sharedMessaging.NewRabbitMQConfigUpdatePublisher(rabbitConn, logger)
	if err != nil {
		sugar.Fatalf("Не удалось создать ConfigUpdatePublisher: %v", err)
	}
	// TODO: Добавить defer configUpdatePublisher.Close()?
	sugar.Info("ConfigUpdatePublisher инициализирован")

	promptPublisher, err := sharedMessaging.NewRabbitMQPromptPublisher(rabbitConn)
	if err != nil {
		sugar.Fatalf("Не удалось создать PromptEventPublisher: %v", err)
	}
	defer func() {
		if err := promptPublisher.Close(); err != nil {
			sugar.Errorf("Ошибка при закрытии канала PromptEventPublisher: %v", err)
		}
	}()
	sugar.Info("PromptEventPublisher инициализирован")

	// --- Создание Push Notification Publisher ---
	pushPublisher, err := messaging.NewRabbitMQPushPublisher(rabbitConn, cfg.RabbitMQ.PushQueueName, logger)
	if err != nil {
		sugar.Fatalf("Не удалось создать PushNotificationPublisher: %v", err)
	}
	defer func() {
		if err := pushPublisher.Close(); err != nil {
			sugar.Errorf("Ошибка при закрытии канала PushNotificationPublisher: %v", err)
		}
	}()

	// --- Инициализация сервисов ---
	var promptSvc *service.PromptService
	if promptRepo != nil && promptPublisher != nil { // Проверяем и репо, и паблишер
		promptSvc = service.NewPromptService(cfg, promptRepo, promptPublisher)
		sugar.Info("PromptService инициализирован")
	} else {
		sugar.Warn("PromptService не инициализирован из-за отсутствия репозитория или издателя")
	}

	// <<< Инициализация ConfigService >>>
	var configSvc service.ConfigService
	if dynamicConfigRepo != nil && configUpdatePublisher != nil {
		configSvc = service.NewConfigService(dynamicConfigRepo, configUpdatePublisher, logger)
		sugar.Info("ConfigService инициализирован")
	} else {
		sugar.Warn("ConfigService не инициализирован из-за отсутствия репозитория или издателя")
	}

	// <<< НОВОЕ: Инициализация PromptHandler >>>
	var promptHandler *handler.PromptHandler
	if promptSvc != nil {
		promptHandler = handler.NewPromptHandler(*promptSvc, cfg, logger) // Используем *promptSvc т.к. NewPromptHandler ожидает значение
		sugar.Info("PromptHandler инициализирован")
	} else {
		sugar.Warn("PromptHandler не инициализирован из-за отсутствия PromptService")
	}

	// <<< Инициализация ConfigHandler >>>
	var configHandler *handler.ConfigHandler
	if configSvc != nil { // Проверяем configSvc напрямую
		configHandler = handler.NewConfigHandler(configSvc, cfg, logger)
		sugar.Info("ConfigHandler инициализирован")
	} else {
		sugar.Warn("ConfigHandler не инициализирован из-за отсутствия ConfigService")
	}

	// --- Инициализация клиентов сервисов ---
	authClient, err := client.NewAuthServiceClient(cfg.AuthServiceURL, cfg.ClientTimeout, logger, cfg.InterServiceSecret)
	if err != nil {
		sugar.Fatalf("Не удалось создать AuthServiceClient: %v", err)
	}
	storyGenClient, err := client.NewStoryGeneratorClient(cfg.StoryGeneratorURL, cfg.ClientTimeout, logger, authClient)
	if err != nil {
		sugar.Fatalf("Не удалось создать StoryGeneratorClient: %v", err)
	}
	gameplayClient, err := client.NewGameplayServiceClient(cfg.GameplayServiceURL, cfg.ClientTimeout, logger, authClient)
	if err != nil {
		sugar.Fatalf("Не удалось создать GameplayServiceClient: %v", err)
	}

	// <<< ПЕРЕНОС: Инициализируем другие клиенты ДО получения токена >>>
	storyGenClient, err = client.NewStoryGeneratorClient(cfg.StoryGeneratorURL, cfg.ClientTimeout, logger, authClient)
	if err != nil {
		sugar.Fatalf("Не удалось создать StoryGeneratorClient: %v", err)
	}
	gameplayClient, err = client.NewGameplayServiceClient(cfg.GameplayServiceURL, cfg.ClientTimeout, logger, authClient)
	if err != nil {
		sugar.Fatalf("Не удалось создать GameplayServiceClient: %v", err)
	}

	// <<< ПОЛУЧЕНИЕ И УСТАНОВКА МЕЖСЕРВИСНОГО ТОКЕНА >>>
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second) // Таймаут на получение токена
		defer cancel()
		maxRetries := 50
		retryDelay := 2 * time.Second

		for i := 0; i < maxRetries; i++ {
			sugar.Infof("Попытка [%d/%d] получить межсервисный токен...", i+1, maxRetries)
			interServiceToken, tokenErr := authClient.GenerateInterServiceToken(ctx, cfg.ServiceID) // Используем ServiceID из конфига
			if tokenErr == nil {
				// Устанавливаем токен для ВСЕХ клиентов, которые его используют
				authClient.SetInterServiceToken(interServiceToken)
				gameplayClient.SetInterServiceToken(interServiceToken)
				storyGenClient.SetInterServiceToken(interServiceToken) // Добавляем установку для storyGenClient
				sugar.Info("Межсервисный токен успешно получен и установлен для всех клиентов.")

				// Запускаем периодическое обновление токена
				go func() {
					ticker := time.NewTicker(cfg.InterServiceTokenTTL / 2)
					defer ticker.Stop()

					for {
						select {
						case <-ticker.C:
							updateCtx, updateCancel := context.WithTimeout(context.Background(), 10*time.Second)
							newToken, err := authClient.GenerateInterServiceToken(updateCtx, cfg.ServiceID)
							updateCancel()

							if err != nil {
								sugar.Errorf("Не удалось обновить межсервисный токен: %v", err)
								continue
							}

							authClient.SetInterServiceToken(newToken)
							gameplayClient.SetInterServiceToken(newToken)
							storyGenClient.SetInterServiceToken(newToken) // Добавляем обновление для storyGenClient
							sugar.Info("Межсервисный токен успешно обновлен для всех клиентов")
						}
					}
				}()

				return // Выходим из горутины при успехе
			}

			sugar.Errorf("Не удалось получить межсервисный токен (попытка %d): %v", i+1, tokenErr)
			if i < maxRetries-1 {
				sugar.Infof("Повторная попытка через %v...", retryDelay)
				time.Sleep(retryDelay)
				retryDelay *= 2 // Увеличиваем задержку
			} else {
				sugar.Fatalf("Не удалось получить межсервисный токен после %d попыток. Завершение работы.", maxRetries)
			}
		}
	}()
	// <<< КОНЕЦ БЛОКА ПОЛУЧЕНИЯ ТОКЕНА >>>

	// --- Создание обработчика HTTP ---
	adminHandler := handler.NewAdminHandler(
		cfg, // Передаем конфиг
		logger,
		authClient,
		storyGenClient,
		gameplayClient,
		pushPublisher, // Push Publisher здесь
		promptSvc,     // <<< Передаем PromptService
		// <<< НОВОЕ: Передаем PromptHandler >>>
		promptHandler,
	)
	sugar.Info("AdminHandler инициализирован")

	// --- Настройка Gin ---
	sugar.Info("Настройка Gin...")
	gin.SetMode(gin.ReleaseMode) // Или gin.DebugMode в зависимости от cfg.Env
	if cfg.Env == "development" {
		gin.SetMode(gin.DebugMode)
	}
	router := gin.New()

	// Логгер и Recovery Middleware (используем кастомный логгер)
	// Вместо gin.Logger() и gin.Recovery()
	router.Use(middleware.GinZapLogger(logger))
	router.Use(gin.Recovery()) // Пока используем стандартный Recovery

	// CORS Middleware
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:3000"}, // <<< ИЗМЕНИТЬ НА АКТУАЛЬНЫЙ АДРЕС FRONTEND
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"}, // Добавляем Authorization
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))
	sugar.Info("Настроен CORS")

	// --- Настройка рендерера шаблонов ---
	templatesDir := "./web/templates"
	funcMap := template.FuncMap{
		"add":    func(a, b int) int { return a + b },
		"toJson": toJson, // Добавляем функцию toJson
		// <<< Добавляем функцию для форматирования времени >>>
		"formatAsDateTime": func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			return t.Format("2006-01-02 15:04:05") // Пример формата
		},
		"derefString": func(s *string) string {
			if s == nil {
				return ""
			}
			return *s
		},
		"bytesToString": func(b []byte) string {
			return string(b)
		},
		"hasRole": func(userRoles []string, targetRole string) bool {
			for _, r := range userRoles {
				if r == targetRole {
					return true
				}
			}
			return false
		},
		"statusBadge": func(status models.StoryStatus) string {
			switch status {
			case models.StatusDraft:
				return "secondary"
			case models.StatusGenerating:
				return "info"
			case models.StatusError:
				return "danger"
			case models.StatusSetupPending:
				return "warning"
			case models.StatusFirstScenePending:
				return "warning"
			case models.StatusReady:
				return "success"
			default:
				return "light"
			}
		},
		"truncate": func(maxLen int, s string) string {
			if len(s) <= maxLen {
				return s
			}
			// Обрезаем до maxLen символов и добавляем ...
			// Осторожно с рунами!
			runes := []rune(s)
			if len(runes) > maxLen {
				return string(runes[:maxLen]) + "..."
			}
			return s // На случай, если символов меньше, чем байт
		},
	}
	multiRenderer := NewMultiTemplateRenderer(templatesDir, funcMap, logger)
	router.HTMLRender = multiRenderer
	sugar.Info("HTML рендерер настроен", zap.String("templatesDir", templatesDir))

	// Статические файлы
	router.Static("/static", "./web/static")
	// Пытаемся отдать стиль лендинга из shared
	router.StaticFile("/style.css", "../shared/static/style.css")
	sugar.Info("Настроена раздача статических файлов")

	// --- Регистрация маршрутов --- (Передаем router)
	adminHandler.RegisterRoutes(router) // Маршруты админки
	sugar.Info("Маршруты AdminHandler зарегистрированы")

	// Регистрация маршрутов API (если ApiHandler инициализирован)
	var apiHandler *handler.ApiHandler
	if promptSvc != nil {
		apiHandler = handler.NewApiHandler(promptSvc, logger)
		sugar.Info("ApiHandler инициализирован")
		api := router.Group("/api")
		// Проверяем, что authClient реализует TokenVerifier
		var tokenVerifier interfaces.TokenVerifier
		if verifier, ok := (interface{}(authClient)).(interfaces.TokenVerifier); ok {
			tokenVerifier = verifier
			sugar.Info("Auth client implements TokenVerifier for inter-service auth.")
		} else {
			sugar.Fatal("Auth client does not implement TokenVerifier interface")
		}

		// Применяем middleware межсервисной аутентификации
		api.Use(middleware.InterServiceAuthMiddlewareGin(tokenVerifier, logger))
		{
			// <<< Маршруты для Prompts >>>
			prompts := api.Group("/prompts")
			{
				prompts.POST("", apiHandler.UpsertPrompt)             // POST /api/prompts
				prompts.GET("", apiHandler.ListPromptsByKey)          // GET /api/prompts?key=...
				prompts.GET("/:key/:language", apiHandler.GetPrompt)  // GET /api/prompts/{key}/{language}
				prompts.DELETE("/:key", apiHandler.DeletePromptByKey) // DELETE /api/prompts/{key}
			}
			// Здесь можно добавить другие API маршруты, если они есть и не обрабатываются adminHandler
			// Например, если GetPublishedStories и другие - это тоже API:
			// api.GET("/published-stories", apiHandler.GetPublishedStories) // (потребует добавить в ApiHandler)
			// ... и так далее ...
		}
		sugar.Info("Маршруты ApiHandler зарегистрированы")
	} else {
		sugar.Warn("API routes for prompts are not registered because ApiHandler is not initialized.")
	}

	// --- Health Check ---
	healthHandler := func(c *gin.Context) {
		// Проверка RabbitMQ
		if rabbitConn == nil || rabbitConn.IsClosed() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "component": "rabbitmq", "message": "connection closed or nil"})
			return
		}
		// Проверка PostgreSQL (если используется)
		if dbPool != nil {
			if err := dbPool.Ping(context.Background()); err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "component": "database", "message": err.Error()})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
	router.GET("/health", healthHandler)
	router.HEAD("/health", healthHandler)

	// --- Маршрут для метрик Prometheus ---
	// Используем стандартный обработчик promhttp
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))
	sugar.Info("Маршрут /metrics для Prometheus настроен")

	// Группа для защищенных роутов админки
	adminGroup := router.Group("/admin")
	adminGroup.Use(adminHandler.AuthMiddleware) // <<< Передаем сам метод как middleware
	{
		adminGroup.GET("/dashboard", adminHandler.GetDashboardData) // <<< Исправлено на большую букву

		// Роуты для промптов (если PromptHandler инициализирован)
		if promptHandler != nil {
			promptHandler.RegisterPromptRoutes(adminGroup)
		} else {
			sugar.Warn("Маршруты для Prompts не зарегистрированы, т.к. PromptHandler не инициализирован")
		}

		// <<< Роуты для динамических настроек (если ConfigHandler инициализирован) >>>
		if configHandler != nil {
			configHandler.RegisterConfigRoutes(adminGroup)
		} else {
			sugar.Warn("Маршруты для Configs не зарегистрированы, т.к. ConfigHandler не инициализирован")
		}

		// Роуты для статистики и управления
		// adminGroup.GET("/stats", adminHandler.ShowStats) // <<< Закомментировано, т.к. нет обработчика
	}

	// --- Запуск HTTP-сервера ---
	srv := &http.Server{
		Addr:    ":" + cfg.ServerPort,
		Handler: router,
	}
	sugar.Info("Запуск HTTP-сервера", zap.String("port", cfg.ServerPort))

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			sugar.Fatalf("Ошибка запуска HTTP-сервера: %v", err)
		}
	}()

	// --- Ожидание сигнала завершения ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	sugar.Info("Получен сигнал завершения, начинаем graceful shutdown...")

	// --- Graceful Shutdown ---
	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()

	// Закрываем соединение с RabbitMQ (каналы закрываются в defer'ах паблишеров)
	if rabbitConn != nil {
		if err := rabbitConn.Close(); err != nil {
			sugar.Errorf("Ошибка при закрытии соединения RabbitMQ: %v", err)
		}
		sugar.Info("Соединение с RabbitMQ закрыто")
	}

	// Закрываем пул соединений с БД
	if dbPool != nil {
		dbPool.Close()
		sugar.Info("Пул соединений PostgreSQL закрыт")
	}

	// Останавливаем HTTP-сервер
	if err := srv.Shutdown(ctxShutdown); err != nil {
		sugar.Fatal("Ошибка при остановке HTTP-сервера: ", zap.Error(err))
	}

	sugar.Info("HTTP-сервер остановлен. Завершение работы.")
}

// connectRabbitMQ остается без изменений, но теперь получает корректный логгер
func connectRabbitMQ(uri string, logger *zap.Logger) (*amqp.Connection, error) {
	var connection *amqp.Connection
	var err error
	maxRetries := 50
	retryDelay := 5 * time.Second

	for i := 0; i < maxRetries; i++ {
		connection, err = amqp.Dial(uri)
		if err == nil {
			logger.Info("Подключение к RabbitMQ успешно установлено")
			go func() {
				notifyClose := make(chan *amqp.Error)
				connection.NotifyClose(notifyClose)
				closeErr := <-notifyClose
				if closeErr != nil {
					logger.Error("Соединение с RabbitMQ разорвано", zap.Error(closeErr))
				}
			}()
			return connection, nil
		}
		logger.Warn("Не удалось подключиться к RabbitMQ, попытка переподключения...",
			zap.Error(err),
			zap.Int("retry", i+1),
			zap.Duration("delay", retryDelay),
		)
		time.Sleep(retryDelay)
	}
	return nil, fmt.Errorf("не удалось подключиться к RabbitMQ после %d попыток: %w", maxRetries, err)
}

// getEnv остается без изменений
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
