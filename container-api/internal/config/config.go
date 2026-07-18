// Package config centralizes runtime configuration, loaded from
// environment variables with local-dev defaults. Only cmd/server/main.go
// calls Load — every other package receives already-resolved values
// through its constructor, consistent with the DI approach used
// throughout (nothing below main.go reaches into the environment
// itself).
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds every environment-tunable setting for the server.
type Config struct {
	// Port the HTTP server listens on.
	Port string
	// DevAPIKey is a static API key mapped to a single "dev-user" owner —
	// a local/dev placeholder for a real identity store (see
	// middleware.APIKeyStore).
	DevAPIKey string
	// LogFilePath is where the JSON-lines application log is written.
	LogFilePath string
	// DBDriver selects which repository.ContainerRepository backend
	// main.go constructs: "sqlite" (default) or "postgres". Both satisfy
	// the same service.ContainerRepository interface, so this is the only
	// place the choice is made.
	DBDriver string
	// DBPath is the SQLite database file for container metadata (used
	// when DBDriver is "sqlite").
	DBPath string
	// DatabaseURL is the Postgres connection string, e.g.
	// "postgres://user:pass@host:5432/dbname?sslmode=disable" (used when
	// DBDriver is "postgres"; required in that case).
	DatabaseURL string
	// UploadsDir is the root directory per-user uploads are stored under.
	UploadsDir string
	// JobWorkers is the number of concurrent async-job workers.
	JobWorkers int
	// JobQueueSize is how many pending jobs may queue before Submit
	// returns job.ErrQueueFull.
	JobQueueSize int
	// ShutdownTimeout bounds both the HTTP server's graceful shutdown and
	// the job-drain wait — either one is abandoned (with a logged
	// warning) if it runs longer than this.
	ShutdownTimeout time.Duration
}

// Load reads configuration from the environment, falling back to
// local-dev defaults for anything unset.
func Load() (*Config, error) {
	jobWorkers, err := intEnv("JOB_WORKERS", 4)
	if err != nil {
		return nil, err
	}
	jobQueueSize, err := intEnv("JOB_QUEUE_SIZE", 100)
	if err != nil {
		return nil, err
	}
	shutdownTimeout, err := durationEnv("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return nil, err
	}

	dbDriver := stringEnv("DB_DRIVER", "sqlite")
	databaseURL := os.Getenv("DATABASE_URL")

	switch dbDriver {
	case "sqlite":
		// DBPath has its own default below; DATABASE_URL is simply unused.
	case "postgres":
		if databaseURL == "" {
			return nil, fmt.Errorf("DB_DRIVER=postgres requires DATABASE_URL to be set")
		}
	default:
		return nil, fmt.Errorf(`invalid DB_DRIVER=%q: must be "sqlite" or "postgres"`, dbDriver)
	}

	return &Config{
		Port:            stringEnv("SERVER_PORT", "8080"),
		DevAPIKey:       stringEnv("DEV_API_KEY", "dev-key"),
		LogFilePath:     stringEnv("LOG_FILE_PATH", "container-api.log"),
		DBDriver:        dbDriver,
		DBPath:          stringEnv("DB_PATH", "container-api.db"),
		DatabaseURL:     databaseURL,
		UploadsDir:      stringEnv("UPLOADS_DIR", "uploads"),
		JobWorkers:      jobWorkers,
		JobQueueSize:    jobQueueSize,
		ShutdownTimeout: shutdownTimeout,
	}, nil
}

// Addr returns the address to pass to http.Server, e.g. ":8080".
func (c *Config) Addr() string { return ":" + c.Port }

func stringEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func intEnv(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s=%q: must be an integer", key, v)
	}
	return n, nil
}

func durationEnv(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s=%q: must be a duration (e.g. \"10s\")", key, v)
	}
	return d, nil
}
