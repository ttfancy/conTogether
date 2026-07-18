package upload_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"contogether/container-api/internal/upload"
	"contogether/logsys"
	"contogether/logsys/backends/memory"
)

func newTestService(t *testing.T) *upload.Service {
	t.Helper()
	store := memory.New()
	logger := logsys.NewManager(store, store, store)
	t.Cleanup(func() { logger.Close() })
	return upload.NewService(t.TempDir(), logger)
}

func TestSaveValidCSVSucceeds(t *testing.T) {
	svc := newTestService(t)
	path, err := svc.Save("owner-1", "data.csv", strings.NewReader("a,b,c\n1,2,3\n"))
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to exist at %s: %v", path, err)
	}
	if filepath.Dir(path) != filepath.Join(svc.RootDir(), "owner-1") {
		t.Fatalf("file not stored under owner directory: %s", path)
	}
}

func TestSaveRejectsPathTraversal(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Save("owner-1", "../../etc/passwd", strings.NewReader("x")); err == nil {
		t.Fatal("expected path traversal filename to be rejected")
	}
	if _, err := svc.Save("owner-1", "../secret.txt", strings.NewReader("x")); err == nil {
		t.Fatal("expected path traversal filename to be rejected")
	}
}

func TestSaveRejectsDisallowedContentType(t *testing.T) {
	svc := newTestService(t)
	// A minimal ELF-like magic byte header, not a permitted content type.
	elfMagic := "\x7fELF" + strings.Repeat("\x00", 100)
	if _, err := svc.Save("owner-1", "not-really.csv", strings.NewReader(elfMagic)); err == nil {
		t.Fatal("expected disallowed content type to be rejected even with a .csv extension")
	}
}

func TestSaveRejectsOversizedUpload(t *testing.T) {
	svc := newTestService(t)
	oversized := strings.NewReader(strings.Repeat("a", upload.MaxUploadBytes+1))
	if _, err := svc.Save("owner-1", "big.csv", oversized); err == nil {
		t.Fatal("expected oversized upload to be rejected")
	}
}
