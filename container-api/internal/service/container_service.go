// Package service holds business logic, sitting between handler and
// repository/container/job. Every dependency it needs (repository,
// Docker client, log manager) is expressed as an interface owned right
// here — the consumer — and injected via NewContainerService, so this
// package never imports its own implementations.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"contogether/container-api/internal/domain"
	"contogether/container-api/internal/applog"
)

var (
	ErrNotFound          = errors.New("container not found")
	ErrForbidden         = errors.New("not the owner of this container")
	ErrInvalidVisibility = errors.New("visibility must be \"private\" or \"public\"")
)

// ContainerRepository is the persistence seam ContainerService needs.
// Satisfied by repository.SQLiteContainerRepo (or any future backend);
// service never imports the repository package directly.
type ContainerRepository interface {
	Save(ctx context.Context, c *domain.Container) error
	FindByID(ctx context.Context, id string) (*domain.Container, error)
	// ListVisibleTo returns everything ownerID may read: its own
	// containers (any visibility) plus every other owner's public ones.
	ListVisibleTo(ctx context.Context, ownerID string) ([]*domain.Container, error)
	UpdateStatus(ctx context.Context, id string, status domain.ContainerStatus) error
	UpdateVisibility(ctx context.Context, id string, visibility domain.Visibility) error
	Delete(ctx context.Context, id string) error
}

// DockerClient is the container-runtime seam ContainerService needs.
// Satisfied by container.DockerWrapper.
type DockerClient interface {
	CreateContainer(ctx context.Context, spec domain.ContainerSpec) (string, error)
	StartContainer(ctx context.Context, dockerID string) error
	StopContainer(ctx context.Context, dockerID string) error
	RemoveContainer(ctx context.Context, dockerID string) error
	StreamLogs(ctx context.Context, dockerID, tail string) (io.ReadCloser, error)
	ExecContainer(ctx context.Context, dockerID string) (domain.ExecSession, error)
}

// IDGenerator produces a new unique ID; injected so tests can supply a
// deterministic generator instead of a real UUID source.
type IDGenerator func() string

// ContainerService implements container creation/lifecycle management on
// top of an injected repository and Docker client.
type ContainerService struct {
	repo   ContainerRepository
	docker DockerClient
	logger *applog.Manager
	newID  IDGenerator

	locks lockRegistry // per-container-ID mutex, guards start/stop/delete races
}

func NewContainerService(repo ContainerRepository, docker DockerClient, logger *applog.Manager, newID IDGenerator) *ContainerService {
	return &ContainerService{
		repo:   repo,
		docker: docker,
		logger: logger,
		newID:  newID,
		locks:  newLockRegistry(),
	}
}

func (s *ContainerService) CreateContainer(ctx context.Context, ownerID string, spec domain.ContainerSpec) (*domain.Container, error) {
	visibility := spec.Visibility
	if visibility == "" {
		visibility = domain.VisibilityPrivate
	}
	if !visibility.Valid() {
		return nil, fmt.Errorf("%w: invalid visibility %q", ErrInvalidVisibility, spec.Visibility)
	}

	dockerID, err := s.docker.CreateContainer(ctx, spec)
	if err != nil {
		if errors.Is(err, domain.ErrContainerNameConflict) {
			// Returned bare, not wrapped with the "create docker
			// container: " prefix below: this message reaches the client
			// as-is (see middleware.Error), and that prefix is internal
			// framing a user asking to rename their container has no use
			// for.
			return nil, err
		}
		return nil, fmt.Errorf("create docker container: %w", err)
	}

	now := time.Now()
	c := &domain.Container{
		ID:         s.newID(),
		DockerID:   dockerID,
		OwnerID:    ownerID,
		Name:       spec.Name,
		Image:      spec.Image,
		Status:     domain.StatusCreated,
		Visibility: visibility,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.repo.Save(ctx, c); err != nil {
		return nil, fmt.Errorf("save container record: %w", err)
	}

	_ = s.logger.WriteLog("INFO", "container created",
		applog.F("container_id", c.ID), applog.F("owner_id", ownerID))
	return c, nil
}

// GetContainer returns c if ownerID owns it OR it's public — a read
// path, not a mutation path. StreamLogs (also read-only) shares this
// check. Start/Stop/Delete deliberately do NOT use this: see
// mustOwnContainer.
func (s *ContainerService) GetContainer(ctx context.Context, ownerID, id string) (*domain.Container, error) {
	c, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, ErrNotFound
	}
	if c.OwnerID != ownerID && c.Visibility != domain.VisibilityPublic {
		return nil, ErrForbidden
	}
	return c, nil
}

// mustOwnContainer is the strict counterpart to GetContainer: visibility
// grants read access only, never the right to mutate someone else's
// container, so Start/Stop/Delete (and job.ContainerOperator's
// fail-fast pre-check, which calls this via the exported
// MustOwnContainer below) reject any non-owner outright regardless of
// the container's visibility.
func (s *ContainerService) mustOwnContainer(ctx context.Context, ownerID, id string) (*domain.Container, error) {
	c, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, ErrNotFound
	}
	if c.OwnerID != ownerID {
		return nil, ErrForbidden
	}
	return c, nil
}

// MustOwnContainer is the exported form of mustOwnContainer, satisfying
// job.ContainerOperator's pre-submit ownership check.
func (s *ContainerService) MustOwnContainer(ctx context.Context, ownerID, id string) (*domain.Container, error) {
	return s.mustOwnContainer(ctx, ownerID, id)
}

