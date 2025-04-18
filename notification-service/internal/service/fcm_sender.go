package service

import (
	"context"
	"fmt"
	"novel-server/notification-service/internal/config"
	"novel-server/notification-service/internal/messaging"

	firebase "firebase.google.com/go/v4"
	fcm "firebase.google.com/go/v4/messaging"
	"go.uber.org/zap"
	"google.golang.org/api/option"
)

// --- Заглушка для FCM Sender ---

type stubFCMSender struct {
	logger *zap.Logger
}

func NewStubFCMSender(logger *zap.Logger) PlatformSender {
	return &stubFCMSender{logger: logger.Named("stub_fcm_sender")}
}

func (s *stubFCMSender) Send(ctx context.Context, tokens []string, notification messaging.PushNotification, data map[string]string) error {
	s.logger.Info("ЗАГЛУШКА: Отправка FCM",
		zap.Strings("tokens", tokens),
		zap.String("title", notification.Title),
		zap.String("body", notification.Body),
		zap.Any("data", data),
	)
	// Имитируем успешную отправку
	return nil
}

func (s *stubFCMSender) Platform() string {
	return "android"
}

// --- Реальный FCM Sender ---

type fcmSender struct {
	client *fcm.Client
	logger *zap.Logger
}

// NewFCMSender создает реальный отправитель FCM.
// Требует путь к файлу ключа сервис-аккаунта Firebase в cfg.CredentialsPath.
func NewFCMSender(cfg config.FCMConfig, logger *zap.Logger) (PlatformSender, error) {
	// Проверяем, что путь к ключу указан. ServerKey нам больше не нужен.
	if cfg.CredentialsPath == "" {
		logger.Warn("Путь к файлу ключа Firebase (FCM_CREDENTIALS_PATH) не указан, FCM sender не будет создан.")
		return nil, nil // Возвращаем nil, nil если FCM не настроен
	}

	opts := option.WithCredentialsFile(cfg.CredentialsPath)
	app, err := firebase.NewApp(context.Background(), nil, opts)
	if err != nil {
		return nil, fmt.Errorf("ошибка инициализации Firebase App из файла '%s': %w", cfg.CredentialsPath, err)
	}

	messagingClient, err := app.Messaging(context.Background())
	if err != nil {
		// Очистка app, если клиент не создан?
		// app.Delete(context.Background()) // Нужно ли?
		return nil, fmt.Errorf("ошибка получения FCM Messaging client: %w", err)
	}

	logger.Info("FCM Sender успешно инициализирован", zap.String("credentials_path", cfg.CredentialsPath))
	return &fcmSender{
		client: messagingClient,
		logger: logger.Named("fcm_sender"),
	}, nil
}

func (s *fcmSender) Send(ctx context.Context, tokens []string, notification messaging.PushNotification, data map[string]string) error {
	// Firebase Admin SDK рекомендует отправлять не более 500 токенов за раз
	// В реальном приложении нужно разбивать tokens на батчи
	// TODO: Реализовать батчинг для > 500 токенов
	if len(tokens) > 500 {
		s.logger.Warn("Количество токенов FCM превышает 500, отправка может завершиться ошибкой или занять много времени. Реализуйте батчинг.", zap.Int("token_count", len(tokens)))
	}

	message := &fcm.MulticastMessage{
		Tokens: tokens,
		Notification: &fcm.Notification{
			Title: notification.Title,
			Body:  notification.Body,
		},
		Data: data,
		Android: &fcm.AndroidConfig{ // Можно добавить специфичные для Android настройки
			Priority: "high",
			// Notification: &fcm.AndroidNotification{
			// 	Sound: "default",
			// },
		},
		// APNS: &fcm.APNSConfig{ // Если отправлять через FCM на iOS
		// 	Headers: map[string]string{"apns-priority": "10"},
		// 	Payload: &fcm.APNSPayload{
		// 		Aps: &fcm.Aps{Alert: &fcm.ApsAlert{...}, Sound: "default"},
		// 	},
		// },
	}

	br, err := s.client.SendMulticast(ctx, message)
	if err != nil {
		s.logger.Error("Ошибка вызова SendMulticast FCM", zap.Error(err))
		// Эта ошибка обычно означает проблему с запросом или соединением, а не с токенами
		return fmt.Errorf("ошибка отправки FCM: %w", err)
	}

	s.logger.Info("Результат отправки FCM",
		zap.Int("success_count", br.SuccessCount),
		zap.Int("failure_count", br.FailureCount),
	)

	if br.FailureCount > 0 {
		// Логируем ошибки для конкретных токенов и собираем невалидные
		invalidTokens := make([]string, 0)
		for idx, resp := range br.Responses {
			if !resp.Success {
				token := "unknown"
				if idx < len(tokens) {
					token = tokens[idx]
				}
				// Проверяем стандартные ошибки FCM из пакета fcm
				if fcm.IsInvalidArgument(resp.Error) ||
					// fcm.IsNotFound(resp.Error) || // Not Found часто означает, что токен устарел - ОШИБКА: такой функции нет
					// Вместо IsNotFound / IsUnregistered используем строки из документации или common errors
					// https://firebase.google.com/docs/cloud-messaging/manage-tokens#detect-invalid-token-responses-from-the-fcm-backend
					// https://firebase.google.com/docs/reference/admin/go/firebase.google.com/go/v4/messaging#pkg-variables
					(fcm.IsUnregistered(resp.Error) || fcm.IsSenderIDMismatch(resp.Error)) { // Unregistered или Mismatch
					invalidTokens = append(invalidTokens, token)
					s.logger.Warn("Обнаружен невалидный/незарегистрированный FCM токен",
						zap.String("token", token),
						zap.Error(resp.Error),
					)
				} else {
					// Другая ошибка доставки
					s.logger.Error("Ошибка доставки FCM для токена",
						zap.String("token", token),
						zap.Error(resp.Error),
					)
				}
			}
		}
		// TODO: Отправить событие или вызвать метод для удаления невалидных токенов из базы данных
		if len(invalidTokens) > 0 {
			s.logger.Info("Невалидные токены для удаления", zap.Strings("tokens", invalidTokens))
			// Например: eventBus.Publish("invalid_fcm_tokens", invalidTokens)
		}
		// Возвращаем общую ошибку, если были неудачные отправки
		return fmt.Errorf("ошибка доставки %d из %d FCM сообщений", br.FailureCount, len(tokens))
	}

	return nil
}

func (s *fcmSender) Platform() string {
	return "android"
}
