package applog_test

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"contogether/container-api/internal/applog"
)

type countingHandler struct {
	mu    sync.Mutex
	count int
}

func (h *countingHandler) Handle(applog.LogEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.count++
}

func newTestManager(t *testing.T) *applog.Manager {
	t.Helper()
	m := applog.NewMemoryManager()
	t.Cleanup(func() { m.Close() })
	return m
}

func TestWriteThenReadRoundTrip(t *testing.T) {
	m := newTestManager(t)
	if err := m.WriteLog("INFO", "server started", applog.F("port", 8080)); err != nil {
		t.Fatalf("WriteLog failed: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	var entries []applog.LogEntry
	for time.Now().Before(deadline) {
		var err error
		entries, err = m.ReadLogs("DEBUG", applog.LogFilter{})
		if err != nil {
			t.Fatalf("ReadLogs failed: %v", err)
		}
		if len(entries) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Message() != "server started" || entries[0].Level() != applog.InfoLevel {
		t.Fatalf("unexpected entry: %+v", entries[0])
	}
	if entries[0].Fields()["port"] != 8080 {
		t.Fatalf("fields = %+v, want port=8080", entries[0].Fields())
	}
}

func TestReadLogsFiltersByMinimumLevel(t *testing.T) {
	m := newTestManager(t)
	for _, lvl := range []string{"DEBUG", "INFO", "WARN", "ERROR"} {
		m.WriteLog(lvl, "msg "+lvl)
	}

	deadline := time.Now().Add(time.Second)
	for {
		entries, _ := m.ReadLogs("WARN", applog.LogFilter{})
		if len(entries) == 2 {
			for _, e := range entries {
				if e.Level() != applog.WarnLevel && e.Level() != applog.ErrorLevel {
					t.Fatalf("ReadLogs(WARN) returned a sub-WARN entry: %+v", e)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected exactly 2 entries (WARN, ERROR), got %+v", entries)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestReadLogsFiltersByContains(t *testing.T) {
	m := applog.NewMemoryManager()
	m.WriteLog("INFO", "container created")
	m.WriteLog("INFO", "upload saved")
	m.Close()

	entries, err := m.ReadLogs("DEBUG", applog.LogFilter{Contains: "container"})
	if err != nil {
		t.Fatalf("ReadLogs failed: %v", err)
	}
	if len(entries) != 1 || entries[0].Message() != "container created" {
		t.Fatalf("ReadLogs(Contains=container) = %+v, want just \"container created\"", entries)
	}
}

func TestClearLogsBoundaryKeepsEntryExactlyAtCutoff(t *testing.T) {
	m := applog.NewMemoryManager()
	m.WriteLog("INFO", "before")
	m.Close() // ensure the write landed before we read timestamps back

	before, _ := m.ReadLogs("DEBUG", applog.LogFilter{})
	if len(before) != 1 {
		t.Fatalf("setup failed: got %d entries, want 1", len(before))
	}
	cutoff := before[0].Timestamp()

	if err := m.ClearLogs(cutoff); err != nil {
		t.Fatalf("ClearLogs failed: %v", err)
	}
	after, _ := m.ReadLogs("DEBUG", applog.LogFilter{})
	if len(after) != 1 {
		t.Fatalf("expected the entry timestamped exactly at cutoff to survive, got %d entries", len(after))
	}
}

func TestWriteLogAfterCloseReturnsErrClosed(t *testing.T) {
	m := applog.NewMemoryManager()
	if err := m.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if err := m.WriteLog("INFO", "should not be accepted"); !errors.Is(err, applog.ErrClosed) {
		t.Fatalf("WriteLog after Close = %v, want ErrClosed", err)
	}
}

func TestRegisterLogHandlerReceivesEntriesAndUnregisterStopsThem(t *testing.T) {
	m := applog.NewMemoryManager()
	h := &countingHandler{}
	unregister := m.RegisterLogHandler(h)

	m.WriteLog("INFO", "one")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		c := h.count
		h.mu.Unlock()
		if c == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	h.mu.Lock()
	got := h.count
	h.mu.Unlock()
	if got != 1 {
		t.Fatalf("handler count = %d, want 1", got)
	}

	unregister()
	m.WriteLog("INFO", "two")
	m.Close() // flushes the queue; if the handler were still registered it'd have fired by now
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.count != 1 {
		t.Fatalf("handler count after unregister = %d, want still 1 (no further calls)", h.count)
	}
}

// TestConcurrentWriteAndClose is the concurrency-control test: many
// goroutines calling WriteLog race a single Close. Run with -race to
// catch data races in the state-guarding RWMutex itself.
func TestConcurrentWriteAndClose(t *testing.T) {
	m := applog.NewMemoryManager()

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() { m.WriteLog("INFO", "concurrent") })
	}
	wg.Go(func() { m.Close() })
	wg.Wait()
}

func TestFileBackedManagerPersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")

	m1, err := applog.NewManager(path)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	m1.WriteLog("INFO", "first run")
	if err := m1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	m2, err := applog.NewManager(path)
	if err != nil {
		t.Fatalf("re-NewManager failed: %v", err)
	}
	defer m2.Close()

	entries, err := m2.ReadLogs("DEBUG", applog.LogFilter{})
	if err != nil {
		t.Fatalf("ReadLogs failed: %v", err)
	}
	if len(entries) != 1 || entries[0].Message() != "first run" {
		t.Fatalf("expected the entry written before restart to survive, got %+v", entries)
	}
}

func TestClearLogsPersistsToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	m, err := applog.NewManager(path)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	m.WriteLog("INFO", "old")
	m.Close()

	m2, err := applog.NewManager(path)
	if err != nil {
		t.Fatalf("re-NewManager failed: %v", err)
	}
	entries, _ := m2.ReadLogs("DEBUG", applog.LogFilter{})
	if len(entries) != 1 {
		t.Fatalf("setup failed: got %d entries, want 1", len(entries))
	}
	future := entries[0].Timestamp().Add(time.Hour)
	if err := m2.ClearLogs(future); err != nil {
		t.Fatalf("ClearLogs failed: %v", err)
	}
	m2.Close()

	m3, err := applog.NewManager(path)
	if err != nil {
		t.Fatalf("re-NewManager after clear failed: %v", err)
	}
	defer m3.Close()
	remaining, _ := m3.ReadLogs("DEBUG", applog.LogFilter{})
	if len(remaining) != 0 {
		t.Fatalf("expected the file to reflect the clear after restart, got %+v", remaining)
	}
}
