// Package handler is the HTTP layer: binds/validates requests, calls
// into a service, and shapes the response. It never talks to a
// repository or Docker client directly.
package handler

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"contogether/container-api/internal/domain"
	"contogether/container-api/internal/middleware"
)

// ContainerServicer is the subset of service.ContainerService this
// handler needs, expressed as an interface so handler tests can inject a
// fake instead of a real service+repository+Docker stack. Start/Stop/
// Delete are NOT here: those go through JobHandler instead, since they
// run asynchronously (see job.Service).
type ContainerServicer interface {
	CreateContainer(ctx context.Context, ownerID string, spec domain.ContainerSpec) (*domain.Container, error)
	GetContainer(ctx context.Context, ownerID, id string) (*domain.Container, error)
	ListContainers(ctx context.Context, ownerID string) ([]*domain.Container, error)
}

// ContainerLogStreamer streams a managed container's own stdout/stderr —
// distinct from LogHandler, which serves container-api's own operational
// logs.
type ContainerLogStreamer interface {
	StreamLogs(ctx context.Context, ownerID, id, tail string) (io.ReadCloser, error)
}

type ContainerHandler struct {
	svc     ContainerServicer
	streams ContainerLogStreamer
}

func NewContainerHandler(svc ContainerServicer, streams ContainerLogStreamer) *ContainerHandler {
	return &ContainerHandler{svc: svc, streams: streams}
}

type createContainerRequest struct {
	Image string   `json:"image" binding:"required"`
	Name  string   `json:"name" binding:"required"`
	Cmd   []string `json:"cmd"`
	Env   []string `json:"env"`
}

type containerResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Image  string `json:"image"`
	Status string `json:"status"`
}

func toResponse(c *domain.Container) containerResponse {
	return containerResponse{ID: c.ID, Name: c.Name, Image: c.Image, Status: string(c.Status)}
}

// CreateContainer godoc
// @Summary      Create a container
// @Tags         containers
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Param        request body createContainerRequest true "Container spec"
// @Success      201 {object} containerResponse
// @Failure      400 {object} map[string]string
// @Router       /containers [post]
func (h *ContainerHandler) CreateContainer(c *gin.Context) {
	var req createContainerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	container, err := h.svc.CreateContainer(c.Request.Context(), middleware.OwnerID(c.Request.Context()), domain.ContainerSpec{
		Image: req.Image,
		Name:  req.Name,
		Cmd:   req.Cmd,
		Env:   req.Env,
	})
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, toResponse(container))
}

// GetContainer godoc
// @Summary      Get a container
// @Tags         containers
// @Produce      json
// @Security     ApiKeyAuth
// @Param        id path string true "Container ID"
// @Success      200 {object} containerResponse
// @Failure      404 {object} map[string]string
// @Router       /containers/{id} [get]
func (h *ContainerHandler) GetContainer(c *gin.Context) {
	container, err := h.svc.GetContainer(c.Request.Context(), middleware.OwnerID(c.Request.Context()), c.Param("id"))
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, toResponse(container))
}

// ListContainers godoc
// @Summary      List the authenticated owner's containers
// @Tags         containers
// @Produce      json
// @Security     ApiKeyAuth
// @Success      200 {array} containerResponse
// @Router       /containers [get]
func (h *ContainerHandler) ListContainers(c *gin.Context) {
	containers, err := h.svc.ListContainers(c.Request.Context(), middleware.OwnerID(c.Request.Context()))
	if err != nil {
		c.Error(err)
		return
	}
	out := make([]containerResponse, len(containers))
	for i, container := range containers {
		out[i] = toResponse(container)
	}
	c.JSON(http.StatusOK, out)
}

// StreamLogs godoc
// @Summary      Live-tail a container's stdout/stderr
// @Description  Server-Sent Events stream of the container's own log output, backfilling `tail` recent lines then following new output as it's written.
// @Tags         containers
// @Produce      text/event-stream
// @Security     ApiKeyAuth
// @Param        id   path  string true  "Container ID"
// @Param        tail query string false "Number of recent lines to backfill (default 100)"
// @Success      200
// @Router       /containers/{id}/logs/stream [get]
func (h *ContainerHandler) StreamLogs(c *gin.Context) {
	tail := c.DefaultQuery("tail", "100")
	stream, err := h.streams.StreamLogs(c.Request.Context(), middleware.OwnerID(c.Request.Context()), c.Param("id"), tail)
	if err != nil {
		c.Error(err)
		return
	}
	defer stream.Close()

	// Closing the underlying stream unblocks a Scan() that's blocked
	// waiting on the next line the moment the client disconnects — the
	// request context is canceled by net/http as soon as the connection
	// closes, so this is what actually stops a live tail promptly instead
	// of it leaking until the container/daemon connection times out.
	//
	// Deliberately not using gin's c.Stream(): it type-asserts the
	// response writer to http.CloseNotifier, which httptest's
	// ResponseRecorder (and potentially other non-standard
	// ResponseWriters) doesn't implement — this loop only needs the
	// context-cancellation signal above, so it doesn't need that.
	go func() {
		<-c.Request.Context().Done()
		stream.Close()
	}()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)

	flusher, canFlush := c.Writer.(http.Flusher)

	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", scanner.Text()); err != nil {
			return
		}
		if canFlush {
			flusher.Flush()
		}
	}
	// Surface a scan failure to the still-connected client as an SSE
	// error event — headers are already sent, so this is the only way
	// left to report it; a closed stream (the common case, since the
	// context-cancellation goroutine above closes it on disconnect)
	// reports as a plain io.ErrClosedPipe here, not a real failure.
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", err.Error())
		if canFlush {
			flusher.Flush()
		}
	}
}
