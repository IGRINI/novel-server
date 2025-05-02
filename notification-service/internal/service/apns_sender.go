package service

import (
	"context"
	"fmt"
	"novel-server/notification-service/internal/config"
	"novel-server/notification-service/internal/messaging"
	"sync"

	"github.com/sideshow/apns2"
	"github.com/sideshow/apns2/payload"
	"github.com/sideshow/apns2/token"
	"go.uber.org/zap"
)

// --- Заглушка для APNS Sender ---

type stubApnsSender struct {
	logger *zap.Logger
}

func NewStubApnsSender(logger *zap.Logger) PlatformSender {
	return &stubApnsSender{logger: logger.Named("stub_apns_sender")}
}

func (s *stubApnsSender) Send(ctx context.Context, tokens []string, notification messaging.PushNotification, data map[string]string) error {
	s.logger.Info("ЗАГЛУШКА: Отправка APNS",
		zap.Strings("tokens", tokens),
		zap.String("title", notification.Title),
		zap.String("body", notification.Body),
		zap.Any("data", data),
	)
	// Имитируем успешную отправку
	return nil
}

func (s *stubApnsSender) Platform() string {
	return "ios"
}

// --- Реальный APNS Sender ---

type apnsSender struct {
	client *apns2.Client
	logger *zap.Logger
	topic  string
}

// NewApnsSender создает реальный отправитель APNS.
// Требует KeyPath, KeyID, TeamID, Topic в cfg.
func NewApnsSender(cfg config.APNSConfig, logger *zap.Logger) (PlatformSender, error) {
	if cfg.KeyPath == "" || cfg.KeyID == "" || cfg.TeamID == "" || cfg.Topic == "" {
		logger.Warn("APNS конфигурация не полная (KeyPath, KeyID, TeamID, Topic), APNS sender не будет создан.")
		return nil, nil // Возвращаем nil, nil если APNS не настроен
	}

	authKey, err := token.AuthKeyFromFile(cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения ключа APNS из файла %s: %w", cfg.KeyPath, err)
	}

	token := &token.Token{
		AuthKey: authKey,
		KeyID:   cfg.KeyID,
		TeamID:  cfg.TeamID,
	}

	// TODO: Добавить флаг cfg.Production для выбора окружения
	client := apns2.NewTokenClient(token).Development()
	logger.Info("Используется APNS Development окружение (Production пока не настраивается)")

	logger.Info("APNS Sender успешно инициализирован",
		zap.String("key_path", cfg.KeyPath),
		zap.String("key_id", cfg.KeyID),
		zap.String("team_id", cfg.TeamID),
		zap.String("topic", cfg.Topic),
	)
	return &apnsSender{
		client: client,
		logger: logger.Named("apns_sender"),
		topic:  cfg.Topic,
	}, nil
}

func (s *apnsSender) Send(ctx context.Context, tokens []string, notification messaging.PushNotification, data map[string]string) error {
	log := s.logger
	log.Info("Начало отправки APNS уведомлений", zap.Int("count", len(tokens)))

	var wg sync.WaitGroup
	var mu sync.Mutex
	invalidTokens := make([]string, 0)
	failureCount := 0
	var firstError error

	// Определяем payload один раз
	payloadData := payload.NewPayload().
		ContentAvailable().
		Sound("default")

	// Добавляем кастомные данные НЕ в aps, а на верхний уровень payload
	// (согласно рекомендациям Apple и возможностям библиотеки go-apns2)
	for k, v := range data {
		payloadData.Custom(k, v)
	}

	// Запускаем отправку в горутинах (можно ограничить количество)
	// TODO: Добавить ограничение на количество одновременных горутин (семафор)
	for _, deviceToken := range tokens {
		wg.Add(1)
		go func(tokenToSend string) {
			defer wg.Done()

			pushNotification := &apns2.Notification{
				DeviceToken: tokenToSend,
				Topic:       s.topic,
				Payload:     payloadData,
				Priority:    apns2.PriorityHigh, // Высокий приоритет для немедленной доставки
			}

			// Используем переданный контекст
			res, err := s.client.PushWithContext(ctx, pushNotification)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				log.Error("Ошибка вызова APNS PushWithContext",
					zap.String("token", tokenToSend),
					zap.Error(err),
				)
				failureCount++
				if firstError == nil {
					firstError = fmt.Errorf("apns send error: %w", err)
				}
				return // Выходим из горутины при ошибке отправки
			}

			if !res.Sent() {
				log.Warn("APNS уведомление не отправлено (ответ от сервера)",
					zap.String("token", tokenToSend),
					zap.Int("status_code", res.StatusCode),
					zap.String("apns_id", res.ApnsID),
					zap.String("reason", res.Reason),
				)
				failureCount++
				if firstError == nil {
					firstError = fmt.Errorf("apns delivery failed: %s (token: %s)", res.Reason, tokenToSend)
				}
				// Проверяем, является ли причина невалидным токеном
				if res.Reason == apns2.ReasonUnregistered || res.Reason == apns2.ReasonBadDeviceToken {
					invalidTokens = append(invalidTokens, tokenToSend)
				}
			} else {
				log.Debug("APNS уведомление успешно отправлено",
					zap.String("token", tokenToSend),
					zap.String("apns_id", res.ApnsID),
				)
			}
		}(deviceToken)
	}

	wg.Wait() // Ожидаем завершения всех горутин

	if len(invalidTokens) > 0 {
		log.Info("Обнаружены невалидные APNS токены для удаления", zap.Strings("tokens", invalidTokens))
		// TODO: Отправить событие или вызвать метод для удаления невалидных токенов
	}

	if failureCount > 0 {
		log.Error("Завершено с ошибками APNS", zap.Int("failures", failureCount), zap.Int("total", len(tokens)))
		return firstError // Возвращаем первую встреченную ошибку
	}

	log.Info("APNS отправка завершена успешно", zap.Int("sent_count", len(tokens)-failureCount))
	return nil
}

func (s *apnsSender) Platform() string {
	return "ios"
}
