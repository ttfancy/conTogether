package domain

import "time"

type JobStatus string

const (
	JobPending JobStatus = "pending"
	JobRunning JobStatus = "running"
	JobDone    JobStatus = "done"
	JobFailed  JobStatus = "failed"
)

type JobOp string

const (
	OpStartContainer  JobOp = "start_container"
	OpStopContainer   JobOp = "stop_container"
	OpDeleteContainer JobOp = "delete_container"
	OpCreateContainer JobOp = "create_container"
)

// Job tracks a long-running container operation submitted asynchronously:
// the handler returns a Job's ID immediately, and the client polls
// GET /jobs/{id} for status.
type Job struct {
	ID          string
	Op          JobOp
	ContainerID string
	Status      JobStatus
	// Stage is an optional, human-readable sub-status shown while
	// Status is "running" — e.g. "pulling image" vs "creating
	// container" for OpCreateContainer. Empty for ops that don't have
	// distinguishable stages (start/stop/delete are each one Docker
	// call).
	Stage     string
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
}
