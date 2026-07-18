package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"contogether/container-api/internal/domain"
	"contogether/container-api/internal/handler"
	"contogether/container-api/internal/job"
	"contogether/container-api/internal/middleware"
	"contogether/container-api/internal/service"
	"github.com/ttfancy/logGO"
	"github.com/ttfancy/logGO/backends/memory"
)

// fakeContainerService is an in-memory handler.ContainerServicer AND
// job.ContainerOperator used to exercise the HTTP layer (routing,
// middleware, status-code mapping, and the real async job pipeline)
// independent of any real repository or Docker daemon. It's shared
// between the container handler and the job service's operator in
// newTestRouter, so it's accessed from both HTTP-handling goroutines and
// job-worker goroutines — hence the mutex.
type fakeContainerService struct {
	mu         sync.Mutex
	containers map[string]*domain.Container
}

func newFakeContainerService() *fakeContainerService {
	return &fakeContainerService{containers: map[string]*domain.Container{
		"ctr-existing": {ID: "ctr-existing", OwnerID: "owner-1", Name: "web", Image: "alpine", Status: domain.StatusCreated},
	}}
}

func (f *fakeContainerService) CreateContainer(_ context.Context, ownerID string, spec domain.ContainerSpec) (*domain.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	visibility := spec.Visibility
	if visibility == "" {
		visibility = domain.VisibilityPrivate
	}
	c := &domain.Container{ID: "ctr-" + spec.Name, OwnerID: ownerID, Name: spec.Name, Image: spec.Image, Status: domain.StatusCreated, Visibility: visibility}
	f.containers[c.ID] = c
	return c, nil
}

// get is the strict owner-only check: used by Start/Stop/Delete (and
// exposed as MustOwnContainer for job.ContainerOperator's pre-submit
// check) — visibility never grants a non-owner the right to mutate.
func (f *fakeContainerService) get(ownerID, id string) (*domain.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.containers[id]
	if !ok {
		return nil, service.ErrNotFound
	}
	if c.OwnerID != ownerID {
		return nil, service.ErrForbidden
	}
	return c, nil
}

// getReadable is the loosened read check GetContainer/StreamLogs use:
// owner, or anyone if the container is public.
func (f *fakeContainerService) getReadable(ownerID, id string) (*domain.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.containers[id]
	if !ok {
		return nil, service.ErrNotFound
	}
	if c.OwnerID != ownerID && c.Visibility != domain.VisibilityPublic {
		return nil, service.ErrForbidden
	}
	return c, nil
}

func (f *fakeContainerService) GetContainer(_ context.Context, ownerID, id string) (*domain.Container, error) {
	return f.getReadable(ownerID, id)
}

func (f *fakeContainerService) MustOwnContainer(_ context.Context, ownerID, id string) (*domain.Container, error) {
	return f.get(ownerID, id)
}

func (f *fakeContainerService) ListContainers(_ context.Context, ownerID string) ([]*domain.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Container
	for _, c := range f.containers {
		if c.OwnerID == ownerID || c.Visibility == domain.VisibilityPublic {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeContainerService) SetVisibility(_ context.Context, ownerID, id string, visibility domain.Visibility) error {
	if _, err := f.get(ownerID, id); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.containers[id].Visibility = visibility
	return nil
}

func (f *fakeContainerService) StartContainer(_ context.Context, ownerID, id string) error {
	_, err := f.get(ownerID, id)
	return err
}

func (f *fakeContainerService) StopContainer(_ context.Context, ownerID, id string) error {
	_, err := f.get(ownerID, id)
	return err
}

func (f *fakeContainerService) DeleteContainer(_ context.Context, ownerID, id string) error {
	if _, err := f.get(ownerID, id); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.containers, id)
	return nil
}

func (f *fakeContainerService) StreamLogs(_ context.Context, ownerID, id, _ string) (io.ReadCloser, error) {
	if _, err := f.getReadable(ownerID, id); err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader("fake log line\n")), nil
}

