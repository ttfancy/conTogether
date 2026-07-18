package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"contogether/container-api/internal/domain"
	"contogether/container-api/internal/middleware"
)

// JobService is the subset of job.Service this handler needs. Start,
// Stop and Delete all funnel through Submit — the response is a Job ID
// the client polls via GetJob, not the operation's own result.
type JobService interface {
	Submit(ctx context.Context, ownerID, containerID string, op domain.JobOp) (*domain.Job, error)
	GetJob(ctx context.Context, id string) (*domain.Job, error)
}

type JobHandler struct {
	jobs JobService
}

func NewJobHandler(jobs JobService) *JobHandler {
	return &JobHandler{jobs: jobs}
}

type jobResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func toJobResponse(j *domain.Job) jobResponse {
	return jobResponse{ID: j.ID, Status: string(j.Status), Error: j.Error}
}

func (h *JobHandler) submit(c *gin.Context, op domain.JobOp) {
	j, err := h.jobs.Submit(c.Request.Context(), middleware.OwnerID(c.Request.Context()), c.Param("id"), op)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusAccepted, toJobResponse(j))
}

// StartContainer godoc
// @Summary      Start a container (asynchronous)
// @Description  Submits a start job and returns immediately; poll GET /jobs/{jobId} for completion.
// @Tags         containers
// @Produce      json
// @Security     ApiKeyAuth
// @Param        id path string true "Container ID"
// @Success      202 {object} jobResponse
// @Router       /containers/{id}/start [post]
func (h *JobHandler) StartContainer(c *gin.Context) { h.submit(c, domain.OpStartContainer) }

// StopContainer godoc
// @Summary      Stop a container (asynchronous)
// @Description  Submits a stop job and returns immediately; poll GET /jobs/{jobId} for completion.
// @Tags         containers
// @Produce      json
// @Security     ApiKeyAuth
// @Param        id path string true "Container ID"
// @Success      202 {object} jobResponse
// @Router       /containers/{id}/stop [post]
func (h *JobHandler) StopContainer(c *gin.Context) { h.submit(c, domain.OpStopContainer) }

// DeleteContainer godoc
// @Summary      Delete a container (asynchronous)
// @Description  Submits a delete job and returns immediately; poll GET /jobs/{jobId} for completion.
// @Tags         containers
// @Produce      json
// @Security     ApiKeyAuth
// @Param        id path string true "Container ID"
// @Success      202 {object} jobResponse
// @Router       /containers/{id} [delete]
func (h *JobHandler) DeleteContainer(c *gin.Context) { h.submit(c, domain.OpDeleteContainer) }

// GetJob godoc
// @Summary      Get job status
// @Tags         jobs
// @Produce      json
// @Security     ApiKeyAuth
// @Param        jobId path string true "Job ID"
// @Success      200 {object} jobResponse
// @Failure      404 {object} map[string]string
// @Router       /jobs/{jobId} [get]
func (h *JobHandler) GetJob(c *gin.Context) {
	j, err := h.jobs.GetJob(c.Request.Context(), c.Param("jobId"))
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, toJobResponse(j))
}
