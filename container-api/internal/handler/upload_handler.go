package handler

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"contogether/container-api/internal/middleware"
)

// Uploader is the subset of upload.Service this handler needs.
type Uploader interface {
	Save(ownerID, filename string, src io.Reader) (string, error)
}

type UploadHandler struct {
	svc Uploader
}

func NewUploadHandler(svc Uploader) *UploadHandler {
	return &UploadHandler{svc: svc}
}

// UploadFile godoc
// @Summary      Upload a data file (CSV/JSON/image/code) to the user's folder
// @Tags         uploads
// @Accept       multipart/form-data
// @Produce      json
// @Security     ApiKeyAuth
// @Param        file formData file true "File to upload"
// @Success      201 {object} map[string]string
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

	path, err := h.svc.Save(middleware.OwnerID(c.Request.Context()), fileHeader.Filename, f)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"path": path})
}
