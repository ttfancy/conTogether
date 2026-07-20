// Package middleware holds cross-cutting request concerns. Auth/OwnerID
// deliberately work on plain context.Context rather than *gin.Context:
// identity resolution (API key -> owner ID) is the same operation
// whether the request arrived over REST (Gin) or WebSocket, so both
// transports share this one implementation instead of each inventing
// its own.
package middleware

import (
	"context"

	"github.com/gin-gonic/gin"
)

type ownerIDContextKey struct{}

// APIKeyStore resolves an API key to the owner ID it authenticates as.
// Deliberately narrow so main.go can back it with anything — a static
// map for local/dev use, or a database-backed lookup later — without
// this middleware changing.
type APIKeyStore interface {
	OwnerForKey(key string) (ownerID string, ok bool)
}

// MapAPIKeyStore is a static, in-memory APIKeyStore.
type MapAPIKeyStore map[string]string

func (m MapAPIKeyStore) OwnerForKey(key string) (string, bool) {
	ownerID, ok := m[key]
	return ownerID, ok
}

// WithOwnerID returns a context carrying ownerID, retrievable via
// OwnerID. Used by both the Gin Auth middleware and internal/wsstream's
// WebSocket auth (see wsstream/auth.go) — one identity representation,
// two transports resolving into it the same way.
func WithOwnerID(ctx context.Context, ownerID string) context.Context {
	return context.WithValue(ctx, ownerIDContextKey{}, ownerID)
}

// OwnerID returns the authenticated owner ID carried by ctx, or "" if
// none is set.
func OwnerID(ctx context.Context) string {
	s, _ := ctx.Value(ownerIDContextKey{}).(string)
	return s
}

// Auth requires a valid X-API-Key header and resolves it to an owner ID
// via store, rather than trusting any client-supplied identity header —
// every handler downstream authorizes against this resolved owner ID,
// never a raw request field.
func Auth(store APIKeyStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("X-API-Key")
		if key == "" {
			c.AbortWithStatusJSON(401, gin.H{"error": "missing X-API-Key header"})
			return
		}
		ownerID, ok := store.OwnerForKey(key)
		if !ok {
			c.AbortWithStatusJSON(401, gin.H{"error": "invalid API key"})
			return
		}
		c.Request = c.Request.WithContext(WithOwnerID(c.Request.Context(), ownerID))
		c.Next()
	}
}
