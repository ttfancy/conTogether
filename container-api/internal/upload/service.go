// Package upload handles per-user file uploads (CSV/JSON/images/code)
// into an isolated directory per owner, with metadata (owner, content
// type, visibility) persisted via an injected Repository so uploads can
// be listed and shared like containers, not just written once.
package upload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"contogether/container-api/internal/domain"
	"github.com/ttfancy/logGO"
)

var (
	ErrNotFound          = errors.New("upload not found")
	ErrForbidden         = errors.New("not the owner of this upload")
	ErrInvalidVisibility = errors.New("visibility must be \"private\" or \"public\"")
)

// allowedContentTypes are the sniffed (not extension-based) MIME types
// accepted for upload. Go's http.DetectContentType has no CSV/JSON
// signature — plain-text data like CSV, JSON, and most source code all
// sniff as one of the text/plain variants below, which is what actually
// covers the CSV/JSON/code requirement. Deliberately NOT included:
// application/octet-stream — that's DetectContentType's fallback for
// anything binary it doesn't recognize (including executables), so
// allowing it would let a relabeled binary through the sniff check.
var allowedContentTypes = map[string]bool{
	"text/plain; charset=utf-8":    true, // CSV, JSON, source code, etc.
	"text/plain; charset=utf-16be": true,
	"text/plain; charset=utf-16le": true,
	"image/png":                    true,
	"image/jpeg":                   true,
	"image/gif":                    true,
}

// MaxUploadBytes is the largest upload Save will accept.
const MaxUploadBytes = 32 << 20 // 32 MiB

// Repository is the persistence seam Service needs for upload metadata.
// Satisfied by repository.SQLiteUploadRepo/PostgresUploadRepo.
type Repository interface {
	Save(ctx context.Context, u *domain.Upload) error
	FindByID(ctx context.Context, id string) (*domain.Upload, error)
	// ListVisibleTo returns everything ownerID may read: its own
	// uploads (any visibility) plus every other owner's public ones.
	ListVisibleTo(ctx context.Context, ownerID string) ([]*domain.Upload, error)
	UpdateVisibility(ctx context.Context, id string, visibility domain.Visibility) error
}

type Service struct {
	rootDir string
	logger  *logGO.Manager
	repo    Repository
	newID   func() string
}

func NewService(rootDir string, logger *logGO.Manager, repo Repository, newID func() string) *Service {
	return &Service{rootDir: rootDir, logger: logger, repo: repo, newID: newID}
}

// RootDir returns the base directory uploads are stored under.
func (s *Service) RootDir() string { return s.rootDir }

// Save validates and writes src (an uploaded file's contents) under
// rootDir/ownerID/, sniffing the actual content type rather than
// trusting the filename extension, and rejecting any filename that
// would escape the per-owner directory (path traversal). The file on
// disk is namespaced by the new upload's ID (not just filename) so two
// uploads sharing a filename never collide on disk — each Upload
// record's Path stays valid even if the owner uploads "data.csv" again
// later.
func (s *Service) Save(ctx context.Context, ownerID, filename string, src io.Reader, visibility domain.Visibility) (*domain.Upload, error) {
	if visibility == "" {
		visibility = domain.VisibilityPrivate
	}
	if !visibility.Valid() {
		return nil, fmt.Errorf("%w: invalid visibility %q", ErrInvalidVisibility, visibility)
	}

	// Reject outright rather than sanitize-via-filepath.Base: silently
	// rewriting "../../etc/passwd" to "passwd" would still be safe, but
	// it hides a suspicious upload behind a successful response instead
	// of surfacing it as a rejected request.
	if filename == "" || filename == "." || filename == ".." || strings.ContainsAny(filename, `/\`) {
		return nil, fmt.Errorf("invalid filename %q", filename)
	}

	ownerDir := filepath.Join(s.rootDir, filepath.Base(ownerID))
	if err := os.MkdirAll(ownerDir, 0o755); err != nil {
		return nil, fmt.Errorf("create owner upload dir: %w", err)
	}

	limited := io.LimitReader(src, MaxUploadBytes+1)
	head := make([]byte, 512)
	n, err := io.ReadFull(limited, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, fmt.Errorf("read upload: %w", err)
	}
	head = head[:n]

	contentType := http.DetectContentType(head)
	if !allowedContentTypes[contentType] {
		return nil, fmt.Errorf("content type %q is not permitted", contentType)
	}

	id := s.newID()
	destPath := filepath.Join(ownerDir, id+"_"+filename)
	dest, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("create destination file: %w", err)
	}
	defer dest.Close()

	written, err := dest.Write(head)
	if err != nil {
		return nil, fmt.Errorf("write upload: %w", err)
	}
	rest, err := io.Copy(dest, limited)
	if err != nil {
		return nil, fmt.Errorf("write upload: %w", err)
	}
	total := int64(written) + rest
	if total > MaxUploadBytes {
		os.Remove(destPath)
		return nil, fmt.Errorf("upload exceeds %d byte limit", MaxUploadBytes)
	}

	u := &domain.Upload{
		ID:          id,
		OwnerID:     ownerID,
		Filename:    filename,
		Path:        destPath,
		ContentType: contentType,
		Size:        total,
		Visibility:  visibility,
		CreatedAt:   time.Now(),
	}
	if err := s.repo.Save(ctx, u); err != nil {
		os.Remove(destPath)
		return nil, fmt.Errorf("save upload record: %w", err)
	}

	_ = s.logger.WriteLog("INFO", "file uploaded",
		logGO.F("owner_id", ownerID), logGO.F("filename", filename), logGO.F("content_type", contentType))
	return u, nil
}

// Get returns the upload's metadata if ownerID owns it OR it's public —
// used both by the download endpoint and anywhere else a single
// upload's record is needed for a read.
func (s *Service) Get(ctx context.Context, ownerID, id string) (*domain.Upload, error) {
	u, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrNotFound
	}
	if u.OwnerID != ownerID && u.Visibility != domain.VisibilityPublic {
		return nil, ErrForbidden
	}
	return u, nil
}

// List returns every upload ownerID may read: its own (any visibility)
// plus every other owner's public ones, most recently created first.
func (s *Service) List(ctx context.Context, ownerID string) ([]*domain.Upload, error) {
	return s.repo.ListVisibleTo(ctx, ownerID)
}

// SetVisibility flips an upload's visibility — owner-only, same
// reasoning as service.ContainerService.SetVisibility: visibility
// grants read access, never the right for someone else to change it.
func (s *Service) SetVisibility(ctx context.Context, ownerID, id string, visibility domain.Visibility) error {
	if !visibility.Valid() {
		return fmt.Errorf("%w: invalid visibility %q", ErrInvalidVisibility, visibility)
	}
	u, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if u == nil {
		return ErrNotFound
	}
	if u.OwnerID != ownerID {
		return ErrForbidden
	}
	if err := s.repo.UpdateVisibility(ctx, id, visibility); err != nil {
		return fmt.Errorf("update visibility: %w", err)
	}
	_ = s.logger.WriteLog("INFO", "upload visibility changed",
		logGO.F("upload_id", id), logGO.F("visibility", string(visibility)))
	return nil
}
