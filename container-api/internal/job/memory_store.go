package job

import (
	"context"
	"fmt"
	"sync"
	"time"

	"contogether/container-api/internal/domain"
)

// MemoryStore is an in-process Store. Job status does not survive a
// process restart — acceptable for this scope, but worth stating
// explicitly: a restart mid-job loses its status (the job itself may
// have completed against Docker; only our bookkeeping of it is gone).
// Swapping in a durable Store (e.g. SQLite-backed) would remove this
// limitation without Service changing.
type MemoryStore struct {
	mu   sync.Mutex
	byID map[string]*domain.Job
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{byID: make(map[string]*domain.Job)}
}

func (s *MemoryStore) Save(_ context.Context, j *domain.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *j
	s.byID[j.ID] = &cp
	return nil
}

func (s *MemoryStore) FindByID(_ context.Context, id string) (*domain.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.byID[id]
	if !ok {
		return nil, nil
	}
	cp := *j
	return &cp, nil
}

func (s *MemoryStore) UpdateStatus(_ context.Context, id string, status domain.JobStatus, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.byID[id]
	if !ok {
		return fmt.Errorf("no such job: %s", id)
	}
	j.Status = status
	j.Error = errMsg
	j.UpdatedAt = time.Now()
	return nil
}
