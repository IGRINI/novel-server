package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"net/mail"
	"novel-server/auth/internal/config"
	"novel-server/auth/internal/domain"
	interfaces "novel-server/shared/interfaces"
	"novel-server/shared/models"
	"strconv"
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
	if logger == nil {
		log.Println("CRITICAL: Logger passed to NewAuthService is nil!") // Use stdlib log as fallback
	} else {
		logger.Info("Initializing AuthService with logger") // Use the passed logger
	}
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

	// Используем перец перед хешированием
	hashedPassword, err := hashPassword(password, s.cfg.PasswordPepper)
	if err != nil {
		s.logger.Error("Failed to hash password during registration", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	user := &models.User{
		Username:     username,
		Email:        email,
		PasswordHash: hashedPassword,
		Roles:        []string{"ROLE_USER"},
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

	// Используем перец при проверке
	if !checkPasswordHash(password, user.PasswordHash, s.cfg.PasswordPepper) {
		// Логируем неуспешную попытку входа (неверный пароль)
		s.logger.Warn("Login failed: invalid password", zap.String("username", username), zap.Uint64("userID", user.ID))
		return nil, models.ErrInvalidCredentials
	}

	// <<< Проверка на бан >>>
	if user.IsBanned {
		s.logger.Warn("Login failed: user is banned", zap.String("username", username), zap.Uint64("userID", user.ID))
		// Возвращаем стандартную ошибку, чтобы не раскрывать причину
		return nil, models.ErrInvalidCredentials
	}
	// <<< Конец проверки на бан >>>

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

// Logout removes the access and refresh tokens from the store.
func (s *authServiceImpl) Logout(ctx context.Context, accessUUID, refreshUUID string) error {
	log := s.logger.With(zap.String("accessUUID", accessUUID), zap.String("refreshUUID", refreshUUID))
	log.Debug("Attempting to logout user by deleting tokens")

	// Используем DeleteTokens для удаления обоих UUID
	deletedCount, err := s.tokenRepo.DeleteTokens(ctx, accessUUID, refreshUUID)

	if err != nil {
		// Логируем ошибку, но не возвращаем ее клиенту, т.к. токены могли уже быть удалены.
		log.Error("Failed to delete tokens during logout", zap.Error(err))
	}

	if deletedCount > 0 {
		log.Info("Tokens deleted successfully during logout", zap.Int64("deletedCount", deletedCount))
	} else {
		log.Info("No tokens found to delete during logout (already expired or logged out)")
	}

	return nil // Успех, даже если токены уже были удалены
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
		return nil, models.ErrTokenInvalid // Общая ошибка для остальных случаев
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
			return nil, models.ErrTokenInvalid // <<< Исправлено
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
	return nil, models.ErrTokenInvalid // <<< Исправлено
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
		return nil, models.ErrTokenInvalid // Общая ошибка на остальные случаи парсинга
	}

	if claims, ok := token.Claims.(*domain.Claims); ok && token.Valid {
		accessUUID := claims.ID
		s.logger.Debug("Access token parsed successfully", zap.Uint64("userID", claims.UserID), zap.String("accessUUID", accessUUID))
		_, err := s.tokenRepo.GetUserIDByAccessUUID(ctx, accessUUID)
		if err != nil {
			if errors.Is(err, models.ErrTokenNotFound) {
				s.logger.Debug("Access token not found in store (revoked/logged out)", zap.String("accessUUID", accessUUID))
				return nil, models.ErrTokenInvalid // Возвращаем общую ошибку "невалидный токен"
			}
			// Ошибка репозитория уже залогирована
			s.logger.Error("Error checking access token existence via repository", zap.Error(err), zap.String("accessUUID", accessUUID))
			return nil, fmt.Errorf("error checking access token existence: %w", err)
		}
		s.logger.Debug("Access token verified successfully against store", zap.Uint64("userID", claims.UserID), zap.String("accessUUID", accessUUID))
		return claims, nil
	}

	s.logger.Warn("Access token verification failed (invalid claims type or signature)")
	return nil, models.ErrTokenInvalid // <<< Исправлено
}

// GenerateInterServiceToken creates a short-lived JWT for inter-service communication.
// The 'serviceName' will be included as the 'subject' claim.
func (s *authServiceImpl) GenerateInterServiceToken(ctx context.Context, serviceName string) (string, error) {
	s.logger.Debug("Entering GenerateInterServiceToken") // Use Debug level
	s.logger.Info("Generating inter-service token",
		zap.String("targetService", serviceName),
		zap.Duration("configuredTTL", s.cfg.InterServiceTokenTTL))

	now := time.Now()
	// Используем TTL из конфигурации

	// <<< Добавляем детальное логирование >>>
	currentTTL := s.cfg.InterServiceTokenTTL // Читаем значение в локальную переменную
	s.logger.Debug("TTL value just before Add()", zap.Duration("readTTL", currentTTL), zap.Time("now", now))

	expirationTime := now.Add(currentTTL) // Используем локальную переменную

	s.logger.Debug("Expiration time calculated", zap.Time("calculatedExp", expirationTime), zap.Duration("usedTTL", currentTTL))
	// <<< Конец детального логирования >>>

	// Используем кастомные claims, включающие RegisteredClaims
	claims := &models.InterServiceClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.cfg.ServiceID,
			Subject:   serviceName,
			ExpiresAt: jwt.NewNumericDate(expirationTime), // Используем вычисленное время
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
		RequestingService: serviceName, // Добавляем кастомное поле
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString([]byte(s.cfg.InterServiceSecret))
	if err != nil {
		s.logger.Error("Failed to sign inter-service token", zap.Error(err), zap.String("requestingService", serviceName))
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	s.logger.Debug("Inter-service token signed successfully", zap.String("requestingService", serviceName))
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
			// <<< Log details before returning expired error >>>
			var expTime time.Time
			if claims, ok := token.Claims.(*jwt.RegisteredClaims); ok && claims.ExpiresAt != nil {
				expTime = claims.ExpiresAt.Time
			}
			s.logger.Warn("Inter-service token verification failed: expired",
				zap.Time("expiresAt", expTime),
				zap.Time("currentTime", time.Now()))
			return "", models.ErrTokenExpired // Return the original error
		}
		if errors.Is(err, jwt.ErrTokenMalformed) {
			s.logger.Warn("Inter-service token verification failed: malformed")
			return "", models.ErrTokenMalformed
		}
		s.logger.Error("Failed to parse inter-service token", zap.Error(err))
		return "", models.ErrTokenInvalid // <<< Исправлено
	}

	if claims, ok := token.Claims.(*jwt.RegisteredClaims); ok && token.Valid {
		// Potentially add checks for Issuer or Audience if needed
		s.logger.Debug("Inter-service token verified successfully", zap.String("subject", claims.Subject), zap.String("issuer", claims.Issuer))
		return claims.Subject, nil
	}

	s.logger.Warn("Inter-service token verification failed (invalid claims type or signature)")
	return "", models.ErrTokenInvalid // <<< Исправлено
}

// ValidateAndGetClaims проверяет токен и статус пользователя.
func (s *authServiceImpl) ValidateAndGetClaims(ctx context.Context, tokenString string) (*domain.Claims, error) {
	log := s.logger.With(zap.String("operation", "ValidateAndGetClaims"))
	log.Debug("Validating token and user status")

	// 1. Проверяем подпись, срок действия и наличие токена в Redis
	claims, err := s.VerifyAccessToken(ctx, tokenString)
	if err != nil {
		// Ошибка уже залогирована в VerifyAccessToken
		// Возвращаем ошибку (ErrTokenExpired, ErrTokenMalformed, ErrTokenInvalid)
		return nil, err
	}

	// 2. Проверяем статус пользователя (не забанен ли)
	log = log.With(zap.Uint64("userID", claims.UserID))
	user, err := s.userRepo.GetUserByID(ctx, claims.UserID)
	if err != nil {
		if errors.Is(err, models.ErrUserNotFound) {
			// Пользователь из токена не найден в БД - токен невалиден
			log.Warn("User from valid token not found in DB")
			return nil, models.ErrTokenInvalid // Считаем токен невалидным
		}
		// Другая ошибка БД
		log.Error("Failed to get user by ID during token validation", zap.Error(err))
		return nil, fmt.Errorf("failed to get user for validation: %w", err)
	}

	if user.IsBanned {
		log.Warn("Token validation failed: user is banned")
		return nil, models.ErrTokenInvalid // Возвращаем общую ошибку невалидного токена
	}

	log.Debug("Token and user status validated successfully")
	return claims, nil
}

// --- Helper Functions ---

// applyPepper applies HMAC-SHA256 using the pepper as the key.
func applyPepper(password, pepper string) []byte {
	h := hmac.New(sha256.New, []byte(pepper))
	h.Write([]byte(password)) // Неважно, если Write возвращает ошибку, она всегда nil для sha256
	return h.Sum(nil)
}

// hashPassword generates a bcrypt hash of the password after applying the pepper.
func hashPassword(password, pepper string) (string, error) {
	// Применяем перец к паролю через HMAC-SHA256
	pepperedPassword := applyPepper(password, pepper)
	// Хешируем результат с помощью bcrypt (он сам добавит свою соль)
	bytes, err := bcrypt.GenerateFromPassword(pepperedPassword, bcrypt.DefaultCost)
	return string(bytes), err
}

// checkPasswordHash compares a plain text password (after applying pepper) with a stored hash.
func checkPasswordHash(password, hash, pepper string) bool {
	// Применяем тот же перец к введенному паролю
	pepperedPassword := applyPepper(password, pepper)
	// bcrypt сам извлечет свою соль из хеша и сравнит
	err := bcrypt.CompareHashAndPassword([]byte(hash), pepperedPassword)
	return err == nil
}

// createTokens generates new access and refresh tokens for a user.
func (s *authServiceImpl) createTokens(ctx context.Context, userID uint64) (*models.TokenDetails, error) {
	s.logger.Debug("Creating new token pair", zap.Uint64("userID", userID))
	user, err := s.userRepo.GetUserByID(ctx, userID)
	if err != nil {
		s.logger.Error("Failed to get user by ID during token creation", zap.Uint64("userID", userID), zap.Error(err))
		return nil, fmt.Errorf("ошибка получения пользователя для создания токена: %w", err)
	}

	td := &models.TokenDetails{}
	td.AtExpires = time.Now().Add(s.cfg.AccessTokenTTL).Unix()
	td.AccessUUID = uuid.New().String()

	td.RtExpires = time.Now().Add(s.cfg.RefreshTokenTTL).Unix()
	td.RefreshUUID = uuid.New().String()

	// Creating Access Token
	acClaims := &domain.Claims{
		UserID: userID,
		Roles:  user.Roles,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        td.AccessUUID,
			ExpiresAt: jwt.NewNumericDate(time.Unix(td.AtExpires, 0)),
			Subject:   fmt.Sprintf("%d", userID),
			Issuer:    "novel-server-auth",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	acToken := jwt.NewWithClaims(jwt.SigningMethodHS256, acClaims)
	td.AccessToken, err = acToken.SignedString([]byte(s.cfg.JWTSecret))
	if err != nil {
		s.logger.Error("Failed to sign access token", zap.Error(err), zap.Uint64("userID", userID))
		return nil, fmt.Errorf("failed to sign access token: %w", err)
	}

	// Creating Refresh Token
	rcClaims := &domain.Claims{
		UserID: userID,
		Roles:  user.Roles,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        td.RefreshUUID,
			ExpiresAt: jwt.NewNumericDate(time.Unix(td.RtExpires, 0)),
			Subject:   fmt.Sprintf("%d", userID),
			Issuer:    "novel-server-auth",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	rtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, rcClaims)
	td.RefreshToken, err = rtToken.SignedString([]byte(s.cfg.JWTSecret))
	if err != nil {
		s.logger.Error("Failed to sign refresh token", zap.Error(err), zap.Uint64("userID", userID))
		return nil, fmt.Errorf("failed to sign refresh token: %w", err)
	}

	return td, nil
}

// BanUser sets the user's status to banned.
func (s *authServiceImpl) BanUser(ctx context.Context, userID uint64) error {
	log := s.logger.With(zap.Uint64("userID", userID))
	log.Info("Attempting to ban user")
	err := s.userRepo.SetUserBanStatus(ctx, userID, true)
	if err != nil {
		log.Error("Failed to set user ban status", zap.Error(err), zap.Bool("isBanned", true))
		return err // Возвращаем ошибку как есть (может быть ErrUserNotFound)
	}
	log.Info("User banned successfully")

	// <<< Удаляем токены пользователя >>>
	deletedCount, delErr := s.tokenRepo.DeleteTokensByUserID(ctx, userID)
	if delErr != nil {
		// Логируем ошибку удаления токенов, но не возвращаем ее, т.к. бан уже произошел
		log.Error("Failed to delete user tokens after ban", zap.Error(delErr))
	} else {
		log.Info("Deleted user tokens after ban", zap.Int64("deletedCount", deletedCount))
	}
	// <<< Конец удаления токенов >>>

	return nil
}

// UnbanUser sets the user's status to not banned.
func (s *authServiceImpl) UnbanUser(ctx context.Context, userID uint64) error {
	log := s.logger.With(zap.Uint64("userID", userID))
	log.Info("Attempting to unban user")
	err := s.userRepo.SetUserBanStatus(ctx, userID, false)
	if err != nil {
		log.Error("Failed to set user ban status", zap.Error(err), zap.Bool("isBanned", false))
		return err // Возвращаем ошибку как есть (может быть ErrUserNotFound)
	}
	log.Info("User unbanned successfully")
	return nil
}

// UpdateUser обновляет данные пользователя (email, роли, статус бана).
// Использует метод репозитория UpdateUserFields для атомарного обновления.
func (s *authServiceImpl) UpdateUser(ctx context.Context, userID uint64, email *string, roles []string, isBanned *bool) error {
	logFields := []zap.Field{zap.Uint64("userID", userID)}
	if email != nil {
		logFields = append(logFields, zap.Stringp("email", email))
	}
	if roles != nil {
		logFields = append(logFields, zap.Strings("roles", roles))
	}
	if isBanned != nil {
		logFields = append(logFields, zap.Boolp("isBanned", isBanned))
	}
	s.logger.Info("Attempting to update user", logFields...)

	// Валидация email, если он передается
	if email != nil {
		*email = strings.ToLower(strings.TrimSpace(*email))
		if _, err := mail.ParseAddress(*email); err != nil {
			s.logger.Warn("Update user attempt with invalid email format", append(logFields, zap.Error(err))...)
			// Используем общую ошибку для невалидных входных данных
			return fmt.Errorf("invalid email format: %w", models.ErrInvalidInput)
		}
	}

	// Вызываем метод репозитория для обновления
	err := s.userRepo.UpdateUserFields(ctx, userID, email, roles, isBanned)
	if err != nil {
		// Логирование уже произошло в репозитории или здесь (для ErrInvalidInput)
		// Просто возвращаем ошибку (может быть ErrUserNotFound, ErrEmailAlreadyExists, ErrInvalidInput или другая ошибка БД)
		return err
	}

	// Если пользователя забанили, нужно удалить его токены
	if isBanned != nil && *isBanned {
		s.logger.Info("User was banned during update, deleting tokens", zap.Uint64("userID", userID))
		deletedCount, delErr := s.tokenRepo.DeleteTokensByUserID(ctx, userID)
		if delErr != nil {
			// Логируем ошибку удаления токенов, но не возвращаем ее, т.к. обновление уже произошло
			s.logger.Error("Failed to delete user tokens after ban during update", zap.Error(delErr), zap.Uint64("userID", userID))
		} else {
			s.logger.Info("Deleted user tokens after ban during update", zap.Int64("deletedCount", deletedCount), zap.Uint64("userID", userID))
		}
	}

	s.logger.Info("User updated successfully", logFields...)
	return nil
}

// UpdatePassword обновляет пароль пользователя и инвалидирует его текущие токены.
func (s *authServiceImpl) UpdatePassword(ctx context.Context, userID uint64, newPassword string) error {
	log := s.logger.With(zap.Uint64("userID", userID))
	log.Info("Attempting to update user password")

	// Проверяем, существует ли пользователь (дополнительная проверка)
	_, err := s.userRepo.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, models.ErrUserNotFound) {
			log.Warn("Attempted to update password for non-existent user")
		}
		// Логирование уже произошло в GetUserByID или здесь
		return err // Возвращаем ошибку (может быть ErrUserNotFound)
	}

	// Генерируем хеш нового пароля с перцем
	newPasswordHash, err := hashPassword(newPassword, s.cfg.PasswordPepper)
	if err != nil {
		log.Error("Failed to hash new password during update", zap.Error(err))
		return fmt.Errorf("failed to hash new password: %w", err)
	}

	// Обновляем хеш в базе данных
	err = s.userRepo.UpdatePasswordHash(ctx, userID, newPasswordHash)
	if err != nil {
		// Логирование уже произошло в UpdatePasswordHash
		return err // Возвращаем ошибку (может быть ErrUserNotFound)
	}

	log.Info("User password hash updated successfully, invalidating tokens...")

	// Инвалидируем все токены пользователя
	deletedCount, delErr := s.tokenRepo.DeleteTokensByUserID(ctx, userID)
	if delErr != nil {
		// Логируем ошибку удаления токенов, но не возвращаем ее, т.к. пароль уже обновлен
		log.Error("Failed to delete user tokens after password update", zap.Error(delErr))
	} else {
		log.Info("Deleted user tokens after password update", zap.Int64("deletedCount", deletedCount))
	}

	log.Info("User password updated and tokens invalidated successfully")
	return nil
}

