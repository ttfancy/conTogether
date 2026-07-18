package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ttfancy/logGO"
)

// LogQuerier is the subset of *logGO.Manager this handler needs —
// exposing container-api's own operational log store over HTTP (not to
// be confused with a managed container's stdout/stderr; see
// ContainerLogStreamer for that).
type LogQuerier interface {
	ReadLogs(level string, filter logGO.LogFilter) ([]logGO.LogEntry, error)
	ClearLogs(before time.Time) error
}

type LogHandler struct {
	logs LogQuerier
}

func NewLogHandler(logs LogQuerier) *LogHandler {
	return &LogHandler{logs: logs}
}

type logEntryResponse struct {
	Timestamp time.Time      `json:"timestamp"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// ReadLogs godoc
// @Summary      Query container-api's own operational logs
// @Description  Returns log entries at or above `level` (default DEBUG = everything), optionally filtered by time range and message substring.
// @Tags         logs
// @Produce      json
// @Security     ApiKeyAuth
// @Param        level    query string false "Minimum level: DEBUG, INFO, WARN, ERROR"
// @Param        since    query string false "RFC3339 timestamp, inclusive lower bound"
// @Param        until    query string false "RFC3339 timestamp, inclusive upper bound"
// @Param        contains query string false "Substring match against the log message"
// @Success      200 {array} logEntryResponse
// @Failure      400 {object} map[string]string
// @Router       /logs [get]
func (h *LogHandler) ReadLogs(c *gin.Context) {
	level := c.DefaultQuery("level", "DEBUG")

	var filter logGO.LogFilter
	if since := c.Query("since"); since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid \"since\": must be RFC3339"})
			return
		}
		filter.Since = t
	}
	if until := c.Query("until"); until != "" {
		t, err := time.Parse(time.RFC3339, until)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid \"until\": must be RFC3339"})
			return
		}
		filter.Until = t
	}
	filter.Contains = c.Query("contains")

	entries, err := h.logs.ReadLogs(level, filter)
	if err != nil {
		c.Error(err)
		return
	}

	out := make([]logEntryResponse, len(entries))
	for i, e := range entries {
		out[i] = logEntryResponse{
			Timestamp: e.Timestamp(),
			Level:     string(e.Level()),
			Message:   e.Message(),
			Fields:    e.Fields(),
		}
	}
	c.JSON(http.StatusOK, out)
}

// ClearLogs godoc
// @Summary      Delete operational log entries older than a cutoff
// @Tags         logs
// @Produce      json
// @Security     ApiKeyAuth
// @Param        before query string true "RFC3339 timestamp; entries strictly before this are removed"
// @Success      200 {object} map[string]string
// @Failure      400 {object} map[string]string
// @Router       /logs [delete]
func (h *LogHandler) ClearLogs(c *gin.Context) {
	before := c.Query("before")
	if before == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "\"before\" query param is required"})
		return
	}
	t, err := time.Parse(time.RFC3339, before)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid \"before\": must be RFC3339"})
		return
	}
	if err := h.logs.ClearLogs(t); err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "cleared"})
}
