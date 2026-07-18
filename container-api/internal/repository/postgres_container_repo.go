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

func (r *PostgresContainerRepo) Save(ctx context.Context, c *domain.Container) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO containers (id, docker_id, owner_id, name, image, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		c.ID, c.DockerID, c.OwnerID, c.Name, c.Image, string(c.Status),
		c.CreatedAt.UnixNano(), c.UpdatedAt.UnixNano(),
	)
	return err
}

func (r *PostgresContainerRepo) FindByID(ctx context.Context, id string) (*domain.Container, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, docker_id, owner_id, name, image, status, created_at, updated_at
		FROM containers WHERE id = $1`, id)

	var c domain.Container
	var status string
	var createdAt, updatedAt int64
	err := row.Scan(&c.ID, &c.DockerID, &c.OwnerID, &c.Name, &c.Image, &status, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // not found is not an error at this layer; service maps it to ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.Status = domain.ContainerStatus(status)
	c.CreatedAt = time.Unix(0, createdAt)
	c.UpdatedAt = time.Unix(0, updatedAt)
	return &c, nil
}

func (r *PostgresContainerRepo) ListByOwner(ctx context.Context, ownerID string) ([]*domain.Container, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, docker_id, owner_id, name, image, status, created_at, updated_at
		FROM containers WHERE owner_id = $1 ORDER BY created_at DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.Container
	for rows.Next() {
		var c domain.Container
		var status string
		var createdAt, updatedAt int64
		if err := rows.Scan(&c.ID, &c.DockerID, &c.OwnerID, &c.Name, &c.Image, &status, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		c.Status = domain.ContainerStatus(status)
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

func (r *PostgresContainerRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM containers WHERE id = $1`, id)
	return err
}
