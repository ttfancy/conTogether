package container_test

import (
	"context"
	"errors"
	"testing"

	"contogether/container-api/internal/container"
	"contogether/container-api/internal/domain"
)

// newTestWrapper skips unless a real Docker daemon is reachable — same
// pattern as the Postgres repository tests skipping without
// TEST_POSTGRES_DSN: this needs a real external dependency, not a fake.
func newTestWrapper(t *testing.T) *container.DockerWrapper {
	t.Helper()
	w, err := container.NewDockerWrapper()
	if err != nil {
		t.Skipf("no reachable Docker daemon; skipping: %v", err)
	}
	return w
}

// TestCreateContainerNameConflictIsRecognized is a regression test for a
// gap found through actual UI testing: creating a second container with
// a name already in use surfaced as a generic "internal server error"
// (the raw Docker SDK conflict error wasn't recognized anywhere), which
// told the user nothing about what actually went wrong or how to fix it.
func TestCreateContainerNameConflictIsRecognized(t *testing.T) {
	w := newTestWrapper(t)
	defer w.Close()
	ctx := context.Background()

	name := "contogether-test-name-conflict"
	spec := domain.ContainerSpec{Image: "alpine", Name: name}

	firstID, err := w.CreateContainer(ctx, spec)
	if err != nil {
		t.Fatalf("first CreateContainer failed: %v", err)
	}
	defer w.RemoveContainer(ctx, firstID)

	_, err = w.CreateContainer(ctx, spec)
	if !errors.Is(err, domain.ErrContainerNameConflict) {
		t.Fatalf("second CreateContainer (same name) = %v, want ErrContainerNameConflict", err)
	}
}
