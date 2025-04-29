package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"novel-server/admin-service/internal/config" // <<< Проверь, правильный ли путь к конфигу

	"github.com/gin-gonic/gin"
)

const flashCookieNameMsg = "flash_msg"
const flashSeparatorMsg = "|"
const flashMaxAgeMsg = 10 // Seconds cookie will live

type flashMessageMsg struct {
	Type    string `json:"t"`
	Message string `json:"m"`
}

// setFlashMsg создает подписанную куку с flash-сообщением.
func setFlashMsg(c *gin.Context, messageType string, message string, cfg *config.Config) error {
	if cfg.JWTSecret == "" {
		return fmt.Errorf("JWTSecret is not configured")
	}

	fm := flashMessageMsg{
		Type:    messageType,
		Message: message,
	}

	jsonData, err := json.Marshal(fm)
	if err != nil {
		return fmt.Errorf("failed to marshal flash message: %w", err)
	}

	// Подписываем данные
	mac := hmac.New(sha256.New, []byte(cfg.JWTSecret))
	mac.Write(jsonData)
	signature := mac.Sum(nil)

	// Кодируем данные и подпись в base64 и объединяем
	encodedData := base64.URLEncoding.EncodeToString(jsonData)
	encodedSignature := base64.URLEncoding.EncodeToString(signature)
	cookieValue := encodedData + flashSeparatorMsg + encodedSignature

	// Устанавливаем куку
	// TODO: Определить Secure и SameSite в зависимости от окружения/конфига
	c.SetCookie(
		flashCookieNameMsg,
		cookieValue,
		flashMaxAgeMsg,
		"/",   // Path
		"",    // Domain (пусто = текущий хост)
		false, // Secure (должно быть true для HTTPS)
		true,  // HttpOnly
	)

	return nil
}

// getFlashMsg читает, проверяет и удаляет куку с flash-сообщением.
func getFlashMsg(c *gin.Context, cfg *config.Config) (messageType string, message string, exists bool) {
	cookie, err := c.Cookie(flashCookieNameMsg)
	if err != nil || cookie == "" {
		return "", "", false // Куки нет или ошибка чтения
	}

	// Немедленно удаляем куку, чтобы она не прочиталась дважды
	// TODO: Определить Secure и SameSite в зависимости от окружения/конфига
	c.SetCookie(
		flashCookieNameMsg,
		"",
		-1,    // Expire immediately
		"/",   // Path
		"",    // Domain
		false, // Secure
		true,  // HttpOnly
	)

	// Разделяем данные и подпись
	parts := strings.SplitN(cookie, flashSeparatorMsg, 2)
	if len(parts) != 2 {
		// Некорректный формат куки
		return "", "", false
	}
	encodedData := parts[0]
	encodedSignature := parts[1]

	// Декодируем подпись
	expectedSignature, err := base64.URLEncoding.DecodeString(encodedSignature)
	if err != nil {
		// Некорректная подпись
		return "", "", false
	}

	// Декодируем данные
	jsonData, err := base64.URLEncoding.DecodeString(encodedData)
	if err != nil {
		// Некорректные данные
		return "", "", false
	}

	// Проверяем подпись
	if cfg.JWTSecret == "" {
		// Логируем ошибку, но не можем проверить подпись
		// log.Error("JWTSecret is not configured, cannot verify flash cookie signature")
		return "", "", false
	}
	mac := hmac.New(sha256.New, []byte(cfg.JWTSecret))
	mac.Write(jsonData)
	actualSignature := mac.Sum(nil)

	if !hmac.Equal(actualSignature, expectedSignature) {
		// Подпись не совпадает
		return "", "", false
	}

	// Десериализуем JSON
	var fm flashMessageMsg
	if err := json.Unmarshal(jsonData, &fm); err != nil {
		// Ошибка десериализации
		return "", "", false
	}

	return fm.Type, fm.Message, true
}
