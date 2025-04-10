package authservice

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

// Добавим ограничения для валидации
const (
	MinUsernameLength    = 4
	MaxUsernameLength    = 32
	MinPasswordLength    = 6
	MaxPasswordLength    = 100
	MaxDisplayNameLength = 100
	ServiceTokenTTL      = 1 * time.Hour // Срок действия служебного токена
)

var (
	ErrUserNotFound          = errors.New("пользователь не найден")
	ErrInvalidPassword       = errors.New("неверный пароль")
	ErrUserAlreadyExists     = errors.New("пользователь уже существует")
	ErrInvalidEmail          = errors.New("неверный формат email")
	ErrInvalidToken          = errors.New("недействительный токен")
	ErrExpiredToken          = errors.New("истекший токен")
	ErrRevokedToken          = errors.New("отозванный токен")
	ErrInvalidUsernameLength = errors.New("длина имени пользователя должна быть от 4 до 32 символов")
	ErrInvalidPasswordLength = errors.New("длина пароля должна быть от 6 до 100 символов")
	ErrInvalidDisplayName    = errors.New("некорректное отображаемое имя")
	ErrServiceNotAuthorized  = errors.New("сервис не авторизован")
)

// CustomClaims определяет структуру для наших JWT claims
type CustomClaims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	jwt.RegisteredClaims
}

// ServiceClaims определяет структуру для межсервисных JWT claims
type ServiceClaims struct {
	ServiceID   string `json:"service_id"`
	ServiceName string `json:"service_name"`
	jwt.RegisteredClaims
}

