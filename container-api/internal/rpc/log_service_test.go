package rpc_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	logsysv1 "contogether/container-api/internal/genproto/logsys/v1"
	"contogether/container-api/internal/genproto/logsys/v1/logsysv1connect"
	"contogether/container-api/internal/middleware"
	"contogether/container-api/internal/rpc"
	"contogether/container-api/internal/service"
	"contogether/logsys"
	"contogether/logsys/backends/memory"
)

type fakeStreamer struct {
	mu      sync.Mutex
	content string
	calls   []string // "ownerID:containerID"
}

func (f *fakeStreamer) StreamLogs(_ context.Context, ownerID, id, _ string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, ownerID+":"+id)
	if ownerID != "owner-1" {
		return nil, service.ErrForbidden
	}
	if id != "ctr-1" {
		return nil, service.ErrNotFound
	}
	return io.NopCloser(strings.NewReader(f.content)), nil
}

// testManager deliberately does not register a t.Cleanup(Close) — Close
// is one-time (it closes an internal channel), and tests that need the
// async write flushed before reading call it explicitly themselves.
func testManager(t *testing.T) *logsys.Manager {
	t.Helper()
	store := memory.New()
	return logsys.NewManager(store, store, store)
}

func TestReadLogsConvertsEntries(t *testing.T) {
	mgr := testManager(t)
	if err := mgr.WriteLog("INFO", "hello", logsys.F("n", 1.0)); err != nil {
		t.Fatalf("WriteLog failed: %v", err)
	}
	mgr.Close() // flush the async write before reading it back

	h := rpc.NewLogServiceHandler(mgr, &fakeStreamer{}, middleware.MapAPIKeyStore{})
	resp, err := h.ReadLogs(context.Background(), connect.NewRequest(&logsysv1.ReadLogsRequest{}))
	if err != nil {
		t.Fatalf("ReadLogs failed: %v", err)
	}
	if len(resp.Msg.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(resp.Msg.Entries))
	}
	e := resp.Msg.Entries[0]
	if e.Level != "INFO" || e.Message != "hello" {
		t.Fatalf("unexpected entry: %+v", e)
	}
	if got := e.Fields.AsMap()["n"]; got != 1.0 {
		t.Fatalf("fields[n] = %v, want 1.0", got)
	}
}

func TestClearLogsRequiresBefore(t *testing.T) {
	h := rpc.NewLogServiceHandler(testManager(t), &fakeStreamer{}, middleware.MapAPIKeyStore{})
	_, err := h.ClearLogs(context.Background(), connect.NewRequest(&logsysv1.ClearLogsRequest{}))
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("ClearLogs without Before = %v, want CodeInvalidArgument", err)
	}
}

// newTestServer boots a real HTTP server serving the Connect handler
// (with the auth interceptor attached, exactly as main.go wires it) so
// StreamContainerLogs — which can't be called directly without a real
// *connect.ServerStream — gets a genuine round trip: real client, real
// connection, real auth header handling.
func newTestServer(t *testing.T, streamer *fakeStreamer, apiKeys middleware.MapAPIKeyStore) (logsysv1connect.LogServiceClient, func()) {
	t.Helper()
	h := rpc.NewLogServiceHandler(testManager(t), streamer, apiKeys)
	path, handler := logsysv1connect.NewLogServiceHandler(h, connect.WithInterceptors(rpc.NewAuthInterceptor(apiKeys)))

	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)

	client := logsysv1connect.NewLogServiceClient(srv.Client(), srv.URL)
	return client, srv.Close
}

func TestStreamContainerLogsRoundTrip(t *testing.T) {
	streamer := &fakeStreamer{content: "line one\nline two\n"}
	apiKeys := middleware.MapAPIKeyStore{"owner-1-key": "owner-1"}
	client, closeServer := newTestServer(t, streamer, apiKeys)
	defer closeServer()

	req := connect.NewRequest(&logsysv1.StreamContainerLogsRequest{ContainerId: "ctr-1"})
	req.Header().Set("X-Api-Key", "owner-1-key")

	stream, err := client.StreamContainerLogs(context.Background(), req)
	if err != nil {
		t.Fatalf("StreamContainerLogs failed: %v", err)
	}
	defer stream.Close()

	var lines []string
	for stream.Receive() {
		lines = append(lines, stream.Msg().Line)
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if len(lines) != 2 || lines[0] != "line one" || lines[1] != "line two" {
		t.Fatalf("got lines %+v, want [\"line one\" \"line two\"]", lines)
	}
}

func TestStreamContainerLogsRequiresAuth(t *testing.T) {
	streamer := &fakeStreamer{content: "line\n"}
	apiKeys := middleware.MapAPIKeyStore{"owner-1-key": "owner-1"}
	client, closeServer := newTestServer(t, streamer, apiKeys)
	defer closeServer()

	req := connect.NewRequest(&logsysv1.StreamContainerLogsRequest{ContainerId: "ctr-1"})
	// deliberately no X-Api-Key header

	stream, err := client.StreamContainerLogs(context.Background(), req)
	if err != nil {
		t.Fatalf("StreamContainerLogs call failed before streaming even started: %v", err)
	}
	defer stream.Close()

	if stream.Receive() {
		t.Fatal("expected no messages without auth")
	}
	var connectErr *connect.Error
	if !errors.As(stream.Err(), &connectErr) || connectErr.Code() != connect.CodeUnauthenticated {
		t.Fatalf("stream error = %v, want CodeUnauthenticated", stream.Err())
	}
}

func TestStreamContainerLogsForbiddenMapsToPermissionDenied(t *testing.T) {
	streamer := &fakeStreamer{content: "line\n"}
	apiKeys := middleware.MapAPIKeyStore{"owner-2-key": "owner-2"}
	client, closeServer := newTestServer(t, streamer, apiKeys)
	defer closeServer()

	req := connect.NewRequest(&logsysv1.StreamContainerLogsRequest{ContainerId: "ctr-1"})
	req.Header().Set("X-Api-Key", "owner-2-key")

	stream, err := client.StreamContainerLogs(context.Background(), req)
	if err != nil {
		t.Fatalf("StreamContainerLogs call failed before streaming even started: %v", err)
	}
	defer stream.Close()

	if stream.Receive() {
		t.Fatal("expected no messages for a forbidden owner")
	}
	var connectErr *connect.Error
	if !errors.As(stream.Err(), &connectErr) || connectErr.Code() != connect.CodePermissionDenied {
		t.Fatalf("stream error = %v, want CodePermissionDenied", stream.Err())
	}
}

func TestReadLogsRequiresAuthViaInterceptor(t *testing.T) {
	apiKeys := middleware.MapAPIKeyStore{"owner-1-key": "owner-1"}
	h := rpc.NewLogServiceHandler(testManager(t), &fakeStreamer{}, apiKeys)
	path, handler := logsysv1connect.NewLogServiceHandler(h, connect.WithInterceptors(rpc.NewAuthInterceptor(apiKeys)))

	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := logsysv1connect.NewLogServiceClient(srv.Client(), srv.URL)

	_, err := client.ReadLogs(context.Background(), connect.NewRequest(&logsysv1.ReadLogsRequest{}))
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeUnauthenticated {
		t.Fatalf("ReadLogs without auth = %v, want CodeUnauthenticated", err)
	}

	req := connect.NewRequest(&logsysv1.ReadLogsRequest{})
	req.Header().Set("X-Api-Key", "owner-1-key")
	if _, err := client.ReadLogs(context.Background(), req); err != nil {
		t.Fatalf("ReadLogs with valid auth failed: %v", err)
	}
}
