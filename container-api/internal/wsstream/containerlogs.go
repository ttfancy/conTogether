package wsstream

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"contogether/container-api/internal/middleware"
	"contogether/container-api/internal/service"
)

// ContainerLogStreamer mirrors handler.ContainerLogStreamer and
// rpc.ContainerLogStreamer — same *service.ContainerService backs all
// three transports (REST/SSE, gRPC/Connect, WebSocket).
type ContainerLogStreamer interface {
	StreamLogs(ctx context.Context, ownerID, id, tail string) (io.ReadCloser, error)
}

// ServeContainerLogs upgrades to a WebSocket and live-tails a managed
// container's stdout/stderr — the WebSocket equivalent of
// GET /containers/{id}/logs/stream (SSE), and the same ownership check
// as every other transport that touches a container.
func ServeContainerLogs(streams ContainerLogStreamer, authStore middleware.APIKeyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ownerID, ok := authStore.OwnerForKey(apiKeyFromRequest(r))
		if !ok {
			http.Error(w, "invalid or missing API key", http.StatusUnauthorized)
			return
		}

		containerID := r.PathValue("id")
		tail := r.URL.Query().Get("tail")
		if tail == "" {
			tail = "100"
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()

		ctx := conn.CloseRead(context.Background())

		rc, err := streams.StreamLogs(ctx, ownerID, containerID, tail)
		if err != nil {
			conn.Close(closeStatusFor(err), err.Error())
			return
		}
		defer rc.Close()

		// Closing rc unblocks a Scan() blocked on the next line the
		// moment the client disconnects — same rationale as the SSE
		// handler (internal/handler/container_handler.go).
		go func() {
			<-ctx.Done()
			rc.Close()
		}()

		scanner := bufio.NewScanner(rc)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Write(writeCtx, websocket.MessageText, scanner.Bytes())
			cancel()
			if err != nil {
				return
			}
		}
		// A closed stream (the common case — the goroutine above closes
		// rc on client disconnect) reports here as a plain read error,
		// not something worth a non-default close code; only a genuine
		// scan failure gets one.
		if err := scanner.Err(); err != nil {
			conn.Close(websocket.StatusInternalError, err.Error())
		}
	}
}

// closeStatusFor maps the same sentinel errors middleware.Error and
// rpc.toConnectError map, to their closest WebSocket close-status
// equivalent — the WS close-code enum is coarser than HTTP status codes
// or gRPC codes, so this is necessarily approximate; the error message
// itself (sent as the close reason) carries the specific detail.
func closeStatusFor(err error) websocket.StatusCode {
	switch {
	case errors.Is(err, service.ErrNotFound):
		return websocket.StatusPolicyViolation
	case errors.Is(err, service.ErrForbidden):
		return websocket.StatusPolicyViolation
	default:
		return websocket.StatusInternalError
	}
}
