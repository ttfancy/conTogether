package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"contogether/container-api/internal/domain"
	"contogether/container-api/internal/migrations"
)

// PostgresContainerRepo is the Postgres implementation of
// service.ContainerRepository — the same interface SQLiteContainerRepo
// satisfies. Timestamps are stored as BIGINT (Unix nanoseconds), same as
// SQLiteContainerRepo, so both backends behave identically rather than
// one relying on a native TIMESTAMPTZ column's own semantics.
type PostgresContainerRepo struct {
	db *sql.DB
}

// NewPostgresContainerRepo opens a connection pool against dsn (a
// standard Postgres connection string, e.g.
// "postgres://user:pass@host:5432/dbname?sslmode=disable") and brings
// its schema up to date via migrations.Apply.
func NewPostgresContainerRepo(dsn string) (*PostgresContainerRepo, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}

	if err := migrations.Apply(db, "postgres"); err != nil {
		db.Close()
		return nil, err
	}
	return &PostgresContainerRepo{db: db}, nil
}

func (r *PostgresContainerRepo) Close() error { return r.db.Close() }

// DB returns the underlying connection pool, so main.go can hand it to
// other repositories (e.g. APIKeyRepo) that need to share it rather than
// opening a second pool against the same DSN.
func (r *PostgresContainerRepo) DB() *sql.DB { return r.db }

func (r *PostgresContainerRepo) Save(ctx context.Context, c *domain.Container) error {
	visibility := c.Visibility
	if visibility == "" {
		visibility = domain.VisibilityPrivate
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO containers (id, docker_id, owner_id, name, image, status, visibility, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		c.ID, c.DockerID, c.OwnerID, c.Name, c.Image, string(c.Status), string(visibility),
		c.CreatedAt.UnixNano(), c.UpdatedAt.UnixNano(),
	)
	return err
}

func (r *PostgresContainerRepo) FindByID(ctx context.Context, id string) (*domain.Container, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, docker_id, owner_id, name, image, status, visibility, created_at, updated_at
		FROM containers WHERE id = $1`, id)

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

// ListVisibleTo returns every container ownerID may read: its own (any
// visibility) plus every other owner's public ones, newest first.
func (r *PostgresContainerRepo) ListVisibleTo(ctx context.Context, ownerID string) ([]*domain.Container, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, docker_id, owner_id, name, image, status, visibility, created_at, updated_at
		FROM containers WHERE owner_id = $1 OR visibility = $2 ORDER BY created_at DESC`,
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

func (r *PostgresContainerRepo) UpdateStatus(ctx context.Context, id string, status domain.ContainerStatus) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE containers SET status = $1, updated_at = $2 WHERE id = $3`,
		string(status), time.Now().UnixNano(), id,
	)
	return err
}

func (r *PostgresContainerRepo) UpdateVisibility(ctx context.Context, id string, visibility domain.Visibility) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE containers SET visibility = $1, updated_at = $2 WHERE id = $3`,
		string(visibility), time.Now().UnixNano(), id,
	)
	return err
}

func (r *PostgresContainerRepo) SetDockerID(ctx context.Context, id, dockerID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE containers SET docker_id = $1, updated_at = $2 WHERE id = $3`,
		dockerID, time.Now().UnixNano(), id,
	)
	return err
}

func (r *PostgresContainerRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM containers WHERE id = $1`, id)
	return err
}
