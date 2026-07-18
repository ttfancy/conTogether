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
)

// Job tracks a long-running container operation submitted asynchronously:
// the handler returns a Job's ID immediately, and the client polls
// GET /jobs/{id} for status.
type Job struct {
	ID          string
	Op          JobOp
	ContainerID string
	Status      JobStatus
	Error       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
