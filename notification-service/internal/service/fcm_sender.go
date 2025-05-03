package service

import (
	"context"
	"fmt"
	"novel-server/notification-service/internal/config"

	// "novel-server/notification-service/internal/messaging" // Больше не нужен
	interfaces "novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models" // <<< Добавлен импорт

	"sync"

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

func (s *stubFCMSender) Send(ctx context.Context, tokens []string, notification sharedModels.PushNotification, data map[string]string) error { // <<< Исправлен тип
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
	client                 *fcm.Client
	logger                 *zap.Logger
	tokenDeletionPublisher interfaces.TokenDeletionPublisher
}

// NewFCMSender создает реальный отправитель FCM.
// Требует путь к файлу ключа сервис-аккаунта Firebase в cfg.CredentialsPath.
// Добавлен аргумент tokenDeletionPublisher.
func NewFCMSender(cfg config.FCMConfig, logger *zap.Logger, tokenDeletionPublisher interfaces.TokenDeletionPublisher) (PlatformSender, error) {
	// Проверяем, что путь к ключу указан. ServerKey нам больше не нужен.
	if cfg.CredentialsPath == "" {
		logger.Warn("Путь к файлу ключа Firebase (FCM_CREDENTIALS_PATH) не указан, FCM sender не будет создан.")
		return nil, nil // Возвращаем nil, nil если FCM не настроен
	}
	// <<< ПРОВЕРКА НА NIL PUBLISHER >>>
	if tokenDeletionPublisher == nil {
		// Возможно, стоит вернуть ошибку или использовать заглушку, если publisher обязателен.
		// Пока просто логируем предупреждение.
		logger.Warn("TokenDeletionPublisher не предоставлен для FCM Sender. Невалидные токены не будут отправляться на удаление.")
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
		client:                 messagingClient,
		logger:                 logger.Named("fcm_sender"),
		tokenDeletionPublisher: tokenDeletionPublisher,
	}, nil
}

func (s *fcmSender) Send(ctx context.Context, tokens []string, notification sharedModels.PushNotification, data map[string]string) error { // <<< Исправлен тип
	log := s.logger
	log.Info("Начало отправки FCM уведомлений (по одному)", zap.Int("count", len(tokens)))

	successCount := 0
	failureCount := 0
	var firstError error
	invalidTokens := make([]string, 0)
	var wg sync.WaitGroup
	var mu sync.Mutex

	// Отправляем каждое сообщение отдельно
	for _, token := range tokens {
		wg.Add(1)
		go func(currentToken string) {
			defer wg.Done()

			msg := &fcm.Message{
				Token: currentToken,
				Data:  data,
				Android: &fcm.AndroidConfig{
					Priority: "high",
				},
			}

			_, err := s.client.Send(ctx, msg)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				failureCount++
				if firstError == nil {
					firstError = err // Сохраняем первую ошибку
				}

				// Логируем и проверяем на невалидный токен
				if fcm.IsUnregistered(err) {
					invalidTokens = append(invalidTokens, currentToken)
					log.Warn("Обнаружен незарегистрированный FCM токен (при Send), будет отправлен на удаление",
						zap.String("token", currentToken),
						zap.Error(err),
					)
				} else if fcm.IsInvalidArgument(err) || fcm.IsSenderIDMismatch(err) {
					invalidTokens = append(invalidTokens, currentToken)
					log.Warn("Обнаружен невалидный FCM токен (аргумент/senderID) (при Send), будет отправлен на удаление",
						zap.String("token", currentToken),
						zap.Error(err),
					)
				} else {
					log.Error("Ошибка отправки FCM для токена (при Send)",
						zap.String("token", currentToken),
						zap.Error(err),
					)
				}
			} else {
				successCount++
				log.Debug("FCM уведомление успешно отправлено (Send)", zap.String("token", currentToken))
			}
		}(token)
	}

	wg.Wait() // Ожидаем завершения всех горутин

	log.Info("Результат отправки FCM (по одному)",
		zap.Int("success_count", successCount),
		zap.Int("failure_count", failureCount),
		zap.Int("total_tokens", len(tokens)),
	)

	if len(invalidTokens) > 0 {
		log.Info("Обнаружены невалидные/незарегистрированные токены для удаления", zap.Strings("tokens", invalidTokens))
		// Отправляем событие или вызываем метод для удаления невалидных токенов из базы данных
		if s.tokenDeletionPublisher != nil {
			for _, token := range invalidTokens {
				// Используем новый контекст, чтобы не зависеть от контекста запроса Send
				// Можно добавить таймаут
				go func(t string) {
					// TODO: Рассмотреть использование отдельного контекста с таймаутом
					err := s.tokenDeletionPublisher.PublishTokenDeletion(context.Background(), t)
					if err != nil {
						log.Error("Не удалось отправить токен на удаление в очередь", zap.String("token", t), zap.Error(err))
					} else {
						log.Info("Токен успешно отправлен в очередь на удаление", zap.String("token", t))
					}
				}(token)
			}
		} else {
			log.Warn("TokenDeletionPublisher не настроен, удаление токенов через очередь не будет выполнено.")
		}
	}

	if firstError != nil {
		// <<< ИЗМЕНЕНО: Не возвращаем ошибку, если все ошибки были только из-за невалидных токенов >>>
		allFailuresAreInvalidTokens := failureCount == len(invalidTokens)
		if !allFailuresAreInvalidTokens {
			// Возвращаем первую ошибку, только если были *другие* неудачные отправки
			return fmt.Errorf("ошибка доставки %d из %d FCM сообщений (первая ошибка: %w)", failureCount-len(invalidTokens), len(tokens), firstError)
		}
		// Если все ошибки были из-за невалидных токенов, считаем операцию условно успешной (остальные доставлены или их не было)
		log.Info("Все ошибки отправки были связаны с невалидными/незарегистрированными токенами, которые отправлены на удаление.")
	}

	return nil // Возвращаем nil, если все ошибки были 'unregistered' или ошибок не было
}

func (s *fcmSender) Platform() string {
	return "android"
}
