package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	// Добавляем для стандартных клеймов
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// Claims - структура для пользовательских клеймов JWT
type Claims struct {
	UserID uint64 `json:"user_id"`
	jwt.RegisteredClaims
}

// JWTAuthMiddleware создает middleware для проверки JWT access токена с использованием Echo.
// Проверяет подпись, срок действия и извлекает user_id.
// Не проверяет отзыв токена (это остается ответственностью auth-сервиса).
func JWTAuthMiddleware(secretKey string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			authHeader := c.Request().Header.Get("Authorization")
			if authHeader == "" {
				// Логирование можно добавить здесь или в вызывающем коде
				return echo.NewHTTPError(http.StatusUnauthorized, "Authorization header missing")
			}

			parts := strings.Split(authHeader, " ")
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				return echo.NewHTTPError(http.StatusUnauthorized, "Invalid Authorization header format")
			}

			tokenString := parts[1]
			claims := &Claims{}

			token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
				// Проверяем метод подписи
				if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
				}
				return []byte(secretKey), nil
			})

			if err != nil {
				// Логируем конкретную ошибку JWT
				c.Logger().Errorf("JWT parsing/validation error: %v", err)
				if errors.Is(err, jwt.ErrTokenExpired) {
					return echo.NewHTTPError(http.StatusUnauthorized, "Token has expired")
				} else if errors.Is(err, jwt.ErrTokenMalformed) {
					return echo.NewHTTPError(http.StatusUnauthorized, "Token is malformed")
				} else if errors.Is(err, jwt.ErrTokenSignatureInvalid) {
					return echo.NewHTTPError(http.StatusUnauthorized, "Token signature is invalid")
				} else {
					// Другие ошибки валидации (например, nbf, iss, aud, если используются)
					return echo.NewHTTPError(http.StatusUnauthorized, fmt.Sprintf("Token validation failed: %v", err))
				}
			}

			if !token.Valid {
				return echo.NewHTTPError(http.StatusUnauthorized, "Token is invalid")
			}

			// Проверяем наличие UserID
			if claims.UserID == 0 {
				c.Logger().Error("UserID missing in JWT claims")
				return echo.NewHTTPError(http.StatusUnauthorized, "Invalid token: UserID missing")
			}

			// Сохраняем ID пользователя в контексте Echo
			c.Set("user_id", strconv.FormatUint(claims.UserID, 10))
			c.Set("access_uuid", claims.ID) // Также сохраняем 'jti' (access_uuid)

			// Логируем успешную аутентификацию
			c.Logger().Infof("User %d authenticated via JWT (AccessUUID: %s)", claims.UserID, claims.ID)

			return next(c) // Передаем управление следующему обработчику
		}
	}
}

// GenerateTestJWT создает тестовый JWT токен.
// ВАЖНО: Эта функция предназначена ТОЛЬКО для использования в тестах.
func GenerateTestJWT(userID uint64, secretKey string, validityDuration time.Duration) (string, error) {
	expirationTime := time.Now().Add(validityDuration)
	claims := &Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			ID:        uuid.NewString(), // Генерируем 'jti'
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(secretKey))
	if err != nil {
		return "", fmt.Errorf("failed to sign test JWT: %w", err)
	}

	return tokenString, nil
}
