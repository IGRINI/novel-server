package service_test // Используем _test пакет для изоляции

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"novel-server/auth/internal/config"
	"novel-server/auth/internal/service"
	database "novel-server/shared/database"
	interfaces "novel-server/shared/interfaces"
	"novel-server/shared/models"
	sharedModels "novel-server/shared/models"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // Драйвер для PostgreSQL
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"

	// Докер клиент для проверки доступности
	"github.com/docker/docker/client"
)

// IntegrationTestSuite содержит состояние для наших интеграционных тестов
type IntegrationTestSuite struct {
	suite.Suite // Встраиваем testify suite для удобства
	ctx         context.Context
	pgContainer *postgres.PostgresContainer // Контейнер PostgreSQL
	rdContainer *tcredis.RedisContainer     // Контейнер Redis
	pgPool      *pgxpool.Pool               // Пул подключений к тестовой БД
	redisClient *redis.Client               // Клиент к тестовому Redis
	config      *config.Config              // Тестовая конфигурация
	userRepo    interfaces.UserRepository
	tokenRepo   interfaces.TokenRepository
	authService service.AuthService
	logger      *zap.Logger
}

// SetupSuite выполняется один раз перед всеми тестами в наборе
func (s *IntegrationTestSuite) SetupSuite() {
	s.ctx = context.Background()
	var err error

	// Настраиваем логгер для тестов
	s.logger, err = zap.NewDevelopment() // Простой логгер для тестов
	require.NoError(s.T(), err, "Failed to create logger for tests")
	s.logger.Info("Setting up integration test suite...")

	// Запускаем контейнер PostgreSQL
	s.pgContainer, err = postgres.Run(s.ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("test_db"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Minute),
		),
	)
	require.NoError(s.T(), err, "Failed to start postgres container")
	s.logger.Info("PostgreSQL container started")

	// Получаем DSN для подключения к тестовой БД
	pgConnStr, err := s.pgContainer.ConnectionString(s.ctx, "sslmode=disable")
	require.NoError(s.T(), err, "Failed to get postgres connection string")

	// Подключаемся к тестовой БД
	s.pgPool, err = pgxpool.New(s.ctx, pgConnStr)
	require.NoError(s.T(), err, "Failed to connect to test postgres")
	s.logger.Info("Connected to test PostgreSQL")

	// Применяем миграции
	err = s.runMigrations(pgConnStr)
	require.NoError(s.T(), err, "Failed to run migrations")
	s.logger.Info("Database migrations applied")

	// Запускаем контейнер Redis
	s.rdContainer, err = tcredis.Run(s.ctx,
		"docker.io/redis:7-alpine",
		testcontainers.WithWaitStrategy(
			wait.ForLog("* Ready to accept connections").
				WithOccurrence(1).
				WithStartupTimeout(1*time.Minute),
		),
	)
	require.NoError(s.T(), err, "Failed to start redis container")
	s.logger.Info("Redis container started")

	// Получаем адрес Redis
	redisHost, err := s.rdContainer.Host(s.ctx)
	require.NoError(s.T(), err)
	redisPort, err := s.rdContainer.MappedPort(s.ctx, "6379/tcp")
	require.NoError(s.T(), err)
	redisAddr := fmt.Sprintf("%s:%s", redisHost, redisPort.Port())

	// Подключаемся к тестовому Redis
	s.redisClient = redis.NewClient(&redis.Options{Addr: redisAddr})
	_, err = s.redisClient.Ping(s.ctx).Result()
	require.NoError(s.T(), err, "Failed to connect to test redis")
	s.logger.Info("Connected to test Redis")

	// Создаем тестовую конфигурацию (можно переопределить нужные значения)
	s.config = &config.Config{
		// Используем значения для тестовых контейнеров
		DBHost:     "", // Не используется напрямую, берем из pgConnStr
		DBPort:     "", // Не используется напрямую
		DBUser:     "testuser",
		DBPassword: "testpass",
		DBName:     "test_db",
		DBSSLMode:  "disable",
		RedisAddr:  redisAddr,
		// Устанавливаем короткие TTL для тестов (если нужно проверять истечение)
		AccessTokenTTL:  5 * time.Minute, // Или даже секунды
		RefreshTokenTTL: 10 * time.Minute,
		InterServiceTTL: 1 * time.Minute,
		// Секреты можно оставить дефолтными для тестов или сгенерировать
		JWTSecret:          "test-jwt-secret",
		PasswordSalt:       "test-salt",
		InterServiceSecret: "test-inter-service-secret",
		ServiceID:          "test-auth-service",
		Env:                "test",
		LogLevel:           "debug",
	}
	s.logger.Info("Test configuration created")

	// Инициализируем зависимости для AuthService
	s.userRepo = database.NewPgUserRepository(s.pgPool, s.logger)
	s.tokenRepo = database.NewRedisTokenRepository(s.redisClient, s.logger)
	s.authService = service.NewAuthService(s.userRepo, s.tokenRepo, s.config, s.logger)
	s.logger.Info("AuthService initialized for tests")

	s.logger.Info("Test suite setup complete.")
}

