package middleware

import (
	"time"

	"github.com/gin-gonic/gin"

	"contogether/logsys"
)

// Logging records request start/end through the injected log manager —
// the same logsys.Manager the container-api's own operations log
// through, so request logs and business logs land in one place. Must be
// registered BEFORE Error (i.e. wrapping it), so that c.Writer.Status()
// reflects the status Error actually wrote — see the comment on
// middleware.Error for what goes wrong if the order is reversed.
func Logging(logger *logsys.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		_ = logger.WriteLog("INFO", "request start",
			logsys.F("method", c.Request.Method), logsys.F("path", c.Request.URL.Path))

		c.Next()

		_ = logger.WriteLog("INFO", "request end",
			logsys.F("method", c.Request.Method),
			logsys.F("path", c.Request.URL.Path),
			logsys.F("status", c.Writer.Status()),
			logsys.F("duration_ms", time.Since(start).Milliseconds()),
		)
	}
}
