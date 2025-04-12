package main

import (
	"context"
	"log"
	"net/http"
	"novel-server/shared/middleware"
	"novel-server/websocket-service/internal/config"
	"novel-server/websocket-service/internal/handler"
	"novel-server/websocket-service/internal/messaging"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/labstack/echo/v4"
	echoMiddleware "github.com/labstack/echo/v4/middleware"
	amqp "github.com/rabbitmq/amqp091-go"
)

func main() {
	// Загружаем .env файл (если есть) для локальной разработки
	_ = godotenv.Load()

	log.Println("Запуск WebSocket сервиса...")

	// Загружаем конфигурацию
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Ошибка загрузки конфигурации: %v", err)
	}

	// Подключаемся к RabbitMQ
	rabbitConn, err := connectRabbitMQ(cfg.RabbitMQURL)
	if err != nil {
		log.Fatalf("Не удалось подключиться к RabbitMQ: %v", err)
	}
	defer rabbitConn.Close()
	log.Println("Успешное подключение к RabbitMQ")

	// Инициализация менеджера соединений
	connManager := handler.NewConnectionManager()

	// Инициализация и запуск консьюмера RabbitMQ
	mqConsumer, err := messaging.NewConsumer(rabbitConn, connManager, cfg.ClientUpdatesQueueName)
	if err != nil {
		log.Fatalf("Не удалось создать консьюмер RabbitMQ: %v", err)
	}
	go func() {
		if err := mqConsumer.StartConsuming(); err != nil {
			log.Printf("Ошибка при работе консьюмера RabbitMQ: %v", err)
			// Можно добавить логику перезапуска или уведомления
		}
	}()
	log.Println("Консьюмер RabbitMQ запущен")

	// Настройка Echo
	e := echo.New()
	e.Use(echoMiddleware.Logger())
	e.Use(echoMiddleware.Recover())

	// Настройка CORS (пример, настройте по своим требованиям)
	e.Use(echoMiddleware.CORSWithConfig(echoMiddleware.CORSConfig{
		AllowOrigins: []string{"*"}, // TODO: Замените на ваш фронтенд URL
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodOptions},
		AllowHeaders: []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAccept, echo.HeaderAuthorization},
	}))

	// Создаем обработчик WebSocket
	wsHandler := handler.NewWebSocketHandler(connManager)

	// Определяем маршрут для WebSocket
	// Применяем middleware для аутентификации JWT
	// Внутри middleware будет извлечен user_id и сохранен в контекст
	wsGroup := e.Group("/ws")
	wsGroup.Use(middleware.JWTAuthMiddleware(cfg.JWTSecret)) // Используем общий middleware
	wsGroup.GET("", wsHandler.Handle)

	log.Printf("WebSocket сервер слушает на порту %s", cfg.Port)

	// Запуск сервера в горутине
	go func() {
		if err := e.Start(":" + cfg.Port); err != nil && err != http.ErrServerClosed {
			e.Logger.Fatal("Ошибка запуска сервера: ", err)
		}
	}()

	// Ожидание сигнала завершения для graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Получен сигнал завершения, начинаем graceful shutdown...")

	// Graceful shutdown Echo
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(ctx); err != nil {
		e.Logger.Fatal("Ошибка при graceful shutdown Echo: ", err)
	}

	// Останавливаем консьюмер (если необходимо реализовать логику остановки)
	// mqConsumer.Stop()

	log.Println("WebSocket сервис успешно остановлен")
}

// connectRabbitMQ пытается подключиться к RabbitMQ с несколькими попытками
func connectRabbitMQ(url string) (*amqp.Connection, error) {
	var conn *amqp.Connection
	var err error
	maxRetries := 5
	retryDelay := 5 * time.Second

	for i := 0; i < maxRetries; i++ {
		conn, err = amqp.Dial(url)
		if err == nil {
			return conn, nil // Успешное подключение
		}
		log.Printf("Не удалось подключиться к RabbitMQ (попытка %d/%d): %v. Повтор через %v...", i+1, maxRetries, err, retryDelay)
		time.Sleep(retryDelay)
	}
	return nil, err // Не удалось подключиться после всех попыток
}
