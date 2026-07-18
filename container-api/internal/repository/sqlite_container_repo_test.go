package repository_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"contogether/container-api/internal/domain"
	"contogether/container-api/internal/repository"
)

func TestSaveFindUpdateDeleteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "containers.db")
	repo, err := repository.NewSQLiteContainerRepo(path)
	if err != nil {
		t.Fatalf("NewSQLiteContainerRepo failed: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()
	now := time.Now()
	c := &domain.Container{
		ID:        "ctr-1",
		DockerID:  "docker-abc",
		OwnerID:   "owner-1",
		Name:      "web",
		Image:     "alpine",
		Status:    domain.StatusCreated,
		CreatedAt: now,
		UpdatedAt: now,
	}
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

func TestListByOwnerReturnsOnlyThatOwnersContainersNewestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "containers.db")
	repo, err := repository.NewSQLiteContainerRepo(path)
	if err != nil {
		t.Fatalf("NewSQLiteContainerRepo failed: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()
	base := time.Now()
	containers := []*domain.Container{
		{ID: "ctr-a1", OwnerID: "owner-a", DockerID: "d1", Name: "a1", Image: "alpine", Status: domain.StatusCreated, CreatedAt: base, UpdatedAt: base},
		{ID: "ctr-a2", OwnerID: "owner-a", DockerID: "d2", Name: "a2", Image: "alpine", Status: domain.StatusCreated, CreatedAt: base.Add(time.Minute), UpdatedAt: base},
		{ID: "ctr-b1", OwnerID: "owner-b", DockerID: "d3", Name: "b1", Image: "alpine", Status: domain.StatusCreated, CreatedAt: base, UpdatedAt: base},
	}
	for _, c := range containers {
		if err := repo.Save(ctx, c); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	got, err := repo.ListByOwner(ctx, "owner-a")
	if err != nil {
		t.Fatalf("ListByOwner failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d containers, want 2 (only owner-a's)", len(got))
	}
	if got[0].ID != "ctr-a2" || got[1].ID != "ctr-a1" {
		t.Fatalf("got IDs [%s %s], want [ctr-a2 ctr-a1] (newest first)", got[0].ID, got[1].ID)
	}
}

func TestListByOwnerReturnsEmptyNotErrorWhenNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "containers.db")
	repo, err := repository.NewSQLiteContainerRepo(path)
	if err != nil {
		t.Fatalf("NewSQLiteContainerRepo failed: %v", err)
	}
	defer repo.Close()

	got, err := repo.ListByOwner(context.Background(), "nobody")
	if err != nil {
		t.Fatalf("ListByOwner failed: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d containers, want 0", len(got))
	}
}

func TestFindByIDReturnsNilNotErrorWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "containers.db")
	repo, err := repository.NewSQLiteContainerRepo(path)
	if err != nil {
		t.Fatalf("NewSQLiteContainerRepo failed: %v", err)
	}
	defer repo.Close()

	got, err := repo.FindByID(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("FindByID unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}