type fakeUploader struct{}

func (fakeUploader) Save(_ context.Context, ownerID, filename string, _ io.Reader, visibility domain.Visibility) (*domain.Upload, error) {
	return &domain.Upload{ID: "up-fake", OwnerID: ownerID, Filename: filename, Path: "/uploads/fake", Visibility: visibility}, nil
}

func (fakeUploader) Get(_ context.Context, _, _ string) (*domain.Upload, error) {
	return nil, service.ErrNotFound
}

func (fakeUploader) List(_ context.Context, _ string) ([]*domain.Upload, error) { return nil, nil }

func (fakeUploader) SetVisibility(_ context.Context, _, _ string, _ domain.Visibility) error {
	return nil
}

// newTestRouter wires the real async job.Service against the fake
// container service — only the outermost repository/Docker layer is
// faked, so this exercises the genuine Submit/worker-pool/poll pipeline
// through HTTP, not a synthetic stand-in for it.
func newTestRouter(t *testing.T) http.Handler {
	t.Helper()
	gin.SetMode(gin.TestMode)

	store := memory.New()
	logger := logGO.NewManager(store, store, store)
	t.Cleanup(func() { logger.Close() })

	// Shared between the container handler and the job service's operator —
	// otherwise a delete submitted via /containers/{id} would mutate a
	// different map than the one /containers/{id} reads back from.
	containerSvc := newFakeContainerService()

	var jobIDCounter atomic.Int64
	jobSvc := job.NewService(job.NewMemoryStore(), containerSvc, logger, func() string {
		return fmt.Sprintf("job-%d", jobIDCounter.Add(1))
	}, 2, 10)
	t.Cleanup(func() { jobSvc.Close() })

	router := gin.New()
	router.Use(middleware.Logging(logger), middleware.Error(logger))
	handler.RegisterHealthRoute(router)

	authGroup := router.Group("/")
	authGroup.Use(middleware.Auth(middleware.MapAPIKeyStore{
		"owner-1-key": "owner-1",
		"owner-2-key": "owner-2",
	}))
	handler.RegisterRoutes(authGroup,
		handler.NewContainerHandler(containerSvc, containerSvc),
		handler.NewUploadHandler(fakeUploader{}),
		handler.NewJobHandler(jobSvc),
		handler.NewLogHandler(logger),
	)

	return router
}

// waitForJobStatus polls GET /jobs/{id} until it reports a terminal
// status or the deadline passes.
func waitForJobStatus(t *testing.T, router http.Handler, apiKey, jobID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec := doRequest(router, http.MethodGet, "/jobs/"+jobID, apiKey, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /jobs/%s = %d, want 200, body=%s", jobID, rec.Code, rec.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid JSON response: %v", err)
		}
		if resp["status"] == "done" || resp["status"] == "failed" {
			return resp
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("job %s never reached a terminal status", jobID)
	return nil
}

func doRequest(router http.Handler, method, path, apiKey string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestHealthzDoesNotRequireAuth(t *testing.T) {
	rec := doRequest(newTestRouter(t), http.MethodGet, "/healthz", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200", rec.Code)
	}
}

func TestContainerRoutesRequireAuth(t *testing.T) {
	rec := doRequest(newTestRouter(t), http.MethodGet, "/containers/ctr-existing", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /containers/ctr-existing without API key = %d, want 401", rec.Code)
	}
}

func TestCreateAndGetContainer(t *testing.T) {
	router := newTestRouter(t)

	body, _ := json.Marshal(map[string]any{"image": "alpine", "name": "web"})
	createRec := doRequest(router, http.MethodPost, "/containers", "owner-1-key", body)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("POST /containers = %d, want 201, body=%s", createRec.Code, createRec.Body.String())
	}

	getRec := doRequest(router, http.MethodGet, "/containers/ctr-existing", "owner-1-key", nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /containers/ctr-existing = %d, want 200, body=%s", getRec.Code, getRec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if resp["id"] != "ctr-existing" {
		t.Fatalf("unexpected response body: %+v", resp)
	}
}

