package upload_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"contogether/container-api/internal/domain"
	"contogether/container-api/internal/upload"
	"github.com/ttfancy/logGO"
	"github.com/ttfancy/logGO/backends/memory"
)

// fakeRepo is an in-memory upload.Repository, standing in for
// repository.SQLiteUploadRepo/PostgresUploadRepo so these tests exercise
// Service's own logic (validation, visibility authorization) without a
// real database.
type fakeRepo struct {
	mu   sync.Mutex
	byID map[string]*domain.Upload
}

func newFakeRepo() *fakeRepo { return &fakeRepo{byID: make(map[string]*domain.Upload)} }

func (r *fakeRepo) Save(_ context.Context, u *domain.Upload) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *u
	r.byID[u.ID] = &cp
	return nil
}

func (r *fakeRepo) FindByID(_ context.Context, id string) (*domain.Upload, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.byID[id]
	if !ok {
		return nil, nil
	}
	cp := *u
	return &cp, nil
}

func (r *fakeRepo) ListVisibleTo(_ context.Context, ownerID string) ([]*domain.Upload, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.Upload
	for _, u := range r.byID {
		if u.OwnerID == ownerID || u.Visibility == domain.VisibilityPublic {
			cp := *u
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *fakeRepo) UpdateVisibility(_ context.Context, id string, visibility domain.Visibility) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.byID[id]
	if !ok {
		return fmt.Errorf("no such upload: %s", id)
	}
	u.Visibility = visibility
	return nil
}

func sequentialIDs() func() string {
	var n atomic.Int64
	return func() string { return fmt.Sprintf("up-%d", n.Add(1)) }
}

func newTestService(t *testing.T) (*upload.Service, *fakeRepo) {
	t.Helper()
	store := memory.New()
	logger := logGO.NewManager(store, store, store)
	t.Cleanup(func() { logger.Close() })
	repo := newFakeRepo()
	return upload.NewService(t.TempDir(), logger, repo, sequentialIDs()), repo
}

func TestSaveValidCSVSucceeds(t *testing.T) {
	svc, _ := newTestService(t)
	u, err := svc.Save(context.Background(), "owner-1", "data.csv", strings.NewReader("a,b,c\n1,2,3\n"), "")
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if _, err := os.Stat(u.Path); err != nil {
		t.Fatalf("expected file to exist at %s: %v", u.Path, err)
	}
	if filepath.Dir(u.Path) != filepath.Join(svc.RootDir(), "owner-1") {
		t.Fatalf("file not stored under owner directory: %s", u.Path)
	}
	if u.Visibility != domain.VisibilityPrivate {
		t.Fatalf("visibility = %q, want default %q", u.Visibility, domain.VisibilityPrivate)
	}
}

func TestSaveRejectsPathTraversal(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.Save(context.Background(), "owner-1", "../../etc/passwd", strings.NewReader("x"), ""); err == nil {
		t.Fatal("expected path traversal filename to be rejected")
	}
	if _, err := svc.Save(context.Background(), "owner-1", "../secret.txt", strings.NewReader("x"), ""); err == nil {
		t.Fatal("expected path traversal filename to be rejected")
	}
}

func TestSaveRejectsDisallowedContentType(t *testing.T) {
	svc, _ := newTestService(t)
	// A minimal ELF-like magic byte header, not a permitted content type.
	elfMagic := "\x7fELF" + strings.Repeat("\x00", 100)
	if _, err := svc.Save(context.Background(), "owner-1", "not-really.csv", strings.NewReader(elfMagic), ""); err == nil {
		t.Fatal("expected disallowed content type to be rejected even with a .csv extension")
	}
}

func TestSaveRejectsOversizedUpload(t *testing.T) {
	svc, _ := newTestService(t)
	oversized := strings.NewReader(strings.Repeat("a", upload.MaxUploadBytes+1))
	if _, err := svc.Save(context.Background(), "owner-1", "big.csv", oversized, ""); err == nil {
		t.Fatal("expected oversized upload to be rejected")
	}
}

func TestSaveRejectsInvalidVisibility(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.Save(context.Background(), "owner-1", "data.csv", strings.NewReader("x"), "sorta-public"); !errors.Is(err, upload.ErrInvalidVisibility) {
		t.Fatalf("Save with invalid visibility = %v, want ErrInvalidVisibility", err)
	}
}

func TestSameFilenameTwiceDoesNotCollideOnDisk(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	first, err := svc.Save(ctx, "owner-1", "data.csv", strings.NewReader("first"), "")
	if err != nil {
		t.Fatalf("first Save failed: %v", err)
	}
	second, err := svc.Save(ctx, "owner-1", "data.csv", strings.NewReader("second"), "")
	if err != nil {
		t.Fatalf("second Save failed: %v", err)
	}
	if first.Path == second.Path {
		t.Fatalf("two uploads of the same filename got the same path: %s", first.Path)
	}

	firstContent, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatalf("reading first upload: %v", err)
	}
	if string(firstContent) != "first" {
		t.Fatalf("first upload's own file was overwritten: got %q, want %q", firstContent, "first")
	}
}

