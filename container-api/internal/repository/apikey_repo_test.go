package repository_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"contogether/container-api/internal/repository"
)

func openTestAPIKeyDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "apikeys.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOwnerForKeyUnknownKeyReturnsFalse(t *testing.T) {
	repo, err := repository.NewAPIKeyRepo(openTestAPIKeyDB(t), "sqlite")
	if err != nil {
		t.Fatalf("NewAPIKeyRepo failed: %v", err)
	}

	if _, ok := repo.OwnerForKey("never-seeded"); ok {
		t.Fatal("expected OwnerForKey to report false for a key that was never seeded")
	}
}

func TestSeedThenOwnerForKeyResolves(t *testing.T) {
	repo, err := repository.NewAPIKeyRepo(openTestAPIKeyDB(t), "sqlite")
	if err != nil {
		t.Fatalf("NewAPIKeyRepo failed: %v", err)
	}

	if err := repo.Seed("dev-key", "dev-user"); err != nil {
		t.Fatalf("Seed failed: %v", err)
	}

	ownerID, ok := repo.OwnerForKey("dev-key")
	if !ok {
		t.Fatal("expected OwnerForKey to resolve a seeded key")
	}
	if ownerID != "dev-user" {
		t.Fatalf("OwnerForKey = %q, want %q", ownerID, "dev-user")
	}
}

// TestPlaintextKeyNeverStored is a regression guard for the whole point
// of hashing: the raw key string must never appear in the table golang-migrate
// created, only its hash.
func TestPlaintextKeyNeverStored(t *testing.T) {
	db := openTestAPIKeyDB(t)
	repo, err := repository.NewAPIKeyRepo(db, "sqlite")
	if err != nil {
		t.Fatalf("NewAPIKeyRepo failed: %v", err)
	}
	if err := repo.Seed("super-secret-key", "owner-1"); err != nil {
		t.Fatalf("Seed failed: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM api_keys WHERE key_hash = ?`, "super-secret-key").Scan(&count); err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if count != 0 {
		t.Fatal("expected the plaintext key to never appear as key_hash")
	}
}

// TestSeedIsIdempotentAndUpdatesOwner verifies Seed can be called on
// every process start (as main.go does) without erroring on a key
// that's already present, and that re-seeding the same key with a
// different owner actually updates the mapping rather than silently
// keeping the old one.
func TestSeedIsIdempotentAndUpdatesOwner(t *testing.T) {
	repo, err := repository.NewAPIKeyRepo(openTestAPIKeyDB(t), "sqlite")
	if err != nil {
		t.Fatalf("NewAPIKeyRepo failed: %v", err)
	}

	if err := repo.Seed("k1", "owner-a"); err != nil {
		t.Fatalf("first Seed failed: %v", err)
	}
	if err := repo.Seed("k1", "owner-b"); err != nil {
		t.Fatalf("second Seed (re-seed) failed: %v", err)
	}

	ownerID, ok := repo.OwnerForKey("k1")
	if !ok || ownerID != "owner-b" {
		t.Fatalf("OwnerForKey after re-seed = (%q, %v), want (%q, true)", ownerID, ok, "owner-b")
	}
}

func TestNewAPIKeyRepoSharesDBWithContainerRepo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	containerRepo, err := repository.NewSQLiteContainerRepo(path)
	if err != nil {
		t.Fatalf("NewSQLiteContainerRepo failed: %v", err)
	}
	defer containerRepo.Close()

	apiKeyRepo, err := repository.NewAPIKeyRepo(containerRepo.DB(), "sqlite")
	if err != nil {
		t.Fatalf("NewAPIKeyRepo against the shared DB failed: %v", err)
	}
	if err := apiKeyRepo.Seed("shared-key", "owner-1"); err != nil {
		t.Fatalf("Seed failed: %v", err)
	}
	if _, ok := apiKeyRepo.OwnerForKey("shared-key"); !ok {
		t.Fatal("expected the seeded key to resolve")
	}
}
