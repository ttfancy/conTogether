// Package repository provides persistence backends satisfying the
// interfaces service defines (e.g. service.ContainerRepository).
// SQLiteContainerRepo is one such backend; swapping to Postgres means
// adding a PostgresContainerRepo here and changing one line in main.go —
// nothing in service or handler changes.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	_ "modernc.org/sqlite"

	"contogether/container-api/internal/domain"
	"contogether/container-api/internal/migrations"
)

type SQLiteContainerRepo struct {
	db *sql.DB
}

// NewSQLiteContainerRepo opens (creating if needed) a SQLite database at
// path and brings its schema up to date via migrations.Apply.
func NewSQLiteContainerRepo(path string) (*SQLiteContainerRepo, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: single-writer; avoid "database is locked"

	if err := migrations.Apply(db, "sqlite"); err != nil {
		db.Close()
		return nil, err
	}
	return &SQLiteContainerRepo{db: db}, nil
}

func (r *SQLiteContainerRepo) Close() error { return r.db.Close() }

// DB returns the underlying connection pool, so main.go can hand it to
// other repositories (e.g. APIKeyRepo) that need to share the same
// SQLite file rather than opening a second, independently-pooled
// connection to it.
func (r *SQLiteContainerRepo) DB() *sql.DB { return r.db }

func (r *SQLiteContainerRepo) Save(ctx context.Context, c *domain.Container) error {
	visibility := c.Visibility
	if visibility == "" {
		visibility = domain.VisibilityPrivate
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO containers (id, docker_id, owner_id, name, image, status, visibility, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.DockerID, c.OwnerID, c.Name, c.Image, string(c.Status), string(visibility),
		c.CreatedAt.UnixNano(), c.UpdatedAt.UnixNano(),
	)
	return err
}

func (r *SQLiteContainerRepo) FindByID(ctx context.Context, id string) (*domain.Container, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, docker_id, owner_id, name, image, status, visibility, created_at, updated_at
		FROM containers WHERE id = ?`, id)

	var c domain.Container
	var status, visibility string
	var createdAt, updatedAt int64
	err := row.Scan(&c.ID, &c.DockerID, &c.OwnerID, &c.Name, &c.Image, &status, &visibility, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // not found is not an error at this layer; service maps it to ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.Status = domain.ContainerStatus(status)
	c.Visibility = domain.Visibility(visibility)
	c.CreatedAt = time.Unix(0, createdAt)
	c.UpdatedAt = time.Unix(0, updatedAt)
	return &c, nil
}

// ListVisibleTo returns every container ownerID may read: its own
// (any visibility) plus every other owner's public ones, newest first.
// This is a superset of the old "list mine only" behavior — see
// service.ContainerService.ListContainers for why "public" extends to
// listing, not just direct Get.
func (r *SQLiteContainerRepo) ListVisibleTo(ctx context.Context, ownerID string) ([]*domain.Container, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, docker_id, owner_id, name, image, status, visibility, created_at, updated_at
		FROM containers WHERE owner_id = ? OR visibility = ? ORDER BY created_at DESC`,
		ownerID, string(domain.VisibilityPublic))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.Container
	for rows.Next() {
		var c domain.Container
		var status, visibility string
		var createdAt, updatedAt int64
		if err := rows.Scan(&c.ID, &c.DockerID, &c.OwnerID, &c.Name, &c.Image, &status, &visibility, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		c.Status = domain.ContainerStatus(status)
		c.Visibility = domain.Visibility(visibility)
		c.CreatedAt = time.Unix(0, createdAt)
		c.UpdatedAt = time.Unix(0, updatedAt)
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (r *SQLiteContainerRepo) UpdateStatus(ctx context.Context, id string, status domain.ContainerStatus) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE containers SET status = ?, updated_at = ? WHERE id = ?`,
		string(status), time.Now().UnixNano(), id,
	)
	return err
}

func (r *SQLiteContainerRepo) UpdateVisibility(ctx context.Context, id string, visibility domain.Visibility) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE containers SET visibility = ?, updated_at = ? WHERE id = ?`,
		string(visibility), time.Now().UnixNano(), id,
	)
	return err
}

func (r *SQLiteContainerRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM containers WHERE id = ?`, id)
	return err
}
