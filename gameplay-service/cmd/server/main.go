package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"novel-server/gameplay-service/internal/config"
	"novel-server/gameplay-service/internal/handler"
	"novel-server/gameplay-service/internal/messaging"
	"novel-server/gameplay-service/internal/service"
	sharedDatabase "novel-server/shared/database"     // Импорт для PublishedStoryRepository
	sharedInterfaces "novel-server/shared/interfaces" // <<< Добавляем импорт shared/interfaces
	sharedLogger "novel-server/shared/logger"         // <<< Импортируем общий логгер
	sharedMessaging "novel-server/shared/messaging"   // <<< Добавляем импорт shared/messaging
	sharedMiddleware "novel-server/shared/middleware" // <<< Импортируем shared/middleware
	sharedModels "novel-server/shared/models"         // <<< Импорт shared/models
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/labstack/echo/v4"
	echoMiddleware "github.com/labstack/echo/v4/middleware"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap" // Импорт zap

	// <<< Импорт для генерации UUID
	"github.com/google/uuid"
)

func main() {
	_ = godotenv.Load()
	log.Println("Запуск Gameplay Service...")

	// <<< Загружаем конфиг ДО инициализации логгера
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Ошибка загрузки конфигурации: %v", err) // Используем стандартный логгер, т.к. zap еще нет
	}

	// --- Инициализация логгера (Используем shared/logger) ---
	logger, err := sharedLogger.New(sharedLogger.Config{
		Level: cfg.LogLevel, // <<< Берем уровень из конфига
	})
	if err != nil {
		log.Fatalf("Не удалось инициализировать логгер: %v", err)
	}
	defer logger.Sync() // Flush буфера логгера при выходе
	logger.Info("Logger initialized", zap.String("logLevel", cfg.LogLevel))
	// --------------------------

	// Убираем повторную загрузку конфига
	// cfg, err := config.LoadConfig()
	// if err != nil {
	// 	logger.Fatal("Ошибка загрузки конфигурации", zap.Error(err))
	// }

	// Подключение к PostgreSQL
	dbPool, err := setupDatabase(cfg, logger) // Передаем логгер
	if err != nil {
		logger.Fatal("Не удалось подключиться к БД", zap.Error(err))
	}
	defer dbPool.Close()
	logger.Info("Успешное подключение к PostgreSQL")

	// Подключение к RabbitMQ
	rabbitConn, err := connectRabbitMQ(cfg.RabbitMQURL, logger) // Передаем логгер
	if err != nil {
		logger.Fatal("Не удалось подключиться к RabbitMQ", zap.Error(err))
	}
	defer rabbitConn.Close()
	logger.Info("Успешное подключение к RabbitMQ")

	// Создаем отдельный канал для TaskPublisher
	pubTaskChannel, err := rabbitConn.Channel()
	if err != nil {
		logger.Fatal("Не удалось открыть канал RabbitMQ для TaskPublisher", zap.Error(err))
	}
	defer pubTaskChannel.Close()

	// Создаем отдельный канал для ClientUpdatePublisher
	pubClientUpdateChannel, err := rabbitConn.Channel()
	if err != nil {
		logger.Fatal("Не удалось открыть канал RabbitMQ для ClientUpdatePublisher", zap.Error(err))
	}
	defer pubClientUpdateChannel.Close()

	// Инициализация зависимостей
	// Передаем logger, он будет использован внутри через .Named()
	storyConfigRepo := sharedDatabase.NewPgStoryConfigRepository(dbPool, logger)
	publishedRepo := sharedDatabase.NewPgPublishedStoryRepository(dbPool, logger)
	sceneRepo := sharedDatabase.NewPgStorySceneRepository(dbPool, logger)
	playerProgressRepo := sharedDatabase.NewPgPlayerProgressRepository(dbPool, logger)

	taskPublisher, err := messaging.NewRabbitMQTaskPublisher(rabbitConn, cfg.GenerationTaskQueue)
	if err != nil {
		logger.Fatal("Не удалось создать TaskPublisher", zap.Error(err))
	}
	clientUpdatePublisher, err := messaging.NewRabbitMQClientUpdatePublisher(rabbitConn, cfg.ClientUpdatesQueueName)
	if err != nil {
		logger.Fatal("Не удалось создать ClientUpdatePublisher", zap.Error(err))
	}
	gameplayService := service.NewGameplayService(storyConfigRepo, publishedRepo, sceneRepo, playerProgressRepo, taskPublisher, dbPool, logger)
	gameplayHandler := handler.NewGameplayHandler(gameplayService, logger, cfg.JWTSecret, cfg.InterServiceSecret)

	// <<< Перезапуск зависших задач при старте >>>
	go requeueStuckTasks(storyConfigRepo, taskPublisher, logger)

	// Инициализация консьюмера уведомлений
	notificationConsumer, err := messaging.NewNotificationConsumer(
		rabbitConn,
		storyConfigRepo,
		publishedRepo,
		sceneRepo,
		clientUpdatePublisher,
		taskPublisher,
		cfg.InternalUpdatesQueueName,
	)
	if err != nil {
		logger.Fatal("Не удалось создать консьюмер уведомлений", zap.Error(err))
	}
	// Запускаем консьюмер в отдельной горутине
	go func() {
		logger.Info("Запуск горутины консьюмера уведомлений...")
		if err := notificationConsumer.StartConsuming(); err != nil {
			logger.Error("Консьюмер уведомлений завершился с ошибкой", zap.Error(err))
		}
		logger.Info("Горутина консьюмера уведомлений завершена.")
	}()

	// Настройка Echo
	e := echo.New()
	// <<< Используем общий логгер запросов из shared/middleware
	e.Use(sharedMiddleware.EchoZapLogger(logger))
	e.Use(echoMiddleware.Recover())
	e.Use(echoMiddleware.CORSWithConfig(echoMiddleware.CORSConfig{ // TODO: Настроить CORS
		AllowOrigins: []string{"*"},
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions},
		AllowHeaders: []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAccept, echo.HeaderAuthorization},
	}))

	// Регистрация маршрутов
	gameplayHandler.RegisterRoutes(e)

	// --- Регистрация healthcheck эндпоинта ---
	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	log.Printf("Gameplay сервер слушает на порту %s", cfg.Port)

	// Запуск HTTP сервера
	go func() {
		if err := e.Start(":" + cfg.Port); err != nil && err != http.ErrServerClosed {
			e.Logger.Fatal("Ошибка запуска HTTP сервера: ", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("Получен сигнал завершения, начинаем graceful shutdown...")

	// Останавливаем консьюмер
	notificationConsumer.Stop()

	// Shutdown Echo
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(ctx); err != nil {
		e.Logger.Fatal("Ошибка при graceful shutdown Echo: ", err)
	}

	log.Println("Gameplay Service успешно остановлен")
}

// <<< Новая функция для перезапуска зависших задач >>>
func requeueStuckTasks(repo sharedInterfaces.StoryConfigRepository, publisher messaging.TaskPublisher, logger *zap.Logger) {
	// Небольшая задержка перед проверкой, чтобы дать другим сервисам время запуститься
	time.Sleep(10 * time.Second)
	logger.Info("Проверка зависших задач генерации...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second) // Таймаут на всю операцию
	defer cancel()

	stuckConfigs, err := repo.FindGeneratingConfigs(ctx)
	if err != nil {
		logger.Error("Не удалось получить список зависших задач", zap.Error(err))
		return
	}

	if len(stuckConfigs) == 0 {
		logger.Info("Зависших задач генерации не найдено.")
		return
	}

	logger.Info("Найдено зависших задач", zap.Int("count", len(stuckConfigs)))

	for _, cfg := range stuckConfigs {
		logger.Warn("Перезапуск зависшей задачи",
			zap.String("storyConfigID", cfg.ID.String()),
			zap.Uint64("userID", cfg.UserID),
			zap.String("status", string(cfg.Status)),
		)

		// Определяем тип промпта на основе текущего статуса
		// Пока что обрабатываем только 'generating'
		var promptType sharedMessaging.PromptType
		var userInput string
		var inputData map[string]interface{}

		if cfg.Status == sharedModels.StatusGenerating {
			promptType = sharedMessaging.PromptTypeNarrator
			// UserInput для начальной генерации - это первый элемент из cfg.UserInput
			if len(cfg.UserInput) > 0 {
				// cfg.UserInput имеет тип json.RawMessage, нужно десериализовать в []string
				var userInputs []string
				if err := json.Unmarshal(cfg.UserInput, &userInputs); err == nil && len(userInputs) > 0 {
					userInput = userInputs[0]
				} else if err != nil {
					logger.Error("Не удалось десериализовать UserInput для перезапуска задачи", zap.String("storyConfigID", cfg.ID.String()), zap.Error(err))
					continue // Пропускаем эту задачу
				} else {
					logger.Error("UserInput пуст для перезапуска задачи", zap.String("storyConfigID", cfg.ID.String()))
					continue // Пропускаем эту задачу
				}
			} else {
				logger.Error("Не удалось извлечь UserInput для перезапуска задачи", zap.String("storyConfigID", cfg.ID.String()))
				continue // Пропускаем эту задачу
			}
			// inputData для narrator обычно nil
			inputData = nil
		} else {
			logger.Warn("Обнаружена зависшая задача с необрабатываемым статусом", zap.String("status", string(cfg.Status)), zap.String("storyConfigID", cfg.ID.String()))
			continue // Пропускаем другие статусы (revising, etc.) в этой простой реализации
		}

		// Генерируем новый TaskID
		newTaskID := uuid.New().String()

		payload := sharedMessaging.GenerationTaskPayload{
			TaskID:        newTaskID,
			UserID:        fmt.Sprintf("%d", cfg.UserID), // Преобразуем uint64 в string
			PromptType:    promptType,
			UserInput:     userInput,
			InputData:     inputData,
			StoryConfigID: cfg.ID.String(), // Передаем ID оригинального конфига
		}

		if err := publisher.PublishGenerationTask(ctx, payload); err != nil {
			logger.Error("Не удалось отправить перезапущенную задачу в очередь",
				zap.String("storyConfigID", cfg.ID.String()),
				zap.String("newTaskID", newTaskID),
				zap.Error(err),
			)
		} else {
			logger.Info("Зависшая задача успешно отправлена в очередь на повторную обработку",
				zap.String("storyConfigID", cfg.ID.String()),
				zap.String("newTaskID", newTaskID),
			)
		}
		// Не меняем статус в БД здесь, пусть story-generator обработает и пришлет уведомление
	}
}

// setupDatabase инициализирует и возвращает пул соединений с БД
func setupDatabase(cfg *config.Config, logger *zap.Logger) (*pgxpool.Pool, error) {
	var dbPool *pgxpool.Pool
	var err error
	maxRetries := 50 // Увеличим количество попыток
	retryDelay := 3 * time.Second

	dsn := cfg.GetDSN()
	poolConfig, parseErr := pgxpool.ParseConfig(dsn)
	if parseErr != nil {
		// Если DSN некорректен, нет смысла пытаться подключаться
		return nil, fmt.Errorf("ошибка парсинга DSN: %w", parseErr)
	}
	poolConfig.MaxConns = int32(cfg.DBMaxConns)
	poolConfig.MaxConnIdleTime = cfg.DBIdleTimeout

	for i := 0; i < maxRetries; i++ {
		attempt := i + 1
		logger.Debug("Попытка подключения к PostgreSQL...",
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", maxRetries),
		)

		// Таймаут на одну попытку подключения и пинга
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		dbPool, err = pgxpool.NewWithConfig(ctx, poolConfig)
		if err != nil {
			logger.Warn("Не удалось создать пул соединений",
				zap.Int("attempt", attempt),
				zap.Error(err),
			)
			cancel()
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
			}
			continue // Переходим к следующей попытке
		}

		// Пытаемся пинговать
		if err = dbPool.Ping(ctx); err != nil {
			logger.Warn("Не удалось выполнить ping к PostgreSQL",
				zap.Int("attempt", attempt),
				zap.Error(err),
			)
			dbPool.Close() // Закрываем неудачный пул
			cancel()
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
			}
			continue // Переходим к следующей попытке
		}

		// Если дошли сюда, подключение и пинг успешны
		cancel() // Отменяем таймаут текущей попытки
		logger.Info("Успешное подключение и ping к PostgreSQL", zap.Int("attempt", attempt))
		return dbPool, nil
	}

	// Если цикл завершился без успешного подключения
	logger.Error("Не удалось подключиться к PostgreSQL после всех попыток", zap.Int("attempts", maxRetries))
	return nil, fmt.Errorf("не удалось подключиться к БД после %d попыток: %w", maxRetries, err) // Возвращаем последнюю ошибку
}

// connectRabbitMQ пытается подключиться к RabbitMQ с несколькими попытками
func connectRabbitMQ(url string, logger *zap.Logger) (*amqp.Connection, error) { // Добавляем логгер
	// ... (реализация без изменений, как в других сервисах) ...
	var conn *amqp.Connection
	var err error
	maxRetries := 5
	retryDelay := 5 * time.Second
	for i := 0; i < maxRetries; i++ {
		conn, err = amqp.Dial(url)
		if err == nil {
			return conn, nil
		}
		logger.Warn("Не удалось подключиться к RabbitMQ",
			zap.Int("attempt", i+1),
			zap.Int("max_attempts", maxRetries),
			zap.Duration("retry_delay", retryDelay),
			zap.Error(err),
		)
		time.Sleep(retryDelay)
	}
	return nil, err
}
