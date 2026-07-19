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

	"contogether/container-api/internal/applog"
	"contogether/container-api/internal/domain"
	"contogether/container-api/internal/service"
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

func (r *fakeRepo) ListVisibleTo(_ context.Context, ownerID string) ([]*domain.Container, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.Container
	for _, c := range r.byID {
		if c.OwnerID == ownerID || c.Visibility == domain.VisibilityPublic {
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

func (r *fakeRepo) UpdateVisibility(_ context.Context, id string, visibility domain.Visibility) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.byID[id]
	if !ok {
		return fmt.Errorf("no such container: %s", id)
	}
	c.Visibility = visibility
	return nil
}

func (r *fakeRepo) SetDockerID(_ context.Context, id, dockerID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.byID[id]
	if !ok {
		return fmt.Errorf("no such container: %s", id)
	}
	c.DockerID = dockerID
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
	execCalls     []string // dockerIDs ExecContainer was called with
	failCreate    error    // if set, CreateContainer returns this error instead of succeeding
	pullOnCreate  bool     // if set, CreateContainer calls onPulling before returning
	// exitsImmediately simulates a container with no long-lived command:
	// IsRunning reports false right after Start, as if the process
	// already quit on its own. Zero value (false) matches every
	// existing test's assumption that a started container stays up.
	exitsImmediately bool
	isRunningErr     error // if set, IsRunning returns this error instead of a bool
}

func (d *fakeDocker) CreateContainer(_ context.Context, spec domain.ContainerSpec, onPulling func()) (string, error) {
	if d.pullOnCreate && onPulling != nil {
		onPulling()
	}
	if d.failCreate != nil {
		return "", d.failCreate
	}
	return "docker-" + spec.Name, nil
}
func (*fakeDocker) StartContainer(context.Context, string) error  { return nil }
func (*fakeDocker) StopContainer(context.Context, string) error   { return nil }
func (*fakeDocker) RemoveContainer(context.Context, string) error { return nil }

func (d *fakeDocker) IsRunning(context.Context, string) (bool, error) {
	if d.isRunningErr != nil {
		return false, d.isRunningErr
	}
	return !d.exitsImmediately, nil
}

func (d *fakeDocker) StreamLogs(_ context.Context, dockerID, _ string) (io.ReadCloser, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.streamCalls = append(d.streamCalls, dockerID)
	return io.NopCloser(strings.NewReader(d.streamContent)), nil
}

func (d *fakeDocker) ExecContainer(_ context.Context, dockerID string) (domain.ExecSession, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.execCalls = append(d.execCalls, dockerID)
	return &fakeExecSession{}, nil
}

// fakeExecSession is a no-op stand-in — these tests only care whether
// Exec reaches the Docker client at all (and with the right ownership
// check), not what a real exec session does once opened.
type fakeExecSession struct{}

func (*fakeExecSession) Read([]byte) (int, error)                 { return 0, io.EOF }
func (*fakeExecSession) Write(p []byte) (int, error)              { return len(p), nil }
func (*fakeExecSession) Close() error                             { return nil }
func (*fakeExecSession) Resize(context.Context, uint, uint) error { return nil }

func testLogger(t *testing.T) *applog.Manager {
	t.Helper()
	mgr := applog.NewMemoryManager()
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

// mustCreateContainer drives BeginCreateContainer then CreateContainer
// (the same two-step flow a job worker runs in production) back to
// back, synchronously — standing in for the single-call synchronous
// CreateContainer these tests used before container creation became
// asynchronous. Most of them are about other behavior (GetContainer,
// visibility, listing, start/stop/delete...) and just need a fully
// realized container to exist; the creation flow itself is exercised
// directly by the Test*CreateContainer* tests below.
func mustCreateContainer(t *testing.T, svc *service.ContainerService, ownerID string, spec domain.ContainerSpec) *domain.Container {
	t.Helper()
	ctx := context.Background()
	c, err := svc.BeginCreateContainer(ctx, ownerID, spec)
	if err != nil {
		t.Fatalf("BeginCreateContainer failed: %v", err)
	}
	if err := svc.CreateContainer(ctx, ownerID, c.ID, spec, func(string) {}); err != nil {
		t.Fatalf("CreateContainer (finish) failed: %v", err)
	}
	updated, err := svc.GetContainer(ctx, ownerID, c.ID)
	if err != nil {
		t.Fatalf("GetContainer after create failed: %v", err)
	}
	return updated
}

func TestBeginCreateContainerPersistsPendingRecord(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := context.Background()

	c, err := svc.BeginCreateContainer(ctx, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web"})
	if err != nil {
		t.Fatalf("BeginCreateContainer failed: %v", err)
	}
	if c.Status != domain.StatusPending {
		t.Fatalf("status = %s, want %s", c.Status, domain.StatusPending)
	}
	if c.DockerID != "" {
		t.Fatalf("expected no DockerID before the create job runs, got %q", c.DockerID)
	}

	stored, err := repo.FindByID(ctx, c.ID)
	if err != nil || stored == nil {
		t.Fatalf("expected a placeholder record to be persisted, err=%v stored=%v", err, stored)
	}
	if stored.OwnerID != "owner-1" || stored.Status != domain.StatusPending {
		t.Fatalf("unexpected stored record: %+v", stored)
	}
}

// TestCreateContainerFinishesPendingRecord is the async job-worker half
// of creation: given a placeholder BeginCreateContainer already
// persisted, CreateContainer should realize it — a real DockerID and
// StatusCreated — and, since this fakeDocker doesn't need to pull,
// report no stages at all.
func TestCreateContainerFinishesPendingRecord(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := context.Background()

	spec := domain.ContainerSpec{Image: "alpine", Name: "web"}
	c, err := svc.BeginCreateContainer(ctx, "owner-1", spec)
	if err != nil {
		t.Fatalf("BeginCreateContainer failed: %v", err)
	}

	var stages []string
	if err := svc.CreateContainer(ctx, "owner-1", c.ID, spec, func(stage string) { stages = append(stages, stage) }); err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}

	stored, err := repo.FindByID(ctx, c.ID)
	if err != nil || stored == nil {
		t.Fatalf("expected record to still be persisted, err=%v stored=%v", err, stored)
	}
	if stored.Status != domain.StatusCreated {
		t.Fatalf("status = %s, want %s", stored.Status, domain.StatusCreated)
	}
	if stored.DockerID != "docker-web" {
		t.Fatalf("DockerID = %q, want %q", stored.DockerID, "docker-web")
	}
	if len(stages) != 0 {
		t.Fatalf("expected no reportStage calls when the image doesn't need pulling, got %+v", stages)
	}
}

// TestCreateContainerReportsPullingImageStage confirms reportStage is
// wired through to the Docker client's onPulling callback — this is
// the whole point of Job.Stage existing: a client polling the job sees
// "pulling image" while a slow pull is happening, not just silence.
func TestCreateContainerReportsPullingImageStage(t *testing.T) {
	svc, _, docker := newTestServiceWithDocker(t)
	docker.pullOnCreate = true
	ctx := context.Background()

	spec := domain.ContainerSpec{Image: "redis", Name: "web"}
	c, err := svc.BeginCreateContainer(ctx, "owner-1", spec)
	if err != nil {
		t.Fatalf("BeginCreateContainer failed: %v", err)
	}

	var stages []string
	if err := svc.CreateContainer(ctx, "owner-1", c.ID, spec, func(stage string) { stages = append(stages, stage) }); err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}
	if len(stages) != 1 || stages[0] != "pulling image" {
		t.Fatalf("stages = %+v, want [%q]", stages, "pulling image")
	}
}

// TestCreateContainerMarksRecordFailedOnDockerError confirms a failed
// Docker-side create doesn't leave the placeholder stuck in "pending"
// forever — it moves to StatusFailed, visible to the owner instead of
// silently hanging.
func TestCreateContainerMarksRecordFailedOnDockerError(t *testing.T) {
	svc, repo, docker := newTestServiceWithDocker(t)
	docker.failCreate = errors.New("simulated docker failure")
	ctx := context.Background()

	spec := domain.ContainerSpec{Image: "alpine", Name: "web"}
	c, err := svc.BeginCreateContainer(ctx, "owner-1", spec)
	if err != nil {
		t.Fatalf("BeginCreateContainer failed: %v", err)
	}

	if err := svc.CreateContainer(ctx, "owner-1", c.ID, spec, func(string) {}); err == nil {
		t.Fatal("expected CreateContainer to fail")
	}

	stored, err := repo.FindByID(ctx, c.ID)
	if err != nil || stored == nil {
		t.Fatalf("expected record to still exist, err=%v stored=%v", err, stored)
	}
	if stored.Status != domain.StatusFailed {
		t.Fatalf("status = %s, want %s", stored.Status, domain.StatusFailed)
	}
}

func TestStreamLogsDelegatesToDockerClientWithResolvedDockerID(t *testing.T) {
	svc, _, docker := newTestServiceWithDocker(t)
	docker.streamContent = "line one\nline two\n"
	ctx := context.Background()

	c := mustCreateContainer(t, svc, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web"})

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

	c := mustCreateContainer(t, svc, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web"})

	if _, err := svc.StreamLogs(ctx, "owner-2", c.ID, "100"); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("StreamLogs as different owner = %v, want ErrForbidden", err)
	}

	docker.mu.Lock()
	defer docker.mu.Unlock()
	if len(docker.streamCalls) != 0 {
		t.Fatalf("expected StreamLogs to never reach the docker client, got calls %+v", docker.streamCalls)
	}
}

func TestExecReachesDockerClientForOwner(t *testing.T) {
	svc, _, docker := newTestServiceWithDocker(t)
	ctx := context.Background()

	c := mustCreateContainer(t, svc, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web"})

	session, err := svc.Exec(ctx, "owner-1", c.ID)
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if session == nil {
		t.Fatal("expected a non-nil session")
	}

	docker.mu.Lock()
	defer docker.mu.Unlock()
	if len(docker.execCalls) != 1 || docker.execCalls[0] != c.DockerID {
		t.Fatalf("ExecContainer calls = %+v, want [%q]", docker.execCalls, c.DockerID)
	}
}

// TestExecForbiddenForOtherOwnerEvenWhenPublic is the same
// authorization boundary as start/stop/delete: Exec grants real control
// (a shell can do anything), so it must use the strict owner-only check
// regardless of visibility — unlike GetContainer/StreamLogs, which a
// public container's visibility does extend to.
func TestExecForbiddenForOtherOwnerEvenWhenPublic(t *testing.T) {
	svc, _, docker := newTestServiceWithDocker(t)
	ctx := context.Background()

	c := mustCreateContainer(t, svc, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web", Visibility: domain.VisibilityPublic})

	if _, err := svc.Exec(ctx, "owner-2", c.ID); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("Exec as different owner on a public container = %v, want ErrForbidden", err)
	}

	docker.mu.Lock()
	defer docker.mu.Unlock()
	if len(docker.execCalls) != 0 {
		t.Fatalf("expected Exec to never reach the docker client, got calls %+v", docker.execCalls)
	}
}

func TestGetContainerForbiddenForOtherOwner(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	c := mustCreateContainer(t, svc, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web"})

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

func TestGetContainerVisibleToAnyoneWhenPublic(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	c := mustCreateContainer(t, svc, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web", Visibility: domain.VisibilityPublic})

	got, err := svc.GetContainer(ctx, "owner-2", c.ID)
	if err != nil {
		t.Fatalf("GetContainer(public, other owner) failed: %v", err)
	}
	if got.ID != c.ID {
		t.Fatalf("got container %+v, want %+v", got, c)
	}
}

func TestCreateContainerRejectsInvalidVisibility(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.BeginCreateContainer(ctx, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web", Visibility: "sorta-public"}); !errors.Is(err, service.ErrInvalidVisibility) {
		t.Fatalf("BeginCreateContainer with invalid visibility = %v, want ErrInvalidVisibility", err)
	}
}

// TestStartStopDeleteForbiddenForNonOwnerEvenWhenPublic is the critical
// authorization boundary: visibility grants read access only.
// GetContainer/StreamLogs allow a non-owner to read a public container,
// but Start/Stop/Delete must still reject them outright — otherwise
// "public" would silently become "anyone can also control this
// container," which is not what visibility means here.
func TestStartStopDeleteForbiddenForNonOwnerEvenWhenPublic(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	c := mustCreateContainer(t, svc, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web", Visibility: domain.VisibilityPublic})

	// Sanity check: a non-owner CAN read it (this is the intended effect
	// of "public").
	if _, err := svc.GetContainer(ctx, "owner-2", c.ID); err != nil {
		t.Fatalf("GetContainer(public, other owner) failed: %v", err)
	}

	if err := svc.StartContainer(ctx, "owner-2", c.ID); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("StartContainer as non-owner on a public container = %v, want ErrForbidden", err)
	}
	if err := svc.StopContainer(ctx, "owner-2", c.ID); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("StopContainer as non-owner on a public container = %v, want ErrForbidden", err)
	}
	if err := svc.DeleteContainer(ctx, "owner-2", c.ID); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("DeleteContainer as non-owner on a public container = %v, want ErrForbidden", err)
	}
	if err := svc.SetVisibility(ctx, "owner-2", c.ID, domain.VisibilityPrivate); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("SetVisibility as non-owner on a public container = %v, want ErrForbidden", err)
	}

	// And MustOwnContainer — job.ContainerOperator's fail-fast pre-check —
	// must reject the non-owner too, for the same reason.
	if _, err := svc.MustOwnContainer(ctx, "owner-2", c.ID); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("MustOwnContainer as non-owner on a public container = %v, want ErrForbidden", err)
	}
}

func TestSetVisibilityByOwnerThenVisibleToOthers(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	c := mustCreateContainer(t, svc, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web"})
	if _, err := svc.GetContainer(ctx, "owner-2", c.ID); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("GetContainer before making public = %v, want ErrForbidden", err)
	}

	if err := svc.SetVisibility(ctx, "owner-1", c.ID, domain.VisibilityPublic); err != nil {
		t.Fatalf("SetVisibility failed: %v", err)
	}
	got, err := svc.GetContainer(ctx, "owner-2", c.ID)
	if err != nil {
		t.Fatalf("GetContainer after making public failed: %v", err)
	}
	if got.Visibility != domain.VisibilityPublic {
		t.Fatalf("visibility = %q, want %q", got.Visibility, domain.VisibilityPublic)
	}
}

func TestListContainersReturnsOnlyOwnersContainers(t *testing.T) {
	svc, _ := newTestService(t)

	mustCreateContainer(t, svc, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "a1"})
	mustCreateContainer(t, svc, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "a2"})
	mustCreateContainer(t, svc, "owner-2", domain.ContainerSpec{Image: "alpine", Name: "b1"})

	got, err := svc.ListContainers(context.Background(), "owner-1")
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

func TestListContainersIncludesOtherOwnersPublicButNotPrivate(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	mine := mustCreateContainer(t, svc, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "mine"})
	mustCreateContainer(t, svc, "owner-2", domain.ContainerSpec{Image: "alpine", Name: "theirs-private"})
	theirsPublic := mustCreateContainer(t, svc, "owner-2", domain.ContainerSpec{Image: "alpine", Name: "theirs-public", Visibility: domain.VisibilityPublic})

	got, err := svc.ListContainers(ctx, "owner-1")
	if err != nil {
		t.Fatalf("ListContainers failed: %v", err)
	}
	ids := make(map[string]bool)
	for _, c := range got {
		ids[c.ID] = true
	}
	if len(got) != 2 || !ids[mine.ID] || !ids[theirsPublic.ID] {
		t.Fatalf("ListContainers(owner-1) = %+v, want exactly [mine, theirs-public]", got)
	}
}

func TestStartStopDeleteLifecycle(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := context.Background()

	c := mustCreateContainer(t, svc, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web"})

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

// TestStartContainerWithNoLongRunningCommandReportsExited is the actual
// bug this guards against: found through real usage — a container with
// nothing keeping it alive (e.g. alpine with no CMD override) exits on
// its own right after Docker starts it, but StartContainer succeeding
// used to always mark it "running" regardless, leaving that status
// permanently wrong until some other action happened to correct it.
func TestStartContainerWithNoLongRunningCommandReportsExited(t *testing.T) {
	svc, repo, docker := newTestServiceWithDocker(t)
	ctx := context.Background()

	c := mustCreateContainer(t, svc, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web"})
	docker.exitsImmediately = true

	if err := svc.StartContainer(ctx, "owner-1", c.ID); err != nil {
		t.Fatalf("StartContainer failed: %v", err)
	}
	started, _ := repo.FindByID(ctx, c.ID)
	if started.Status != domain.StatusExited {
		t.Fatalf("status after start = %s, want %s", started.Status, domain.StatusExited)
	}
}

// TestStartContainerFallsBackToRunningIfIsRunningCheckFails confirms a
// failure in the follow-up check doesn't fail Start itself (the daemon
// really did start the container) — it just falls back to the prior
// default of assuming it's running.
func TestStartContainerFallsBackToRunningIfIsRunningCheckFails(t *testing.T) {
	svc, repo, docker := newTestServiceWithDocker(t)
	ctx := context.Background()

	c := mustCreateContainer(t, svc, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web"})
	docker.isRunningErr = errors.New("simulated inspect failure")

	if err := svc.StartContainer(ctx, "owner-1", c.ID); err != nil {
		t.Fatalf("StartContainer failed: %v", err)
	}
	started, _ := repo.FindByID(ctx, c.ID)
	if started.Status != domain.StatusRunning {
		t.Fatalf("status after start = %s, want %s (fallback when IsRunning itself fails)", started.Status, domain.StatusRunning)
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

	c := mustCreateContainer(t, svc, "owner-1", domain.ContainerSpec{Image: "alpine", Name: "web"})

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