// SetVisibility flips a container's visibility — owner-only, like
// Start/Stop/Delete, since visibility is a property of the resource
// only its owner should control.
func (s *ContainerService) SetVisibility(ctx context.Context, ownerID, id string, visibility domain.Visibility) error {
	if !visibility.Valid() {
		return fmt.Errorf("%w: invalid visibility %q", ErrInvalidVisibility, visibility)
	}
	return s.withLock(id, func() error {
		if _, err := s.mustOwnContainer(ctx, ownerID, id); err != nil {
			return err
		}
		if err := s.repo.UpdateVisibility(ctx, id, visibility); err != nil {
			return fmt.Errorf("update visibility: %w", err)
		}
		_ = s.logger.WriteLog("INFO", "container visibility changed",
			applog.F("container_id", id), applog.F("visibility", string(visibility)))
		return nil
	})
}

// ListContainers returns every container ownerID may read: its own (any
// visibility) plus every other owner's public ones, most recently
// created first.
func (s *ContainerService) ListContainers(ctx context.Context, ownerID string) ([]*domain.Container, error) {
	return s.repo.ListVisibleTo(ctx, ownerID)
}

// StreamLogs returns the container's live stdout/stderr, backfilling
// `tail` recent lines first. Deliberately not run through withLock:
// unlike start/stop/delete, this is a long-lived read (a client may keep
// it open for minutes), and holding the per-container mutex for that
// whole time would block every lifecycle operation on the container
// until the viewer disconnects.
func (s *ContainerService) StreamLogs(ctx context.Context, ownerID, id, tail string) (io.ReadCloser, error) {
	c, err := s.GetContainer(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	return s.docker.StreamLogs(ctx, c.DockerID, tail)
}

// Exec opens an interactive shell session inside the container — unlike
// StreamLogs (read-only), this grants real control (an attacker with a
// shell can do anything the container's process can), so it's gated by
// the strict owner-only check (mustOwnContainer), never the
// public-readable GetContainer: visibility grants read access, not
// control. Not wrapped in withLock either: like StreamLogs, a session
// can run for as long as the user keeps it open, and holding the
// per-container lock for that whole time would block every start/stop/
// delete on the container until they close it — Docker's own exec
// mechanism already handles a session outliving (or being cut short by)
// the container's lifecycle safely at the daemon level.
func (s *ContainerService) Exec(ctx context.Context, ownerID, id string) (domain.ExecSession, error) {
	c, err := s.mustOwnContainer(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	return s.docker.ExecContainer(ctx, c.DockerID)
}

func (s *ContainerService) StartContainer(ctx context.Context, ownerID, id string) error {
	return s.withLock(id, func() error {
		c, err := s.mustOwnContainer(ctx, ownerID, id)
		if err != nil {
			return err
		}
		if err := s.docker.StartContainer(ctx, c.DockerID); err != nil {
			return fmt.Errorf("start docker container: %w", err)
		}
		if err := s.repo.UpdateStatus(ctx, id, domain.StatusRunning); err != nil {
			return fmt.Errorf("update status: %w", err)
		}
		_ = s.logger.WriteLog("INFO", "container started", applog.F("container_id", id))
		return nil
	})
}

func (s *ContainerService) StopContainer(ctx context.Context, ownerID, id string) error {
	return s.withLock(id, func() error {
		c, err := s.mustOwnContainer(ctx, ownerID, id)
		if err != nil {
			return err
		}
		if err := s.docker.StopContainer(ctx, c.DockerID); err != nil {
			return fmt.Errorf("stop docker container: %w", err)
		}
		if err := s.repo.UpdateStatus(ctx, id, domain.StatusStopped); err != nil {
			return fmt.Errorf("update status: %w", err)
		}
		_ = s.logger.WriteLog("INFO", "container stopped", applog.F("container_id", id))
		return nil
	})
}

func (s *ContainerService) DeleteContainer(ctx context.Context, ownerID, id string) error {
	return s.withLock(id, func() error {
		c, err := s.mustOwnContainer(ctx, ownerID, id)
		if err != nil {
			return err
		}
		if err := s.docker.RemoveContainer(ctx, c.DockerID); err != nil {
			return fmt.Errorf("remove docker container: %w", err)
		}
		if err := s.repo.Delete(ctx, id); err != nil {
			return fmt.Errorf("delete container record: %w", err)
		}
		_ = s.logger.WriteLog("INFO", "container deleted", applog.F("container_id", id))
		return nil
	})
}

// withLock serializes start/stop/delete for a single container ID so a
// racing pair of requests (e.g. delete + start) can't both proceed
// against the same underlying Docker container at once.
func (s *ContainerService) withLock(id string, fn func() error) error {
	mu := s.locks.forID(id)
	mu.Lock()
	defer mu.Unlock()
	return fn()
}

// lockRegistry hands out one *sync.Mutex per container ID, creating it
// exactly once even under concurrent first-access (guarded by mu) —
// two goroutines racing to lock the same ID must get the same mutex
// instance, or the lock provides no real exclusion.
type lockRegistry struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newLockRegistry() lockRegistry {
	return lockRegistry{locks: make(map[string]*sync.Mutex)}
}

func (r *lockRegistry) forID(id string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.locks[id]
	if !ok {
		l = &sync.Mutex{}
		r.locks[id] = l
	}
	return l
}
