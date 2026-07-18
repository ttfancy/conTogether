package wsstream_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"contogether/container-api/internal/middleware"
	"contogether/container-api/internal/service"
	"contogether/container-api/internal/wsstream"
)

type fakeStreamer struct {
	mu      sync.Mutex
	content string
}

func (f *fakeStreamer) StreamLogs(_ context.Context, ownerID, id, _ string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ownerID != "owner-1" {
		return nil, service.ErrForbidden
	}
	if id != "ctr-1" {
		return nil, service.ErrNotFound
	}
	return io.NopCloser(strings.NewReader(f.content)), nil
}

func newContainerLogsServer(t *testing.T, streamer *fakeStreamer, apiKeys middleware.MapAPIKeyStore) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/containers/{id}/logs", wsstream.ServeContainerLogs(streamer, apiKeys))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestServeContainerLogsRoundTrip(t *testing.T) {
	streamer := &fakeStreamer{content: "line one\nline two\n"}
	apiKeys := middleware.MapAPIKeyStore{"owner-1-key": "owner-1"}
	srv := newContainerLogsServer(t, streamer, apiKeys)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL, "/ws/containers/ctr-1/logs")+"?api_key=owner-1-key", nil)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.CloseNow()

	var lines []string
	for range 2 {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("Read failed: %v", err)
		}
		lines = append(lines, string(data))
	}
	if lines[0] != "line one" || lines[1] != "line two" {
		t.Fatalf("got lines %+v, want [\"line one\" \"line two\"]", lines)
	}
}

func TestServeContainerLogsRequiresAuth(t *testing.T) {
	streamer := &fakeStreamer{content: "line\n"}
	apiKeys := middleware.MapAPIKeyStore{"owner-1-key": "owner-1"}
	srv := newContainerLogsServer(t, streamer, apiKeys)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, wsURL(srv.URL, "/ws/containers/ctr-1/logs"), nil)
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

func TestServeContainerLogsForbiddenClosesConnection(t *testing.T) {
	streamer := &fakeStreamer{content: "line\n"}
	apiKeys := middleware.MapAPIKeyStore{"owner-2-key": "owner-2"}
	srv := newContainerLogsServer(t, streamer, apiKeys)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL, "/ws/containers/ctr-1/logs")+"?api_key=owner-2-key", nil)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.CloseNow()

	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("expected the connection to close for a forbidden owner")
	}
	if closeErr := websocket.CloseStatus(err); closeErr != websocket.StatusPolicyViolation {
		t.Fatalf("close status = %v, want StatusPolicyViolation", closeErr)
	}
}