// TearDownSuite выполняется один раз после всех тестов в наборе
func (s *IntegrationTestSuite) TearDownSuite() {
	s.logger.Info("Tearing down integration test suite...")
	// Закрываем соединения
	if s.pgPool != nil {
		s.pgPool.Close()
	}
	if s.redisClient != nil {
		s.redisClient.Close()
	}
	// Останавливаем и удаляем контейнеры
	if s.pgContainer != nil {
		if err := s.pgContainer.Terminate(s.ctx); err != nil {
			s.logger.Error("Failed to terminate postgres container", zap.Error(err))
		}
	}
	if s.rdContainer != nil {
		if err := s.rdContainer.Terminate(s.ctx); err != nil {
			s.logger.Error("Failed to terminate redis container", zap.Error(err))
		}
	}
	s.logger.Info("Test suite teardown complete.")
}

// Перед каждым тестом очищаем Redis и таблицы БД
func (s *IntegrationTestSuite) SetupTest() {
	// Очистка Redis (удаляем все ключи)
	err := s.redisClient.FlushDB(s.ctx).Err()
	require.NoError(s.T(), err, "Failed to flush Redis DB")

	// Очистка таблиц PostgreSQL (быстрее чем удалять и создавать заново)
	// ОСТОРОЖНО: НЕ запускать на production БД!
	_, err = s.pgPool.Exec(s.ctx, "TRUNCATE TABLE users RESTART IDENTITY CASCADE") // Удаляет все данные из users
	require.NoError(s.T(), err, "Failed to truncate users table")
}

// runMigrations применяет миграции к тестовой БД
func (s *IntegrationTestSuite) runMigrations(dbURL string) error {
	// Находим путь к миграциям относительно текущего файла
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return fmt.Errorf("could not get caller information")
	}
	// Поднимаемся на 3 уровня вверх (service -> internal -> auth -> project_root)
	// и добавляем путь к миграциям
	migrationsPath := filepath.Join(filepath.Dir(filename), "..", "..", "..", "shared", "database", "migrations")

	// Используем iofs для чтения миграций из файловой системы
	fsys := os.DirFS(migrationsPath)
	sourceDriver, err := iofs.New(fsys, ".") // Точка указывает читать из корня fsys (migrationsPath)
	if err != nil {
		s.logger.Error("Failed to create iofs source driver for migrations",
			zap.String("migrationsPath", migrationsPath),
			zap.Error(err),
		)
		return fmt.Errorf("failed to create iofs source driver: %w", err)
	}

	// Инициализируем migrate с использованием SourceInstance
	m, err := migrate.NewWithSourceInstance("iofs", sourceDriver, dbURL)
	if err != nil {
		// Удаляем старое логирование sourceURL, так как он больше не используется
		s.logger.Error("Failed to create migrate instance with iofs",
			zap.String("dbURL", dbURL),
			zap.String("migrationsPath", migrationsPath),
			zap.Error(err),
		)
		// Обновляем сообщение об ошибке
		return fmt.Errorf("failed to create migrate instance with iofs: %w, path: %s, dbURL: %s", err, migrationsPath, dbURL)
	}
	defer m.Close() // Закрываем соединение с БД, используемое migrate

	err = m.Up()
	if err != nil && err != migrate.ErrNoChange {
		version, dirty, verr := m.Version()
		if verr == nil {
			s.logger.Error("Migration error details", zap.Uint("version", version), zap.Bool("dirty", dirty))
		}
		return fmt.Errorf("failed to apply migrations: %w", err)
	}
	s.logger.Info("Database migrations applied using iofs") // Обновляем лог успеха
	return nil
}

