// Command server is the composition root: the only place in this
// program that knows about every concrete type. Everything below it
// (service, handler, middleware) is wired against interfaces, so
// swapping SQLite for Postgres or the Docker client for a fake in tests
// means changing lines here and nowhere else.
//
// @title                       conTogether Container API
// @version                     1.0
// @description                RESTful API for managing per-user Docker containers, file uploads, and async job status.
// @BasePath                    /
// @securityDefinitions.apikey  ApiKeyAuth
// @in                          header
// @name                        X-API-Key
package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"contogether/container-api/internal/genproto/logsys/v1/logsysv1connect"

	_ "contogether/container-api/docs"
	"contogether/container-api/internal/config"
	"contogether/container-api/internal/container"
	"contogether/container-api/internal/handler"
	"contogether/container-api/internal/job"
	"contogether/container-api/internal/middleware"
	"contogether/container-api/internal/repository"
	"contogether/container-api/internal/rpc"
	"contogether/container-api/internal/service"
	"contogether/container-api/internal/upload"
	"contogether/container-api/internal/webui"
	"contogether/container-api/internal/wsstream"
	"contogether/logsys"
	logfile "contogether/logsys/backends/file"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	logStore, err := logfile.Open(cfg.LogFilePath)
	if err != nil {
		log.Fatalf("open log file: %v", err)
	}
	logger := logsys.NewManager(logStore, logStore, logStore)

	repos, err := openRepos(cfg)
	if err != nil {
		log.Fatalf("open repositories: %v", err)
	}
	defer repos.closer.Close()

	// The dev API key is a placeholder identity — see config.Config's
	// DevAPIKey doc comment — but it's now seeded into the same
	// api_keys table a real key-issuing flow would use, not just an
	// in-memory map, so OwnerForKey lookups are identical either way.
	if err := repos.apiKeys.Seed(cfg.DevAPIKey, "dev-user"); err != nil {
		log.Fatalf("seed dev API key: %v", err)
	}

	dockerClient, err := container.NewDockerWrapper()
	if err != nil {
		log.Fatalf("connect to docker: %v", err)
	}
	defer dockerClient.Close()

	newID := func() string { return uuid.NewString() }

	containerSvc := service.NewContainerService(repos.container, dockerClient, logger, newID)
	uploadSvc := upload.NewService(cfg.UploadsDir, logger, repos.uploads, newID)

	// containerSvc satisfies job.ContainerOperator structurally (Start/
	// Stop/DeleteContainer) — jobSvc is what actually executes an async
	// container operation once a worker picks it off the queue.
	jobSvc := job.NewService(job.NewMemoryStore(), containerSvc, logger, newID, cfg.JobWorkers, cfg.JobQueueSize)

	containerHandler := handler.NewContainerHandler(containerSvc, containerSvc)
	uploadHandler := handler.NewUploadHandler(uploadSvc)
	jobHandler := handler.NewJobHandler(jobSvc)
	logHandler := handler.NewLogHandler(logger)

	apiKeys := repos.apiKeys

	router := gin.New()
	router.Use(middleware.Logging(logger), middleware.Error(logger))
	handler.RegisterHealthRoute(router)
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	authenticated := router.Group("/")
	authenticated.Use(middleware.Auth(apiKeys))
	handler.RegisterRoutes(authenticated, containerHandler, uploadHandler, jobHandler, logHandler)

	// Anything not matched by a registered API route above falls through
	// to the embedded frontend (see internal/webui) — its own handler
	// then falls further through to index.html for anything that isn't a
	// real static file, so React Router's client-side routes work on a
	// direct navigation or hard refresh.
	webUIHandler, err := webui.Handler()
	if err != nil {
		log.Fatalf("load embedded frontend: %v", err)
	}
	router.NoRoute(gin.WrapH(webUIHandler))

	// Same LogQuerier/ContainerLogStreamer the REST handlers use, exposed
	// over gRPC/gRPC-Web/Connect-JSON too — see internal/rpc for why this
	// is a second transport onto identical service objects, not a
	// parallel implementation.
	logServiceHandler := rpc.NewLogServiceHandler(logger, containerSvc, apiKeys)
	logServicePath, connectHandler := logsysv1connect.NewLogServiceHandler(
		logServiceHandler,
		connect.WithInterceptors(rpc.NewAuthInterceptor(apiKeys)),
	)

	// Same log data over a third transport — WebSocket — see
	// internal/wsstream for why this needs its own auth (browsers can't
	// set headers on the WS handshake, so the API key travels as a query
	// param there instead).
	mux := http.NewServeMux()
	mux.Handle(logServicePath, connectHandler)
	mux.HandleFunc("GET /ws/logs", wsstream.ServeAppLogs(logger, apiKeys))
	mux.HandleFunc("GET /ws/containers/{id}/logs", wsstream.ServeContainerLogs(containerSvc, apiKeys))
	mux.Handle("/", router)

	// h2c: plain-text HTTP/2, so full (non-Web) gRPC clients work locally
	// without TLS. gRPC-Web and Connect's own JSON protocol are fine over
	// HTTP/1.1 and unaffected by this either way.
	srv := &http.Server{Addr: cfg.Addr(), Handler: h2c.NewHandler(mux, &http2.Server{})}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()
	_ = logger.WriteLog("INFO", "server listening", logsys.F("addr", srv.Addr))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	_ = logger.WriteLog("INFO", "shutdown initiated")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = logger.WriteLog("ERROR", "http server shutdown error", logsys.F("error", err.Error()))
	}

	_ = logger.WriteLog("INFO", "draining in-flight jobs")
	jobsDrained := make(chan error, 1)
	go func() { jobsDrained <- jobSvc.Close() }()
	select {
	case err := <-jobsDrained:
		if err != nil {
			_ = logger.WriteLog("ERROR", "job service shutdown error", logsys.F("error", err.Error()))
		}
	case <-time.After(cfg.ShutdownTimeout):
		// A stuck Docker call must not hang shutdown forever; log and
		// proceed — any job still running is abandoned mid-flight.
		_ = logger.WriteLog("ERROR", "job drain timed out, proceeding with shutdown")
	}

	_ = logger.WriteLog("INFO", "shutdown complete")
	logger.Close()
}