// TokenDetails содержит информацию о сгенерированных токенах
type TokenDetails struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	UserID       string    `json:"user_id"`
	Username     string    `json:"username"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// UserRepository интерфейс для доступа к данным пользователей
type UserRepository interface {
	CreateUser(ctx context.Context, user *User) error
	GetUserByUsername(ctx context.Context, username string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserByID(ctx context.Context, userID string) (*User, error)
	UpdateDisplayName(ctx context.Context, userID string, displayName string) error

	// Методы для работы с refresh токенами
	CreateRefreshToken(ctx context.Context, token *RefreshToken) error
	GetRefreshTokenByToken(ctx context.Context, tokenStr string) (*RefreshToken, error)
	RevokeAllUserTokens(ctx context.Context, userID string) error
	RevokeRefreshToken(ctx context.Context, tokenStr string) error
	DeleteExpiredTokens(ctx context.Context) error
}

// Service предоставляет методы для работы с аутентификацией
type Service struct {
	repo            UserRepository
	jwtSecret       []byte
	passwordSalt    string
	accessTokenTTL  time.Duration     // Время жизни access token (короткое)
	refreshTokenTTL time.Duration     // Время жизни refresh token (длинное)
	trustedServices map[string]string // Мапа доверенных сервисов: serviceID -> serviceName
}

// NewService создает новый экземпляр сервиса аутентификации
func NewService(repo UserRepository, jwtSecret string, passwordSalt string) *Service {
	// Инициализируем сервис с доверенными микросервисами
	trustedServices := map[string]string{
		"novel-service":  "Novel Service",
		"story-service":  "Story Generator Service",
		"engine-service": "Game Engine Service",
	}

	return &Service{
		repo:            repo,
		jwtSecret:       []byte(jwtSecret),
		passwordSalt:    passwordSalt,
		accessTokenTTL:  1 * time.Hour,      // По умолчанию 1 час для Access Token
		refreshTokenTTL: 7 * 24 * time.Hour, // По умолчанию 7 дней для Refresh Token
		trustedServices: trustedServices,
	}
}

// Установка времени жизни токенов
func (s *Service) SetTokenTTL(accessTTL, refreshTTL time.Duration) {
	s.accessTokenTTL = accessTTL
	s.refreshTokenTTL = refreshTTL
}

// Генерация случайного токена для refresh token
func generateRefreshToken() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// Register регистрирует нового пользователя
func (s *Service) Register(ctx context.Context, username, email, password string) error {
	// Проверяем длину username
	if len(username) < MinUsernameLength || len(username) > MaxUsernameLength {
		return ErrInvalidUsernameLength
	}

	// Проверяем длину пароля
	if len(password) < MinPasswordLength || len(password) > MaxPasswordLength {
		return ErrInvalidPasswordLength
	}

	// Преобразуем username в lowercase
	lowercaseUsername := strings.ToLower(username)

	// Проверяем, существует ли пользователь с таким username
	_, err := s.repo.GetUserByUsername(ctx, lowercaseUsername)
	if err == nil {
		return ErrUserAlreadyExists
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("ошибка при проверке существования пользователя: %w", err)
	}

	// Проверяем, существует ли пользователь с таким email
	_, err = s.repo.GetUserByEmail(ctx, email)
	if err == nil {
		return ErrUserAlreadyExists
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("ошибка при проверке email: %w", err)
	}

	// Добавляем соль к паролю перед хешированием
	saltedPassword := password + s.passwordSalt

	// Хешируем пароль с солью
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(saltedPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("ошибка при хешировании пароля: %w", err)
	}

	// Создаем нового пользователя
	user := &User{
		Username:     lowercaseUsername, // Сохраняем username в lowercase
		DisplayName:  username,          // Сохраняем displayName как есть
		Email:        email,
		PasswordHash: string(hashedPassword),
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	if err := s.repo.CreateUser(ctx, user); err != nil {
		return fmt.Errorf("ошибка при создании пользователя: %w", err)
	}

	return nil
}

// Login проверяет учетные данные и возвращает детали токенов
func (s *Service) Login(ctx context.Context, username, password string) (*TokenDetails, error) {
	// Проверяем длину пароля
	if len(password) < MinPasswordLength || len(password) > MaxPasswordLength {
		return nil, ErrInvalidPasswordLength
	}

	// Преобразуем username в lowercase
	lowercaseUsername := strings.ToLower(username)

	// Получаем пользователя
	user, err := s.repo.GetUserByUsername(ctx, lowercaseUsername)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("ошибка при поиске пользователя: %w", err)
	}

	// Добавляем соль к паролю перед сравнением
	saltedPassword := password + s.passwordSalt

	// Проверяем пароль с солью
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(saltedPassword)); err != nil {
		return nil, ErrInvalidPassword
	}

	// Генерируем токены
	return s.CreateTokens(ctx, user)
}

// CreateTokens генерирует пару токенов (access и refresh)
func (s *Service) CreateTokens(ctx context.Context, user *User) (*TokenDetails, error) {
	// Создаем детали токенов
	tokenDetails := &TokenDetails{
		UserID:    user.ID,
		Username:  user.Username,
		ExpiresAt: time.Now().Add(s.accessTokenTTL),
	}

	// Создаем JWT токен (access token) с кастомными claims
	claims := CustomClaims{
		UserID:   user.ID,
		Username: user.Username,
		Email:    user.Email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(tokenDetails.ExpiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   user.ID, // Используем ID пользователя как Subject
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	// Подписываем токен
	accessToken, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("ошибка при создании токена доступа: %w", err)
	}
	tokenDetails.AccessToken = accessToken

	// Создаем refresh token
	refreshToken, err := generateRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("ошибка при генерации токена обновления: %w", err)
	}
	tokenDetails.RefreshToken = refreshToken

	// Сохраняем refresh token в базе данных
	refreshTokenObj := &RefreshToken{
		UserID:    user.ID,
		Token:     refreshToken,
		ExpiresAt: time.Now().Add(s.refreshTokenTTL),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Revoked:   false,
	}

	if err := s.repo.CreateRefreshToken(ctx, refreshTokenObj); err != nil {
		return nil, fmt.Errorf("ошибка при сохранении токена обновления: %w", err)
	}

	return tokenDetails, nil
}

// ValidateAccessToken проверяет действительность токена доступа
func (s *Service) ValidateAccessToken(tokenString string) (bool, *CustomClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &CustomClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Проверяем, что алгоритм подписи соответствует ожидаемому
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("неожиданный метод подписи: %v", token.Header["alg"])
		}
		return s.jwtSecret, nil
	})

	if err != nil {
		// Проверяем, связана ли ошибка с истекшим токеном
		if errors.Is(err, jwt.ErrTokenExpired) {
			return false, nil, ErrExpiredToken
		}
		return false, nil, ErrInvalidToken
	}

	if !token.Valid {
		return false, nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*CustomClaims)
	if !ok {
		return false, nil, ErrInvalidToken
	}

	return true, claims, nil
}

// RefreshTokens обновляет токены с использованием refresh token
func (s *Service) RefreshTokens(ctx context.Context, refreshToken string) (*TokenDetails, error) {
	// Получаем refresh token из базы данных
	tokenObj, err := s.repo.GetRefreshTokenByToken(ctx, refreshToken)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalidToken
		}
		return nil, fmt.Errorf("ошибка при поиске токена обновления: %w", err)
	}

	// Проверяем, не отозван ли токен
	if tokenObj.Revoked {
		return nil, ErrRevokedToken
	}

	// Проверяем срок действия токена
	if tokenObj.ExpiresAt.Before(time.Now()) {
		return nil, ErrExpiredToken
	}

	// Получаем пользователя
	user, err := s.repo.GetUserByID(ctx, tokenObj.UserID)
	if err != nil {
		return nil, fmt.Errorf("ошибка при поиске пользователя: %w", err)
	}

	// Отзываем текущий refresh token
	if err := s.repo.RevokeRefreshToken(ctx, refreshToken); err != nil {
		return nil, fmt.Errorf("ошибка при отзыве токена обновления: %w", err)
	}

	// Создаем новую пару токенов
	return s.CreateTokens(ctx, user)
}

// RevokeAllUserTokens отзывает все токены пользователя
func (s *Service) RevokeAllUserTokens(ctx context.Context, userID string) error {
	return s.repo.RevokeAllUserTokens(ctx, userID)
}

// RevokeToken отзывает конкретный токен обновления
func (s *Service) RevokeToken(ctx context.Context, refreshToken string) error {
	return s.repo.RevokeRefreshToken(ctx, refreshToken)
}

// CleanupExpiredTokens удаляет просроченные токены
func (s *Service) CleanupExpiredTokens(ctx context.Context) error {
	return s.repo.DeleteExpiredTokens(ctx)
}

// UpdateDisplayName обновляет отображаемое имя пользователя
func (s *Service) UpdateDisplayName(ctx context.Context, userID, displayName string) error {
	if displayName == "" || len(displayName) > MaxDisplayNameLength {
		return ErrInvalidDisplayName
	}

	return s.repo.UpdateDisplayName(ctx, userID, displayName)
}

// CreateServiceToken создает токен для межсервисной аутентификации
func (s *Service) CreateServiceToken(serviceID string) (string, error) {
	// Проверяем, является ли сервис доверенным
	serviceName, ok := s.trustedServices[serviceID]
	if !ok {
		return "", ErrServiceNotAuthorized
	}

	// Создаем JWT токен для межсервисной аутентификации
	claims := ServiceClaims{
		ServiceID:   serviceID,
		ServiceName: serviceName,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ServiceTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   serviceID,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	// Подписываем токен
	signedToken, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("ошибка при создании межсервисного токена: %w", err)
	}

	return signedToken, nil
}

// ValidateServiceToken проверяет токен межсервисной аутентификации
func (s *Service) ValidateServiceToken(tokenString string) (bool, *ServiceClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &ServiceClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Проверяем, что алгоритм подписи соответствует ожидаемому
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("неожиданный метод подписи: %v", token.Header["alg"])
		}
		return s.jwtSecret, nil
	})

	if err != nil {
		// Проверяем, связана ли ошибка с истекшим токеном
		if errors.Is(err, jwt.ErrTokenExpired) {
			return false, nil, ErrExpiredToken
		}
		return false, nil, ErrInvalidToken
	}

	if !token.Valid {
		return false, nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*ServiceClaims)
	if !ok {
		return false, nil, ErrInvalidToken
	}

	// Проверяем, является ли сервис доверенным
	if _, ok := s.trustedServices[claims.ServiceID]; !ok {
		return false, nil, ErrServiceNotAuthorized
	}

	return true, claims, nil
}