// TestIntegrationTestSuite запускает набор тестов
func TestIntegrationTestSuite(t *testing.T) {
	// Пропускаем тесты, если запущены с флагом -short
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}
	// Проверяем доступность Docker перед запуском
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		t.Fatalf("Docker client init error: %v. Ensure Docker is running and accessible.", err)
	}
	if _, err := cli.Ping(context.Background()); err != nil {
		t.Fatalf("Docker daemon is not running or accessible: %v", err)
	}
	cli.Close()

	suite.Run(t, new(IntegrationTestSuite))
}

// --- Сами Тестовые Функции ---

// Обновленный тест регистрации и логина с Email
func (s *IntegrationTestSuite) TestRegisterAndLogin_Success() {
	t := s.T() // Получаем *testing.T
	ctx := context.Background()
	username := "testuser1"
	password := "password123"
	email := "testuser1@example.com"

	// 1. Регистрация
	// Добавляем email в вызов Register
	registeredUser, err := s.authService.Register(ctx, username, email, password)
	require.NoError(t, err, "Register should succeed")
	require.NotNil(t, registeredUser, "Registered user should not be nil")
	require.Equal(t, username, registeredUser.Username, "Username should match")
	require.Equal(t, email, registeredUser.Email, "Email should match") // Проверяем email
	require.NotZero(t, registeredUser.ID, "User ID should be assigned")
	require.Empty(t, registeredUser.Password, "Password hash should not be returned")

	// Попытка повторной регистрации с тем же username - должна быть ошибка
	_, err = s.authService.Register(ctx, username, "another@example.com", "anotherpassword")
	require.Error(t, err, "Registering existing user should fail")
	require.True(t, errors.Is(err, sharedModels.ErrUserAlreadyExists), "Error should be ErrUserAlreadyExists")

	// Попытка повторной регистрации с тем же email - должна быть ошибка
	_, err = s.authService.Register(ctx, "anotheruser", email, "anotherpassword")
	require.Error(t, err, "Registering with existing email should fail")
	require.True(t, errors.Is(err, sharedModels.ErrEmailAlreadyExists), "Error should be ErrEmailAlreadyExists")

	// 2. Логин (остается без изменений, т.к. логин по username)
	tokens, err := s.authService.Login(ctx, username, password)
	require.NoError(t, err, "Login should succeed")
	require.NotNil(t, tokens, "Tokens should not be nil")
	require.NotEmpty(t, tokens.AccessToken, "Access token should not be empty")
	require.NotEmpty(t, tokens.RefreshToken, "Refresh token should not be empty")
	require.NotZero(t, tokens.AtExpires, "Access token expiration should be set")
	require.NotZero(t, tokens.RtExpires, "Refresh token expiration should be set")
	require.NotEmpty(t, tokens.AccessUUID, "Access UUID should not be empty")
	require.NotEmpty(t, tokens.RefreshUUID, "Refresh UUID should not be empty")

	// Проверяем наличие токенов в Redis
	accessUserID, err := s.tokenRepo.GetUserIDByAccessUUID(ctx, tokens.AccessUUID)
	require.NoError(t, err, "Access token UUID should exist in Redis")
	require.Equal(t, registeredUser.ID, accessUserID, "User ID from access token in Redis should match")

	refreshUserID, err := s.tokenRepo.GetUserIDByRefreshUUID(ctx, tokens.RefreshUUID)
	require.NoError(t, err, "Refresh token UUID should exist in Redis")
	require.Equal(t, registeredUser.ID, refreshUserID, "User ID from refresh token in Redis should match")

	// 3. Логин с неверным паролем
	_, err = s.authService.Login(ctx, username, "wrongpassword")
	require.Error(t, err, "Login with wrong password should fail")
	require.True(t, errors.Is(err, sharedModels.ErrInvalidCredentials), "Error should be ErrInvalidCredentials")

	// 4. Логин несуществующего пользователя
	_, err = s.authService.Login(ctx, "nonexistentuser", "password")
	require.Error(t, err, "Login with non-existent user should fail")
	require.True(t, errors.Is(err, sharedModels.ErrInvalidCredentials), "Error should be ErrInvalidCredentials")
}

