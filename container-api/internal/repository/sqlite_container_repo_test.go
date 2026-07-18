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
	if got.Visibility != domain.VisibilityPrivate {
		t.Fatalf("visibility = %q, want default %q when unset on Save", got.Visibility, domain.VisibilityPrivate)
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

func TestListVisibleToReturnsOnlyThatOwnersContainersNewestFirst(t *testing.T) {
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

	got, err := repo.ListVisibleTo(ctx, "owner-a")
	if err != nil {
		t.Fatalf("ListVisibleTo failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d containers, want 2 (only owner-a's — owner-b's is private)", len(got))
	}
	if got[0].ID != "ctr-a2" || got[1].ID != "ctr-a1" {
		t.Fatalf("got IDs [%s %s], want [ctr-a2 ctr-a1] (newest first)", got[0].ID, got[1].ID)
	}
}

func TestListVisibleToIncludesOtherOwnersPublicContainers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "containers.db")
	repo, err := repository.NewSQLiteContainerRepo(path)
	if err != nil {
		t.Fatalf("NewSQLiteContainerRepo failed: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()
	now := time.Now()
	containers := []*domain.Container{
		{ID: "ctr-mine", OwnerID: "owner-a", DockerID: "d1", Name: "mine", Image: "alpine", Status: domain.StatusCreated, Visibility: domain.VisibilityPrivate, CreatedAt: now, UpdatedAt: now},
		{ID: "ctr-their-private", OwnerID: "owner-b", DockerID: "d2", Name: "priv", Image: "alpine", Status: domain.StatusCreated, Visibility: domain.VisibilityPrivate, CreatedAt: now, UpdatedAt: now},
		{ID: "ctr-their-public", OwnerID: "owner-b", DockerID: "d3", Name: "pub", Image: "alpine", Status: domain.StatusCreated, Visibility: domain.VisibilityPublic, CreatedAt: now, UpdatedAt: now},
	}
	for _, c := range containers {
		if err := repo.Save(ctx, c); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	got, err := repo.ListVisibleTo(ctx, "owner-a")
	if err != nil {
		t.Fatalf("ListVisibleTo failed: %v", err)
	}
	ids := make(map[string]bool)
	for _, c := range got {
		ids[c.ID] = true
	}
	if len(got) != 2 || !ids["ctr-mine"] || !ids["ctr-their-public"] {
		t.Fatalf("ListVisibleTo(owner-a) = %+v, want exactly [ctr-mine, ctr-their-public]", got)
	}
}

func TestListVisibleToReturnsEmptyNotErrorWhenNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "containers.db")
	repo, err := repository.NewSQLiteContainerRepo(path)
	if err != nil {
		t.Fatalf("NewSQLiteContainerRepo failed: %v", err)
	}
	defer repo.Close()

	got, err := repo.ListVisibleTo(context.Background(), "nobody")
	if err != nil {
		t.Fatalf("ListVisibleTo failed: %v", err)
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
