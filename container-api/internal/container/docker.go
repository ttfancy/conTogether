// Package container adapts the Docker Engine SDK to the narrow surface
// ContainerService actually needs (create/start/stop/remove), so the
// service layer depends on this small type rather than the full
// *client.Client — see service.DockerClient for the interface this
// satisfies.
package container

import (
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types"
	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
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

// CreateContainer creates spec's container, pulling its image first if
// the daemon doesn't already have it — the daemon only reports a
// missing image at create time, not before, so rather than make every
// caller pre-pull, this pulls once and retries. Without this, any image
// not already present locally (anything but whatever happens to be
// cached, e.g. alpine in dev) surfaced as an opaque "internal server
// error" instead of actually working. onPulling, if non-nil, is called
// right before the pull starts — the caller's only way to know this
// (potentially slow) path was taken, since it's not visible otherwise
// until either step finishes.
func (w *DockerWrapper) CreateContainer(ctx context.Context, spec domain.ContainerSpec, onPulling func()) (string, error) {
	resp, err := w.createContainer(ctx, spec)
	if errdefs.IsNotFound(err) {
		if onPulling != nil {
			onPulling()
		}
		if pullErr := w.pullImage(ctx, spec.Image); pullErr != nil {
			return "", fmt.Errorf("pull image %q: %w", spec.Image, pullErr)
		}
		resp, err = w.createContainer(ctx, spec)
	}
	if err != nil {
		// A name collision surfaces from the daemon as a 409 Conflict —
		// translated to a sentinel here (rather than left as a raw Docker
		// SDK error) so the HTTP layer can map it to a specific, useful
		// message instead of the generic "internal server error" every
		// other unrecognized Docker failure gets.
		if errdefs.IsConflict(err) {
			return "", fmt.Errorf("%w: %q", domain.ErrContainerNameConflict, spec.Name)
		}
		return "", err
	}
	return resp.ID, nil
}

func (w *DockerWrapper) createContainer(ctx context.Context, spec domain.ContainerSpec) (dockercontainer.CreateResponse, error) {
	return w.cli.ContainerCreate(ctx,
		&dockercontainer.Config{Image: spec.Image, Cmd: spec.Cmd, Env: spec.Env},
		nil, nil, nil, spec.Name,
	)
}

func (w *DockerWrapper) pullImage(ctx context.Context, image string) error {
	rc, err := w.cli.ImagePull(ctx, image, types.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer rc.Close()
	// Pulling is asynchronous from the daemon's perspective until this
	// stream is drained — ContainerCreate would otherwise race a pull
	// that's still in progress.
	_, err = io.Copy(io.Discard, rc)
	return err
}

func (w *DockerWrapper) StartContainer(ctx context.Context, dockerID string) error {
	return w.cli.ContainerStart(ctx, dockerID, dockercontainer.StartOptions{})
}

func (w *DockerWrapper) StopContainer(ctx context.Context, dockerID string) error {
	return w.cli.ContainerStop(ctx, dockerID, dockercontainer.StopOptions{})
}

// IsRunning reports whether dockerID is actually running right now.
// ContainerStart succeeding only means the daemon accepted the start
// request — a container with no long-lived command (e.g. no CMD
// override) runs and exits on its own almost immediately, which
// ContainerStart itself never reports as an error. Called right after
// StartContainer so the caller can tell the two cases apart instead of
// reporting "running" for something that already isn't.
func (w *DockerWrapper) IsRunning(ctx context.Context, dockerID string) (bool, error) {
	info, err := w.cli.ContainerInspect(ctx, dockerID)
	if err != nil {
		return false, err
	}
	return info.State != nil && info.State.Running, nil
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

// defaultExecCols/Rows seed the exec session's initial TTY size before
// the frontend's first real resize message arrives (it can't send one
// until xterm.js has mounted and measured its container element) —
// close enough to avoid a jarring reflow, not load-bearing since a
// resize immediately corrects it.
const (
	defaultExecCols = 80
	defaultExecRows = 24
)

// ExecContainer starts an interactive shell (/bin/sh, TTY-attached)
// inside the container and returns a live session bridging stdin/stdout
// — the interactive-terminal feature's Docker half. Unlike StreamLogs
// (read-only), this grants real control over the container, which is
// why service.ContainerService gates it through the strict owner-only
// check (mustOwnContainer), never the public-readable one.
func (w *DockerWrapper) ExecContainer(ctx context.Context, dockerID string) (domain.ExecSession, error) {
	size := [2]uint{defaultExecRows, defaultExecCols}
	created, err := w.cli.ContainerExecCreate(ctx, dockerID, types.ExecConfig{
		Cmd:          []string{"/bin/sh"},
		Tty:          true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		ConsoleSize:  &size,
	})
	if err != nil {
		return nil, fmt.Errorf("create exec: %w", err)
	}

	resp, err := w.cli.ContainerExecAttach(ctx, created.ID, types.ExecStartCheck{Tty: true})
	if err != nil {
		return nil, fmt.Errorf("attach exec: %w", err)
	}
	return &ExecSession{execID: created.ID, resp: resp, cli: w.cli}, nil
}

// ExecSession is a live, TTY-attached docker exec session: reading
// yields the shell's combined stdout/stderr (no stdcopy demuxing needed
// — a TTY session is a single unmultiplexed stream, unlike StreamLogs'
// non-TTY containers), writing sends keystrokes as stdin.
type ExecSession struct {
	execID string
	resp   types.HijackedResponse
	cli    *client.Client
}

func (s *ExecSession) Read(p []byte) (int, error)  { return s.resp.Reader.Read(p) }
func (s *ExecSession) Write(p []byte) (int, error) { return s.resp.Conn.Write(p) }

func (s *ExecSession) Close() error {
	s.resp.Close()
	return nil
}

// Resize adjusts the exec session's TTY size — called whenever the
// browser's terminal element resizes, so full-screen TUI programs
// (vim, top, ...) inside the shell render correctly instead of at
// whatever size the session happened to start at.
func (s *ExecSession) Resize(ctx context.Context, cols, rows uint) error {
	return s.cli.ContainerExecResize(ctx, s.execID, dockercontainer.ResizeOptions{Height: rows, Width: cols})
}

func (w *DockerWrapper) Close() error { return w.cli.Close() }
