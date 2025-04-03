package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrUserNotFound      = errors.New("пользователь не найден")
	ErrInvalidPassword   = errors.New("неверный пароль")
	ErrUserAlreadyExists = errors.New("пользователь уже существует")
	ErrInvalidEmail      = errors.New("неверный формат email")
)

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type UserRepository interface {
	CreateUser(ctx context.Context, user *User) error
	GetUserByUsername(ctx context.Context, username string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
}

type Service struct {
	repo      UserRepository
	jwtSecret []byte
}

func NewService(repo UserRepository, jwtSecret string) *Service {
	return &Service{
		repo:      repo,
		jwtSecret: []byte(jwtSecret),
	}
}

func (s *Service) Register(ctx context.Context, username, email, password string) error {
	// Проверяем, существует ли пользователь с таким username
	_, err := s.repo.GetUserByUsername(ctx, username)
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

	// Хешируем пароль
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("ошибка при хешировании пароля: %w", err)
	}

	// Создаем нового пользователя
	user := &User{
		Username:     username,
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

// Login проверяет учетные данные и возвращает JWT токен и ID пользователя
func (s *Service) Login(ctx context.Context, username, password string) (string, string, error) {
	// Получаем пользователя
	user, err := s.repo.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrUserNotFound
		}
		return "", "", fmt.Errorf("ошибка при поиске пользователя: %w", err)
	}

	// Проверяем пароль
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return "", "", ErrInvalidPassword
	}

	// Создаем JWT токен
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":  user.ID, // Убедитесь, что user.ID это строка (UUID)
		"username": user.Username,
		"email":    user.Email,
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
	})

	// Подписываем токен
	tokenString, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return "", "", fmt.Errorf("ошибка при создании токена: %w", err)
	}

	return tokenString, user.ID, nil
}
