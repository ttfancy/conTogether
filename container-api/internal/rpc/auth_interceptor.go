// Package rpc exposes container-api's log operations over gRPC,
// gRPC-Web, and Connect's own JSON protocol — all three from one
// Connect-generated handler — as an alternative transport to the REST
// endpoints in internal/handler. Both transports call into the exact
// same service objects (applog.Manager, service.ContainerService);
// this package only adapts wire formats, it holds no business logic of
// its own.
package rpc

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	"contogether/container-api/internal/middleware"
)

var (
	errMissingAPIKey = errors.New("missing X-Api-Key header")
	errInvalidAPIKey = errors.New("invalid API key")
)

// NewAuthInterceptor mirrors middleware.Auth for Connect/gRPC clients:
// same X-Api-Key header, same store, same refusal to trust a
// client-supplied identity — resolved server-side and carried via
// context the same way middleware.OwnerID reads it for REST requests,
// so downstream service code is identical regardless of which
// transport the request arrived over. Covers unary calls; streaming
// calls authenticate via the same authenticate() helper directly (see
// log_service.go), since Connect's streaming interceptor hook wraps a
// different conn type than the unary path.
func NewAuthInterceptor(store middleware.APIKeyStore) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			ctx, err := authenticate(ctx, store, req.Header())
			if err != nil {
				return nil, err
			}
			return next(ctx, req)
		}
	})
}

func authenticate(ctx context.Context, store middleware.APIKeyStore, header http.Header) (context.Context, error) {
	key := header.Get("X-Api-Key")
	if key == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errMissingAPIKey)
	}
	ownerID, ok := store.OwnerForKey(key)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errInvalidAPIKey)
	}
	return middleware.WithOwnerID(ctx, ownerID), nil
}
