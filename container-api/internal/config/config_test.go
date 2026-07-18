package config_test

import (
	"testing"
	"time"

	"contogether/container-api/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Port != "8080" {
		t.Fatalf("Port = %q, want %q", cfg.Port, "8080")
	}
	if cfg.Addr() != ":8080" {
		t.Fatalf("Addr() = %q, want %q", cfg.Addr(), ":8080")
	}
	if cfg.DBDriver != "sqlite" {
		t.Fatalf("DBDriver = %q, want %q", cfg.DBDriver, "sqlite")
	}
	if cfg.JobWorkers != 4 || cfg.JobQueueSize != 100 {
		t.Fatalf("unexpected job defaults: workers=%d queueSize=%d", cfg.JobWorkers, cfg.JobQueueSize)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("ShutdownTimeout = %v, want 10s", cfg.ShutdownTimeout)
	}
}

func TestLoadSelectsPostgresWithDatabaseURL(t *testing.T) {
	t.Setenv("DB_DRIVER", "postgres")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db?sslmode=disable")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.DBDriver != "postgres" {
		t.Fatalf("DBDriver = %q, want %q", cfg.DBDriver, "postgres")
	}
	if cfg.DatabaseURL == "" {
		t.Fatal("expected DatabaseURL to be set")
	}
}

func TestLoadRejectsPostgresWithoutDatabaseURL(t *testing.T) {
	t.Setenv("DB_DRIVER", "postgres")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected Load to reject DB_DRIVER=postgres without DATABASE_URL")
	}
}

func TestLoadRejectsUnknownDBDriver(t *testing.T) {
	t.Setenv("DB_DRIVER", "mysql")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected Load to reject an unknown DB_DRIVER")
	}
}

func TestLoadReadsEnvOverrides(t *testing.T) {
	t.Setenv("SERVER_PORT", "9090")
	t.Setenv("JOB_WORKERS", "8")
	t.Setenv("SHUTDOWN_TIMEOUT", "30s")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Port != "9090" || cfg.Addr() != ":9090" {
		t.Fatalf("Port/Addr = %q/%q, want 9090/:9090", cfg.Port, cfg.Addr())
	}
	if cfg.JobWorkers != 8 {
		t.Fatalf("JobWorkers = %d, want 8", cfg.JobWorkers)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Fatalf("ShutdownTimeout = %v, want 30s", cfg.ShutdownTimeout)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Setenv("JOB_WORKERS", "not-a-number")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected Load to reject a non-integer JOB_WORKERS")
	}
}
