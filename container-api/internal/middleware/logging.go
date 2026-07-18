package middleware

import (
	"time"

	"github.com/gin-gonic/gin"

	"contogether/container-api/internal/applog"
)

// Logging records request start/end through the injected log manager —
// the same applog.Manager the container-api's own operations log
// through, so request logs and business logs land in one place. Must be
// registered BEFORE Error (i.e. wrapping it), so that c.Writer.Status()
// reflects the status Error actually wrote — see the comment on
// middleware.Error for what goes wrong if the order is reversed.
func Logging(logger *applog.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		_ = logger.WriteLog("INFO", "request start",
			applog.F("method", c.Request.Method), applog.F("path", c.Request.URL.Path))

		c.Next()

		_ = logger.WriteLog("INFO", "request end",
			applog.F("method", c.Request.Method),
			applog.F("path", c.Request.URL.Path),
			applog.F("status", c.Writer.Status()),
			applog.F("duration_ms", time.Since(start).Milliseconds()),
		)
	}
}
