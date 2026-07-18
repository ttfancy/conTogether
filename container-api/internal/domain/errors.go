package domain

import "errors"

// ErrContainerNameConflict is returned when the requested container name
// is already in use by another Docker container on the same daemon —
// detected in the container package (which knows about Docker's own
// conflict semantics) and checked here in domain, the shared,
// behavior-free package, so both container and the HTTP error-mapping
// middleware can reference the same sentinel without either needing to
// import the other's Docker/HTTP-specific internals.
var ErrContainerNameConflict = errors.New("a container with this name already exists")
