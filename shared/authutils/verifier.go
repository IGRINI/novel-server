package authutils

import (
	"context"
	"errors"
	"fmt"
	"novel-server/shared/models" // Используем модели из shared

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

// JWTVerifier проверяет JWT токены.
type JWTVerifier struct {
	jwtSecret string
	logger    *zap.Logger
}

// NewJWTVerifier создает новый экземпляр JWTVerifier.
// Принимает секрет и опционально логгер. Если логгер nil, используется Noop.
func NewJWTVerifier(jwtSecret string, logger *zap.Logger) (*JWTVerifier, error) {
	if jwtSecret == "" {
		return nil, errors.New("JWT secret cannot be empty")
	}
	if logger == nil {
		logger = zap.NewNop() // Используем Noop логгер, если не предоставлен
	}
	return &JWTVerifier{
		jwtSecret: jwtSecret,
		logger:    logger.Named("JWTVerifier"), // Добавляем имя логгеру
	}, nil
}

// VerifyToken проверяет подпись JWT, его валидность и извлекает claims.
// Реализует сигнатуру, совместимую с shared/middleware.TokenVerifier.
func (v *JWTVerifier) VerifyToken(ctx context.Context, tokenString string) (*models.Claims, error) {
	log := v.logger.With(zap.String("tokenSnippet", tokenSnippet(tokenString))) // Используем вспомогательную функцию для логгирования
	claims := &models.Claims{}

	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		// Проверяем метод подписи
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			log.Warn("Unexpected signing method", zap.Any("alg", token.Header["alg"]))
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(v.jwtSecret), nil
	})

	if err != nil {
		log.Warn("Failed to parse or verify token", zap.Error(err))
		// Используем errors.Is для точной проверки и возвращаем ошибки из shared/models
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, models.ErrTokenExpired
		} else if errors.Is(err, jwt.ErrTokenMalformed) {
			return nil, models.ErrTokenMalformed
		} else if errors.Is(err, jwt.ErrTokenSignatureInvalid) { // Эта ошибка тоже важна
			return nil, models.ErrTokenInvalid // Невалидная подпись = невалидный токен
		}
		// Для других ошибок парсинга считаем токен невалидным, оборачивая исходную ошибку
		return nil, fmt.Errorf("%w: %v", models.ErrTokenInvalid, err)
	}

	// Дополнительная проверка валидности токена (хотя ParseWithClaims уже должен это делать)
	if !token.Valid {
		log.Warn("Token is invalid despite no parsing error")
		return nil, models.ErrTokenInvalid
	}

	// Проверка наличия обязательных полей в claims
	if claims.UserID == 0 {
		log.Warn("Token missing UserID", zap.Any("claims", claims))
		return nil, fmt.Errorf("%w: UserID missing", models.ErrTokenInvalid)
	}
	// Можно добавить проверку claims.Roles != nil, если это требуется

	log.Debug("Token verified successfully", zap.Uint64("userID", claims.UserID), zap.Strings("roles", claims.Roles))
	return claims, nil
}

// tokenSnippet возвращает безопасную для логгирования часть токена.
func tokenSnippet(tokenString string) string {
	limit := 15
	if len(tokenString) > limit {
		return tokenString[:limit] + "..."
	}
	return tokenString
} 