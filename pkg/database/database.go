package database

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Database представляет подключение к базе данных
type Database struct {
	Pool *pgxpool.Pool
}

// Config содержит настройки для подключения к базе данных
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string
}

// New создает новое подключение к базе данных PostgreSQL
func New(cfg Config) (*Database, error) {
	connString := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.DBName, cfg.SSLMode,
	)

	// Настраиваем конфигурацию пула
	poolConfig, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("ошибка при разборе строки подключения: %w", err)
	}

	// Устанавливаем максимальное количество соединений
	poolConfig.MaxConns = 10

	// Создаем пул соединений
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать пул подключений: %w", err)
	}

	// Проверяем подключение к базе данных
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("не удалось подключиться к базе данных: %w", err)
	}

	log.Println("Успешное подключение к базе данных PostgreSQL")

	return &Database{
		Pool: pool,
	}, nil
}

// Close закрывает подключение к базе данных
func (db *Database) Close() {
	if db.Pool != nil {
		db.Pool.Close()
		log.Println("Подключение к базе данных закрыто")
	}
}

// ExecuteInTransaction выполняет функцию в транзакции
func (db *Database) ExecuteInTransaction(ctx context.Context, fn func(tx pgx.Tx) error) error {
	// Начинаем транзакцию
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("ошибка при начале транзакции: %w", err)
	}

	// Выполняем функцию
	if err := fn(tx); err != nil {
		// Откатываем транзакцию в случае ошибки
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			return fmt.Errorf("ошибка при выполнении транзакции: %w (ошибка отката: %v)", err, rbErr)
		}
		return err
	}

	// Фиксируем транзакцию
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("ошибка при фиксации транзакции: %w", err)
	}

	return nil
}
