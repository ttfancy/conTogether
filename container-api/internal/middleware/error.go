package middleware

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"contogether/container-api/internal/job"
	"contogether/container-api/internal/service"
	"contogether/logsys"
)

type errorResponse struct {
	Error string `json:"error"`
}

// Error recovers from panics and translates errors handlers attach via
// c.Error(err) into HTTP responses. It must be registered AFTER Logging
// (i.e. nested inside it) — not before — so that by the time Logging's
// own post-c.Next() code reads c.Writer.Status(), this middleware has
// already turned a collected error into the real status code. Gin runs
// post-c.Next() code in reverse registration order, so registering Error
// first would make its status-writing run after Logging already
// recorded whatever status happened to be set beforehand (Gin's
// implicit 200, since handlers here call c.Error(err) without setting
// one themselves) — every error response would then be logged as 200
// despite the client correctly receiving 404/403/etc. This one still
// catches panics from Auth and every handler; the only thing it can no
// longer protect is a panic inside Logging's own code, which is a much
// smaller risk than guaranteed-wrong status on every logged error.
func Error(logger *logsys.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				_ = logger.WriteLog("ERROR", "panic recovered", logsys.F("panic", fmt.Sprintf("%v", r)))
				c.AbortWithStatusJSON(http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			}
		}()

		c.Next()

		if len(c.Errors) == 0 {
			return
		}
		err := c.Errors.Last().Err
		_ = logger.WriteLog("ERROR", "request error", logsys.F("error", err.Error()))

		switch {
		case errors.Is(err, service.ErrNotFound), errors.Is(err, job.ErrNotFound):
			c.JSON(http.StatusNotFound, errorResponse{Error: "not found"})
		case errors.Is(err, service.ErrForbidden):
			c.JSON(http.StatusForbidden, errorResponse{Error: "forbidden"})
		case errors.Is(err, job.ErrQueueFull), errors.Is(err, job.ErrClosed):
			c.JSON(http.StatusServiceUnavailable, errorResponse{Error: "service temporarily unavailable, try again"})
		default:
			// Deliberately generic: never echo err.Error() to the client —
			// it may contain internal details (SQL, file paths, driver
			// errors). The full error is already in the log above.
			c.JSON(http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		}
	}
}
