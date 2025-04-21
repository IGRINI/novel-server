package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"novel-server/notification-service/internal/config"
	"novel-server/notification-service/internal/messaging"
	"novel-server/notification-service/internal/service"
	sharedLogger "novel-server/shared/logger"
	"os"
	"os/signal"
	"syscall"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

func main() {
	// --- Загрузка конфигурации ---
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Ошибка загрузки конфигурации: %v", err)
	}

	// --- Инициализация логгера (Используем shared/logger) ---
	logger, err := sharedLogger.New(sharedLogger.Config{
		Level:    cfg.Log.Level,
		Encoding: "json", // Или cfg.Log.Encoding, если есть
	})
	if err != nil {
		log.Fatalf("Ошибка инициализации логгера: %v", err)
	}
	defer logger.Sync()
	sugar := logger.Sugar()
	sugar.Info("Логгер инициализирован", zap.String("logLevel", cfg.Log.Level))

	// --- Подключение к RabbitMQ ---
	rabbitConn, err := connectRabbitMQ(cfg.RabbitMQ.URI, logger)
	if err != nil {
		sugar.Fatalf("Не удалось подключиться к RabbitMQ: %v", err)
	}
	defer rabbitConn.Close()
	sugar.Info("Успешно подключено к RabbitMQ")

	// --- Инициализация зависимостей ---
	// Используем общий таймаут для HTTP клиента
	httpClient := &http.Client{
		Timeout: 10 * time.Second, // Общий таймаут для запроса
		// ReadTimeout и WriteTimeout настраиваются через http.Transport, если нужно более тонко
	}
	// Передаем interServiceSecret в конструктор
	tokenProvider := service.NewHTTPTokenProvider(httpClient, cfg.TokenService.URL, logger, cfg.InterServiceSecret)

	// Инициализируем отправителей
	var fcmSender service.PlatformSender
	var apnsSender service.PlatformSender
	var errSender error // Для ошибок инициализации

	// Инициализируем FCM Sender
	fcmSender, errSender = service.NewFCMSender(cfg.FCM, logger)
	if errSender != nil {
		sugar.Fatalf("Ошибка инициализации FCM Sender: %v", errSender)
	}
	if fcmSender == nil {
		// Если NewFCMSender вернул nil, nil (т.к. не настроен), используем заглушку
		sugar.Warn("FCM Sender не инициализирован (конфигурация отсутствует?), используется заглушка.")
		fcmSender = service.NewStubFCMSender(logger)
	}

	// Инициализируем APNS Sender
	apnsSender, errSender = service.NewApnsSender(cfg.APNS, logger)
	if errSender != nil {
		sugar.Fatalf("Ошибка инициализации APNS Sender: %v", errSender)
	}
	if apnsSender == nil {
		sugar.Warn("APNS Sender не инициализирован (конфигурация отсутствует?), используется заглушка.")
		apnsSender = service.NewStubApnsSender(logger)
	}

	notificationService := service.NewNotificationService(tokenProvider, logger, fcmSender, apnsSender)

	// --- Инициализация обработчика сообщений и консьюмера ---
	processor := messaging.NewProcessor(logger, notificationService)
	consumer, err := messaging.NewConsumer(rabbitConn, logger, cfg.PushQueueName, cfg.WorkerConcurrency, processor)
	if err != nil {
		sugar.Fatalf("Не удалось создать консьюмера RabbitMQ: %v", err)
	}

	// --- Запуск Health Check сервера ---
	healthSrv := startHealthCheckServer(cfg.HealthCheckPort, logger)

	// --- Запуск консьюмера в отдельной горутине ---
	consumerErrChan := make(chan error, 1) // Канал для ошибки консьюмера
	go func() {
		sugar.Info("Запуск консьюмера RabbitMQ...")
		err := consumer.Start()
		if err != nil {
			sugar.Errorf("Консьюмер RabbitMQ завершился с ошибкой: %v", err)
		}
		consumerErrChan <- err // Отправляем ошибку (или nil) в канал
		sugar.Info("Консьюмер RabbitMQ остановлен.")
	}()

	// --- Ожидание сигнала завершения или ошибки консьюмера ---
	sugar.Info("Сервис уведомлений запущен. Нажмите Ctrl+C для выхода.")
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
		sugar.Info("Получен сигнал завершения, начинаем остановку...")
	case err := <-consumerErrChan:
		if err != nil {
			sugar.Errorf("Консьюмер завершился с ошибкой, инициируем остановку: %v", err)
		} else {
			sugar.Info("Консьюмер завершился без ошибок, инициируем остановку.")
		}
	}

	// --- Graceful shutdown ---
	// Останавливаем Health Check сервер
	sugar.Info("Остановка Health Check сервера...")
	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := healthSrv.Shutdown(ctxShutdown); err != nil {
		sugar.Errorf("Ошибка при остановке Health Check сервера: %v", err)
	}
	sugar.Info("Health Check сервер остановлен.")

	// Останавливаем консьюмер
	sugar.Info("Остановка консьюмера RabbitMQ...")
	consumer.Stop() // Это разблокирует горутину консьюмера и дождется ее завершения

	// Дожидаемся фактического завершения горутины консьюмера (на случай если она еще не завершилась)
	<-consumerErrChan
	sugar.Info("Горутина консьюмера RabbitMQ подтвердила завершение.")

	sugar.Info("Сервис уведомлений успешно остановлен.")
}

// --- Функция запуска Health Check сервера ---
func startHealthCheckServer(port string, logger *zap.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	go func() {
		logger.Info("Запуск Health Check сервера", zap.String("port", port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Ошибка запуска Health Check сервера", zap.Error(err))
		}
	}()

	return srv
}

// connectRabbitMQ пытается подключиться к RabbitMQ с несколькими попытками
func connectRabbitMQ(uri string, logger *zap.Logger) (*amqp.Connection, error) {
	var connection *amqp.Connection
	var err error
	maxRetries := 50
	retryDelay := 5 * time.Second

	for i := 0; i < maxRetries; i++ {
		connection, err = amqp.Dial(uri)
		if err == nil {
			logger.Info("Подключение к RabbitMQ успешно установлено")
			// Добавляем обработчик разрыва соединения
			go func() {
				notifyClose := make(chan *amqp.Error)
				connection.NotifyClose(notifyClose)
				closeErr := <-notifyClose
				if closeErr != nil {
					logger.Error("Соединение с RabbitMQ разорвано", zap.Error(closeErr))
					// TODO: Попытаться переподключиться или завершить приложение
					// В простом случае можно просто завершить работу:
					// log.Fatalf("Соединение с RabbitMQ потеряно: %v", closeErr)
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
