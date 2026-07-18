package migrations_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"contogether/container-api/internal/migrations"
)

func TestApplySQLiteCreatesSchema(t *testing.T) {
	db := openTestSQLite(t)

	if err := migrations.Apply(db, "sqlite"); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	assertSQLiteTableExists(t, db, "containers")
	assertSQLiteIndexExists(t, db, "idx_containers_owner_id")
}

// TestApplyIsIdempotent verifies Apply is safe to call on every process
// start, not just the first — the actual point of tracking applied
// versions instead of a bare CREATE TABLE IF NOT EXISTS.
func TestApplyIsIdempotent(t *testing.T) {
	db := openTestSQLite(t)

	if err := migrations.Apply(db, "sqlite"); err != nil {
		t.Fatalf("first Apply failed: %v", err)
	}
	if err := migrations.Apply(db, "sqlite"); err != nil {
		t.Fatalf("second Apply (already up to date) failed: %v", err)
	}

	assertSQLiteTableExists(t, db, "containers")
}

func TestApplyRejectsUnknownDriver(t *testing.T) {
	db := openTestSQLite(t)
	if err := migrations.Apply(db, "mysql"); err == nil {
		t.Fatal("expected Apply to reject an unknown driver name")
	}
}

// TestStepDownRemovesIndexOnlyAndPreservesData verifies 0002's down
// migration does exactly what it should — drop the index and nothing
// else. It seeds a row before rolling back specifically to prove this
// step is non-destructive to data, unlike 0001's down migration (which
// drops the whole table). Down migrations aren't uniformly dangerous;
// what each one actually does has to be checked, not assumed.
func TestStepDownRemovesIndexOnlyAndPreservesData(t *testing.T) {
	db := openTestSQLite(t)
	if err := migrations.Apply(db, "sqlite"); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	assertSQLiteIndexExists(t, db, "idx_containers_owner_id")

	if _, err := db.Exec(`
		INSERT INTO containers (id, docker_id, owner_id, name, image, status, created_at, updated_at)
		VALUES ('ctr-1', 'docker-1', 'owner-1', 'web', 'alpine', 'created', 0, 0)`); err != nil {
		t.Fatalf("seed insert failed: %v", err)
	}

	if err := migrations.Steps(db, "sqlite", -1); err != nil {
		t.Fatalf("Steps(-1) failed: %v", err)
	}

	assertSQLiteTableExists(t, db, "containers")
	assertSQLiteIndexNotExists(t, db, "idx_containers_owner_id")

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM containers WHERE id = 'ctr-1'`).Scan(&count); err != nil {
		t.Fatalf("query after rollback failed: %v", err)
	}
	if count != 1 {
		t.Fatal("expected the seeded row to survive an index-only rollback")
	}
}

// TestDownRemovesTableEntirely verifies 0001's down migration — this one
// IS destructive, which is expected: rolling back past the table's own
// creation necessarily removes whatever is in it.
func TestDownRemovesTableEntirely(t *testing.T) {
	db := openTestSQLite(t)
	if err := migrations.Apply(db, "sqlite"); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	if err := migrations.Down(db, "sqlite"); err != nil {
		t.Fatalf("Down failed: %v", err)
	}

	assertSQLiteTableNotExists(t, db, "containers")
}

// TestUpDownUpRoundTrip is the strongest check: full rollback followed
// by re-applying from scratch must land on exactly the same schema. This
// is what actually proves the down migrations are inverses of the up
// migrations, not just "believed correct."
func TestUpDownUpRoundTrip(t *testing.T) {
	db := openTestSQLite(t)

	if err := migrations.Apply(db, "sqlite"); err != nil {
		t.Fatalf("first Apply failed: %v", err)
	}
	if err := migrations.Down(db, "sqlite"); err != nil {
		t.Fatalf("Down failed: %v", err)
	}
	assertSQLiteTableNotExists(t, db, "containers")

	if err := migrations.Apply(db, "sqlite"); err != nil {
		t.Fatalf("second Apply (after full rollback) failed: %v", err)
	}
	assertSQLiteTableExists(t, db, "containers")
	assertSQLiteIndexExists(t, db, "idx_containers_owner_id")
}

func openTestSQLite(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "migrate-test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func assertSQLiteTableExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	var got string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got)
	if err != nil {
		t.Fatalf("table %q not found: %v", name, err)
	}
}

func assertSQLiteTableNotExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	var got string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got)
	if err == nil {
		t.Fatalf("expected table %q to be gone, but it exists", name)
	} else if err != sql.ErrNoRows {
		t.Fatalf("unexpected error checking for table %q: %v", name, err)
	}
}

func assertSQLiteIndexExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	var got string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&got)
	if err != nil {
		t.Fatalf("index %q not found (0002 migration didn't run): %v", name, err)
	}
}

func assertSQLiteIndexNotExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	var got string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&got)
	if err == nil {
		t.Fatalf("expected index %q to be gone, but it exists", name)
	} else if err != sql.ErrNoRows {
		t.Fatalf("unexpected error checking for index %q: %v", name, err)
	}
}

// TestApplyPostgres exercises the same up/down behavior against a real
// Postgres instance, skipping unless TEST_POSTGRES_DSN is set — see
// internal/repository/postgres_container_repo_test.go for the same
// pattern.
func TestApplyPostgres(t *testing.T) {
	db := openTestPostgres(t)

	if err := migrations.Apply(db, "postgres"); err != nil {
		t.Fatalf("first Apply failed: %v", err)
	}
	if err := migrations.Apply(db, "postgres"); err != nil {
		t.Fatalf("second Apply (already up to date) failed: %v", err)
	}

	assertPostgresIndexExists(t, db, "idx_containers_owner_id")
}

func TestUpDownUpRoundTripPostgres(t *testing.T) {
	db := openTestPostgres(t)

	if err := migrations.Apply(db, "postgres"); err != nil {
		t.Fatalf("first Apply failed: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO containers (id, docker_id, owner_id, name, image, status, created_at, updated_at)
		VALUES ('ctr-pg-1', 'docker-1', 'owner-1', 'web', 'alpine', 'created', 0, 0)`); err != nil {
		t.Fatalf("seed insert failed: %v", err)
	}

	if err := migrations.Steps(db, "postgres", -1); err != nil {
		t.Fatalf("Steps(-1) failed: %v", err)
	}
	assertPostgresIndexNotExists(t, db, "idx_containers_owner_id")
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM containers WHERE id = 'ctr-pg-1'`).Scan(&count); err != nil {
		t.Fatalf("query after rollback failed: %v", err)
	}
	if count != 1 {
		t.Fatal("expected the seeded row to survive an index-only rollback")
	}

	if err := migrations.Down(db, "postgres"); err != nil {
		t.Fatalf("Down failed: %v", err)
	}
	assertPostgresTableNotExists(t, db, "containers")

	if err := migrations.Apply(db, "postgres"); err != nil {
		t.Fatalf("re-Apply after full rollback failed: %v", err)
	}
	assertPostgresIndexExists(t, db, "idx_containers_owner_id")
}

func openTestPostgres(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping Postgres migration test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(`DROP TABLE IF EXISTS containers`)
		db.Exec(`DROP TABLE IF EXISTS schema_migrations`)
		db.Close()
	})
	return db
}

func assertPostgresIndexExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	var got string
	if err := db.QueryRow(`SELECT indexname FROM pg_indexes WHERE indexname = $1`, name).Scan(&got); err != nil {
		t.Fatalf("index %q not found: %v", name, err)
	}
}

func assertPostgresIndexNotExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	var got string
	err := db.QueryRow(`SELECT indexname FROM pg_indexes WHERE indexname = $1`, name).Scan(&got)
	if err == nil {
		t.Fatalf("expected index %q to be gone, but it exists", name)
	} else if err != sql.ErrNoRows {
		t.Fatalf("unexpected error checking for index %q: %v", name, err)
	}
}

func assertPostgresTableNotExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	var got string
	err := db.QueryRow(`SELECT tablename FROM pg_tables WHERE tablename = $1`, name).Scan(&got)
	if err == nil {
		t.Fatalf("expected table %q to be gone, but it exists", name)
	} else if err != sql.ErrNoRows {
		t.Fatalf("unexpected error checking for table %q: %v", name, err)
	}
}
