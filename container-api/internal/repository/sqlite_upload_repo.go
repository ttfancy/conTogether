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

// SQLiteUploadRepo is the SQLite implementation of upload.Repository —
// persists Upload metadata (owner, visibility, where the file lives on
// disk) alongside the containers table in the same database.
type SQLiteUploadRepo struct {
	db *sql.DB
}

// NewSQLiteUploadRepo wraps db (already open; typically shared with
// SQLiteContainerRepo against the same file via its DB() accessor) and
// brings the schema up to date via migrations.Apply — idempotent, so
// safe even if another repository already applied the same migrations.
func NewSQLiteUploadRepo(db *sql.DB) (*SQLiteUploadRepo, error) {
	if err := migrations.Apply(db, "sqlite"); err != nil {
		return nil, err
	}
	return &SQLiteUploadRepo{db: db}, nil
}

func (r *SQLiteUploadRepo) Save(ctx context.Context, u *domain.Upload) error {
	visibility := u.Visibility
	if visibility == "" {
		visibility = domain.VisibilityPrivate
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO uploads (id, owner_id, filename, path, content_type, size, visibility, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.OwnerID, u.Filename, u.Path, u.ContentType, u.Size, string(visibility), u.CreatedAt.UnixNano(),
	)
	return err
}

func (r *SQLiteUploadRepo) FindByID(ctx context.Context, id string) (*domain.Upload, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, owner_id, filename, path, content_type, size, visibility, created_at
		FROM uploads WHERE id = ?`, id)
	return scanUpload(row.Scan)
}

// ListVisibleTo returns every upload ownerID may read: its own (any
// visibility) plus every other owner's public ones, newest first.
func (r *SQLiteUploadRepo) ListVisibleTo(ctx context.Context, ownerID string) ([]*domain.Upload, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, owner_id, filename, path, content_type, size, visibility, created_at
		FROM uploads WHERE owner_id = ? OR visibility = ? ORDER BY created_at DESC`,
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

func (r *SQLiteUploadRepo) UpdateVisibility(ctx context.Context, id string, visibility domain.Visibility) error {
	_, err := r.db.ExecContext(ctx, `UPDATE uploads SET visibility = ? WHERE id = ?`, string(visibility), id)
	return err
}

// scanUpload adapts either a *sql.Row.Scan or *sql.Rows.Scan (same
// signature) into a domain.Upload, shared by FindByID and
// ListVisibleTo so the column-order/type-conversion logic lives in one
// place.
func scanUpload(scan func(dest ...any) error) (*domain.Upload, error) {
	var u domain.Upload
	var visibility string
	var createdAt int64
	err := scan(&u.ID, &u.OwnerID, &u.Filename, &u.Path, &u.ContentType, &u.Size, &visibility, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.Visibility = domain.Visibility(visibility)
	u.CreatedAt = time.Unix(0, createdAt)
	return &u, nil
}
