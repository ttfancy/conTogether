package handler

import (
	"context"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"contogether/container-api/internal/domain"
	"contogether/container-api/internal/middleware"
)

// Uploader is the subset of upload.Service this handler needs.
type Uploader interface {
	Save(ctx context.Context, ownerID, filename string, src io.Reader, visibility domain.Visibility) (*domain.Upload, error)
	Get(ctx context.Context, ownerID, id string) (*domain.Upload, error)
	// List returns everything ownerID may read: its own uploads (any
	// visibility) plus every other owner's public ones.
	List(ctx context.Context, ownerID string) ([]*domain.Upload, error)
	SetVisibility(ctx context.Context, ownerID, id string, visibility domain.Visibility) error
}

type UploadHandler struct {
	svc Uploader
}

func NewUploadHandler(svc Uploader) *UploadHandler {
	return &UploadHandler{svc: svc}
}

type uploadResponse struct {
	ID          string `json:"id"`
	OwnerID     string `json:"owner_id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Visibility  string `json:"visibility"`
	// IsOwner mirrors containerResponse's — see its comment.
	IsOwner bool `json:"is_owner"`
}

func toUploadResponse(u *domain.Upload, callerOwnerID string) uploadResponse {
	return uploadResponse{
		ID:          u.ID,
		OwnerID:     u.OwnerID,
		Filename:    u.Filename,
		ContentType: u.ContentType,
		Size:        u.Size,
		Visibility:  string(u.Visibility),
		IsOwner:     u.OwnerID == callerOwnerID,
	}
}

// UploadFile godoc
// @Summary      Upload a data file (CSV/JSON/image/code) to the user's folder
// @Tags         uploads
// @Accept       multipart/form-data
// @Produce      json
// @Security     ApiKeyAuth
// @Param        file       formData file   true  "File to upload"
// @Param        visibility formData string false "\"private\" (default) or \"public\""
// @Success      201 {object} uploadResponse
// @Failure      400 {object} map[string]string
// @Router       /uploads [post]
func (h *UploadHandler) UploadFile(c *gin.Context) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing \"file\" form field"})
		return
	}

	f, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "could not open uploaded file"})
		return
	}
	defer f.Close()

	ownerID := middleware.OwnerID(c.Request.Context())
	visibility := domain.Visibility(c.DefaultPostForm("visibility", string(domain.VisibilityPrivate)))
	u, err := h.svc.Save(c.Request.Context(), ownerID, fileHeader.Filename, f, visibility)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, toUploadResponse(u, ownerID))
}

// ListUploads godoc
// @Summary      List uploads the authenticated owner can see (their own, plus everyone's public ones)
// @Tags         uploads
// @Produce      json
// @Security     ApiKeyAuth
// @Success      200 {array} uploadResponse
// @Router       /uploads [get]
func (h *UploadHandler) ListUploads(c *gin.Context) {
	ownerID := middleware.OwnerID(c.Request.Context())
	uploads, err := h.svc.List(c.Request.Context(), ownerID)
	if err != nil {
		c.Error(err)
		return
	}
	out := make([]uploadResponse, len(uploads))
	for i, u := range uploads {
		out[i] = toUploadResponse(u, ownerID)
	}
	c.JSON(http.StatusOK, out)
}

// DownloadUpload godoc
// @Summary      Download an upload (owner, or anyone if it's public)
// @Tags         uploads
// @Security     ApiKeyAuth
// @Param        id path string true "Upload ID"
// @Success      200 {file} file
// @Failure      403 {object} map[string]string
// @Failure      404 {object} map[string]string
// @Router       /uploads/{id} [get]
func (h *UploadHandler) DownloadUpload(c *gin.Context) {
	u, err := h.svc.Get(c.Request.Context(), middleware.OwnerID(c.Request.Context()), c.Param("id"))
	if err != nil {
		c.Error(err)
		return
	}
	c.FileAttachment(u.Path, u.Filename)
}

// SetVisibility godoc
// @Summary      Change an upload's visibility (owner-only)
// @Tags         uploads
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Param        id      path string              true "Upload ID"
// @Param        request body setVisibilityRequest true "Desired visibility"
// @Success      200 {object} uploadResponse
// @Failure      400 {object} map[string]string
// @Failure      403 {object} map[string]string
// @Router       /uploads/{id}/visibility [put]
func (h *UploadHandler) SetVisibility(c *gin.Context) {
	var req setVisibilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ownerID := middleware.OwnerID(c.Request.Context())
	id := c.Param("id")
	if err := h.svc.SetVisibility(c.Request.Context(), ownerID, id, domain.Visibility(req.Visibility)); err != nil {
		c.Error(err)
		return
	}

	u, err := h.svc.Get(c.Request.Context(), ownerID, id)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, toUploadResponse(u, ownerID))
}
