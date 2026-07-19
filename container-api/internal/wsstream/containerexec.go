package wsstream

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"contogether/container-api/internal/domain"
	"contogether/container-api/internal/middleware"
)

// ContainerExecer is the one thing this handler needs from
// *service.ContainerService — a full interactive shell inside the
// container, not just a read-only log stream.
type ContainerExecer interface {
	Exec(ctx context.Context, ownerID, id string) (domain.ExecSession, error)
}

// resizeMessage is the one client-to-server control message this
// protocol defines, sent as a WS *text* frame (raw terminal input is
// sent as *binary* frames instead) — see ServeContainerExec's doc
// comment for why the split exists at all.
type resizeMessage struct {
	Type string `json:"type"`
	Cols uint   `json:"cols"`
	Rows uint   `json:"rows"`
}

// ServeContainerExec upgrades to a WebSocket and bridges it to a real
// interactive shell inside the container (docker exec, TTY-attached).
// Owner-only, unlike the log transports: visibility grants read access,
// never control, and a shell is about as much control as it gets — see
// service.ContainerService.Exec's use of the strict ownership check.
//
// Wire protocol: binary WS frames carry raw PTY bytes in both
// directions (keystrokes in, terminal output out — a single stream, no
// stdout/stderr split, since a TTY session doesn't have one); text WS
// frames from the client are JSON resize messages
// ({"type":"resize","cols":..,"rows":..}). A single WebSocket needs some
// way to distinguish "this is terminal input" from "this is a control
// message" — the binary-data/text-control split is the same one tools
// like ttyd and gotty use for exactly this reason.
func ServeContainerExec(execer ContainerExecer, authStore middleware.APIKeyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ownerID, ok := authStore.OwnerForKey(apiKeyFromRequest(r))
		if !ok {
			http.Error(w, "invalid or missing API key", http.StatusUnauthorized)
			return
		}
		containerID := r.PathValue("id")

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()

		// Deliberately context.Background(), not conn.CloseRead(...): a
		// terminal session needs to keep reading (and writing) both
		// directions for its whole lifetime, unlike the send-only log
		// tails elsewhere in this package. The session ends when either
		// side closes it or an I/O error occurs, handled explicitly
		// below rather than via context cancellation.
		ctx := context.Background()

		session, err := execer.Exec(ctx, ownerID, containerID)
		if err != nil {
			conn.Close(closeStatusFor(err), err.Error())
			return
		}

		readerDone := make(chan struct{})
		// Closing session unblocks the reader goroutine's blocked Read()
		// the moment either side hangs up. Deferred exactly once here
		// (not also at the point the loop below breaks) so there's no
		// question of a harmless-but-sloppy double Close.
		defer func() {
			session.Close()
			<-readerDone
		}()
		go func() {
			defer close(readerDone)
			buf := make([]byte, 32*1024)
			for {
				n, err := session.Read(buf)
				if n > 0 {
					writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					werr := conn.Write(writeCtx, websocket.MessageBinary, buf[:n])
					cancel()
					if werr != nil {
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()

		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				break
			}
			if typ == websocket.MessageText {
				var msg resizeMessage
				if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
					_ = session.Resize(ctx, msg.Cols, msg.Rows)
				}
				continue
			}
			if _, err := session.Write(data); err != nil {
				break
			}
		}
	}
}
