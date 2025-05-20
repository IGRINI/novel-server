package database

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"novel-server/internal/logger"
)

const (
	defaultMaxConns          = 5                // Максимальное количество соединений в пуле
	defaultMinConns          = 1                // Минимальное количество соединений в пуле
	defaultMaxConnLifetime   = time.Hour        // Максимальное время жизни соединения
	defaultMaxConnIdleTime   = 30 * time.Minute // Максимальное время простоя соединения
	defaultHealthCheckPeriod = time.Minute      // Периодичность проверки работоспособности соединения
	defaultConnectTimeout    = 5 * time.Second  // Таймаут подключения
)

// NewDBPool создает и возвращает пул соединений к PostgreSQL.
// Он читает параметры подключения из переменных окружения
// и пытается создать базу данных, если она не существует.
func NewDBPool(ctx context.Context) (*pgxpool.Pool, error) {
	host := os.Getenv("DATABASE_HOST")
	port := os.Getenv("DATABASE_PORT")
	user := os.Getenv("DATABASE_USER")
	password := os.Getenv("DATABASE_PASSWORD")
	targetDBName := os.Getenv("DATABASE_NAME") // Целевая БД
	adminDBName := "postgres"                  // БД для админ. задач (обычно postgres)

	if host == "" {
		host = "localhost"
	}
	if port == "" {
		port = "5432"
	}
	if user == "" || password == "" || targetDBName == "" {
		return nil, fmt.Errorf("DATABASE_USER, DATABASE_PASSWORD, and DATABASE_NAME environment variables must be set")
	}

	// --- Шаг 1: Проверка и создание БД (если необходимо) ---
	logger.Logger.Info("Checking if target database exists", "db", targetDBName)
	// Подключаемся к административной БД (например, postgres)
	adminDsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		user, password, host, port, adminDBName)

	adminConn, err := pgx.Connect(ctx, adminDsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to admin database ('%s') to check target database existence: %w", adminDBName, err)
	}
	defer adminConn.Close(ctx)

	var exists bool
	checkQuery := "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)"
	err = adminConn.QueryRow(ctx, checkQuery, targetDBName).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("failed to check if database '%s' exists: %w", targetDBName, err)
	}

	if !exists {
		logger.Logger.Info("Database does not exist, attempting create", "db", targetDBName)
		// ВАЖНО: Пользователь %s должен иметь права CREATEDB!
		logger.Logger.Warn("User must have CREATEDB privileges", "user", user)
		createQuery := fmt.Sprintf("CREATE DATABASE \"%s\"", targetDBName) // Используем кавычки на случай спецсимволов
		_, err = adminConn.Exec(ctx, createQuery)
		if err != nil {
			// Проверяем на ошибку 'permission denied'
			if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "42501" { // 42501 = insufficient_privilege
				logger.Logger.Error("User lacks permission to create database", "user", user, "db", targetDBName)
				return nil, fmt.Errorf("user '%s' lacks permission to create database '%s': %w", user, targetDBName, err)
			}
			return nil, fmt.Errorf("failed to create database '%s': %w", targetDBName, err)
		}
		logger.Logger.Info("Database created", "db", targetDBName)
	} else {
		logger.Logger.Info("Database already exists", "db", targetDBName)
	}
	// Закрываем админское соединение после проверки/создания
	adminConn.Close(ctx)
	// ---------------------------------------------------------

	// --- Шаг 2: Подключение к целевой БД с использованием пула ---
	logger.Logger.Info("Connecting to target database", "db", targetDBName)
	targetDsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		user, password, host, port, targetDBName)

	config, err := pgxpool.ParseConfig(targetDsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database config for target database '%s': %w", targetDBName, err)
	}

	// Настройка параметров пула
	config.MaxConns = defaultMaxConns
	config.MinConns = defaultMinConns
	config.MaxConnLifetime = defaultMaxConnLifetime
	config.MaxConnIdleTime = defaultMaxConnIdleTime
	config.HealthCheckPeriod = defaultHealthCheckPeriod
	config.ConnConfig.ConnectTimeout = defaultConnectTimeout

	// Создание пула соединений
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create database connection pool: %w", err)
	}

	// Проверка соединения с целевой БД
	err = pool.Ping(ctx)
	if err != nil {
		pool.Close() // Закрываем пул, если пинг не удался
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return pool, nil
}
