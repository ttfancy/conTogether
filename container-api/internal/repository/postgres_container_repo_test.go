package repository_test

import (
	"context"
	"os"
	"testing"
	"time"

	"contogether/container-api/internal/domain"
	"contogether/container-api/internal/repository"
)

// testPostgresDSN skips the test unless TEST_POSTGRES_DSN points at a
// reachable Postgres instance — these tests need a real server, unlike
// the SQLite tests which just use a temp file.
func testPostgresDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping Postgres repository tests")
	}
	return dsn
}

func TestPostgresSaveFindUpdateDeleteRoundTrip(t *testing.T) {
	dsn := testPostgresDSN(t)
	repo, err := repository.NewPostgresContainerRepo(dsn)
	if err != nil {
		t.Fatalf("NewPostgresContainerRepo failed: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()
	now := time.Now()
	c := &domain.Container{
		ID:        "pg-ctr-1",
		DockerID:  "docker-abc",
		OwnerID:   "owner-1",
		Name:      "web",
		Image:     "alpine",
		Status:    domain.StatusCreated,
		CreatedAt: now,
		UpdatedAt: now,
	}
	defer repo.Delete(ctx, c.ID) // best-effort cleanup even if an assertion fails

	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	got, err := repo.FindByID(ctx, c.ID)
	if err != nil || got == nil {
		t.Fatalf("FindByID failed: err=%v got=%v", err, got)
	}
	if got.DockerID != c.DockerID || got.OwnerID != c.OwnerID || got.Status != domain.StatusCreated {
		t.Fatalf("unexpected record: %+v", got)
	}

	if err := repo.UpdateStatus(ctx, c.ID, domain.StatusRunning); err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}
	updated, err := repo.FindByID(ctx, c.ID)
	if err != nil || updated.Status != domain.StatusRunning {
		t.Fatalf("expected status running, got %+v (err=%v)", updated, err)
	}

	if err := repo.UpdateVisibility(ctx, c.ID, domain.VisibilityPublic); err != nil {
		t.Fatalf("UpdateVisibility failed: %v", err)
	}
	madePublic, err := repo.FindByID(ctx, c.ID)
	if err != nil || madePublic.Visibility != domain.VisibilityPublic {
		t.Fatalf("expected visibility public, got %+v (err=%v)", madePublic, err)
	}

	if err := repo.Delete(ctx, c.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	gone, err := repo.FindByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("FindByID after delete failed: %v", err)
	}
	if gone != nil {
		t.Fatalf("expected nil after delete, got %+v", gone)
	}
}

func TestPostgresListVisibleToReturnsOnlyThatOwnersContainers(t *testing.T) {
	dsn := testPostgresDSN(t)
	repo, err := repository.NewPostgresContainerRepo(dsn)
	if err != nil {
		t.Fatalf("NewPostgresContainerRepo failed: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()
	base := time.Now()
	containers := []*domain.Container{
		{ID: "pg-list-a1", OwnerID: "pg-owner-a", DockerID: "d1", Name: "a1", Image: "alpine", Status: domain.StatusCreated, CreatedAt: base, UpdatedAt: base},
		{ID: "pg-list-a2", OwnerID: "pg-owner-a", DockerID: "d2", Name: "a2", Image: "alpine", Status: domain.StatusCreated, CreatedAt: base.Add(time.Minute), UpdatedAt: base},
		{ID: "pg-list-b1", OwnerID: "pg-owner-b", DockerID: "d3", Name: "b1", Image: "alpine", Status: domain.StatusCreated, CreatedAt: base, UpdatedAt: base},
	}
	for _, c := range containers {
		defer repo.Delete(ctx, c.ID)
		if err := repo.Save(ctx, c); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	got, err := repo.ListVisibleTo(ctx, "pg-owner-a")
	if err != nil {
		t.Fatalf("ListVisibleTo failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d containers, want 2 (only pg-owner-a's)", len(got))
	}
	if got[0].ID != "pg-list-a2" || got[1].ID != "pg-list-a1" {
		t.Fatalf("got IDs [%s %s], want [pg-list-a2 pg-list-a1] (newest first)", got[0].ID, got[1].ID)
	}
}

func TestPostgresFindByIDReturnsNilNotErrorWhenMissing(t *testing.T) {
	dsn := testPostgresDSN(t)
	repo, err := repository.NewPostgresContainerRepo(dsn)
	if err != nil {
		t.Fatalf("NewPostgresContainerRepo failed: %v", err)
	}
	defer repo.Close()

	got, err := repo.FindByID(context.Background(), "pg-does-not-exist")
	if err != nil {
		t.Fatalf("FindByID unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}