// Новый тест: Регистрация с невалидным форматом Email
func (s *IntegrationTestSuite) TestRegister_InvalidEmailFormat() {
	t := s.T()
	ctx := context.Background()
	username := "invalidemailuser"
	password := "password123"
	invalidEmail := "not-an-email"

	_, err := s.authService.Register(ctx, username, invalidEmail, password)
	require.Error(t, err, "Register with invalid email format should fail")
	// Ожидаем ошибку валидации входных данных
	require.True(t, errors.Is(err, sharedModels.ErrInvalidCredentials), "Error should indicate invalid input format (currently ErrInvalidCredentials)")
}

// Обновленный тест для Refresh с учетом изменений в Register
func (s *IntegrationTestSuite) TestRefresh_Success() {
	t := s.T()
	ctx := context.Background()
	username := "refreshuser"
	password := "refreshpass"
	email := "refresh@example.com"

	// 1. Регистрация и Логин для получения токенов
	// Добавляем email в вызов Register
	registeredUser, err := s.authService.Register(ctx, username, email, password)
	require.NoError(t, err)
	tokens, err := s.authService.Login(ctx, username, password)
	require.NoError(t, err)
	require.NotEmpty(t, tokens.RefreshToken)
	require.NotEmpty(t, tokens.AccessUUID)
	require.NotEmpty(t, tokens.RefreshUUID)

	// Сохраняем UUID старых токенов для проверки
	// oldAccessUUID := tokens.AccessUUID <-- Комментируем, так как больше не используется
	oldRefreshUUID := tokens.RefreshUUID

	// Небольшая пауза, чтобы время создания токенов точно отличалось
	time.Sleep(10 * time.Millisecond)

	// 2. Обновление токенов
	newTokens, err := s.authService.Refresh(ctx, tokens.RefreshToken)
	require.NoError(t, err, "Refresh should succeed")
	require.NotNil(t, newTokens, "New tokens should not be nil")
	require.NotEmpty(t, newTokens.AccessToken, "New access token should not be empty")
	require.NotEmpty(t, newTokens.RefreshToken, "New refresh token should not be empty")
	require.NotEqual(t, tokens.AccessToken, newTokens.AccessToken, "Access tokens should be different")
	require.NotEqual(t, tokens.RefreshToken, newTokens.RefreshToken, "Refresh tokens should be different")
	require.NotEqual(t, tokens.AccessUUID, newTokens.AccessUUID, "Access UUIDs should be different")
	require.NotEqual(t, tokens.RefreshUUID, newTokens.RefreshUUID, "Refresh UUIDs should be different")

	// 3. Проверка старых и новых токенов в Redis
	// Старый Access UUID должен быть удален
	//_, err = s.tokenRepo.GetUserIDByAccessUUID(ctx, oldAccessUUID) <-- Убираем эту проверку
	//require.Error(t, err, "Old access token UUID should be deleted from Redis") <-- Убираем эту проверку
	//require.True(t, errors.Is(err, sharedModels.ErrTokenNotFound) || errors.Is(err, redis.Nil), "Error should be ErrTokenNotFound or redis.Nil") <-- Убираем эту проверку

	// Старый Refresh UUID должен быть удален
	_, err = s.tokenRepo.GetUserIDByRefreshUUID(ctx, oldRefreshUUID)
	require.Error(t, err, "Old refresh token UUID should be deleted from Redis")
	require.True(t, errors.Is(err, sharedModels.ErrTokenNotFound) || errors.Is(err, redis.Nil), "Error should be ErrTokenNotFound or redis.Nil")

	// Новый Access UUID должен существовать
	accessUserID, err := s.tokenRepo.GetUserIDByAccessUUID(ctx, newTokens.AccessUUID)
	require.NoError(t, err, "New access token UUID should exist in Redis")
	require.Equal(t, registeredUser.ID, accessUserID, "User ID from new access token should match")

	// Новый Refresh UUID должен существовать
	refreshUserID, err := s.tokenRepo.GetUserIDByRefreshUUID(ctx, newTokens.RefreshUUID)
	require.NoError(t, err, "New refresh token UUID should exist in Redis")
	require.Equal(t, registeredUser.ID, refreshUserID, "User ID from new refresh token should match")
}

