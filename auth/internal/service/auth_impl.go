package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"novel-server/auth/internal/config"
	"novel-server/auth/internal/domain"
	"novel-server/shared/models"
	"shared/interfaces"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// Compile-time check to ensure authServiceImpl implements AuthService
var _ AuthService = (*authServiceImpl)(nil)

// authServiceImpl implements the AuthService interface.
type authServiceImpl struct {
	userRepo  interfaces.UserRepository
	tokenRepo interfaces.TokenRepository
	cfg       *config.Config
	logger    *zap.Logger
}

// NewAuthService creates a new instance of authServiceImpl.
func NewAuthService(userRepo interfaces.UserRepository, tokenRepo interfaces.TokenRepository, cfg *config.Config, logger *zap.Logger) AuthService {
	return &authServiceImpl{
		userRepo:  userRepo,
		tokenRepo: tokenRepo,
		cfg:       cfg,
		logger:    logger.Named("AuthService"),
	}
}

// Register creates a new user.
func (s *authServiceImpl) Register(ctx context.Context, username, email, password string) (*models.User, error) {
	// Приводим email к нижнему регистру и убираем пробелы
	email = strings.ToLower(strings.TrimSpace(email))
	username = strings.TrimSpace(username)

	logFields := []zap.Field{zap.String("username", username), zap.String("email", email)}
	s.logger.Info("Registering new user", logFields...)

	// Валидация формата email (простая)
	if _, err := mail.ParseAddress(email); err != nil {
		s.logger.Warn("Registration attempt with invalid email format", append(logFields, zap.Error(err))...)
		// Можно вернуть более специфичную ошибку, но пока ограничимся ErrInvalidCredentials
		// т.к. это ошибка входных данных пользователя
		return nil, fmt.Errorf("invalid email format: %w", models.ErrInvalidCredentials)
	}

	// Проверка на пустой username или password (хотя может проверяться и выше)
	if username == "" || password == "" {
		s.logger.Warn("Registration attempt with empty username or password", logFields...)
		return nil, models.ErrInvalidCredentials // Используем общую ошибку для невалидных данных
	}

	// Проверка существования пользователя по username
	existingUser, err := s.userRepo.GetUserByUsername(ctx, username)
	if err != nil && !errors.Is(err, models.ErrUserNotFound) {
		s.logger.Error("Error checking existing username during registration", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("error checking existing username: %w", err)
	}
	if existingUser != nil {
		s.logger.Warn("Registration attempt for existing username", logFields...)
		return nil, models.ErrUserAlreadyExists
	}

	// Проверка существования пользователя по email
	existingUser, err = s.userRepo.GetUserByEmail(ctx, email)
	if err != nil && !errors.Is(err, models.ErrUserNotFound) {
		s.logger.Error("Error checking existing email during registration", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("error checking existing email: %w", err)
	}
	if existingUser != nil {
		s.logger.Warn("Registration attempt for existing email", logFields...)
		return nil, models.ErrEmailAlreadyExists // Возвращаем новую ошибку
	}

	hashedPassword, err := hashPassword(password, s.cfg.PasswordSalt)
	if err != nil {
		s.logger.Error("Failed to hash password during registration", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	user := &models.User{
		Username: username,
		Email:    email,
		Password: hashedPassword,
	}

	err = s.userRepo.CreateUser(ctx, user)
	if err != nil {
		// Обработка ошибок уникальности (ErrUserAlreadyExists, ErrEmailAlreadyExists)
		// уже должна быть в репозитории. Если пришла другая ошибка - логируем и возвращаем.
		if !errors.Is(err, models.ErrUserAlreadyExists) && !errors.Is(err, models.ErrEmailAlreadyExists) {
			s.logger.Error("Failed to create user via repository", append(logFields, zap.Error(err))...)
			// Не оборачиваем снова, т.к. репозиторий уже обернул
			return nil, err
		}
		// Если ошибка уже обработана репозиторием (ErrUserAlreadyExists или ErrEmailAlreadyExists), просто возвращаем её
		return nil, err
	}

	// Don't return password hash
	user.Password = ""
	s.logger.Info("User registered successfully", zap.Uint64("userID", user.ID), zap.String("username", user.Username), zap.String("email", user.Email))
	return user, nil
}

// Login authenticates a user and returns token details.
func (s *authServiceImpl) Login(ctx context.Context, username, password string) (*models.TokenDetails, error) {
	s.logger.Info("Login attempt", zap.String("username", username))
	user, err := s.userRepo.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, models.ErrUserNotFound) {
			// Логируем неуспешную попытку входа (пользователь не найден)
			s.logger.Warn("Login failed: user not found", zap.String("username", username))
			return nil, models.ErrInvalidCredentials
		}
		// Другая ошибка репозитория (уже залогирована репо)
		s.logger.Error("Login failed: error getting user from repository", zap.Error(err), zap.String("username", username))
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if !checkPasswordHash(password, s.cfg.PasswordSalt, user.Password) {
		// Логируем неуспешную попытку входа (неверный пароль)
		s.logger.Warn("Login failed: invalid password", zap.String("username", username), zap.Uint64("userID", user.ID))
		return nil, models.ErrInvalidCredentials
	}

	td, err := s.createTokens(ctx, user.ID)
	if err != nil {
		s.logger.Error("Failed to create tokens during login", zap.Error(err), zap.Uint64("userID", user.ID))
		return nil, fmt.Errorf("failed to create tokens: %w", err)
	}

	err = s.tokenRepo.SetToken(ctx, user.ID, td)
	if err != nil {
		// Ошибка уже залогирована репозиторием
		s.logger.Error("Failed to save token details via repository during login", zap.Error(err), zap.Uint64("userID", user.ID))
		return nil, fmt.Errorf("failed to save token details: %w", err) // Ошибка уже обернута репо
	}

	s.logger.Info("User logged in successfully", zap.Uint64("userID", user.ID))
	return td, nil
}

// Logout invalidates user's tokens.
func (s *authServiceImpl) Logout(ctx context.Context, accessUUID, refreshUUID string) error {
	s.logger.Info("Logout attempt", zap.String("accessUUID", accessUUID), zap.String("refreshUUID", refreshUUID))
	deleted, err := s.tokenRepo.DeleteTokens(ctx, accessUUID, refreshUUID)
	if err != nil {
		// Ошибка уже залогирована репозиторием
		s.logger.Error("Error deleting tokens via repository during logout", zap.Error(err), zap.String("accessUUID", accessUUID), zap.String("refreshUUID", refreshUUID))
		return fmt.Errorf("failed to delete tokens: %w", err) // Ошибка уже обернута репо
	}
	if deleted == 0 {
		s.logger.Warn("Logout attempt: No tokens found to delete", zap.String("accessUUID", accessUUID), zap.String("refreshUUID", refreshUUID))
	} else {
		s.logger.Info("Tokens deleted successfully during logout", zap.Int64("deletedCount", deleted), zap.String("accessUUID", accessUUID), zap.String("refreshUUID", refreshUUID))
	}
	return nil
}

// Refresh issues new access and refresh tokens based on a valid refresh token.
func (s *authServiceImpl) Refresh(ctx context.Context, refreshTokenString string) (*models.TokenDetails, error) {
	s.logger.Info("Token refresh attempt") // Не логируем сам токен
	token, err := jwt.ParseWithClaims(refreshTokenString, &domain.Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.cfg.JWTSecret), nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			s.logger.Warn("Refresh attempt with expired token")
			return nil, models.ErrTokenExpired
		}
		if errors.Is(err, jwt.ErrTokenMalformed) {
			s.logger.Warn("Refresh attempt with malformed token")
			return nil, models.ErrTokenMalformed
		}
		s.logger.Error("Failed to parse refresh token", zap.Error(err))
		return nil, models.ErrInvalidToken // Общая ошибка для остальных случаев
	}

	if claims, ok := token.Claims.(*domain.Claims); ok && token.Valid {
		refreshUUID := claims.ID
		s.logger.Debug("Refresh token parsed successfully", zap.Uint64("userID", claims.UserID), zap.String("refreshUUID", refreshUUID))

		userID, err := s.tokenRepo.GetUserIDByRefreshUUID(ctx, refreshUUID)
		if err != nil {
			if errors.Is(err, models.ErrTokenNotFound) {
				s.logger.Warn("Refresh attempt with invalid/revoked token in store", zap.String("refreshUUID", refreshUUID))
				return nil, models.ErrTokenNotFound
			}
			// Ошибка репозитория уже залогирована
			s.logger.Error("Error checking refresh token existence via repository", zap.Error(err), zap.String("refreshUUID", refreshUUID))
			return nil, fmt.Errorf("error checking refresh token existence: %w", err)
		}

		if userID != claims.UserID {
			s.logger.Error("Refresh token user ID mismatch", zap.Uint64("tokenUserID", claims.UserID), zap.Uint64("repoUserID", userID), zap.String("refreshUUID", refreshUUID))
			return nil, models.ErrInvalidToken
		}

		s.logger.Debug("Refresh token verified against store", zap.Uint64("userID", userID), zap.String("refreshUUID", refreshUUID))

		// --- Логика удаления и создания ---
		newTd, err := s.createTokens(ctx, claims.UserID)
		if err != nil {
			// Ошибка уже залогирована в createTokens
			return nil, fmt.Errorf("failed to create new tokens during refresh: %w", err)
		}

		// Пытаемся удалить старые токены
		_, delErr := s.tokenRepo.DeleteTokens(ctx, "", refreshUUID) // Репо залогирует детали
		if delErr != nil {
			// Логируем здесь, т.к. это некритично для пользователя, но важно для нас
			s.logger.Error("Non-critical: Failed to delete old refresh token during refresh process", zap.Error(delErr), zap.String("refreshUUID", refreshUUID))
		}

		// Сохраняем новые токены
		err = s.tokenRepo.SetToken(ctx, claims.UserID, newTd)
		if err != nil {
			// Ошибка уже залогирована репозиторием
			s.logger.Error("Failed to save new token details via repository during refresh", zap.Error(err), zap.Uint64("userID", claims.UserID))
			return nil, fmt.Errorf("failed to save new token details: %w", err)
		}

		s.logger.Info("Token refreshed successfully", zap.Uint64("userID", claims.UserID))
		return newTd, nil

	}

	s.logger.Warn("Refresh attempt with invalid token structure or signature")
	return nil, models.ErrInvalidToken
}

// VerifyAccessToken parses and validates an access token string.
func (s *authServiceImpl) VerifyAccessToken(ctx context.Context, tokenString string) (*domain.Claims, error) {
	s.logger.Debug("Verifying access token") // Не логируем сам токен
	token, err := jwt.ParseWithClaims(tokenString, &domain.Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.cfg.JWTSecret), nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			s.logger.Debug("Access token verification failed: expired")
			return nil, models.ErrTokenExpired
		}
		if errors.Is(err, jwt.ErrTokenMalformed) {
			s.logger.Warn("Access token verification failed: malformed")
			return nil, models.ErrTokenMalformed
		}
		s.logger.Error("Failed to parse access token", zap.Error(err))
		return nil, models.ErrInvalidToken // Общая ошибка на остальные случаи парсинга
	}

	if claims, ok := token.Claims.(*domain.Claims); ok && token.Valid {
		accessUUID := claims.ID
		s.logger.Debug("Access token parsed successfully", zap.Uint64("userID", claims.UserID), zap.String("accessUUID", accessUUID))
		_, err := s.tokenRepo.GetUserIDByAccessUUID(ctx, accessUUID)
		if err != nil {
			if errors.Is(err, models.ErrTokenNotFound) {
				s.logger.Debug("Access token not found in store (revoked/logged out)", zap.String("accessUUID", accessUUID))
				return nil, models.ErrInvalidToken // Возвращаем общую ошибку "невалидный токен"
			}
			// Ошибка репозитория уже залогирована
			s.logger.Error("Error checking access token existence via repository", zap.Error(err), zap.String("accessUUID", accessUUID))
			return nil, fmt.Errorf("error checking access token existence: %w", err)
		}
		s.logger.Debug("Access token verified successfully against store", zap.Uint64("userID", claims.UserID), zap.String("accessUUID", accessUUID))
		return claims, nil
	}

	s.logger.Warn("Access token verification failed (invalid claims type or signature)")
	return nil, models.ErrInvalidToken
}