// repos bundles every repository the server needs, all sharing one
// underlying *sql.DB connection (see ContainerRepository.DB()) rather
// than each opening its own — one file/DSN, one pool, no risk of a
// second SQLite connection contending with the first over the same
// database file.
type repos struct {
	container service.ContainerRepository
	apiKeys   *repository.APIKeyRepo
	uploads   upload.Repository
	closer    io.Closer
}

// openRepos constructs every repository for the configured backend —
// cfg.DBDriver ("sqlite" or "postgres", validated by config.Load) is
// branched on here and nowhere else; everything downstream only ever
// sees interfaces.
func openRepos(cfg *config.Config) (*repos, error) {
	if cfg.DBDriver == "postgres" {
		containerRepo, err := repository.NewPostgresContainerRepo(cfg.DatabaseURL)
		if err != nil {
			return nil, err
		}
		db := containerRepo.DB()
		apiKeyRepo, err := repository.NewAPIKeyRepo(db, "postgres")
		if err != nil {
			containerRepo.Close()
			return nil, err
		}
		uploadRepo, err := repository.NewPostgresUploadRepo(db)
		if err != nil {
			containerRepo.Close()
			return nil, err
		}
		return &repos{container: containerRepo, apiKeys: apiKeyRepo, uploads: uploadRepo, closer: containerRepo}, nil
	}

	containerRepo, err := repository.NewSQLiteContainerRepo(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	db := containerRepo.DB()
	apiKeyRepo, err := repository.NewAPIKeyRepo(db, "sqlite")
	if err != nil {
		containerRepo.Close()
		return nil, err
	}
	uploadRepo, err := repository.NewSQLiteUploadRepo(db)
	if err != nil {
		containerRepo.Close()
		return nil, err
	}
	return &repos{container: containerRepo, apiKeys: apiKeyRepo, uploads: uploadRepo, closer: containerRepo}, nil
}