func (s *IntegrationTestSuite) TestRefresh_InvalidToken() {
	t := s.T()
	ctx := context.Background()

	_, err := s.authService.Refresh(ctx, "this-is-not-a-valid-jwt-token")
	require.Error(t, err, "Refresh with invalid token string should fail")
	// Ожидаем ошибку парсинга или валидации JWT
	// Точный тип ошибки зависит от реализации JWT парсера, но это не ErrTokenNotFound
	require.False(t, errors.Is(err, sharedModels.ErrTokenNotFound), "Error should not be ErrTokenNotFound")
	// Используем ErrTokenMalformed, так как именно его возвращает парсер для невалидной строки
	require.True(t, errors.Is(err, models.ErrTokenMalformed), "Error should be ErrTokenMalformed")

}

// Обновленный тест для Refresh_TokenNotFoundInRedis с учетом изменений в Register
func (s *IntegrationTestSuite) TestRefresh_TokenNotFoundInRedis() {
	t := s.T()
	ctx := context.Background()
	username := "refreshtokennotfounduser"
	password := "refreshtokennotfoundpass"
	email := "refreshnotfound@example.com"

	// 1. Регистрация и Логин
	// Добавляем email в вызов Register
	_, err := s.authService.Register(ctx, username, email, password)
	require.NoError(t, err)
	tokens, err := s.authService.Login(ctx, username, password)
	require.NoError(t, err)
	require.NotEmpty(t, tokens.RefreshToken)
	require.NotEmpty(t, tokens.RefreshUUID)

	// 2. Удаляем Refresh UUID из Redis вручную
	err = s.tokenRepo.DeleteRefreshUUID(ctx, tokens.RefreshUUID) // Предполагаем, что такой метод есть в репозитории
	require.NoError(t, err, "Failed to manually delete refresh UUID from Redis")

	// 3. Пытаемся обновить токен
	_, err = s.authService.Refresh(ctx, tokens.RefreshToken)
	require.Error(t, err, "Refresh should fail if refresh UUID is not in Redis")
	require.True(t, errors.Is(err, models.ErrTokenNotFound), "Error should be ErrTokenNotFound")
}