// GenerateInterServiceToken creates a short-lived token for internal service communication.
func (s *authServiceImpl) GenerateInterServiceToken(ctx context.Context, serviceName string) (string, error) {
	s.logger.Info("Generating inter-service token", zap.String("targetService", serviceName))
	claims := jwt.RegisteredClaims{
		Issuer:    s.cfg.ServiceID,
		Subject:   serviceName,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.cfg.InterServiceTTL)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ID:        uuid.NewString(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString([]byte(s.cfg.InterServiceSecret))
	if err != nil {
		s.logger.Error("Failed to sign inter-service token", zap.Error(err))
		return "", fmt.Errorf("failed to sign inter-service token: %w", err)
	}
	return signedToken, nil
}

// VerifyInterServiceToken validates a token presumably issued by another internal service or this service.
func (s *authServiceImpl) VerifyInterServiceToken(ctx context.Context, tokenString string) (string, error) {
	s.logger.Debug("Verifying inter-service token")
	token, err := jwt.ParseWithClaims(tokenString, &jwt.RegisteredClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.cfg.InterServiceSecret), nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			s.logger.Debug("Inter-service token verification failed: expired")
			return "", models.ErrTokenExpired
		}
		if errors.Is(err, jwt.ErrTokenMalformed) {
			s.logger.Warn("Inter-service token verification failed: malformed")
			return "", models.ErrTokenMalformed
		}
		s.logger.Error("Failed to parse inter-service token", zap.Error(err))
		return "", models.ErrInvalidToken
	}

	if claims, ok := token.Claims.(*jwt.RegisteredClaims); ok && token.Valid {
		// Potentially add checks for Issuer or Audience if needed
		s.logger.Debug("Inter-service token verified successfully", zap.String("subject", claims.Subject), zap.String("issuer", claims.Issuer))
		return claims.Subject, nil
	}

	s.logger.Warn("Inter-service token verification failed (invalid claims type or signature)")
	return "", models.ErrInvalidToken
}

