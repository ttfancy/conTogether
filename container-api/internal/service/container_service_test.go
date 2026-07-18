package service_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"contogether/container-api/internal/domain"
	"contogether/container-api/internal/service"
	"contogether/logsys"
	"contogether/logsys/backends/memory"
)

type fakeRepo struct {
	mu   sync.Mutex
	byID map[string]*domain.Container
}

func newFakeRepo() *fakeRepo { return &fakeRepo{byID: make(map[string]*domain.Container)} }

func (r *fakeRepo) Save(_ context.Context, c *domain.Container) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *c
	r.byID[c.ID] = &cp
	return nil
}

func (r *fakeRepo) FindByID(_ context.Context, id string) (*domain.Container, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.byID[id]
	if !ok {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}

func (r *fakeRepo) ListByOwner(_ context.Context, ownerID string) ([]*domain.Container, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.Container
	for _, c := range r.byID {
		if c.OwnerID == ownerID {
			cp := *c
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *fakeRepo) UpdateStatus(_ context.Context, id string, status domain.ContainerStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.byID[id]
	if !ok {
		return fmt.Errorf("no such container: %s", id)
	}
	c.Status = status
	return nil
}

func (r *fakeRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, id)
	return nil
}

type fakeDocker struct {
	mu            sync.Mutex
	streamContent string
	streamCalls   []string // dockerIDs StreamLogs was called with
}

func (*fakeDocker) CreateContainer(_ context.Context, spec domain.ContainerSpec) (string, error) {
	return "docker-" + spec.Name, nil
}
func (*fakeDocker) StartContainer(context.Context, string) error  { return nil }
func (*fakeDocker) StopContainer(context.Context, string) error   { return nil }
func (*fakeDocker) RemoveContainer(context.Context, string) error { return nil }

func (d *fakeDocker) StreamLogs(_ context.Context, dockerID, _ string) (io.ReadCloser, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.streamCalls = append(d.streamCalls, dockerID)
	return io.NopCloser(strings.NewReader(d.streamContent)), nil
}

func testLogger(t *testing.T) *logsys.Manager {
	t.Helper()
	store := memory.New()
	mgr := logsys.NewManager(store, store, store)
	t.Cleanup(func() { mgr.Close() })
	return mgr
}

func sequentialIDs() service.IDGenerator {
	var n atomic.Int64
	return func() string {
		return fmt.Sprintf("ctr-%d", n.Add(1))
	}
}

func newTestService(t *testing.T) (*service.ContainerService, *fakeRepo) {
	svc, repo, _ := newTestServiceWithDocker(t)
	return svc, repo
}

func newTestServiceWithDocker(t *testing.T) (*service.ContainerService, *fakeRepo, *fakeDocker) {
	repo := newFakeRepo()
	docker := &fakeDocker{}
	svc := service.NewContainerService(repo, docker, testLogger(t), sequentialIDs())
	return svc, repo, docker
}

func TestCreateContainerPersistsRecord(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := context.Background()

	c, err := svc.CreateContainer(ctx, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web"})
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}
	if c.Status != domain.StatusCreated {
		t.Fatalf("status = %s, want %s", c.Status, domain.StatusCreated)
	}

	stored, err := repo.FindByID(ctx, c.ID)
	if err != nil || stored == nil {
		t.Fatalf("expected record to be persisted, err=%v stored=%v", err, stored)
	}
	if stored.OwnerID != "owner-1" || stored.DockerID != "docker-web" {
		t.Fatalf("unexpected stored record: %+v", stored)
	}
}

func TestStreamLogsDelegatesToDockerClientWithResolvedDockerID(t *testing.T) {
	svc, _, docker := newTestServiceWithDocker(t)
	docker.streamContent = "line one\nline two\n"
	ctx := context.Background()

	c, err := svc.CreateContainer(ctx, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web"})
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}

	stream, err := svc.StreamLogs(ctx, "owner-1", c.ID, "100")
	if err != nil {
		t.Fatalf("StreamLogs failed: %v", err)
	}
	defer stream.Close()

	data, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("reading stream failed: %v", err)
	}
	if string(data) != "line one\nline two\n" {
		t.Fatalf("stream content = %q, want the fake docker client's content", data)
	}

	docker.mu.Lock()
	defer docker.mu.Unlock()
	if len(docker.streamCalls) != 1 || docker.streamCalls[0] != c.DockerID {
		t.Fatalf("StreamLogs called docker client with %+v, want [%q]", docker.streamCalls, c.DockerID)
	}
}

func TestStreamLogsForbiddenForOtherOwnerNeverReachesDocker(t *testing.T) {
	svc, _, docker := newTestServiceWithDocker(t)
	ctx := context.Background()

	c, err := svc.CreateContainer(ctx, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web"})
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}

	if _, err := svc.StreamLogs(ctx, "owner-2", c.ID, "100"); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("StreamLogs as different owner = %v, want ErrForbidden", err)
	}

	docker.mu.Lock()
	defer docker.mu.Unlock()
	if len(docker.streamCalls) != 0 {
		t.Fatalf("expected StreamLogs to never reach the docker client, got calls %+v", docker.streamCalls)
	}
}

