package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	flashCookieName = "flash_message"
	flashCookieTTL  = 5 * time.Second // Короткое время жизни куки
)

// FlashMessage хранит тип и текст сообщения для пользователя.
type FlashMessage struct {
	Type    string `json:"type"` // e.g., "success", "error", "info"
	Message string `json:"message"`
}

// setFlashMessage устанавливает подписанную куку с flash-сообщением.
// Использует HMAC-SHA256 для подписи и Base64 для кодирования.
func setFlashMessage(c *gin.Context, msgType, message string, jwtSecret []byte) error {
	flash := FlashMessage{Type: msgType, Message: message}
	jsonData, err := json.Marshal(flash)
	if err != nil {
		return fmt.Errorf("failed to marshal flash message: %w", err)
	}

	// Подписываем данные
	mac := hmac.New(sha256.New, jwtSecret)
	mac.Write(jsonData)
	signature := mac.Sum(nil)

	// Кодируем данные и подпись в Base64
	signedData := append(signature, jsonData...)
	encodedValue := base64.URLEncoding.EncodeToString(signedData)

	c.SetCookie(flashCookieName,
		encodedValue,
		int(flashCookieTTL.Seconds()),
		"/",  // Path
		"",   // Domain (пусто для текущего хоста)
		true, // Secure (рекомендуется для HTTPS, можно сделать зависимым от ENV)
		true, // HttpOnly
	)
	return nil
}

// getFlashMessage читает, проверяет и удаляет куку с flash-сообщением.
// Возвращает сообщение, если оно валидно, иначе nil.
func getFlashMessage(c *gin.Context, jwtSecret []byte) (*FlashMessage, error) {
	cookie, err := c.Cookie(flashCookieName)
	if err != nil {
		if errors.Is(err, http.ErrNoCookie) {
			return nil, nil // Нет куки - нет сообщения
		}
		return nil, fmt.Errorf("failed to get flash cookie: %w", err)
	}

	// Удаляем куку сразу после чтения, чтобы она не осталась
	c.SetCookie(flashCookieName, "", -1, "/", "", true, true)

	// Декодируем из Base64
	signedData, err := base64.URLEncoding.DecodeString(cookie)
	if err != nil {
		return nil, fmt.Errorf("failed to decode flash cookie: %w", err)
	}

	if len(signedData) < sha256.Size {
		return nil, fmt.Errorf("invalid flash cookie length")
	}

	// Разделяем подпись и данные
	receivedSig := signedData[:sha256.Size]
	jsonData := signedData[sha256.Size:]

	// Проверяем подпись
	mac := hmac.New(sha256.New, jwtSecret)
	mac.Write(jsonData)
	expectedSig := mac.Sum(nil)

	if !hmac.Equal(receivedSig, expectedSig) {
		return nil, fmt.Errorf("invalid flash cookie signature")
	}

	// Десериализуем данные
	var flash FlashMessage
	if err := json.Unmarshal(jsonData, &flash); err != nil {
		return nil, fmt.Errorf("failed to unmarshal flash message: %w", err)
	}

	return &flash, nil
}
