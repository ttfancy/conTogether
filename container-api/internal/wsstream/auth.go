// Package wsstream exposes log data over WebSocket, alongside the REST
// (internal/handler) transport — same underlying data, a second wire
// protocol.
package wsstream

import "net/http"

// apiKeyFromRequest reads the API key from either the X-Api-Key header
// (used everywhere else in this app) or an api_key query parameter.
// The query param exists because browsers' WebSocket constructor can't
// set custom headers on the handshake request — there's no way for a
// frontend to attach X-Api-Key to a `new WebSocket(url)` call, so a
// query param is the accepted pattern for WS auth. Non-browser clients
// that can set headers may still use X-Api-Key.
func apiKeyFromRequest(r *http.Request) string {
	if key := r.URL.Query().Get("api_key"); key != "" {
		return key
	}
	return r.Header.Get("X-Api-Key")
}
