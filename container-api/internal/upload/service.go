// Package upload handles per-user file uploads (CSV/JSON/images/code)
// into an isolated directory per owner.
package upload

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"contogether/logsys"
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

type Service struct {
	rootDir string
	logger  *logsys.Manager
}

func NewService(rootDir string, logger *logsys.Manager) *Service {
	return &Service{rootDir: rootDir, logger: logger}
}

// RootDir returns the base directory uploads are stored under.
func (s *Service) RootDir() string { return s.rootDir }

// Save validates and writes src (an uploaded file's contents) under
// rootDir/ownerID/filename, sniffing the actual content type rather than
// trusting the filename extension, and rejecting any filename that would
// escape the per-owner directory (path traversal).
func (s *Service) Save(ownerID, filename string, src io.Reader) (string, error) {
	// Reject outright rather than sanitize-via-filepath.Base: silently
	// rewriting "../../etc/passwd" to "passwd" would still be safe, but
	// it hides a suspicious upload behind a successful response instead
	// of surfacing it as a rejected request.
	if filename == "" || filename == "." || filename == ".." || strings.ContainsAny(filename, `/\`) {
		return "", fmt.Errorf("invalid filename %q", filename)
	}
	safeName := filename

	ownerDir := filepath.Join(s.rootDir, filepath.Base(ownerID))
	if err := os.MkdirAll(ownerDir, 0o755); err != nil {
		return "", fmt.Errorf("create owner upload dir: %w", err)
	}

	limited := io.LimitReader(src, MaxUploadBytes+1)
	head := make([]byte, 512)
	n, err := io.ReadFull(limited, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", fmt.Errorf("read upload: %w", err)
	}
	head = head[:n]

	contentType := http.DetectContentType(head)
	if !allowedContentTypes[contentType] {
		return "", fmt.Errorf("content type %q is not permitted", contentType)
	}

	destPath := filepath.Join(ownerDir, safeName)
	dest, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("create destination file: %w", err)
	}
	defer dest.Close()

	written, err := dest.Write(head)
	if err != nil {
		return "", fmt.Errorf("write upload: %w", err)
	}
	rest, err := io.Copy(dest, limited)
	if err != nil {
		return "", fmt.Errorf("write upload: %w", err)
	}
	if int64(written)+rest > MaxUploadBytes {
		os.Remove(destPath)
		return "", fmt.Errorf("upload exceeds %d byte limit", MaxUploadBytes)
	}

	_ = s.logger.WriteLog("INFO", "file uploaded",
		logsys.F("owner_id", ownerID), logsys.F("filename", safeName), logsys.F("content_type", contentType))
	return destPath, nil
}
