// Package domain holds data types shared across layers (service,
// repository, handler) without behavior of their own — keeping them
// separate from any one layer avoids import cycles between the
// interfaces (owned by service) and their implementations (repository,
// container).
package domain

import "time"

type ContainerStatus string

const (
	// StatusPending is a placeholder record BeginCreateContainer
	// persists immediately, before the (possibly slow, if the image
	// needs pulling) Docker work a job worker does asynchronously has
	// actually run — see service.ContainerService.CreateContainer.
	StatusPending ContainerStatus = "pending"
	StatusCreated ContainerStatus = "created"
	StatusRunning ContainerStatus = "running"
	StatusStopped ContainerStatus = "stopped"
	StatusRemoved ContainerStatus = "removed"
	// StatusExited marks a container that was started but didn't stay
	// running — e.g. no long-lived command to run (no CMD override) —
	// as distinct from StatusStopped, which only ever means an owner
	// explicitly stopped it. See (*ContainerService).StartContainer.
	StatusExited ContainerStatus = "exited"
	// StatusFailed marks a placeholder whose Docker-side creation
	// failed (e.g. an image pull error) — left visible rather than
	// silently deleted, so the owner can see it happened and delete it.
	StatusFailed ContainerStatus = "failed"
)

// Container is the persisted record of a container this API manages,
// distinct from the raw Docker container it wraps: OwnerID and Status
// are our own bookkeeping, DockerID is the foreign key into the Docker
// daemon.
type Container struct {
	ID         string
	DockerID   string
	OwnerID    string
	Name       string
	Image      string
	Status     ContainerStatus
	Visibility Visibility
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ContainerSpec describes a container to be created. It lives in domain
// (not the container package) so service.DockerClient can reference it
// without depending on the concrete Docker SDK wrapper.
type ContainerSpec struct {
	Image      string
	Name       string
	Cmd        []string
	Env        []string
	Visibility Visibility
}
