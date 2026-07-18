// Package domain holds data types shared across layers (service,
// repository, handler) without behavior of their own — keeping them
// separate from any one layer avoids import cycles between the
// interfaces (owned by service) and their implementations (repository,
// container).
package domain

import "time"

type ContainerStatus string

const (
	StatusCreated ContainerStatus = "created"
	StatusRunning ContainerStatus = "running"
	StatusStopped ContainerStatus = "stopped"
	StatusRemoved ContainerStatus = "removed"
)

// Container is the persisted record of a container this API manages,
// distinct from the raw Docker container it wraps: OwnerID and Status
// are our own bookkeeping, DockerID is the foreign key into the Docker
// daemon.
type Container struct {
	ID        string
	DockerID  string
	OwnerID   string
	Name      string
	Image     string
	Status    ContainerStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ContainerSpec describes a container to be created. It lives in domain
// (not the container package) so service.DockerClient can reference it
// without depending on the concrete Docker SDK wrapper.
type ContainerSpec struct {
	Image string
	Name  string
	Cmd   []string
	Env   []string
}
