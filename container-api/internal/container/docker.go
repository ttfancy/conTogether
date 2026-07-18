// Package container adapts the Docker Engine SDK to the narrow surface
// ContainerService actually needs (create/start/stop/remove), so the
// service layer depends on this small type rather than the full
// *client.Client — see service.DockerClient for the interface this
// satisfies.
package container

import (
	"context"
	"io"

	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"contogether/container-api/internal/domain"
)

// DockerWrapper wraps the Docker Engine SDK client.
type DockerWrapper struct {
	cli *client.Client
}

// NewDockerWrapper connects using the standard DOCKER_HOST/DOCKER_* env
// vars (client.FromEnv), negotiating the API version against the daemon.
func NewDockerWrapper() (*DockerWrapper, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &DockerWrapper{cli: cli}, nil
}

func (w *DockerWrapper) CreateContainer(ctx context.Context, spec domain.ContainerSpec) (string, error) {
	resp, err := w.cli.ContainerCreate(ctx,
		&dockercontainer.Config{Image: spec.Image, Cmd: spec.Cmd, Env: spec.Env},
		nil, nil, nil, spec.Name,
	)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

func (w *DockerWrapper) StartContainer(ctx context.Context, dockerID string) error {
	return w.cli.ContainerStart(ctx, dockerID, dockercontainer.StartOptions{})
}

func (w *DockerWrapper) StopContainer(ctx context.Context, dockerID string) error {
	return w.cli.ContainerStop(ctx, dockerID, dockercontainer.StopOptions{})
}

func (w *DockerWrapper) RemoveContainer(ctx context.Context, dockerID string) error {
	return w.cli.ContainerRemove(ctx, dockerID, dockercontainer.RemoveOptions{Force: true})
}

// StreamLogs returns the container's combined stdout/stderr as a plain
// (non-multiplexed) stream, following new output as it's written until
// the caller closes the returned reader or the container's log stream
// itself ends. tail is how many recent lines to backfill before
// following ("" or "all" for everything Docker has retained).
//
// Containers created without a TTY (ours all are — see CreateContainer)
// have their stdout/stderr multiplexed by Docker into a single stream
// with an 8-byte frame header per chunk; stdcopy.StdCopy demultiplexes
// that back into plain text, which is why this isn't just
// cli.ContainerLogs's result returned directly.
func (w *DockerWrapper) StreamLogs(ctx context.Context, dockerID, tail string) (io.ReadCloser, error) {
	raw, err := w.cli.ContainerLogs(ctx, dockerID, dockercontainer.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Timestamps: true,
		Tail:       tail,
	})
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	go func() {
		_, copyErr := stdcopy.StdCopy(pw, pw, raw)
		pw.CloseWithError(copyErr)
		raw.Close()
	}()
	return pr, nil
}

func (w *DockerWrapper) Close() error { return w.cli.Close() }
