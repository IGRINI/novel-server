package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"novel-server/admin-service/internal/client"
	"novel-server/admin-service/internal/config"
	"novel-server/admin-service/internal/handler"
	"novel-server/admin-service/internal/messaging"
	sharedLogger "novel-server/shared/logger"
	sharedMiddleware "novel-server/shared/middleware"
	sharedModels "novel-server/shared/models"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"

	"html/template"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"
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

	// --- Подключение к RabbitMQ ---
	rabbitConn, err := connectRabbitMQ(cfg.RabbitMQ.URL, logger) // Передаем созданный логгер
	if err != nil {
		sugar.Fatalf("Не удалось подключиться к RabbitMQ: %v", err)
	}
	defer rabbitConn.Close()
	sugar.Info("Успешно подключено к RabbitMQ")

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

	// --- Инициализация клиентов сервисов ---
	authClient, err := client.NewAuthServiceClient(cfg.AuthServiceURL, cfg.ClientTimeout, logger, cfg.InterServiceSecret)
	if err != nil {
		sugar.Fatalf("Не удалось создать AuthServiceClient: %v", err)
	}

	// <<< ПЕРЕНОС: Инициализируем другие клиенты ДО получения токена >>>
	storyGenClient, err := client.NewStoryGeneratorClient(cfg.StoryGeneratorURL, cfg.ClientTimeout, logger, authClient)
	if err != nil {
		sugar.Fatalf("Не удалось создать StoryGeneratorClient: %v", err)
	}
	gameplayClient, err := client.NewGameplayServiceClient(cfg.GameplayServiceURL, cfg.ClientTimeout, logger, authClient)
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
	h := handler.NewAdminHandler(cfg, logger, authClient, storyGenClient, gameplayClient, pushPublisher)

	// --- Настройка Gin ---
	router := gin.New()
	router.RedirectTrailingSlash = true // Добавляем автоматическое перенаправление для слешей

	// <<< УДАЛЯЕМ НАСТРОЙКУ ДОВЕРЕННЫХ ПРОКСИ >>>
	/*
		ttrustedProxiesEnv := os.Getenv("TRUSTED_PROXIES") // Читаем из ENV
		var trustedProxies []string
		if trustedProxiesEnv != "" {
			trustedProxies = strings.Split(trustedProxiesEnv, ",")
			// Удаляем лишние пробелы вокруг IP/CIDR
			for i := range trustedProxies {
				trustedProxies[i] = strings.TrimSpace(trustedProxies[i])
			}
			sugar.Infof("Настроены доверенные прокси: %v", trustedProxies)
		} else {
			sugar.Warn("Переменная окружения TRUSTED_PROXIES не установлена! Заголовки X-Forwarded-* НЕ будут обработаны.")
			trustedProxies = []string{}
		}
		err = router.SetTrustedProxies(trustedProxies) // Передаем список
		if err != nil {
			sugar.Fatalf("Не удалось настроить доверенные прокси: %v", err)
		}
		// Обработку X-Forwarded-Proto можно оставить включенной,
		// но она не будет иметь эффекта без доверенных прокси.
		// Можно и закомментировать, если для IP она не нужна.
		// router.ForwardedByClientIP = true
	*/

	// <<< Регистрация кастомных функций шаблонов >>>
	// Сохраняем FuncMap для передачи в рендерер
	funcMap := template.FuncMap{
		// Используем реальную функцию HasRole с адаптером для типа
		"hasRole": func(userArg interface{}, targetRole string) bool {
			// Адаптируем тип userArg к ожидаемому типу, например *sharedModels.User
			// Если в шаблон передается другой тип, измените его здесь.
			if u, ok := userArg.(*sharedModels.User); ok {
				// Вызываем реальную функцию проверки роли
				return sharedModels.HasRole(u.Roles, targetRole)
			} else if rolesSlice, ok := userArg.([]string); ok {
				// Обрабатываем случай, если передается слайс строк
				return sharedModels.HasRole(rolesSlice, targetRole)
			}
			// Не смогли извлечь роли, логируем и возвращаем false
			logger.Error("Функция шаблона 'hasRole' получила аргумент пользователя неподдерживаемого типа", zap.String("argType", fmt.Sprintf("%T", userArg)))
			return false
		},
		// Функция для сложения двух целых чисел в шаблоне
		"add": func(a, b int) int {
			return a + b
		},
		// Функция для вычитания двух целых чисел в шаблоне
		"sub": func(a, b int) int {
			return a - b
		},
		// Функция для нахождения максимума из двух целых чисел
		"max": func(a, b int) int {
			if a > b {
				return a
			}
			return b
		},
		// <<< ДОБАВЛЕНО: Кастомные функции форматирования >>>
		"default": func(value interface{}, defaultValue string) interface{} {
			// Проверяем, является ли значение "нулевым" (nil, пустая строка, 0 и т.д.)
			v := reflect.ValueOf(value)
			switch v.Kind() {
			case reflect.String:
				if v.String() == "" {
					return defaultValue
				}
			case reflect.Ptr, reflect.Interface:
				if v.IsNil() {
					return defaultValue
				}
			case reflect.Slice, reflect.Map, reflect.Array:
				if v.Len() == 0 {
					return defaultValue
				}
			case reflect.Invalid: // Для nil интерфейса
				return defaultValue
			}
			// Если значение не нулевое, возвращаем его
			return value
		},
		"derefString": func(s *string) string {
			if s != nil {
				return *s
			}
			return ""
		},
		"formatAsDateTime": func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			// Форматируем дату и время в формате ДД.ММ.ГГГГ ЧЧ:ММ:СС
			return t.Format("02.01.2006 15:04:05")
		},
		"bytesToString": func(b []byte) string {
			return string(b)
		},
		"statusBadge": func(status sharedModels.StoryStatus) string {
			switch status {
			case sharedModels.StatusGenerating:
				return "warning"
			case sharedModels.StatusDraft:
				return "primary"
			case sharedModels.StatusError:
				return "danger"
			case sharedModels.StatusReady:
				return "success"
			case sharedModels.StatusSetupPending:
				return "info"
			default:
				return "secondary"
			}
		},
		// Можно добавить другие функции здесь
	}
	router.SetFuncMap(funcMap) // Устанавливаем FuncMap для Gin, хотя кастомный рендер тоже его получит

	// <<< Загрузка шаблонов через кастомный рендерер >>>
	// Путь к шаблонам внутри контейнера
	templatesDir := "/app/web/templates"
	router.HTMLRender = NewMultiTemplateRenderer(templatesDir, funcMap, logger) // <<< Используем кастомный рендерер

	router.Use(gin.Recovery())
	router.Use(sharedMiddleware.GinZapLogger(logger))

	// Используем АБСОЛЮТНЫЙ путь внутри контейнера
	custom404Path := "/app/web/static/404.html"
	router.Use(handler.CustomErrorMiddleware(logger, custom404Path))

	// CORS Middleware
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization", "HX-Request", "HX-Target", "HX-Current-URL"}
	corsConfig.AllowCredentials = true
	router.Use(cors.New(corsConfig))

	// Health Check Endpoint
	healthHandler := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
	router.GET("/health", healthHandler)
	router.HEAD("/health", healthHandler)

	// Настройка маршрутов
	h.RegisterRoutes(router)

	// --- Запуск HTTP сервера ---
	serverAddr := ":" + cfg.ServerPort
	srv := &http.Server{
		Addr:    serverAddr,
		Handler: router,
	}

	go func() {
		sugar.Infof("Admin сервер запускается на порту %s", cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			sugar.Fatalf("Ошибка запуска HTTP сервера: %v", err)
		}
	}()

	// --- Запуск сервера метрик Prometheus ---
	go func() {
		metricsAddr := ":9094" // Порт для метрик - ИЗМЕНЕН НА 9094
		metricsMux := http.NewServeMux()
		metricsMux.Handle("/metrics", promhttp.Handler()) // Регистрируем стандартный обработчик
		sugar.Infof("Сервер метрик Prometheus запускается на порту %s", metricsAddr)
		if err := http.ListenAndServe(metricsAddr, metricsMux); err != nil {
			sugar.Fatalf("Ошибка запуска сервера метрик: %v", err)
		}
	}()

	// --- Graceful shutdown ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	sugar.Info("Получен сигнал завершения, начинаем остановку сервера...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		sugar.Fatalf("Ошибка при остановке сервера: %v", err)
	}

	sugar.Info("Сервер успешно остановлен")
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
