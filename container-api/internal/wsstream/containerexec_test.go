package wsstream_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"contogether/container-api/internal/domain"
	"contogether/container-api/internal/middleware"
	"contogether/container-api/internal/service"
	"contogether/container-api/internal/wsstream"
)

// fakeExecSession simulates a docker exec session with two in-memory
// pipes: writes from the handler (shell "output") are readable via
// outR, and writes the handler makes on behalf of the client (keystroke
// "input") are readable back out via inR — letting a test observe both
// directions without a real Docker daemon.
type fakeExecSession struct {
	outR *io.PipeReader
	outW *io.PipeWriter
	inR  *io.PipeReader
	inW  *io.PipeWriter

	mu          sync.Mutex
	resizeCalls []resizeCall
}

type resizeCall struct{ cols, rows uint }

func newFakeExecSession() *fakeExecSession {
	outR, outW := io.Pipe()
	inR, inW := io.Pipe()
	return &fakeExecSession{outR: outR, outW: outW, inR: inR, inW: inW}
}

func (s *fakeExecSession) Read(p []byte) (int, error)  { return s.outR.Read(p) }
func (s *fakeExecSession) Write(p []byte) (int, error) { return s.inW.Write(p) }

func (s *fakeExecSession) Close() error {
	s.outR.Close()
	s.inW.Close()
	return nil
}

func (s *fakeExecSession) Resize(_ context.Context, cols, rows uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resizeCalls = append(s.resizeCalls, resizeCall{cols, rows})
	return nil
}

func (s *fakeExecSession) resizes() []resizeCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]resizeCall(nil), s.resizeCalls...)
}

type fakeExecer struct {
	session *fakeExecSession
}

func (f *fakeExecer) Exec(_ context.Context, ownerID, id string) (domain.ExecSession, error) {
	if ownerID != "owner-1" {
		return nil, service.ErrForbidden
	}
	if id != "ctr-1" {
		return nil, service.ErrNotFound
	}
	return f.session, nil
}

func newContainerExecServer(t *testing.T, execer *fakeExecer, apiKeys middleware.MapAPIKeyStore) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/containers/{id}/exec", wsstream.ServeContainerExec(execer, apiKeys))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestServeContainerExecRequiresAuth(t *testing.T) {
	srv := newContainerExecServer(t, &fakeExecer{session: newFakeExecSession()}, middleware.MapAPIKeyStore{"owner-1-key": "owner-1"})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, wsURL(srv.URL, "/ws/containers/ctr-1/exec"), nil)
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

func TestServeContainerExecForbiddenClosesConnection(t *testing.T) {
	srv := newContainerExecServer(t, &fakeExecer{session: newFakeExecSession()}, middleware.MapAPIKeyStore{"owner-2-key": "owner-2"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL, "/ws/containers/ctr-1/exec")+"?api_key=owner-2-key", nil)
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

// TestServeContainerExecBridgesBothDirections is the actual point of
// this handler: shell output reaches the client as binary WS frames,
// and client keystrokes reach the shell's stdin — a real bidirectional
// bridge, not just a one-way tail like the log transports.
func TestServeContainerExecBridgesBothDirections(t *testing.T) {
	session := newFakeExecSession()
	srv := newContainerExecServer(t, &fakeExecer{session: session}, middleware.MapAPIKeyStore{"owner-1-key": "owner-1"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL, "/ws/containers/ctr-1/exec")+"?api_key=owner-1-key", nil)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.CloseNow()

	// Shell -> client.
	go session.outW.Write([]byte("hello from shell\n"))
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if typ != websocket.MessageBinary || string(data) != "hello from shell\n" {
		t.Fatalf("got (%v, %q), want (MessageBinary, %q)", typ, data, "hello from shell\n")
	}

	// Client -> shell.
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("ls\n")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	buf := make([]byte, 16)
	n, err := session.inR.Read(buf)
	if err != nil {
		t.Fatalf("reading what the client sent failed: %v", err)
	}
	if string(buf[:n]) != "ls\n" {
		t.Fatalf("shell received %q, want %q", buf[:n], "ls\n")
	}

	// Client -> resize control message.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":100,"rows":40}`)); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(session.resizes()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	resizes := session.resizes()
	if len(resizes) != 1 || resizes[0] != (resizeCall{100, 40}) {
		t.Fatalf("resize calls = %+v, want exactly one {100, 40}", resizes)
	}
}
