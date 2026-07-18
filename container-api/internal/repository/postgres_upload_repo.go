package repository

import (
	"context"
	"database/sql"

	_ "github.com/jackc/pgx/v5/stdlib"

	"contogether/container-api/internal/domain"
	"contogether/container-api/internal/migrations"
)

// PostgresUploadRepo is the Postgres implementation of upload.Repository
// — same shape as SQLiteUploadRepo, different placeholder syntax.
type PostgresUploadRepo struct {
	db *sql.DB
}

// NewPostgresUploadRepo wraps db (already open; typically shared with
// PostgresContainerRepo via its DB() accessor) and brings the schema up
// to date via migrations.Apply — idempotent.
func NewPostgresUploadRepo(db *sql.DB) (*PostgresUploadRepo, error) {
	if err := migrations.Apply(db, "postgres"); err != nil {
		return nil, err
	}
	return &PostgresUploadRepo{db: db}, nil
}

func (r *PostgresUploadRepo) Save(ctx context.Context, u *domain.Upload) error {
	visibility := u.Visibility
	if visibility == "" {
		visibility = domain.VisibilityPrivate
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO uploads (id, owner_id, filename, path, content_type, size, visibility, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		u.ID, u.OwnerID, u.Filename, u.Path, u.ContentType, u.Size, string(visibility), u.CreatedAt.UnixNano(),
	)
	return err
}

func (r *PostgresUploadRepo) FindByID(ctx context.Context, id string) (*domain.Upload, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, owner_id, filename, path, content_type, size, visibility, created_at
		FROM uploads WHERE id = $1`, id)
	return scanUpload(row.Scan)
}

// ListVisibleTo returns every upload ownerID may read: its own (any
// visibility) plus every other owner's public ones, newest first.
func (r *PostgresUploadRepo) ListVisibleTo(ctx context.Context, ownerID string) ([]*domain.Upload, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, owner_id, filename, path, content_type, size, visibility, created_at
		FROM uploads WHERE owner_id = $1 OR visibility = $2 ORDER BY created_at DESC`,
		ownerID, string(domain.VisibilityPublic))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.Upload
	for rows.Next() {
		u, err := scanUpload(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *PostgresUploadRepo) UpdateVisibility(ctx context.Context, id string, visibility domain.Visibility) error {
	_, err := r.db.ExecContext(ctx, `UPDATE uploads SET visibility = $1 WHERE id = $2`, string(visibility), id)
	return err
}
