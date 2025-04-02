package migration

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/rs/zerolog/log"
)

// Config содержит настройки для миграций
type Config struct {
	MigrationsPath string
	MigrationsFS   fs.FS
}

// Migrator выполняет миграции базы данных
type Migrator struct {
	config Config
	pool   *pgxpool.Pool
}

// NewMigrator создает новый экземпляр Migrator
func NewMigrator(config Config, pool *pgxpool.Pool) *Migrator {
	return &Migrator{
		config: config,
		pool:   pool,
	}
}

// Up применяет все доступные миграции
func (m *Migrator) Up(ctx context.Context) error {
	migrator, err := m.createMigrator(ctx)
	if err != nil {
		return fmt.Errorf("failed to create migrator: %w", err)
	}
	defer migrator.Close()

	if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	log.Info().Msg("database migrations applied successfully")
	return nil
}

// Down откатывает все миграции
func (m *Migrator) Down(ctx context.Context) error {
	migrator, err := m.createMigrator(ctx)
	if err != nil {
		return fmt.Errorf("failed to create migrator: %w", err)
	}
	defer migrator.Close()

	if err := migrator.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to rollback migrations: %w", err)
	}

	log.Info().Msg("database migrations rolled back successfully")
	return nil
}

// ForceVersion устанавливает версию миграции принудительно
func (m *Migrator) ForceVersion(ctx context.Context, version uint) error {
	migrator, err := m.createMigrator(ctx)
	if err != nil {
		return fmt.Errorf("failed to create migrator: %w", err)
	}
	defer migrator.Close()

	if err := migrator.Force(int(version)); err != nil {
		return fmt.Errorf("failed to force migration version: %w", err)
	}

	log.Info().Uint("version", version).Msg("database migration version forced")
	return nil
}

// Version возвращает текущую версию миграции
func (m *Migrator) Version(ctx context.Context) (uint, bool, error) {
	migrator, err := m.createMigrator(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("failed to create migrator: %w", err)
	}
	defer migrator.Close()

	version, dirty, err := migrator.Version()
	if err != nil {
		if errors.Is(err, migrate.ErrNilVersion) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("failed to get migration version: %w", err)
	}

	return uint(version), dirty, nil
}

// createMigrator создает экземпляр migrate.Migrate
func (m *Migrator) createMigrator(ctx context.Context) (*migrate.Migrate, error) {
	conn, err := m.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	// Создаем sql.DB из pgx коннекта
	db := stdlib.OpenDBFromPool(m.pool)

	// Создаем драйвер для базы данных
	driver, err := postgres.WithInstance(db, &postgres.Config{
		MigrationsTable:       "schema_migrations",
		MigrationsTableQuoted: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create postgres driver: %w", err)
	}

	// Создаем источник миграций из FS
	source, err := iofs.New(m.config.MigrationsFS, m.config.MigrationsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create source driver: %w", err)
	}

	// Создаем migrate экземпляр
	migrator, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		return nil, fmt.Errorf("failed to create migrator: %w", err)
	}

	// Устанавливаем таймаут для миграций
	migrator.LockTimeout = 30 * time.Second

	return migrator, nil
}
