package repository

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"contogether/container-api/internal/migrations"
)

// APIKeyRepo is a DB-backed middleware.APIKeyStore: it never stores a
// key's plaintext, only a SHA-256 hash of it, same principle as password
// storage — a stolen DB backup shouldn't hand out working keys. SHA-256
// (not bcrypt) is enough here because API keys are already high-entropy
// random tokens, unlike user-chosen passwords; there's no dictionary to
// defend against, so a slow hash buys nothing.
//
// One struct serves both drivers (sqlite/postgres) — unlike
// SQLiteContainerRepo/PostgresContainerRepo, which differ in enough
// queries to justify separate types, this repo only has two: a lookup
// and an upsert, so branching on placeholder syntax inline is
// proportionate rather than duplicating a whole type for two lines of
// difference.
type APIKeyRepo struct {
	db     *sql.DB
	driver string
}

// NewAPIKeyRepo wraps db (already open; may be shared with a
// ContainerRepository against the same database) and brings the schema
// up to date via migrations.Apply — safe to call even if another
// repository already applied the same migration set.
func NewAPIKeyRepo(db *sql.DB, driver string) (*APIKeyRepo, error) {
	if err := migrations.Apply(db, driver); err != nil {
		return nil, err
	}
	return &APIKeyRepo{db: db, driver: driver}, nil
}

func hashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// OwnerForKey satisfies middleware.APIKeyStore.
func (r *APIKeyRepo) OwnerForKey(key string) (string, bool) {
	query := "SELECT owner_id FROM api_keys WHERE key_hash = ?"
	if r.driver == "postgres" {
		query = "SELECT owner_id FROM api_keys WHERE key_hash = $1"
	}
	var ownerID string
	err := r.db.QueryRow(query, hashAPIKey(key)).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		return "", false
	}
	return ownerID, true
}

// Seed upserts a key -> owner mapping — called at boot with the
// configured static dev key(s) so the mapping is durable in the
// database rather than only ever living in an in-memory map. Idempotent:
// safe to call on every process start.
func (r *APIKeyRepo) Seed(key, ownerID string) error {
	query := `
		INSERT INTO api_keys (key_hash, owner_id, created_at) VALUES (?, ?, ?)
		ON CONFLICT (key_hash) DO UPDATE SET owner_id = excluded.owner_id`
	if r.driver == "postgres" {
		query = `
			INSERT INTO api_keys (key_hash, owner_id, created_at) VALUES ($1, $2, $3)
			ON CONFLICT (key_hash) DO UPDATE SET owner_id = excluded.owner_id`
	}
	_, err := r.db.Exec(query, hashAPIKey(key), ownerID, time.Now().UnixNano())
	return err
}
