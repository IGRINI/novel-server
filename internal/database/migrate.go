package database

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migration представляет информацию о миграции
type Migration struct {
	Version int
	Up      string
	Down    string
}

// RunMigrations выполняет все миграции из указанной директории
func RunMigrations(ctx context.Context, db *pgxpool.Pool, migrationsDir string) error {
	log.Printf("[DB] Starting migrations from directory: %s", migrationsDir)

	// Создаем таблицу для отслеживания миграций, если её нет
	if err := createMigrationsTable(ctx, db); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	// Получаем список выполненных миграций
	applied, err := getAppliedMigrations(ctx, db)
	if err != nil {
		return fmt.Errorf("failed to get applied migrations: %w", err)
	}

	// Получаем список файлов миграций
	files, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	// Сортируем файлы по имени (они должны быть в формате 001_name.sql, 002_name.sql и т.д.)
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() < files[j].Name()
	})

	// Применяем каждую миграцию
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".sql") {
			continue
		}

		version := getMigrationVersion(file.Name())
		if version == 0 {
			log.Printf("[DB] Skipping invalid migration file: %s", file.Name())
			continue
		}

		// Пропускаем уже примененные миграции
		if applied[version] {
			log.Printf("[DB] Migration %d already applied", version)
			continue
		}

		// Читаем и применяем миграцию
		if err := applyMigration(ctx, db, filepath.Join(migrationsDir, file.Name()), version); err != nil {
			return fmt.Errorf("failed to apply migration %d: %w", version, err)
		}

		log.Printf("[DB] Successfully applied migration %d", version)
	}

	return nil
}

// createMigrationsTable создает таблицу для отслеживания миграций
func createMigrationsTable(ctx context.Context, db *pgxpool.Pool) error {
	sql := `
		CREATE TABLE IF NOT EXISTS migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)`
	_, err := db.Exec(ctx, sql)
	return err
}

// getAppliedMigrations возвращает список уже примененных миграций
func getAppliedMigrations(ctx context.Context, db *pgxpool.Pool) (map[int]bool, error) {
	sql := `SELECT version FROM migrations`
	rows, err := db.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

// getMigrationVersion извлекает версию миграции из имени файла
func getMigrationVersion(filename string) int {
	var version int
	_, err := fmt.Sscanf(filename, "%d_", &version)
	if err != nil {
		return 0
	}
	return version
}

// applyMigration применяет одну миграцию
func applyMigration(ctx context.Context, db *pgxpool.Pool, filepath string, version int) error {
	content, err := os.ReadFile(filepath)
	if err != nil {
		return err
	}

	// Разделяем миграцию на Up и Down части
	parts := strings.Split(string(content), "-- +migrate Down")
	if len(parts) != 2 {
		return fmt.Errorf("invalid migration file format: %s", filepath)
	}

	upSQL := strings.TrimPrefix(parts[0], "-- +migrate Up")
	upSQL = strings.TrimSpace(upSQL)

	// Начинаем транзакцию
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Выполняем миграцию
	if _, err := tx.Exec(ctx, upSQL); err != nil {
		return fmt.Errorf("failed to execute migration: %w", err)
	}

	// Отмечаем миграцию как выполненную
	if _, err := tx.Exec(ctx, "INSERT INTO migrations (version) VALUES ($1)", version); err != nil {
		return fmt.Errorf("failed to mark migration as applied: %w", err)
	}

	// Подтверждаем транзакцию
	return tx.Commit(ctx)
}
