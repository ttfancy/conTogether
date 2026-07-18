package rpc

import (
	"errors"

	"connectrpc.com/connect"

	"contogether/container-api/internal/job"
	"contogether/container-api/internal/service"
)

// toConnectError maps the same sentinel errors middleware.Error maps to
// HTTP status codes (see internal/middleware/error.go) to their Connect
// equivalents, so a client sees "not found" as 404 over REST and
// NotFound over gRPC/Connect — one set of error semantics, two
// transports.
func toConnectError(err error) error {
	switch {
	case errors.Is(err, service.ErrNotFound), errors.Is(err, job.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, service.ErrForbidden):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, job.ErrQueueFull), errors.Is(err, job.ErrClosed):
		return connect.NewError(connect.CodeUnavailable, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
