package database

import (
	"context"
	"fmt"
	"novel-server/internal/logger"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"
)

// InitDB инициализирует подключение к базе данных и выполняет миграции
func InitDB(ctx context.Context) (*pgxpool.Pool, error) {
	config := NewConfig()
	logger.Logger.Info("Connecting to database", "host", config.Host, "port", config.Port)

	// Создаем пул соединений
	db, err := pgxpool.New(ctx, config.ConnectionString())
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Проверяем соединение
	if err := db.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	logger.Logger.Info("Successfully connected to database")

	// Получаем путь к директории с миграциями
	migrationsDir := filepath.Join("migrations")
	if _, err := os.Stat(migrationsDir); os.IsNotExist(err) {
		logger.Logger.Warn("Migrations directory not found", "dir", migrationsDir)
		return db, nil
	}

	// Выполняем миграции
	if err := RunMigrations(ctx, db, migrationsDir); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	logger.Logger.Info("Database initialization completed")
	return db, nil
}

// CloseDB закрывает соединение с базой данных
func CloseDB(db *pgxpool.Pool) {
	if db != nil {
		db.Close()
		logger.Logger.Info("Database connection closed")
	}
}
