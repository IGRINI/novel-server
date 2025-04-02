package database

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ApplyMigrations применяет миграции к базе данных
func ApplyMigrations(pool *pgxpool.Pool, migrationsPath string) error {
	sqlDB, err := sql.Open("postgres", pool.Config().ConnString())
	if err != nil {
		return fmt.Errorf("не удалось создать подключение к БД: %w", err)
	}
	defer sqlDB.Close()

	// Создаем драйвер для миграций
	driver, err := postgres.WithInstance(sqlDB, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("не удалось создать драйвер миграций: %w", err)
	}

	// Создаем источник миграций из встроенных файлов
	d, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("не удалось создать источник миграций: %w", err)
	}

	// Создаем экземпляр migrate
	m, err := migrate.NewWithInstance("iofs", d, "postgres", driver)
	if err != nil {
		return fmt.Errorf("не удалось создать экземпляр migrate: %w", err)
	}

	// Применяем миграции
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("не удалось применить миграции: %w", err)
	}

	return nil
}
