package wsstream_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"contogether/container-api/internal/middleware"
	"contogether/container-api/internal/wsstream"
	"github.com/ttfancy/logGO"
	"github.com/ttfancy/logGO/backends/memory"
)

func wsURL(serverURL, path string) string {
	return "ws" + strings.TrimPrefix(serverURL, "http") + path
}

func TestServeAppLogsRequiresAuth(t *testing.T) {
	mgr := logGO.NewManager(memory.New(), memory.New(), memory.New())
	apiKeys := middleware.MapAPIKeyStore{"owner-1-key": "owner-1"}

	srv := httptest.NewServer(wsstream.ServeAppLogs(mgr, apiKeys))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, wsURL(srv.URL, "/ws/logs"), nil)
	if err == nil {
		t.Fatal("expected Dial without an API key to fail")
	}
	if resp == nil || resp.StatusCode != 401 {
		status := -1
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("status = %d, want 401", status)
	}
}

// TestServeAppLogsReceivesLiveEntries proves the actual point of this
// handler: it uses logGO.Manager.RegisterLogHandler to bridge live
// entries into the WebSocket, not a poll loop reading GET /logs.
func TestServeAppLogsReceivesLiveEntries(t *testing.T) {
	mgr := logGO.NewManager(memory.New(), memory.New(), memory.New())
	apiKeys := middleware.MapAPIKeyStore{"owner-1-key": "owner-1"}

	srv := httptest.NewServer(wsstream.ServeAppLogs(mgr, apiKeys))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL, "/ws/logs")+"?api_key=owner-1-key", nil)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.CloseNow()

	if err := mgr.WriteLog("INFO", "hello from the test"); err != nil {
		t.Fatalf("WriteLog failed: %v", err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	var got struct {
		Level   string `json:"level"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("invalid JSON message %q: %v", data, err)
	}
	if got.Level != "INFO" || got.Message != "hello from the test" {
		t.Fatalf("got %+v, want level=INFO message=%q", got, "hello from the test")
	}
}

// TestServeAppLogsDoesNotBlockOtherWrites checks that this client's
// registered handler doesn't hold up WriteLog for anyone else — the
// handler is invoked from logGO.Manager's single shared write-loop
// goroutine, so a slow/unread WebSocket client must never be allowed to
// stall it.
func TestServeAppLogsDoesNotBlockOtherWrites(t *testing.T) {
	mgr := logGO.NewManager(memory.New(), memory.New(), memory.New())
	apiKeys := middleware.MapAPIKeyStore{"owner-1-key": "owner-1"}

	srv := httptest.NewServer(wsstream.ServeAppLogs(mgr, apiKeys))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL, "/ws/logs")+"?api_key=owner-1-key", nil)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.CloseNow()
	// Deliberately never read from conn — simulating a stuck client.

	done := make(chan error, 1)
	go func() {
		for range 200 {
			if err := mgr.WriteLog("INFO", "burst"); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WriteLog failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WriteLog calls stalled — a slow WS client blocked the shared write-loop")
	}
}
