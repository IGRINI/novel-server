package database

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"
)

// InitDB инициализирует подключение к базе данных и выполняет миграции
func InitDB(ctx context.Context) (*pgxpool.Pool, error) {
	config := NewConfig()
	log.Printf("[DB] Connecting to database at %s:%d", config.Host, config.Port)

	// Создаем пул соединений
	db, err := pgxpool.New(ctx, config.ConnectionString())
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Проверяем соединение
	if err := db.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	log.Printf("[DB] Successfully connected to database")

	// Получаем путь к директории с миграциями
	migrationsDir := filepath.Join("migrations")
	if _, err := os.Stat(migrationsDir); os.IsNotExist(err) {
		log.Printf("[DB] Migrations directory not found: %s", migrationsDir)
		return db, nil
	}

	// Выполняем миграции
	if err := RunMigrations(ctx, db, migrationsDir); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	log.Printf("[DB] Database initialization completed successfully")
	return db, nil
}

// CloseDB закрывает соединение с базой данных
func CloseDB(db *pgxpool.Pool) {
	if db != nil {
		db.Close()
		log.Printf("[DB] Database connection closed")
	}
}
