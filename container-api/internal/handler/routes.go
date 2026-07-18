package handler

import "github.com/gin-gonic/gin"

// RegisterHealthRoute registers the liveness endpoint. Kept separate
// from RegisterRoutes and mounted outside the authenticated group, so
// orchestrators/load balancers can probe it without an API key.
func RegisterHealthRoute(router gin.IRouter) {
	router.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
}

// RegisterRoutes wires the authenticated API routes onto router (call
// with a group that already has middleware.Auth attached). Start/Stop/
// Delete go through jobs (async, 202 + Job ID) rather than containers.
func RegisterRoutes(router gin.IRouter, containers *ContainerHandler, uploads *UploadHandler, jobs *JobHandler, logs *LogHandler) {
	router.POST("/containers", containers.CreateContainer)
	router.GET("/containers", containers.ListContainers)
	router.GET("/containers/:id", containers.GetContainer)
	router.POST("/containers/:id/start", jobs.StartContainer)
	router.POST("/containers/:id/stop", jobs.StopContainer)
	router.DELETE("/containers/:id", jobs.DeleteContainer)
	router.GET("/containers/:id/logs/stream", containers.StreamLogs)
	router.PUT("/containers/:id/visibility", containers.SetVisibility)

	router.GET("/jobs/:jobId", jobs.GetJob)

	router.POST("/uploads", uploads.UploadFile)
	router.GET("/uploads", uploads.ListUploads)
	router.GET("/uploads/:id", uploads.DownloadUpload)
	router.PUT("/uploads/:id/visibility", uploads.SetVisibility)

	router.GET("/logs", logs.ReadLogs)
	router.DELETE("/logs", logs.ClearLogs)
}
