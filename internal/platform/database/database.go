package database

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
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
	log.Printf("Checking if target database '%s' exists...", targetDBName)
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
		log.Printf("Database '%s' does not exist. Attempting to create...", targetDBName)
		// ВАЖНО: Пользователь %s должен иметь права CREATEDB!
		log.Printf("IMPORTANT: User '%s' must have CREATEDB privileges in PostgreSQL!", user)
		createQuery := fmt.Sprintf("CREATE DATABASE \"%s\"", targetDBName) // Используем кавычки на случай спецсимволов
		_, err = adminConn.Exec(ctx, createQuery)
		if err != nil {
			// Проверяем на ошибку 'permission denied'
			if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "42501" { // 42501 = insufficient_privilege
				log.Printf("ERROR: User '%s' does not have permission to create database '%s'. Please grant CREATEDB privilege or create the database manually.", user, targetDBName)
				return nil, fmt.Errorf("user '%s' lacks permission to create database '%s': %w", user, targetDBName, err)
			}
			return nil, fmt.Errorf("failed to create database '%s': %w", targetDBName, err)
		}
		log.Printf("Database '%s' created successfully.", targetDBName)
	} else {
		log.Printf("Database '%s' already exists.", targetDBName)
	}
	// Закрываем админское соединение после проверки/создания
	adminConn.Close(ctx)
	// ---------------------------------------------------------

	// --- Шаг 2: Подключение к целевой БД с использованием пула ---
	log.Printf("Connecting to target database '%s' using connection pool...", targetDBName)
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