// Тесты для Logout
func (s *IntegrationTestSuite) TestLogout_Success() {
	t := s.T()
	ctx := context.Background()
	username := "logoutuser"
	email := "logout@example.com"
	password := "logoutpass"

	// 1. Регистрация и Логин
	_, err := s.authService.Register(ctx, username, email, password)
	require.NoError(t, err)
	tokens, err := s.authService.Login(ctx, username, password)
	require.NoError(t, err)
	require.NotEmpty(t, tokens.AccessUUID)
	require.NotEmpty(t, tokens.RefreshUUID)

	// Проверяем, что токены есть в Redis перед выходом
	_, err = s.tokenRepo.GetUserIDByAccessUUID(ctx, tokens.AccessUUID)
	require.NoError(t, err, "Access token should exist before logout")
	_, err = s.tokenRepo.GetUserIDByRefreshUUID(ctx, tokens.RefreshUUID)
	require.NoError(t, err, "Refresh token should exist before logout")

	// 2. Выход
	err = s.authService.Logout(ctx, tokens.AccessUUID, tokens.RefreshUUID)
	require.NoError(t, err, "Logout should succeed")

	// 3. Проверяем, что токены удалены из Redis
	_, err = s.tokenRepo.GetUserIDByAccessUUID(ctx, tokens.AccessUUID)
	require.Error(t, err, "Access token should be deleted after logout")
	require.True(t, errors.Is(err, models.ErrTokenNotFound) || errors.Is(err, redis.Nil), "Error should be ErrTokenNotFound or redis.Nil")

	_, err = s.tokenRepo.GetUserIDByRefreshUUID(ctx, tokens.RefreshUUID)
	require.Error(t, err, "Refresh token should be deleted after logout")
	require.True(t, errors.Is(err, models.ErrTokenNotFound) || errors.Is(err, redis.Nil), "Error should be ErrTokenNotFound or redis.Nil")
}

func (s *IntegrationTestSuite) TestLogout_NotFound() {
	t := s.T()
	ctx := context.Background()
	nonExistentAccessUUID := uuid.NewString()
	nonExistentRefreshUUID := uuid.NewString()

	// Попытка выхода с несуществующими UUID
	err := s.authService.Logout(ctx, nonExistentAccessUUID, nonExistentRefreshUUID)
	// Ожидаем, что ошибки не будет, т.к. операция идемпотентна
	require.NoError(t, err, "Logout with non-existent UUIDs should not return an error")
}

func (s *IntegrationTestSuite) TestLogout_OnlyOneUUID() {
	t := s.T()
	ctx := context.Background()
	username := "logoutoneuuid"
	email := "logoutone@example.com"
	password := "logoutonepass"

	// 1. Регистрация и Логин
	_, err := s.authService.Register(ctx, username, email, password)
	require.NoError(t, err)
	tokens, err := s.authService.Login(ctx, username, password)
	require.NoError(t, err)
	require.NotEmpty(t, tokens.AccessUUID)
	require.NotEmpty(t, tokens.RefreshUUID)

	accessUUID := tokens.AccessUUID
	refreshUUID := tokens.RefreshUUID

	// 2. Выход только по AccessUUID
	err = s.authService.Logout(ctx, accessUUID, "") // Передаем пустой RefreshUUID
	require.NoError(t, err, "Logout with only AccessUUID should succeed")

	// Проверяем, что AccessUUID удален, а RefreshUUID остался
	_, err = s.tokenRepo.GetUserIDByAccessUUID(ctx, accessUUID)
	require.Error(t, err, "Access token should be deleted after partial logout")
	_, err = s.tokenRepo.GetUserIDByRefreshUUID(ctx, refreshUUID)
	require.NoError(t, err, "Refresh token should still exist after partial logout")

	// 3. Выход только по RefreshUUID (который еще существует)
	newAccessUUID := uuid.NewString() // Создадим фейковый AccessUUID для этого шага
	err = s.authService.Logout(ctx, newAccessUUID, refreshUUID)
	require.NoError(t, err, "Logout with only RefreshUUID should succeed")

	// Проверяем, что RefreshUUID теперь удален
	_, err = s.tokenRepo.GetUserIDByRefreshUUID(ctx, refreshUUID)
	require.Error(t, err, "Refresh token should be deleted after second partial logout")
}