func TestListContainersReturnsOnlyOwnersContainers(t *testing.T) {
	router := newTestRouter(t)

	rec := doRequest(router, http.MethodGet, "/containers", "owner-1-key", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /containers = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var containers []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &containers); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if len(containers) != 1 || containers[0]["id"] != "ctr-existing" {
		t.Fatalf("GET /containers as owner-1 = %+v, want just [ctr-existing]", containers)
	}

	otherOwnerRec := doRequest(router, http.MethodGet, "/containers", "owner-2-key", nil)
	if otherOwnerRec.Code != http.StatusOK {
		t.Fatalf("GET /containers as owner-2 = %d, want 200, body=%s", otherOwnerRec.Code, otherOwnerRec.Body.String())
	}
	var empty []map[string]any
	if err := json.Unmarshal(otherOwnerRec.Body.Bytes(), &empty); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("GET /containers as owner-2 (owns nothing) = %+v, want empty", empty)
	}
}

func TestListContainersRequiresAuth(t *testing.T) {
	rec := doRequest(newTestRouter(t), http.MethodGet, "/containers", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /containers without API key = %d, want 401", rec.Code)
	}
}

func TestGetContainerForbiddenMapsTo403(t *testing.T) {
	router := newTestRouter(t)
	rec := doRequest(router, http.MethodGet, "/containers/ctr-existing", "owner-2-key", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET another owner's container = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetContainerNotFoundMapsTo404(t *testing.T) {
	router := newTestRouter(t)
	rec := doRequest(router, http.MethodGet, "/containers/does-not-exist", "owner-1-key", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET unknown container = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

// TestStartStopDeleteContainer exercises the real async pipeline end to
// end through HTTP: each lifecycle call returns 202 + a Job ID
// immediately, and the actual effect (e.g. the container disappearing)
// only becomes visible once polling GET /jobs/{id} reports "done".
func TestStartStopDeleteContainer(t *testing.T) {
	router := newTestRouter(t)

	submit := func(method, path string) string {
		rec := doRequest(router, method, path, "owner-1-key", nil)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("%s %s = %d, want 202, body=%s", method, path, rec.Code, rec.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid JSON response: %v", err)
		}
		return resp["id"].(string)
	}

	startJob := submit(http.MethodPost, "/containers/ctr-existing/start")
	if status := waitForJobStatus(t, router, "owner-1-key", startJob); status["status"] != "done" {
		t.Fatalf("start job = %+v, want done", status)
	}

	stopJob := submit(http.MethodPost, "/containers/ctr-existing/stop")
	if status := waitForJobStatus(t, router, "owner-1-key", stopJob); status["status"] != "done" {
		t.Fatalf("stop job = %+v, want done", status)
	}

	deleteJob := submit(http.MethodDelete, "/containers/ctr-existing")
	if status := waitForJobStatus(t, router, "owner-1-key", deleteJob); status["status"] != "done" {
		t.Fatalf("delete job = %+v, want done", status)
	}

	if rec := doRequest(router, http.MethodGet, "/containers/ctr-existing", "owner-1-key", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete job completed = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

// TestJobSubmitForbiddenMapsTo403 checks that a job submitted for a
// container owned by someone else fails fast (at Submit, before ever
// reaching a worker) rather than silently queuing.
func TestJobSubmitForbiddenMapsTo403(t *testing.T) {
	router := newTestRouter(t)
	rec := doRequest(router, http.MethodPost, "/containers/ctr-existing/start", "owner-2-key", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("start as different owner = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetJobNotFoundMapsTo404(t *testing.T) {
	router := newTestRouter(t)
	rec := doRequest(router, http.MethodGet, "/jobs/does-not-exist", "owner-1-key", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET unknown job = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

// waitForLogEntries polls GET /logs until it sees at least one entry —
// middleware.Logging's WriteLog calls are async, so an entry from a
// request that just completed isn't necessarily persisted yet.
func waitForLogEntries(t *testing.T, router http.Handler) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec := doRequest(router, http.MethodGet, "/logs", "owner-1-key", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /logs = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}
		var entries []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
			t.Fatalf("invalid JSON response: %v", err)
		}
		if len(entries) > 0 {
			return entries
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no log entries appeared before the deadline")
	return nil
}

// TestReadLogsReturnsRequestEntries exercises the real pipeline —
// middleware.Logging writes through the same logGO.Manager GET /logs
// reads from — not a fake standing in for either side.
func TestReadLogsReturnsRequestEntries(t *testing.T) {
	router := newTestRouter(t)
	doRequest(router, http.MethodGet, "/healthz", "", nil)

	entries := waitForLogEntries(t, router)
	found := false
	for _, e := range entries {
		fields, _ := e["fields"].(map[string]any)
		if fields != nil && fields["path"] == "/healthz" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected an entry logging the /healthz request, got %+v", entries)
	}
}

// TestLoggingRecordsActualErrorStatus is a regression test: middleware
// ordering must have Logging wrapping Error, not the reverse, or every
// c.Error()-routed response (handlers here never set a status
// themselves — they rely on the Error middleware to translate the
// collected error into one) gets logged with Gin's default 200 instead
// of the real status the client actually received. Caught via real
// end-to-end testing (a live WebSocket app-log tail against a running
// server), not by a unit test — this test exists so it can't regress
// silently again.
func TestLoggingRecordsActualErrorStatus(t *testing.T) {
	router := newTestRouter(t)
	rec := doRequest(router, http.MethodGet, "/containers/does-not-exist-marker", "owner-1-key", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET unknown container = %d, want 404", rec.Code)
	}

	entries := waitForLogEntries(t, router)
	var loggedStatus any
	found := false
	for _, e := range entries {
		fields, _ := e["fields"].(map[string]any)
		if fields == nil || fields["path"] != "/containers/does-not-exist-marker" {
			continue
		}
		if e["message"] != "request end" {
			continue
		}
		loggedStatus = fields["status"]
		found = true
		break
	}
	if !found {
		t.Fatalf("expected a \"request end\" entry for the 404 request, got %+v", entries)
	}
	if loggedStatus != float64(http.StatusNotFound) {
		t.Fatalf("logged status = %v, want %d (the client received the correct code — the *logged* one must match it)", loggedStatus, http.StatusNotFound)
	}
}

func TestReadLogsRejectsInvalidTimestamp(t *testing.T) {
	router := newTestRouter(t)
	rec := doRequest(router, http.MethodGet, "/logs?since=not-a-timestamp", "owner-1-key", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /logs?since=invalid = %d, want 400", rec.Code)
	}
}

func TestClearLogsRequiresBeforeParam(t *testing.T) {
	router := newTestRouter(t)
	rec := doRequest(router, http.MethodDelete, "/logs", "owner-1-key", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("DELETE /logs without \"before\" = %d, want 400", rec.Code)
	}
}

// TestClearLogsRemovesEntries checks that a pre-existing entry is gone
// after clearing — not that the log is entirely empty afterward. The
// DELETE /logs request is itself logged by middleware.Logging, and its
// own "request end" entry can only be written after the handler (which
// does the clearing) returns, so it's necessarily still there; that's
// expected, not a bug.
func TestClearLogsRemovesEntries(t *testing.T) {
	router := newTestRouter(t)
	doRequest(router, http.MethodGet, "/healthz", "", nil)
	waitForLogEntries(t, router) // confirm the entry actually landed before clearing

	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	rec := doRequest(router, http.MethodDelete, "/logs?before="+future, "owner-1-key", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE /logs = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	getRec := doRequest(router, http.MethodGet, "/logs", "owner-1-key", nil)
	var entries []map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	for _, e := range entries {
		fields, _ := e["fields"].(map[string]any)
		if fields != nil && fields["path"] == "/healthz" {
			t.Fatalf("expected the /healthz entry to have been cleared, but it's still present: %+v", entries)
		}
	}
}

// TestStreamContainerLogs checks the SSE response shape and that it
// carries the container's actual log content, ownership-checked like
// every other container operation.
func TestStreamContainerLogs(t *testing.T) {
	router := newTestRouter(t)

	rec := doRequest(router, http.MethodGet, "/containers/ctr-existing/logs/stream", "owner-1-key", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET .../logs/stream = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	if !strings.Contains(rec.Body.String(), "data: fake log line") {
		t.Fatalf("SSE body = %q, want it to contain the fake container's log line", rec.Body.String())
	}
}

func TestStreamContainerLogsForbiddenMapsTo403(t *testing.T) {
	router := newTestRouter(t)
	rec := doRequest(router, http.MethodGet, "/containers/ctr-existing/logs/stream", "owner-2-key", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("stream as different owner = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
}

// TestPublicContainerReadableByOtherOwnerButNotMutable is the end-to-end
// version of the authorization boundary also checked at the service
// layer (see TestStartStopDeleteForbiddenForNonOwnerEvenWhenPublic in
// internal/service): a public container must be readable (GET, list)
// by any authenticated caller, but start/stop/delete must still map to
// 403 for a non-owner — visibility never extends to control.
func TestPublicContainerReadableByOtherOwnerButNotMutable(t *testing.T) {
	router := newTestRouter(t)

	body, _ := json.Marshal(map[string]any{"image": "alpine", "name": "shared", "visibility": "public"})
	createRec := doRequest(router, http.MethodPost, "/containers", "owner-1-key", body)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("POST /containers (public) = %d, want 201, body=%s", createRec.Code, createRec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	id := created["id"].(string)
	if created["visibility"] != "public" {
		t.Fatalf("created container visibility = %v, want %q", created["visibility"], "public")
	}

	getRec := doRequest(router, http.MethodGet, "/containers/"+id, "owner-2-key", nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET public container as different owner = %d, want 200, body=%s", getRec.Code, getRec.Body.String())
	}

	listRec := doRequest(router, http.MethodGet, "/containers", "owner-2-key", nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /containers as owner-2 = %d, want 200, body=%s", listRec.Code, listRec.Body.String())
	}
	var containers []map[string]any
	if err := json.Unmarshal(listRec.Body.Bytes(), &containers); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	found := false
	for _, c := range containers {
		if c["id"] == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("GET /containers as owner-2 = %+v, want it to include the public container %q", containers, id)
	}

	if rec := doRequest(router, http.MethodPost, "/containers/"+id+"/start", "owner-2-key", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("start as different owner on a public container = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(router, http.MethodDelete, "/containers/"+id, "owner-2-key", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("delete as different owner on a public container = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSetVisibilityRequiresOwnership(t *testing.T) {
	router := newTestRouter(t)

	body, _ := json.Marshal(map[string]any{"visibility": "public"})
	forbiddenRec := doRequest(router, http.MethodPut, "/containers/ctr-existing/visibility", "owner-2-key", body)
	if forbiddenRec.Code != http.StatusForbidden {
		t.Fatalf("PUT visibility as different owner = %d, want 403, body=%s", forbiddenRec.Code, forbiddenRec.Body.String())
	}

	okRec := doRequest(router, http.MethodPut, "/containers/ctr-existing/visibility", "owner-1-key", body)
	if okRec.Code != http.StatusOK {
		t.Fatalf("PUT visibility as owner = %d, want 200, body=%s", okRec.Code, okRec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(okRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if resp["visibility"] != "public" {
		t.Fatalf("visibility after PUT = %v, want %q", resp["visibility"], "public")
	}
}
