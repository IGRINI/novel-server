package service

import (
	"context"
	"fmt"
	"novel-server/notification-service/internal/messaging"
	sharedModels "novel-server/shared/models"
	"sync"

	"go.uber.org/zap"
)

// NotificationSender убран отсюда и перемещен в пакет messaging,
// чтобы избежать цикла импорта.
// type NotificationSender interface {
// 	SendNotification(ctx context.Context, payload messaging.PushNotificationPayload) error
// }

// PlatformSender определяет интерфейс для отправки на конкретную платформу (FCM/APNS).
type PlatformSender interface {
	Send(ctx context.Context, tokens []string, notification sharedModels.PushNotification, data map[string]string) error
	Platform() string // "android" или "ios"
}

// --- Реализация основного сервиса отправки ---

type notificationService struct {
	tokenProvider TokenProvider
	logger        *zap.Logger
	fcmSender     PlatformSender // Может быть nil, если FCM не настроен
	apnsSender    PlatformSender // Может быть nil, если APNS не настроен
}

// NewNotificationService создает новый сервис отправки уведомлений.
// Возвращает конкретный тип *notificationService, а не интерфейс,
// так как интерфейс теперь в другом пакете.
func NewNotificationService(tp TokenProvider, logger *zap.Logger, fcmSender, apnsSender PlatformSender) *notificationService {
	if fcmSender == nil {
		logger.Warn("FCM sender не инициализирован.")
	}
	if apnsSender == nil {
		logger.Warn("APNS sender не инициализирован.")
	}
	return &notificationService{
		tokenProvider: tp,
		logger:        logger.Named("notification_service"),
		fcmSender:     fcmSender,
		apnsSender:    apnsSender,
	}
}

// Убедимся, что *notificationService реализует нужный интерфейс (который теперь в messaging)
var _ messaging.NotificationSender = (*notificationService)(nil)

func (s *notificationService) SendNotification(ctx context.Context, payload sharedModels.PushNotificationPayload) error {
	log := s.logger.With(zap.String("user_id", payload.UserID.String()))
	log.Info("Получен запрос на отправку уведомления")

	// 1. Получаем токены пользователя
	deviceTokens, err := s.tokenProvider.GetUserDeviceTokens(ctx, payload.UserID)
	if err != nil {
		log.Error("Ошибка получения токенов пользователя", zap.Error(err))
		// Не возвращаем ошибку, т.к. это может быть временная проблема с сервисом токенов
		// Или можно вернуть, если считаем это критичным
		return nil // Пока не возвращаем ошибку
	}

	if len(deviceTokens) == 0 {
		log.Warn("Не найдено активных токенов для пользователя")
		return nil
	}

	log.Info("Найдено токенов", zap.Int("count", len(deviceTokens)))

	// 2. Группируем токены по платформам
	androidTokens := make([]string, 0)
	iosTokens := make([]string, 0)
	for _, dt := range deviceTokens {
		switch dt.Platform {
		case "android":
			androidTokens = append(androidTokens, dt.Token)
		case "ios":
			iosTokens = append(iosTokens, dt.Token)
		default:
			log.Warn("Неизвестная платформа токена", zap.String("token", dt.Token), zap.String("platform", dt.Platform))
		}
	}

	// 3. Отправляем уведомления параллельно
	var wg sync.WaitGroup
	var sendErrors []error
	var mu sync.Mutex // Для безопасной записи в sendErrors

	if s.fcmSender != nil && len(androidTokens) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Info("Отправка на Android (FCM)", zap.Int("count", len(androidTokens)))
			err := s.fcmSender.Send(ctx, androidTokens, payload.Notification, payload.Data)
			if err != nil {
				log.Error("Ошибка отправки FCM", zap.Error(err))
				mu.Lock()
				sendErrors = append(sendErrors, fmt.Errorf("fcm: %w", err))
				mu.Unlock()
			}
		}()
	}

	if s.apnsSender != nil && len(iosTokens) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Info("Отправка на iOS (APNS)", zap.Int("count", len(iosTokens)))
			err := s.apnsSender.Send(ctx, iosTokens, payload.Notification, payload.Data)
			if err != nil {
				log.Error("Ошибка отправки APNS", zap.Error(err))
				mu.Lock()
				sendErrors = append(sendErrors, fmt.Errorf("apns: %w", err))
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if len(sendErrors) > 0 {
		// Можно собрать ошибки в одну
		// compositeError := errors.Join(sendErrors...)
		log.Error("Произошли ошибки во время отправки уведомлений", zap.Errors("errors", sendErrors))
		// Возвращаем первую ошибку или собранную ошибку
		return sendErrors[0]
	}

	log.Info("Отправка уведомлений завершена успешно")
	return nil
}
