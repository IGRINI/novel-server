package service

import (
	"novel-server/shared/utils"
	"novel-server/websocket-service/internal/config"

	"github.com/rs/zerolog"
)

// AuthService предоставляет методы для взаимодействия с аутентификацией/авторизацией.
// В данном случае, он просто хранит JWT секрет.
type AuthService struct {
	cfg    *config.AuthServiceConfig
	logger zerolog.Logger
	// Здесь можно добавить HTTP клиент для запросов к auth-service, если нужно
}

// NewAuthService создает новый экземпляр AuthService.
func NewAuthService(cfg *config.AuthServiceConfig, logger zerolog.Logger) *AuthService {
	// Загружаем JWT секрет
	var loadErr error
	cfg.JWTSecret, loadErr = utils.ReadSecret("jwt_secret")
	if loadErr != nil {
		// Логируем фатальную ошибку, так как без секрета сервис не может работать
		logger.Fatal().Err(loadErr).Msg("Failed to load jwt_secret")
	}
	logger.Info().Msg("JWT secret loaded successfully")

	return &AuthService{
		cfg:    cfg,
		logger: logger.With().Str("component", "AuthService").Logger(),
	}
}

// GetJWTSecret возвращает загруженный JWT секрет.
// (Пример метода, может понадобиться для ws_handler)
func (s *AuthService) GetJWTSecret() []byte {
	return []byte(s.cfg.JWTSecret)
}

// Тут можно добавить метод для валидации токена,
// который будет либо использовать JWT секрет локально,
// либо делать запрос к auth-service через HTTP.
// func (s *AuthService) ValidateToken(tokenString string) (claims jwt.MapClaims, err error) {
// 	 token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
// 		 if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
// 			 return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
// 		 }
// 		 return s.GetJWTSecret(), nil
// 	 })
// 	 if err != nil {
// 		 return nil, fmt.Errorf("token parse error: %w", err)
// 	 }
// 	 if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
// 		 return claims, nil
// 	 } else {
// 		 return nil, fmt.Errorf("invalid token")
// 	 }
// }