func TestGetForbiddenForOtherOwnerWhenPrivate(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	u, err := svc.Save(ctx, "owner-1", "secret.csv", strings.NewReader("x"), domain.VisibilityPrivate)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if _, err := svc.Get(ctx, "owner-2", u.ID); !errors.Is(err, upload.ErrForbidden) {
		t.Fatalf("Get as different owner = %v, want ErrForbidden", err)
	}
	if _, err := svc.Get(ctx, "owner-1", u.ID); err != nil {
		t.Fatalf("Get as owner failed: %v", err)
	}
}

func TestGetVisibleToAnyoneWhenPublic(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	u, err := svc.Save(ctx, "owner-1", "shared.csv", strings.NewReader("x"), domain.VisibilityPublic)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	got, err := svc.Get(ctx, "owner-2", u.ID)
	if err != nil {
		t.Fatalf("Get a public upload as a different owner failed: %v", err)
	}
	if got.ID != u.ID {
		t.Fatalf("got upload %+v, want %+v", got, u)
	}
}

func TestListIncludesOwnPrivateAndOthersPublicOnly(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.Save(ctx, "owner-1", "mine-private.csv", strings.NewReader("x"), domain.VisibilityPrivate); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if _, err := svc.Save(ctx, "owner-2", "theirs-private.csv", strings.NewReader("x"), domain.VisibilityPrivate); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if _, err := svc.Save(ctx, "owner-2", "theirs-public.csv", strings.NewReader("x"), domain.VisibilityPublic); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	got, err := svc.List(ctx, "owner-1")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	names := make(map[string]bool)
	for _, u := range got {
		names[u.Filename] = true
	}
	if len(got) != 2 || !names["mine-private.csv"] || !names["theirs-public.csv"] {
		t.Fatalf("List(owner-1) = %+v, want exactly [mine-private.csv, theirs-public.csv]", got)
	}
}

func TestSetVisibilityForbiddenForNonOwner(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	u, err := svc.Save(ctx, "owner-1", "data.csv", strings.NewReader("x"), domain.VisibilityPrivate)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if err := svc.SetVisibility(ctx, "owner-2", u.ID, domain.VisibilityPublic); !errors.Is(err, upload.ErrForbidden) {
		t.Fatalf("SetVisibility as different owner = %v, want ErrForbidden", err)
	}

	if err := svc.SetVisibility(ctx, "owner-1", u.ID, domain.VisibilityPublic); err != nil {
		t.Fatalf("SetVisibility as owner failed: %v", err)
	}
	got, err := svc.Get(ctx, "owner-2", u.ID)
	if err != nil {
		t.Fatalf("Get after making public failed: %v", err)
	}
	if got.Visibility != domain.VisibilityPublic {
		t.Fatalf("visibility after SetVisibility = %q, want %q", got.Visibility, domain.VisibilityPublic)
	}
}