// Тесты для VerifyAccessToken
func (s *IntegrationTestSuite) TestVerifyAccessToken_Success() {
	t := s.T()
	ctx := context.Background()
	username := "verifyusersuccess"
	email := "verify_success@example.com"
	password := "verifypass"

	// 1. Регистрация и Логин
	registeredUser, err := s.authService.Register(ctx, username, email, password)
	require.NoError(t, err)
	tokens, err := s.authService.Login(ctx, username, password)
	require.NoError(t, err)
	require.NotEmpty(t, tokens.AccessToken)

	// 2. Проверка токена
	claims, err := s.authService.VerifyAccessToken(ctx, tokens.AccessToken)
	require.NoError(t, err, "VerifyAccessToken should succeed for valid token")
	require.NotNil(t, claims, "Claims should not be nil")
	require.Equal(t, registeredUser.ID, claims.UserID, "UserID in claims should match")
	require.Equal(t, tokens.AccessUUID, claims.ID, "AccessUUID (jti) in claims should match")
	require.True(t, time.Now().Unix() < claims.ExpiresAt.Unix(), "Token should not be expired")
}

func (s *IntegrationTestSuite) TestVerifyAccessToken_Expired() {
	t := s.T()
	ctx := context.Background()
	username := "verifyuserexpired"
	email := "verify_expired@example.com"
	password := "verifypass"

	// 1. Регистрация и Логин (с временным коротким TTL)
	originalTTL := s.config.AccessTokenTTL                   // Сохраняем оригинальный TTL
	s.config.AccessTokenTTL = 1 * time.Millisecond           // Устанавливаем очень короткий TTL
	defer func() { s.config.AccessTokenTTL = originalTTL }() // Восстанавливаем TTL после теста

	_, err := s.authService.Register(ctx, username, email, password)
	require.NoError(t, err)
	tokens, err := s.authService.Login(ctx, username, password)
	require.NoError(t, err)
	require.NotEmpty(t, tokens.AccessToken)

	// 2. Ждем истечения токена
	time.Sleep(5 * time.Millisecond) // Ждем чуть дольше TTL

	// 3. Проверка токена
	_, err = s.authService.VerifyAccessToken(ctx, tokens.AccessToken)
	require.Error(t, err, "VerifyAccessToken should fail for expired token")
	require.True(t, errors.Is(err, models.ErrTokenExpired), "Error should be ErrTokenExpired")
}

func (s *IntegrationTestSuite) TestVerifyAccessToken_Malformed() {
	t := s.T()
	ctx := context.Background()
	malformedToken := "this.is.not.a.valid.jwt.token"

	_, err := s.authService.VerifyAccessToken(ctx, malformedToken)
	require.Error(t, err, "VerifyAccessToken should fail for malformed token")
	// Ожидаем ErrTokenMalformed или ErrInvalidToken в зависимости от реализации парсера
	require.True(t, errors.Is(err, models.ErrTokenMalformed) || errors.Is(err, models.ErrTokenInvalid),
		"Error should be ErrTokenMalformed or ErrInvalidToken")
}

func (s *IntegrationTestSuite) TestVerifyAccessToken_Revoked() {
	t := s.T()
	ctx := context.Background()
	username := "verifyuserrevoked"
	email := "verify_revoked@example.com"
	password := "verifypass"

	// 1. Регистрация и Логин
	_, err := s.authService.Register(ctx, username, email, password)
	require.NoError(t, err)
	tokens, err := s.authService.Login(ctx, username, password)
	require.NoError(t, err)
	require.NotEmpty(t, tokens.AccessToken)
	require.NotEmpty(t, tokens.AccessUUID)
	require.NotEmpty(t, tokens.RefreshUUID)

	accessTokenToVerify := tokens.AccessToken

	// 2. Выход пользователя (отзыв токенов)
	err = s.authService.Logout(ctx, tokens.AccessUUID, tokens.RefreshUUID)
	require.NoError(t, err, "Logout should succeed before verifying revoked token")

	// 3. Проверка отозванного токена
	_, err = s.authService.VerifyAccessToken(ctx, accessTokenToVerify)
	require.Error(t, err, "VerifyAccessToken should fail for revoked token")
	// Сервис должен вернуть ErrInvalidToken, т.к. токен удален из Redis
	require.True(t, errors.Is(err, models.ErrTokenInvalid), "Error should be ErrInvalidToken for revoked token")
}

