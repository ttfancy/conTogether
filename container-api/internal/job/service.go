// Package job implements asynchronous processing of long-running
// container operations: Submit persists a domain.Job and hands work to
// a bounded worker pool, returning immediately with a Job ID; callers
// poll GetJob for status. This is the seam between the HTTP layer
// (which must respond fast) and ContainerService (whose Docker calls
// can be slow).
package job

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"contogether/container-api/internal/applog"
	"contogether/container-api/internal/domain"
)

var (
	ErrNotFound  = errors.New("job: not found")
	ErrClosed    = errors.New("job: service is closed")
	ErrQueueFull = errors.New("job: queue is full, try again later")
)

// ContainerOperator is the subset of service.ContainerService a worker
// needs to actually execute a job. service.ContainerService satisfies
// this without either package importing the other. MustOwnContainer is
// used by Submit itself (see below) to fail fast on ownership/existence
// errors rather than only surfacing them later as a job failure —
// deliberately the strict owner-only check (service.ContainerService's
// exported MustOwnContainer), not the public-readable GetContainer:
// visibility grants read access to someone else's container, never the
// right to start/stop/delete it.
type ContainerOperator interface {
	MustOwnContainer(ctx context.Context, ownerID, containerID string) (*domain.Container, error)
	StartContainer(ctx context.Context, ownerID, containerID string) error
	StopContainer(ctx context.Context, ownerID, containerID string) error
	DeleteContainer(ctx context.Context, ownerID, containerID string) error
	// CreateContainer does the actual Docker work for a container
	// SubmitCreate already persisted a placeholder record for.
	// reportStage lets it surface sub-status (e.g. "pulling image")
	// while running, via Service.execute wiring it to Store.UpdateStage.
	CreateContainer(ctx context.Context, ownerID, containerID string, spec domain.ContainerSpec, reportStage func(string)) error
}

// Store persists Job records so status survives across GetJob polls.
// Satisfied by MemoryStore here; a durable (e.g. SQLite) store could be
// swapped in without changing Service.
type Store interface {
	Save(ctx context.Context, j *domain.Job) error
	FindByID(ctx context.Context, id string) (*domain.Job, error)
	UpdateStatus(ctx context.Context, id string, status domain.JobStatus, errMsg string) error
	// UpdateStage records Job.Stage without touching Status/Error — a
	// separate call from UpdateStatus since a job can move through
	// several stages while still just "running".
	UpdateStage(ctx context.Context, id string, stage string) error
}

type task struct {
	jobID       string
	op          domain.JobOp
	ownerID     string
	containerID string
	// spec is only populated for OpCreateContainer — start/stop/delete
	// operate on a container that already fully exists.
	spec domain.ContainerSpec
}

// Service submits jobs to a fixed-size worker pool. Close uses the same
// RWMutex-guarded channel-close pattern as applog.Manager: Submit holds
// RLock for its whole check-then-send, Close takes Lock (which waits
// out every in-flight Submit) before closing the task channel.
type Service struct {
	store    Store
	operator ContainerOperator
	logger   *applog.Manager
	newID    func() string

	tasks chan task

	stateMu sync.RWMutex
	closed  bool
	wg      sync.WaitGroup
}

// NewService starts a pool of `workers` goroutines pulling from a queue
// of size `queueSize`. Submit does not block when the queue is full; it
// returns ErrQueueFull instead.
func NewService(store Store, operator ContainerOperator, logger *applog.Manager, newID func() string, workers, queueSize int) *Service {
	s := &Service{
		store:    store,
		operator: operator,
		logger:   logger,
		newID:    newID,
		tasks:    make(chan task, queueSize),
	}
	for range workers {
		s.wg.Add(1)
		go s.worker()
	}
	return s
}

