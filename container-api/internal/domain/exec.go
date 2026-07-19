package domain

import "context"

// ExecSession is a live, interactive shell session inside a container —
// satisfied by *container.ExecSession, referenced by both
// service.DockerClient (the interface) and container.DockerWrapper (the
// implementation). It lives here, not in service, because a Go method
// signature must match its interface exactly down to the named return
// type: if container.DockerWrapper.ExecContainer returned a
// container-local interface and service.DockerClient.ExecContainer
// declared a different (even structurally identical) service-local one,
// the two wouldn't satisfy each other — same reason ContainerSpec and
// ErrContainerNameConflict live here instead of in just one side.
type ExecSession interface {
	Read(p []byte) (n int, err error)
	Write(p []byte) (n int, err error)
	Close() error
	Resize(ctx context.Context, cols, rows uint) error
}
