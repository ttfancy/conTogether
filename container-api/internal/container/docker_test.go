package container_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"

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

	firstID, err := w.CreateContainer(ctx, spec, nil)
	if err != nil {
		t.Fatalf("first CreateContainer failed: %v", err)
	}
	defer w.RemoveContainer(ctx, firstID)

	_, err = w.CreateContainer(ctx, spec, nil)
	if !errors.Is(err, domain.ErrContainerNameConflict) {
		t.Fatalf("second CreateContainer (same name) = %v, want ErrContainerNameConflict", err)
	}
}

// TestCreateContainerPullsMissingImage is a regression test for a gap
// found through actual UI testing: picking a real, correctly-spelled
// image from the create form's dropdown still surfaced a generic
// "internal server error" if that image wasn't already cached locally,
// because CreateContainer never pulled — only whatever happened to
// already be present (e.g. alpine in dev) ever worked.
func TestCreateContainerPullsMissingImage(t *testing.T) {
	w := newTestWrapper(t)
	defer w.Close()
	ctx := context.Background()

	const image = "hello-world"
	removeImageIfPresent(t, image)

	spec := domain.ContainerSpec{Image: image, Name: "contogether-test-pull-missing-image"}
	id, err := w.CreateContainer(ctx, spec, nil)
	if err != nil {
		t.Fatalf("CreateContainer with an uncached image failed: %v", err)
	}
	defer w.RemoveContainer(ctx, id)
}

// TestCreateContainerCallsOnPullingWhenImageMissing confirms the
// callback callers rely on to report a "pulling image" stage actually
// fires — this is the only signal a caller has that CreateContainer
// took the slow path, so a regression here would silently mean the UI
// never shows that stage even though a pull is genuinely happening.
func TestCreateContainerCallsOnPullingWhenImageMissing(t *testing.T) {
	w := newTestWrapper(t)
	defer w.Close()
	ctx := context.Background()

	const image = "hello-world"
	removeImageIfPresent(t, image)

	var called bool
	spec := domain.ContainerSpec{Image: image, Name: "contogether-test-onpulling-callback"}
	id, err := w.CreateContainer(ctx, spec, func() { called = true })
	if err != nil {
		t.Fatalf("CreateContainer with an uncached image failed: %v", err)
	}
	defer w.RemoveContainer(ctx, id)

	if !called {
		t.Fatal("onPulling was never called even though the image had to be pulled")
	}
}

// TestIsRunningTrueWhileContainerHasWorkToDo is the baseline: a
// container running a long-lived command should report running right
// after Start.
func TestIsRunningTrueWhileContainerHasWorkToDo(t *testing.T) {
	w := newTestWrapper(t)
	defer w.Close()
	ctx := context.Background()

	spec := domain.ContainerSpec{Image: "alpine", Name: "contogether-test-isrunning-true", Cmd: []string{"sleep", "30"}}
	id, err := w.CreateContainer(ctx, spec, nil)
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}
	defer w.RemoveContainer(ctx, id)

	if err := w.StartContainer(ctx, id); err != nil {
		t.Fatalf("StartContainer failed: %v", err)
	}

	running, err := w.IsRunning(ctx, id)
	if err != nil {
		t.Fatalf("IsRunning failed: %v", err)
	}
	if !running {
		t.Fatal("expected a container running `sleep 30` to still be running")
	}
}

// TestIsRunningFalseWhenContainerExitsOnItsOwn is the real scenario
// IsRunning exists to catch, found through actual UI testing: a
// container with no long-lived command (no CMD override) starts and
// exits on its own almost immediately — ContainerStart itself never
// reports that as an error, so without this check the container would
// be reported "running" indefinitely even though it already isn't.
func TestIsRunningFalseWhenContainerExitsOnItsOwn(t *testing.T) {
	w := newTestWrapper(t)
	defer w.Close()
	ctx := context.Background()

	spec := domain.ContainerSpec{Image: "alpine", Name: "contogether-test-isrunning-false"}
	id, err := w.CreateContainer(ctx, spec, nil)
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}
	defer w.RemoveContainer(ctx, id)

	if err := w.StartContainer(ctx, id); err != nil {
		t.Fatalf("StartContainer failed: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var running bool
	for time.Now().Before(deadline) {
		running, err = w.IsRunning(ctx, id)
		if err != nil {
			t.Fatalf("IsRunning failed: %v", err)
		}
		if !running {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if running {
		t.Fatal("expected a container with no long-lived command to have exited on its own")
	}
}

// removeImageIfPresent talks to the Docker SDK directly rather than
// through DockerWrapper — removing an image is test setup, not
// something the service layer needs, so it doesn't belong on the
// production wrapper's public surface.
func removeImageIfPresent(t *testing.T, image string) {
	t.Helper()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("connect to Docker for test setup: %v", err)
	}
	defer cli.Close()
	if _, err := cli.ImageRemove(context.Background(), image, types.ImageRemoveOptions{Force: true}); err != nil {
		t.Logf("removeImageIfPresent(%q): %v (fine if it was already absent)", image, err)
	}
}