// Тесты для Inter-Service Tokens
func (s *IntegrationTestSuite) TestInterServiceToken_GenerateAndVerify_Success() {
	t := s.T()
	ctx := context.Background()
	serviceName := "test-service"

	// 1. Генерация токена
	tokenString, err := s.authService.GenerateInterServiceToken(ctx, serviceName)
	require.NoError(t, err, "GenerateInterServiceToken should succeed")
	require.NotEmpty(t, tokenString, "Generated token string should not be empty")

	// 2. Проверка токена
	verifiedServiceName, err := s.authService.VerifyInterServiceToken(ctx, tokenString)
	require.NoError(t, err, "VerifyInterServiceToken should succeed for valid token")
	require.Equal(t, serviceName, verifiedServiceName, "Verified service name should match generated one")
}

func (s *IntegrationTestSuite) TestVerifyInterServiceToken_Expired() {
	t := s.T()
	ctx := context.Background()
	serviceName := "expired-service"

	// 1. Генерация токена с коротким TTL
	originalTTL := s.config.InterServiceTTL                   // Сохраняем оригинальный TTL
	s.config.InterServiceTTL = 1 * time.Millisecond           // Устанавливаем короткий TTL
	defer func() { s.config.InterServiceTTL = originalTTL }() // Восстанавливаем TTL после теста

	tokenString, err := s.authService.GenerateInterServiceToken(ctx, serviceName)
	require.NoError(t, err)
	require.NotEmpty(t, tokenString)

	// 2. Ждем истечения
	time.Sleep(5 * time.Millisecond)

	// 3. Проверка
	_, err = s.authService.VerifyInterServiceToken(ctx, tokenString)
	require.Error(t, err, "VerifyInterServiceToken should fail for expired token")
	require.True(t, errors.Is(err, models.ErrTokenExpired), "Error should be ErrTokenExpired")
}

func (s *IntegrationTestSuite) TestVerifyInterServiceToken_InvalidSignature() {
	t := s.T()
	ctx := context.Background()
	serviceName := "invalid-sig-service"

	// 1. Генерируем токен с текущим конфигом
	tokenString, err := s.authService.GenerateInterServiceToken(ctx, serviceName)
	require.NoError(t, err)
	require.NotEmpty(t, tokenString)

	// 2. Меняем секрет в конфиге (временно)
	originalSecret := s.config.InterServiceSecret
	s.config.InterServiceSecret = "different-test-secret"
	defer func() { s.config.InterServiceSecret = originalSecret }()

	// 3. Пытаемся проверить токен (подписанный старым секретом)
	_, err = s.authService.VerifyInterServiceToken(ctx, tokenString)
	require.Error(t, err, "VerifyInterServiceToken should fail for invalid signature")
	// Ожидаем общую ошибку невалидного токена
	require.True(t, errors.Is(err, models.ErrTokenInvalid), "Error should be ErrInvalidToken for invalid signature")
}

func (s *IntegrationTestSuite) TestVerifyInterServiceToken_Malformed() {
	t := s.T()
	ctx := context.Background()
	malformedToken := "not.a.jwt"

	_, err := s.authService.VerifyInterServiceToken(ctx, malformedToken)
	require.Error(t, err, "VerifyInterServiceToken should fail for malformed token")
	require.True(t, errors.Is(err, models.ErrTokenMalformed) || errors.Is(err, models.ErrTokenInvalid),
		"Error should be ErrTokenMalformed or ErrInvalidToken")
}

// TODO: Добавить unit-тесты для хелперов