// --- Helper Functions ---

// hashPassword generates a bcrypt hash of the password using a salt.
func hashPassword(password, salt string) (string, error) {
	// Combine password and salt before hashing
	saltedPassword := password + salt
	bytes, err := bcrypt.GenerateFromPassword([]byte(saltedPassword), bcrypt.DefaultCost)
	return string(bytes), err
}

// checkPasswordHash compares a plain text password with a stored hash.
func checkPasswordHash(password, salt, hash string) bool {
	saltedPassword := password + salt
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(saltedPassword))
	return err == nil
}

// createTokens generates new access and refresh tokens for a user.
func (s *authServiceImpl) createTokens(ctx context.Context, userID uint64) (*models.TokenDetails, error) {
	s.logger.Debug("Creating new token pair", zap.Uint64("userID", userID))
	td := &models.TokenDetails{}
	td.AtExpires = time.Now().Add(s.cfg.AccessTokenTTL).Unix()
	td.AccessUUID = uuid.NewString()

	td.RtExpires = time.Now().Add(s.cfg.RefreshTokenTTL).Unix()
	td.RefreshUUID = uuid.NewString()

	var err error

	// Creating Access Token
	atClaims := domain.Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Unix(td.AtExpires, 0)),
			ID:        td.AccessUUID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    s.cfg.ServiceID,
		},
	}
	at := jwt.NewWithClaims(jwt.SigningMethodHS256, atClaims)
	td.AccessToken, err = at.SignedString([]byte(s.cfg.JWTSecret))
	if err != nil {
		s.logger.Error("Failed to sign access token", zap.Error(err), zap.Uint64("userID", userID))
		return nil, fmt.Errorf("failed to sign access token: %w", err)
	}

	// Creating Refresh Token
	rtClaims := domain.Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Unix(td.RtExpires, 0)),
			ID:        td.RefreshUUID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    s.cfg.ServiceID,
		},
	}
	rt := jwt.NewWithClaims(jwt.SigningMethodHS256, rtClaims)
	td.RefreshToken, err = rt.SignedString([]byte(s.cfg.JWTSecret))
	if err != nil {
		s.logger.Error("Failed to sign refresh token", zap.Error(err), zap.Uint64("userID", userID))
		return nil, fmt.Errorf("failed to sign refresh token: %w", err)
	}
	return td, nil
}
