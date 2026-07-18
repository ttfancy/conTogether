// Package migrations holds versioned schema migrations for both
// repository backends (sqlite/, postgres/, one .up.sql/.down.sql pair
// per version), embedded into the binary so there's nothing extra to
// ship or mount at deploy time. Apply is called once from each
// repository constructor — safe to call on every process start, since
// golang-migrate tracks which versions have already run (in a
// schema_migrations table it manages) and no-ops once a database is
// current.
//
// This replaces a bare `CREATE TABLE IF NOT EXISTS` at connection time,
// which only ever handles the very first deployment: it can't express
// "add a column" or "add an index" to a database that already has data
// in it, which is the actual point of a migration system.
package migrations

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	sqlitemigrate "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed sqlite/*.sql
var sqliteFS embed.FS

//go:embed postgres/*.sql
var postgresFS embed.FS

// newMigrator builds a *migrate.Migrate for driverName ("sqlite" or
// "postgres") against db.
//
// It deliberately never closes the database driver it builds: both
// golang-migrate backends' Close() closes the *underlying* *sql.DB, not
// just their own wrapper — and WithInstance is handed the same *sql.DB
// the caller (a repository constructor, or a test) keeps using
// afterward. Closing it here would sever that connection out from under
// the caller the moment migrations finished.
func newMigrator(db *sql.DB, driverName string) (*migrate.Migrate, error) {
	switch driverName {
	case "sqlite":
		drv, err := sqlitemigrate.WithInstance(db, &sqlitemigrate.Config{})
		if err != nil {
			return nil, fmt.Errorf("migrations: init sqlite database driver: %w", err)
		}
		src, err := iofs.New(sqliteFS, "sqlite")
		if err != nil {
			return nil, fmt.Errorf("migrations: init sqlite source driver: %w", err)
		}
		m, err := migrate.NewWithInstance("iofs", src, "sqlite", drv)
		if err != nil {
			return nil, fmt.Errorf("migrations: init migrate instance: %w", err)
		}
		return m, nil
	case "postgres":
		drv, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
		if err != nil {
			return nil, fmt.Errorf("migrations: init postgres database driver: %w", err)
		}
		src, err := iofs.New(postgresFS, "postgres")
		if err != nil {
			return nil, fmt.Errorf("migrations: init postgres source driver: %w", err)
		}
		m, err := migrate.NewWithInstance("iofs", src, "postgres", drv)
		if err != nil {
			return nil, fmt.Errorf("migrations: init migrate instance: %w", err)
		}
		return m, nil
	default:
		return nil, fmt.Errorf("migrations: unknown driver %q", driverName)
	}
}

// Apply runs every pending migration for driverName against db, in
// version order. Called once from each repository constructor — safe to
// call on every process start (see package doc).
func Apply(db *sql.DB, driverName string) error {
	m, err := newMigrator(db, driverName)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrations: apply: %w", err)
	}
	return nil
}

// Down rolls back every applied migration for driverName, leaving the
// schema empty. Not called anywhere in the app itself — there's no
// operational rollback command here (use golang-migrate's own CLI
// against the configured DSN for that). This exists so tests can verify
// the .down.sql files actually reverse the schema correctly, rather than
// leaving them "believed correct" until an emergency rollback is the
// first time they're ever run.
func Down(db *sql.DB, driverName string) error {
	m, err := newMigrator(db, driverName)
	if err != nil {
		return err
	}
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrations: down: %w", err)
	}
	return nil
}

// Steps applies exactly n migrations for driverName: positive n moves
// forward, negative n moves backward. Same rationale as Down — test-only
// today, not exposed as an app command.
func Steps(db *sql.DB, driverName string, n int) error {
	m, err := newMigrator(db, driverName)
	if err != nil {
		return err
	}
	if err := m.Steps(n); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrations: steps(%d): %w", n, err)
	}
	return nil
}
