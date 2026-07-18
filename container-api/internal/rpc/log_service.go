package rpc

import (
	"bufio"
	"context"
	"errors"
	"io"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	logsysv1 "contogether/container-api/internal/genproto/logsys/v1"
	"contogether/container-api/internal/middleware"
	"contogether/container-api/internal/applog"
)

var errClearLogsRequiresBefore = errors.New("before is required")

// LogQuerier mirrors handler.LogQuerier — the same *applog.Manager
// backs both the REST and Connect/gRPC transports; this interface is
// defined again here (rather than imported from the handler package)
// so this package doesn't depend on the REST layer to describe what it
// needs.
type LogQuerier interface {
	ReadLogs(level string, filter applog.LogFilter) ([]applog.LogEntry, error)
	ClearLogs(before time.Time) error
}

// ContainerLogStreamer mirrors handler.ContainerLogStreamer.
type ContainerLogStreamer interface {
	StreamLogs(ctx context.Context, ownerID, id, tail string) (io.ReadCloser, error)
}

// LogServiceHandler implements logsysv1connect.LogServiceHandler,
// adapting the Connect wire format to the same service objects the
// REST handlers call — see internal/handler/log_handler.go and
// container_handler.go for the REST side of this exact same logic.
// ReadLogs/ClearLogs are authenticated via the interceptor in
// auth_interceptor.go (wired in main.go); StreamContainerLogs
// authenticates itself directly (see comment there for why).
type LogServiceHandler struct {
	logs      LogQuerier
	streams   ContainerLogStreamer
	authStore middleware.APIKeyStore
}

func NewLogServiceHandler(logs LogQuerier, streams ContainerLogStreamer, authStore middleware.APIKeyStore) *LogServiceHandler {
	return &LogServiceHandler{logs: logs, streams: streams, authStore: authStore}
}

func (h *LogServiceHandler) ReadLogs(ctx context.Context, req *connect.Request[logsysv1.ReadLogsRequest]) (*connect.Response[logsysv1.ReadLogsResponse], error) {
	level := req.Msg.GetLevel()
	if level == "" {
		level = "DEBUG"
	}

	var filter applog.LogFilter
	if req.Msg.Since != nil {
		filter.Since = req.Msg.Since.AsTime()
	}
	if req.Msg.Until != nil {
		filter.Until = req.Msg.Until.AsTime()
	}
	filter.Contains = req.Msg.GetContains()

	entries, err := h.logs.ReadLogs(level, filter)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	out := make([]*logsysv1.LogEntry, len(entries))
	for i, e := range entries {
		fields, err := structpb.NewStruct(e.Fields())
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		out[i] = &logsysv1.LogEntry{
			Timestamp: timestamppb.New(e.Timestamp()),
			Level:     string(e.Level()),
			Message:   e.Message(),
			Fields:    fields,
		}
	}
	return connect.NewResponse(&logsysv1.ReadLogsResponse{Entries: out}), nil
}

func (h *LogServiceHandler) ClearLogs(ctx context.Context, req *connect.Request[logsysv1.ClearLogsRequest]) (*connect.Response[logsysv1.ClearLogsResponse], error) {
	if req.Msg.Before == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errClearLogsRequiresBefore)
	}
	if err := h.logs.ClearLogs(req.Msg.Before.AsTime()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&logsysv1.ClearLogsResponse{}), nil
}

func (h *LogServiceHandler) StreamContainerLogs(ctx context.Context, req *connect.Request[logsysv1.StreamContainerLogsRequest], stream *connect.ServerStream[logsysv1.StreamContainerLogsResponse]) error {
	// Authenticated here rather than via the unary interceptor: Connect's
	// interceptor hook wraps a different conn type for streaming calls,
	// and req.Header() is already available on the request we have in
	// hand, so a second interceptor implementation would add indirection
	// without adding safety.
	ctx, err := authenticate(ctx, h.authStore, req.Header())
	if err != nil {
		return err
	}

	tail := req.Msg.GetTail()
	if tail == "" {
		tail = "100"
	}

	rc, err := h.streams.StreamLogs(ctx, middleware.OwnerID(ctx), req.Msg.GetContainerId(), tail)
	if err != nil {
		return toConnectError(err)
	}
	defer rc.Close()

	go func() {
		<-ctx.Done()
		rc.Close()
	}()

	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if err := stream.Send(&logsysv1.StreamContainerLogsResponse{Line: scanner.Text()}); err != nil {
			return err
		}
	}
	return scanner.Err()
}
