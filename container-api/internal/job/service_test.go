package job_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"contogether/container-api/internal/domain"
	"contogether/container-api/internal/job"
	"contogether/logsys"
	"contogether/logsys/backends/memory"
)

type recordingOperator struct {
	mu        sync.Mutex
	started   []string
	stopped   []string
	removed   []string
	failOn    string // containerID that should fail every op
	rejectGet string // containerID for which GetContainer fails (simulates not-found/forbidden)
}

func (o *recordingOperator) GetContainer(_ context.Context, _, id string) (*domain.Container, error) {
	if id == o.rejectGet {
		return nil, fmt.Errorf("simulated rejection for %s", id)
	}
	return &domain.Container{ID: id}, nil
}

func (o *recordingOperator) StartContainer(_ context.Context, _, id string) error {
	return o.record(&o.started, id)
}
func (o *recordingOperator) StopContainer(_ context.Context, _, id string) error {
	return o.record(&o.stopped, id)
}
func (o *recordingOperator) DeleteContainer(_ context.Context, _, id string) error {
	return o.record(&o.removed, id)
}

func (o *recordingOperator) record(slice *[]string, id string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if id == o.failOn {
		return fmt.Errorf("simulated failure for %s", id)
	}
	*slice = append(*slice, id)
	return nil
}

func testLogger(t *testing.T) *logsys.Manager {
	t.Helper()
	store := memory.New()
	mgr := logsys.NewManager(store, store, store)
	t.Cleanup(func() { mgr.Close() })
	return mgr
}

func sequentialIDs() func() string {
	var n atomic.Int64
	return func() string { return fmt.Sprintf("job-%d", n.Add(1)) }
}

func waitForStatus(t *testing.T, svc *job.Service, id string, want domain.JobStatus) *domain.Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		j, err := svc.GetJob(context.Background(), id)
		if err != nil {
			t.Fatalf("GetJob failed: %v", err)
		}
		if j.Status == want {
			return j
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("job %s never reached status %s", id, want)
	return nil
}

func TestSubmitRunsToCompletion(t *testing.T) {
	op := &recordingOperator{}
	svc := job.NewService(job.NewMemoryStore(), op, testLogger(t), sequentialIDs(), 2, 10)
	defer svc.Close()

	j, err := svc.Submit(context.Background(), "owner-1", "ctr-1", domain.OpStartContainer)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	done := waitForStatus(t, svc, j.ID, domain.JobDone)
	if done.Error != "" {
		t.Fatalf("expected no error, got %q", done.Error)
	}

	op.mu.Lock()
	defer op.mu.Unlock()
	if len(op.started) != 1 || op.started[0] != "ctr-1" {
		t.Fatalf("expected StartContainer to run once for ctr-1, got %+v", op.started)
	}
}

func TestSubmitRecordsOperatorFailure(t *testing.T) {
	op := &recordingOperator{failOn: "ctr-bad"}
	svc := job.NewService(job.NewMemoryStore(), op, testLogger(t), sequentialIDs(), 2, 10)
	defer svc.Close()

	j, err := svc.Submit(context.Background(), "owner-1", "ctr-bad", domain.OpStopContainer)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	failed := waitForStatus(t, svc, j.ID, domain.JobFailed)
	if failed.Error == "" {
		t.Fatal("expected job.Error to be populated on failure")
	}
}

// TestSubmitFailsFastOnGetContainerError verifies that an
// ownership/existence error is returned synchronously from Submit
// itself — and that the operation never runs — rather than only
// surfacing later as an async job failure the caller would have to
// poll for.
func TestSubmitFailsFastOnGetContainerError(t *testing.T) {
	op := &recordingOperator{rejectGet: "ctr-forbidden"}
	svc := job.NewService(job.NewMemoryStore(), op, testLogger(t), sequentialIDs(), 2, 10)
	defer svc.Close()

	_, err := svc.Submit(context.Background(), "owner-1", "ctr-forbidden", domain.OpStartContainer)
	if err == nil {
		t.Fatal("expected Submit to fail synchronously for a rejected container")
	}

	time.Sleep(20 * time.Millisecond) // give a worker a chance to (wrongly) run
	op.mu.Lock()
	defer op.mu.Unlock()
	if len(op.started) != 0 {
		t.Fatalf("expected StartContainer never to run, but got %+v", op.started)
	}
}

func TestGetJobNotFound(t *testing.T) {
	svc := job.NewService(job.NewMemoryStore(), &recordingOperator{}, testLogger(t), sequentialIDs(), 1, 10)
	defer svc.Close()

	if _, err := svc.GetJob(context.Background(), "does-not-exist"); !errors.Is(err, job.ErrNotFound) {
		t.Fatalf("GetJob(unknown) = %v, want ErrNotFound", err)
	}
}

func TestSubmitAfterCloseFails(t *testing.T) {
	svc := job.NewService(job.NewMemoryStore(), &recordingOperator{}, testLogger(t), sequentialIDs(), 1, 10)
	if err := svc.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if _, err := svc.Submit(context.Background(), "owner-1", "ctr-1", domain.OpStartContainer); !errors.Is(err, job.ErrClosed) {
		t.Fatalf("Submit after Close = %v, want ErrClosed", err)
	}
}

// TestQueueFullReturnsErrQueueFull uses zero workers so nothing ever
// drains the queue, making "full" deterministic rather than a timing
// race against a live worker.
func TestQueueFullReturnsErrQueueFull(t *testing.T) {
	svc := job.NewService(job.NewMemoryStore(), &recordingOperator{}, testLogger(t), sequentialIDs(), 0, 1)
	defer svc.Close()

	if _, err := svc.Submit(context.Background(), "owner-1", "ctr-1", domain.OpStartContainer); err != nil {
		t.Fatalf("first Submit should fit in the buffer: %v", err)
	}
	if _, err := svc.Submit(context.Background(), "owner-1", "ctr-2", domain.OpStartContainer); !errors.Is(err, job.ErrQueueFull) {
		t.Fatalf("second Submit = %v, want ErrQueueFull", err)
	}
}

// TestCloseDrainsInFlightJobs submits several jobs to a slow operator
// and asserts Close doesn't return until all of them have actually run.
func TestCloseDrainsInFlightJobs(t *testing.T) {
	op := &recordingOperator{}
	svc := job.NewService(job.NewMemoryStore(), op, testLogger(t), sequentialIDs(), 2, 10)

	ids := make([]string, 0, 5)
	for i := range 5 {
		j, err := svc.Submit(context.Background(), "owner-1", fmt.Sprintf("ctr-%d", i), domain.OpDeleteContainer)
		if err != nil {
			t.Fatalf("Submit failed: %v", err)
		}
		ids = append(ids, j.ID)
	}

	if err := svc.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	op.mu.Lock()
	defer op.mu.Unlock()
	if len(op.removed) != len(ids) {
		t.Fatalf("expected all %d jobs to have run before Close returned, got %d", len(ids), len(op.removed))
	}
}