// --- Новый метод для обновления токена администратора ---

// RefreshAdminToken validates an admin's refresh token, checks admin role, generates new tokens, and returns them with claims.
func (s *authServiceImpl) RefreshAdminToken(ctx context.Context, refreshTokenString string) (*models.TokenDetails, *models.Claims, error) {
	log := s.logger.With(zap.String("method", "RefreshAdminToken"))
	log.Info("Admin token refresh attempt") // Не логируем сам токен

	// 1. Парсим и валидируем подпись Refresh токена
	token, err := jwt.ParseWithClaims(refreshTokenString, &domain.Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.cfg.JWTSecret), nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			log.Warn("Admin refresh attempt with expired token")
			return nil, nil, models.ErrTokenExpired
		}
		if errors.Is(err, jwt.ErrTokenMalformed) {
			log.Warn("Admin refresh attempt with malformed token")
			return nil, nil, models.ErrTokenMalformed
		}
		log.Error("Failed to parse admin refresh token", zap.Error(err))
		return nil, nil, models.ErrTokenInvalid // Общая ошибка для остальных случаев
	}

	// 2. Проверяем валидность токена и извлекаем клеймы
	claims, ok := token.Claims.(*domain.Claims)
	if !ok || !token.Valid {
		log.Warn("Admin refresh attempt with invalid token claims or signature")
		return nil, nil, models.ErrTokenInvalid
	}

	refreshUUID := claims.ID
	userID := claims.UserID
	log = log.With(zap.Uint64("userID", userID), zap.String("refreshUUID", refreshUUID))
	log.Debug("Admin refresh token parsed successfully")

	// 3. Проверяем наличие Refresh токена в хранилище (Redis)
	storedUserID, err := s.tokenRepo.GetUserIDByRefreshUUID(ctx, refreshUUID)
	if err != nil {
		if errors.Is(err, models.ErrTokenNotFound) {
			log.Warn("Admin refresh attempt with invalid/revoked token in store")
			return nil, nil, models.ErrTokenNotFound // Токен не найден (возможно, уже вышел)
		}
		log.Error("Error checking admin refresh token existence via repository", zap.Error(err))
		return nil, nil, fmt.Errorf("error checking refresh token existence: %w", err) // Ошибка репозитория
	}

	// 4. Сверяем UserID из токена и из хранилища
	if storedUserID != userID {
		log.Error("Admin refresh token user ID mismatch", zap.Uint64("tokenUserID", userID), zap.Uint64("repoUserID", storedUserID))
		// Если ID не совпадают, это серьезная проблема, возможно, попытка подмены.
		// Удаляем токены из хранилища на всякий случай.
		_, _ = s.tokenRepo.DeleteTokens(ctx, "", refreshUUID) // Игнорируем ошибку удаления
		return nil, nil, models.ErrTokenInvalid
	}

	log.Debug("Admin refresh token verified against store")

	// 5. Получаем данные пользователя из БД, чтобы проверить роль
	user, err := s.userRepo.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, models.ErrUserNotFound) {
			log.Error("User associated with admin refresh token not found in DB", zap.Error(err))
			// Пользователя нет, хотя токен был валиден? Очень странно.
			// Удаляем токены на всякий случай.
			_, _ = s.tokenRepo.DeleteTokens(ctx, "", refreshUUID)
			return nil, nil, models.ErrUserNotFound
		}
		log.Error("Error fetching user details from repository for admin refresh", zap.Error(err))
		return nil, nil, fmt.Errorf("error fetching user details: %w", err)
	}

	// 6. Проверяем, забанен ли пользователь
	if user.IsBanned {
		log.Warn("Admin refresh attempt for a banned user")
		// Удаляем токены забаненного пользователя
		_, _ = s.tokenRepo.DeleteTokens(ctx, "", refreshUUID)
		return nil, nil, models.ErrForbidden // Используем 403 Forbidden
	}

	// 7. Проверяем наличие роли администратора
	if !models.HasRole(user.Roles, models.RoleAdmin) {
		log.Warn("Refresh attempt by non-admin user using admin endpoint")
		// Пользователь не админ, но пытается использовать админский рефреш?!
		// Удаляем его токены.
		_, _ = s.tokenRepo.DeleteTokens(ctx, "", refreshUUID)
		return nil, nil, models.ErrForbidden // 403 Forbidden - нет прав
	}

	log.Debug("Admin role verified for user")

	// 8. Генерируем новую пару токенов
	newTd, err := s.createTokens(ctx, userID)
	if err != nil {
		// Ошибка уже залогирована в createTokens
		return nil, nil, fmt.Errorf("failed to create new tokens during admin refresh: %w", err)
	}

	// 9. Удаляем старый Refresh токен и сохраняем новые
	// Сначала удаляем старый
	_, delErr := s.tokenRepo.DeleteTokens(ctx, "", refreshUUID) // Удаляем только старый refresh UUID
	if delErr != nil {
		log.Error("Failed to delete old refresh token during admin refresh", zap.Error(delErr))
		// Это не должно блокировать возврат новых токенов, но логируем ошибку.
	}
	// Затем сохраняем новые
	err = s.tokenRepo.SetToken(ctx, userID, newTd)
	if err != nil {
		// Ошибка уже залогирована репозиторием
		log.Error("Failed to save new token details via repository during admin refresh", zap.Error(err))
		// Если не смогли сохранить новые токены, то это критично
		return nil, nil, fmt.Errorf("failed to save new token details: %w", err)
	}

	// 10. Создаем новые Claims на основе пользователя и нового Access Token UUID
	newClaims := &models.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatUint(user.ID, 10),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.cfg.AccessTokenTTL)), // <<< Исправлено: Используем AccessTokenTTL
			ID:        newTd.AccessUUID,                                         // Используем UUID нового Access токена
		},
		UserID: user.ID,
		Roles:  user.Roles, // Передаем актуальные роли
	}

	log.Info("Admin token refreshed successfully")
	return newTd, newClaims, nil
}
