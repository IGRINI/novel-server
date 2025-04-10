package authservice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client предоставляет методы для взаимодействия с Auth Service
type Client struct {
	baseURL      string
	serviceID    string
	serviceToken string
	tokenExpires time.Time
	httpClient   *http.Client
	apiKey       string // Необязательный API ключ для дополнительной безопасности
}

// ClientConfig содержит настройки для клиента Auth Service
type ClientConfig struct {
	BaseURL    string
	ServiceID  string
	APIKey     string
	Timeout    time.Duration
	MaxRetries int
}

// NewClient создает новый клиент для Auth Service
func NewClient(cfg ClientConfig) *Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}

	httpClient := &http.Client{
		Timeout: cfg.Timeout,
	}

	return &Client{
		baseURL:    cfg.BaseURL,
		serviceID:  cfg.ServiceID,
		apiKey:     cfg.APIKey,
		httpClient: httpClient,
	}
}

// ValidateToken проверяет действительность пользовательского токена
func (c *Client) ValidateToken(ctx context.Context, token string) (*ValidateTokenResponse, error) {
	// Проверяем/обновляем сервисный токен
	if err := c.ensureServiceToken(ctx); err != nil {
		return nil, fmt.Errorf("ошибка при получении сервисного токена: %w", err)
	}

	// Подготавливаем запрос
	reqBody := ValidateTokenRequest{
		Token: token,
	}
	reqData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("ошибка при сериализации запроса: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/internal/validate-token", bytes.NewBuffer(reqData))
	if err != nil {
		return nil, fmt.Errorf("ошибка при создании запроса: %w", err)
	}

	// Устанавливаем заголовки
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Service-Authorization", "Bearer "+c.serviceToken)

	// Выполняем запрос
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ошибка при выполнении запроса: %w", err)
	}
	defer resp.Body.Close()

	// Проверяем код ответа
	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
			return nil, fmt.Errorf("ошибка при получении статуса токена, код: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("ошибка при проверке токена: %s", errResp.Error)
	}

	// Парсим ответ
	var validateResp ValidateTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&validateResp); err != nil {
		return nil, fmt.Errorf("ошибка при разборе ответа: %w", err)
	}

	return &validateResp, nil
}

// ensureServiceToken получает или обновляет сервисный токен
func (c *Client) ensureServiceToken(ctx context.Context) error {
	// Проверяем, не истек ли текущий токен
	if c.serviceToken != "" && time.Now().Before(c.tokenExpires) {
		return nil
	}

	// Подготавливаем запрос для получения нового токена
	reqBody := ServiceTokenRequest{
		ServiceID:   c.serviceID,
		ServiceName: c.serviceID, // Имя сервиса определяется на стороне Auth Service
		APIKey:      c.apiKey,
	}
	reqData, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("ошибка при сериализации запроса: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/service/token", bytes.NewBuffer(reqData))
	if err != nil {
		return fmt.Errorf("ошибка при создании запроса: %w", err)
	}

	// Устанавливаем заголовки
	req.Header.Set("Content-Type", "application/json")

	// Выполняем запрос
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ошибка при выполнении запроса: %w", err)
	}
	defer resp.Body.Close()

	// Проверяем код ответа
	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
			return fmt.Errorf("ошибка при получении сервисного токена, код: %d", resp.StatusCode)
		}
		return fmt.Errorf("ошибка при получении сервисного токена: %s", errResp.Error)
	}

	// Парсим ответ
	var tokenResp ServiceTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("ошибка при разборе ответа: %w", err)
	}

	// Сохраняем токен и время его истечения
	c.serviceToken = tokenResp.Token
	c.tokenExpires = time.Unix(tokenResp.ExpiresAt, 0).Add(-5 * time.Minute) // Обновлять за 5 минут до истечения

	return nil
}

// GetUserInfo извлекает информацию о пользователе из токена
func (c *Client) GetUserInfo(ctx context.Context, token string) (*UserInfo, error) {
	// Проверяем токен
	validateResp, err := c.ValidateToken(ctx, token)
	if err != nil {
		return nil, err
	}

	if !validateResp.Valid {
		return nil, fmt.Errorf("недействительный токен пользователя")
	}

	// Возвращаем информацию о пользователе
	return &UserInfo{
		ID:       validateResp.UserID,
		Username: validateResp.Username,
		Email:    validateResp.Email,
	}, nil
}

// UserInfo содержит основную информацию о пользователе
type UserInfo struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
}