func TestGetContainerForbiddenForOtherOwner(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	c, err := svc.CreateContainer(ctx, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web"})
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}

	if _, err := svc.GetContainer(ctx, "owner-2", c.ID); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("GetContainer as different owner = %v, want ErrForbidden", err)
	}
}

func TestGetContainerNotFound(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.GetContainer(context.Background(), "owner-1", "does-not-exist"); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("GetContainer(unknown) = %v, want ErrNotFound", err)
	}
}

func TestListContainersReturnsOnlyOwnersContainers(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.CreateContainer(ctx, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "a1"}); err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}
	if _, err := svc.CreateContainer(ctx, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "a2"}); err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}
	if _, err := svc.CreateContainer(ctx, "owner-2", domain.ContainerSpec{Image: "alpine", Name: "b1"}); err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}

	got, err := svc.ListContainers(ctx, "owner-1")
	if err != nil {
		t.Fatalf("ListContainers failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d containers, want 2 (only owner-1's)", len(got))
	}
	for _, c := range got {
		if c.OwnerID != "owner-1" {
			t.Fatalf("ListContainers(owner-1) returned a container owned by %q", c.OwnerID)
		}
	}
}

func TestStartStopDeleteLifecycle(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := context.Background()

	c, err := svc.CreateContainer(ctx, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web"})
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}

	if err := svc.StartContainer(ctx, "owner-1", c.ID); err != nil {
		t.Fatalf("StartContainer failed: %v", err)
	}
	started, _ := repo.FindByID(ctx, c.ID)
	if started.Status != domain.StatusRunning {
		t.Fatalf("status after start = %s, want %s", started.Status, domain.StatusRunning)
	}

	if err := svc.StopContainer(ctx, "owner-1", c.ID); err != nil {
		t.Fatalf("StopContainer failed: %v", err)
	}
	stopped, _ := repo.FindByID(ctx, c.ID)
	if stopped.Status != domain.StatusStopped {
		t.Fatalf("status after stop = %s, want %s", stopped.Status, domain.StatusStopped)
	}

	if err := svc.DeleteContainer(ctx, "owner-1", c.ID); err != nil {
		t.Fatalf("DeleteContainer failed: %v", err)
	}
	if _, err := svc.GetContainer(ctx, "owner-1", c.ID); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("GetContainer after delete = %v, want ErrNotFound", err)
	}
}

// TestConcurrentStartAndDelete is the concurrency-control test: many
// concurrent Start calls race a single Delete call on the same
// container. Run with -race to catch data races in the per-ID lock
// registry itself, and assert the outcome is always coherent — every
// Start either succeeds or fails with ErrNotFound (never anything else,
// e.g. a Docker-level error from operating on an already-removed
// container), and the container is guaranteed gone once everything
// settles.
func TestConcurrentStartAndDelete(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	c, err := svc.CreateContainer(ctx, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web"})
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 20)
	for i := range errs {
		wg.Go(func() {
			errs[i] = svc.StartContainer(ctx, "owner-1", c.ID)
		})
	}
	wg.Go(func() {
		if err := svc.DeleteContainer(ctx, "owner-1", c.ID); err != nil {
			t.Errorf("DeleteContainer failed: %v", err)
		}
	})
	wg.Wait()

	for i, err := range errs {
		if err != nil && !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("start[%d] = %v, want nil or ErrNotFound", i, err)
		}
	}

	if _, err := svc.GetContainer(ctx, "owner-1", c.ID); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("GetContainer after concurrent ops = %v, want ErrNotFound (delete always runs)", err)
	}
}
