package auth

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var jwtSecret []byte
var jwtExpiration time.Duration

// CustomClaims определяет пользовательские данные, которые мы хотим хранить в токене.
type CustomClaims struct {
	UserID string `json:"user_id"`
	jwt.RegisteredClaims
}

// --- Ключ контекста для UserID ---
// contextKey - тип для ключа контекста, чтобы избежать коллизий.
type contextKey string

// UserIDKey - ключ для хранения ID пользователя в контексте (экспортируемый).
const UserIDKey = contextKey("userID")

// --- Конец ключа контекста ---

// InitJWT инициализирует параметры JWT из переменных окружения.
func InitJWT() error {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		return errors.New("JWT_SECRET environment variable not set")
	}
	jwtSecret = []byte(secret)

	expMinutesStr := os.Getenv("JWT_EXPIRATION_MINUTES")
	if expMinutesStr == "" {
		expMinutesStr = "60" // Default to 60 minutes
	}
	expMinutes, err := strconv.Atoi(expMinutesStr)
	if err != nil {
		return fmt.Errorf("invalid JWT_EXPIRATION_MINUTES value: %w", err)
	}
	jwtExpiration = time.Duration(expMinutes) * time.Minute
	log.Printf("JWT initialized with expiration: %v", jwtExpiration)
	return nil
}

// GenerateToken создает новый JWT для указанного UserID.
func GenerateToken(userID string) (string, error) {
	if len(jwtSecret) == 0 {
		return "", errors.New("JWT secret not initialized")
	}

	expirationTime := time.Now().Add(jwtExpiration)
	claims := &CustomClaims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "novel-server", // Можно добавить
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(jwtSecret)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	return tokenString, nil
}

// ValidateToken проверяет JWT и возвращает CustomClaims, если токен валиден.
func ValidateToken(tokenString string) (*CustomClaims, error) {
	if len(jwtSecret) == 0 {
		return nil, errors.New("JWT secret not initialized")
	}

	token, err := jwt.ParseWithClaims(tokenString, &CustomClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Убедимся, что метод подписи - HMAC
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return jwtSecret, nil
	})

	if err != nil {
		// Обрабатываем конкретные ошибки валидации
		if errors.Is(err, jwt.ErrTokenMalformed) {
			return nil, fmt.Errorf("token is malformed: %w", err)
		} else if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, fmt.Errorf("token is expired: %w", err)
		} else if errors.Is(err, jwt.ErrTokenNotValidYet) {
			return nil, fmt.Errorf("token not active yet: %w", err)
		}
		// Другие ошибки
		return nil, fmt.Errorf("could not parse token: %w", err)
	}

	if claims, ok := token.Claims.(*CustomClaims); ok && token.Valid {
		return claims, nil
	} else {
		return nil, errors.New("invalid token")
	}
}
