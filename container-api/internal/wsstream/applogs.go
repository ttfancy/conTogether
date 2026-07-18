package wsstream

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"contogether/container-api/internal/middleware"
	"github.com/ttfancy/logGO"
)

const clientBufferSize = 64

// LogRegistrar is the one thing this handler needs from *logGO.Manager
// — the exact extension point the assignment's design called for
// (RegisterLogHandler, "用於擴展功能"). This is that extension actually
// used for something: bridging live entries into a WebSocket.
type LogRegistrar interface {
	RegisterLogHandler(handler logGO.LogHandler) (unregister func())
}

type logEntryJSON struct {
	Timestamp time.Time      `json:"timestamp"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// ServeAppLogs upgrades to a WebSocket and live-tails container-api's
// own operational logs — new entries only, from the moment of connect;
// pair with GET /logs (REST) or LogService.ReadLogs (Connect/gRPC) for
// history. Gated by any valid API key, not ownership — app logs aren't
// owned by a particular user, matching the REST endpoint's behavior.
func ServeAppLogs(registrar LogRegistrar, authStore middleware.APIKeyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authStore.OwnerForKey(apiKeyFromRequest(r)); !ok {
			http.Error(w, "invalid or missing API key", http.StatusUnauthorized)
			return
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return // Accept already wrote an appropriate HTTP error response
		}
		defer conn.CloseNow()

		// This connection is send-only from the server's side; CloseRead
		// still handles control frames (ping/pong/close) per the WS spec
		// and cancels its returned context once the client disconnects.
		ctx := conn.CloseRead(context.Background())

		entries := make(chan logEntryJSON, clientBufferSize)
		unregister := registrar.RegisterLogHandler(logGO.LogHandlerFunc(func(e logGO.LogEntry) {
			msg := logEntryJSON{Timestamp: e.Timestamp(), Level: string(e.Level()), Message: e.Message(), Fields: e.Fields()}
			select {
			case entries <- msg:
			default:
				// A slow/stuck WS client must not block the shared
				// logGO write-loop every other log write also goes
				// through — drop for this client instead.
			}
		}))
		defer unregister()

		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-entries:
				data, err := json.Marshal(msg)
				if err != nil {
					continue
				}
				writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err = conn.Write(writeCtx, websocket.MessageText, data)
				cancel()
				if err != nil {
					return
				}
			}
		}
	}
}
