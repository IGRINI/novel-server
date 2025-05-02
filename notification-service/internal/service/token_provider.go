package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"novel-server/shared/models"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// TokenProvider определяет интерфейс для получения токенов устройств пользователя.
type TokenProvider interface {
	GetUserDeviceTokens(ctx context.Context, userID uuid.UUID) ([]models.DeviceTokenInfo, error)
}

// --- Реализация по умолчанию (заглушка или через HTTP) ---

// HTTPClient интерфейс для *http.Client для мокирования
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type httpTokenProvider struct {
	client HTTPClient // Интерфейс для HTTP клиента (для тестируемости)
	url    string     // Базовый URL сервиса токенов (например, http://auth-service:8081)
	logger *zap.Logger
	secret string // Добавлено поле для секрета
}

// NewHTTPTokenProvider создает новый экземпляр провайдера токенов через HTTP.
func NewHTTPTokenProvider(client HTTPClient, url string, logger *zap.Logger, interServiceSecret string) TokenProvider {
	if url == "" {
		logger.Warn("URL для TokenService не указан, используется заглушка TokenProvider")
		return &stubTokenProvider{logger: logger}
	}
	if interServiceSecret == "" {
		logger.Warn("InterServiceSecret не установлен для HTTPTokenProvider, запросы к внутренним API Auth Service могут быть отклонены")
	}
	logger.Info("Инициализация HTTP Token Provider", zap.String("url", url), zap.Bool("secretLoaded", interServiceSecret != ""))
	return &httpTokenProvider{
		client: client,
		url:    url,
		logger: logger.Named("http_token_provider"),
		secret: interServiceSecret,
	}
}

func (p *httpTokenProvider) GetUserDeviceTokens(ctx context.Context, userID uuid.UUID) ([]models.DeviceTokenInfo, error) {
	log := p.logger.With(zap.String("user_id", userID.String()))
	targetURL := fmt.Sprintf("%s/internal/auth/users/%s/device-tokens", p.url, userID)
	log.Debug("Запрос токенов устройства", zap.String("url", targetURL))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		log.Error("Ошибка создания HTTP запроса для получения токенов", zap.Error(err))
		return nil, fmt.Errorf("ошибка создания запроса к token service: %w", err)
	}

	if p.secret != "" {
		req.Header.Set("X-Internal-Service-Token", p.secret)
		log.Debug("X-Internal-Service-Token header added")
	} else {
		log.Warn("Inter-service secret is empty, X-Internal-Service-Token header not added")
	}

	req.Header.Set("Accept", "application/json")

	// Используем переданный http.Client (который может иметь таймауты)
	start := time.Now()
	resp, err := p.client.Do(req)
	duration := time.Since(start)
	if err != nil {
		log.Error("Ошибка выполнения HTTP запроса к token service", zap.Error(err), zap.Duration("duration", duration))
		return nil, fmt.Errorf("ошибка запроса к token service: %w", err)
	}
	defer resp.Body.Close()

	log.Debug("Ответ от token service получен", zap.Int("status_code", resp.StatusCode), zap.Duration("duration", duration))

	if resp.StatusCode != http.StatusOK {
		// TODO: Прочитать тело ответа для получения деталей ошибки?
		log.Error("Token service вернул неожиданный статус", zap.Int("status_code", resp.StatusCode))
		return nil, fmt.Errorf("token service вернул статус %d", resp.StatusCode)
	}

	var tokens []models.DeviceTokenInfo
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		log.Error("Ошибка декодирования JSON ответа от token service", zap.Error(err))
		return nil, fmt.Errorf("ошибка декодирования ответа token service: %w", err)
	}

	log.Info("Токены устройства успешно получены", zap.Int("count", len(tokens)))
	return tokens, nil
}

// --- Заглушка для TokenProvider ---

type stubTokenProvider struct {
	logger *zap.Logger
}

func (p *stubTokenProvider) GetUserDeviceTokens(ctx context.Context, userID uuid.UUID) ([]models.DeviceTokenInfo, error) {
	p.logger.Warn("Используется ЗАГЛУШКА для TokenProvider", zap.String("user_id", userID.String()))
	// Возвращаем пустой список или тестовые данные
	return []models.DeviceTokenInfo{},
		// return []models.DeviceTokenInfo{
		// 	{Token: "fake-android-token-" + userID.String(), Platform: "android"},
		// 	{Token: "fake-ios-token-" + userID.String(), Platform: "ios"},
		// },
		nil
}