// Submit persists a pending Job and enqueues it for background
// execution, returning immediately. Ownership/existence errors are
// checked synchronously here (via MustOwnContainer) and returned
// directly — they're already known at submission time, so burying them
// in an async job failure would only make the client poll to learn what
// Submit could have told it up front. Only the actual container
// operation is asynchronous.
func (s *Service) Submit(ctx context.Context, ownerID, containerID string, op domain.JobOp) (*domain.Job, error) {
	return s.submit(ctx, ownerID, containerID, op, domain.ContainerSpec{})
}

// SubmitCreate is Submit's counterpart for OpCreateContainer: the same
// fire-and-poll shape, but it also needs the ContainerSpec to actually
// create with (start/stop/delete need only the containerID, already
// fully persisted by the time they're submitted).
func (s *Service) SubmitCreate(ctx context.Context, ownerID, containerID string, spec domain.ContainerSpec) (*domain.Job, error) {
	return s.submit(ctx, ownerID, containerID, domain.OpCreateContainer, spec)
}

func (s *Service) submit(ctx context.Context, ownerID, containerID string, op domain.JobOp, spec domain.ContainerSpec) (*domain.Job, error) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.closed {
		return nil, ErrClosed
	}

	if _, err := s.operator.MustOwnContainer(ctx, ownerID, containerID); err != nil {
		return nil, err
	}

	now := time.Now()
	j := &domain.Job{
		ID: s.newID(), Op: op, ContainerID: containerID,
		Status: domain.JobPending, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.Save(ctx, j); err != nil {
		return nil, fmt.Errorf("save job: %w", err)
	}

	select {
	case s.tasks <- task{jobID: j.ID, op: op, ownerID: ownerID, containerID: containerID, spec: spec}:
		return j, nil
	default:
		_ = s.store.UpdateStatus(ctx, j.ID, domain.JobFailed, "queue full")
		return nil, ErrQueueFull
	}
}

// GetJob returns a previously submitted job's current status.
func (s *Service) GetJob(ctx context.Context, id string) (*domain.Job, error) {
	j, err := s.store.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, ErrNotFound
	}
	return j, nil
}

func (s *Service) worker() {
	defer s.wg.Done()
	for t := range s.tasks {
		s.execute(t)
	}
}

func (s *Service) execute(t task) {
	ctx := context.Background()
	_ = s.store.UpdateStatus(ctx, t.jobID, domain.JobRunning, "")

	var err error
	switch t.op {
	case domain.OpStartContainer:
		err = s.operator.StartContainer(ctx, t.ownerID, t.containerID)
	case domain.OpStopContainer:
		err = s.operator.StopContainer(ctx, t.ownerID, t.containerID)
	case domain.OpDeleteContainer:
		err = s.operator.DeleteContainer(ctx, t.ownerID, t.containerID)
	case domain.OpCreateContainer:
		_ = s.store.UpdateStage(ctx, t.jobID, "creating container")
		err = s.operator.CreateContainer(ctx, t.ownerID, t.containerID, t.spec, func(stage string) {
			_ = s.store.UpdateStage(ctx, t.jobID, stage)
		})
	default:
		err = fmt.Errorf("unknown job op %q", t.op)
	}

	if err != nil {
		_ = s.store.UpdateStatus(ctx, t.jobID, domain.JobFailed, err.Error())
		_ = s.logger.WriteLog("ERROR", "job failed", applog.F("job_id", t.jobID), applog.F("op", string(t.op)), applog.F("error", err.Error()))
		return
	}
	_ = s.store.UpdateStatus(ctx, t.jobID, domain.JobDone, "")
	_ = s.logger.WriteLog("INFO", "job done", applog.F("job_id", t.jobID), applog.F("op", string(t.op)))
}

// Close stops accepting new jobs and blocks until every already-queued
// job finishes, then returns. Call as part of graceful shutdown, after
// the HTTP server stops accepting requests and before closing the
// logger (workers still log while draining).
func (s *Service) Close() error {
	s.stateMu.Lock()
	s.closed = true
	close(s.tasks) // safe: Lock() waited out every Submit holding RLock
	s.stateMu.Unlock()

	s.wg.Wait()
	return nil
}
