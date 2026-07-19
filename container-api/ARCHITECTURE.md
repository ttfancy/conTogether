# Architecture

This is the single place that explains *how* container-api is put together
and *why* — the root [`README.md`](../README.md) and
[`container-api/README.md`](README.md) cover building/running/testing;
[`docs/diagrams/`](../docs/diagrams/) has the visual sequence/component
diagrams this document links out to rather than re-drawing.

## Layering

```
internal/handler   HTTP layer: bind/validate a request, call a service, shape the response
internal/service   business logic: ownership checks, visibility rules, the per-container lock
internal/job       async job queue + worker pool (create/start/stop/delete all run here)
internal/repository  persistence — SQLite and Postgres, chosen at runtime via config
internal/container   Docker Engine SDK wrapper
internal/upload      per-owner file upload validation + storage
internal/applog      conTogether's own small, dependency-free structured logger
internal/middleware  auth, request logging, panic recovery + error → status-code mapping
internal/domain      shared data types (Container, Job, ContainerSpec, Upload, ExecSession) — no behavior
internal/rpc         gRPC/Connect transport for log operations
internal/wsstream    WebSocket transport for log tails and the interactive terminal
internal/genproto    generated code from proto/logsys/v1/logsys.proto (buf generate)
internal/webui       embeds the built frontend (../web), serves it with SPA fallback
```

Requests flow strictly downward: `handler → service → {repository, container,
job, applog}`. Nothing in `internal/service` imports `internal/repository`,
`internal/container`, or `internal/job` — see the next section for why, and
[`04-container-api-components.puml`](../docs/diagrams/04-container-api-components.puml)
for the full component map.

## Dependency injection: interfaces live with their consumer

Every interface this codebase depends on — `service.ContainerRepository`,
`service.DockerClient`, `job.ContainerOperator`, `job.Store`,
`middleware.APIKeyStore`, `handler.ContainerServicer`, and more — is declared
in the package that *uses* it, not the package that implements it. So
`internal/service` defines what it needs from a repository and a Docker
client without ever importing `internal/repository` or `internal/container`
itself; those packages just happen to produce types that satisfy the
interfaces `service` already declared.

This is what makes `internal/config`'s `DB_DRIVER` a runtime choice instead
of a compile-time one: `openContainerRepo` in `cmd/server/main.go` is the
only place that branches on it, constructing either
`repository.SQLiteContainerRepo` or `repository.PostgresContainerRepo` —
both satisfy `service.ContainerRepository`, so `ContainerService` and
everything above it never knows or cares which one it's talking to. The same
seam is why every package's own tests run against a hand-written fake
instead of a real database or Docker daemon — see any `internal/*/​*_test.go`.

`cmd/server/main.go` is the one place every concrete type is actually named
and wired together — the "composition root."

## Data model & persistence

`domain.Container` is the persisted record this API manages, distinct from
the raw Docker container it wraps: `OwnerID`/`Status`/`Visibility` are this
app's own bookkeeping, `DockerID` is the foreign key into the Docker daemon.
`ContainerStatus` moves through `pending → created → running/stopped →
removed`, with `exited` and `failed` as two ways a container can end up
somewhere other than where an owner's last action asked for (see below).

`ContainerRepository` (SQLite or Postgres, chosen at runtime) is the only
thing that persists `domain.Container`. Both implementations share the same
migration set (`internal/migrations`), applied idempotently at startup.

## Container lifecycle: why everything is a job

`POST /containers`, `/start`, `/stop`, and `DELETE /containers/{id}` all
return `202 Accepted` with a job ID immediately — none of them block the
HTTP request on the actual Docker call. `internal/job.Service` holds a fixed
worker pool draining a task channel; the client polls `GET /jobs/{jobId}`
for `status` (`pending → running → done/failed`) and, for a create job
specifically, a `stage` field (`"creating container"` vs `"pulling image"`).

Two things make **create** worth calling out specifically, since it's the
newest of the four to become async:

1. **Auto-pull.** `internal/container/docker.go`'s `CreateContainer` pulls
   the image if the daemon doesn't already have it, then retries — without
   this, any image not already cached locally (anything but whatever
   happens to be pulled in dev) surfaced as an opaque "internal server
   error." Pulling can take real time, which is exactly the kind of thing
   this API doesn't make an HTTP request wait on anywhere else.
2. **Two-phase creation.** `ContainerService.BeginCreateContainer` persists
   a `pending` placeholder record *synchronously*, so the handler has a real
   container ID to hand back and a job to submit against — the actual
   Docker work happens in `ContainerService.CreateContainer` (a different
   method, matching `job.ContainerOperator`'s interface), run by a worker.
   See [`06-create-container.puml`](../docs/diagrams/06-create-container.puml).

`exited` is a related, separate detection: `StartContainer` checks
`IsRunning` right after asking Docker to start a container, because a
container with nothing keeping it alive (no `CMD` override) runs and quits
on its own almost immediately — `ContainerStart` succeeding doesn't mean the
process stayed up. Without this check, such a container would show
`running` forever.

Ownership/existence errors (403/404) are checked *synchronously* inside
`Submit`/`SubmitCreate`, before a job is even queued — they're already known
at submission time, so making the client poll to discover a request was
invalid all along would be worse UX for no benefit. See
[`07-async-job.puml`](../docs/diagrams/07-async-job.puml).

## Concurrency control

`ContainerService` hands out one `sync.Mutex` per container ID (created
exactly-once even under concurrent first access, guarded by a small registry
mutex) — purely in-process, no database-level locking. `StartContainer`,
`StopContainer`, `DeleteContainer`, and `SetVisibility` all serialize
through it, so — the spec's own example — a start and a delete racing on the
same container can't both proceed against Docker at once; whichever loses
the race re-reads the container's current state and reacts correctly (404 if
it's already gone, not some generic conflict). See
`TestConcurrentStartAndDelete` (run under `-race`) and
[`08-concurrency-control.puml`](../docs/diagrams/08-concurrency-control.puml).

## Authorization: visibility is a read grant, never a control grant

Every container and upload has a `Visibility` (`private` or `public`).
`public` extends **read** access — `GetContainer`, `ListContainers`,
`StreamLogs`, upload `Get`/`List`/download — to any authenticated caller,
not just the owner. It never extends **control**: `StartContainer`,
`StopContainer`, `DeleteContainer`, `Exec`, and `SetVisibility` all use a
stricter `mustOwnContainer` check that rejects any non-owner outright,
regardless of visibility. Two different check functions
(`GetContainer`'s loosened check vs. `mustOwnContainer`'s strict one) exist
specifically so this distinction can't be accidentally blurred by reusing
one function for both. See
`TestStartStopDeleteForbiddenForNonOwnerEvenWhenPublic`.

## Multi-protocol log delivery

Container-api's own operational log (`internal/applog.Manager`) and a
managed container's stdout/stderr are each reachable over more than one
transport, all backed by the exact same service methods:

- **REST**: `GET/DELETE /logs` (app log), `GET /containers/{id}/logs/stream`
  (Server-Sent Events tail of a container's own output).
- **WebSocket** (`internal/wsstream`): `GET /ws/logs` (live app-log tail),
  `GET /ws/containers/{id}/logs` (live container-log tail), and
  `GET /ws/containers/{id}/exec` (see below — a different kind of stream).
- **gRPC/Connect** (`internal/rpc`, `proto/logsys/v1`): `LogService`'s
  `ReadLogs`/`ClearLogs`/`StreamContainerLogs`, authenticated the same way
  as the REST routes (`NewAuthInterceptor`), mirroring the same operations
  over a schema'd, binary-capable transport instead of plain JSON.

## Interactive terminal

`GET /ws/containers/{id}/exec` bridges a WebSocket to a real, TTY-attached
shell inside the container (`docker exec`), using binary WS frames for raw
PTY I/O in both directions and text frames for JSON resize control
messages. This is real control, not a read — it uses the strict
`mustOwnContainer` check, same as start/stop/delete, so a public
container's visibility never grants a non-owner a shell into it. The
frontend's `/containers/{id}/exec` page drives it with xterm.js.

## Uploads

`internal/upload.Service` stores each file under `UPLOADS_DIR/<ownerID>/`,
sniffing the actual content type rather than trusting the client-supplied
one, with the same private/public visibility model as containers.

## conTogether's own logging (`internal/applog`)

A small, self-contained, dependency-free logger — async writes via a
buffered channel, race-free `Close` (an `RWMutex` guards the
check-then-send so `Close` can't race a send), an extension point
(`RegisterLogHandler`) that both the WebSocket app-log tail and the gRPC
`LogService` are built on.

## Graceful shutdown

On `SIGINT`/`SIGTERM`: stop accepting new HTTP connections → let in-flight
requests finish (`http.Server.Shutdown`) → close the job queue and wait for
already-queued jobs to finish, racing that against `SHUTDOWN_TIMEOUT` so one
stuck Docker call can't hang the process forever → log "shutdown complete"
→ close the logger last (after the last `WriteLog`, since one after `Close`
would return `ErrClosed`). See
[`09-graceful-shutdown.puml`](../docs/diagrams/09-graceful-shutdown.puml).

## Testing strategy

Every package is tested against a hand-written fake for whatever it depends
on (never a real database or Docker daemon) *except* `internal/container`,
which deliberately runs its handful of tests against a real Docker daemon
(skipping if none is reachable) — the same reasoning as the Postgres
repository tests skipping without `TEST_POSTGRES_DSN`: some things (does
Docker actually report a name conflict as 409-shaped? does an image really
get pulled? does a container with no command really exit on its own?) are
only worth trusting when checked against the real thing, not a fake that
just assumes the answer. `internal/handler`'s integration tests wire the
real HTTP router and the real `job.Service` against fakes only at the
repository/Docker boundary, so they exercise the genuine async pipeline —
submit, worker, poll — end to end.

## Diagram index

| Diagram | Covers |
|---|---|
| [`04-container-api-components.puml`](../docs/diagrams/04-container-api-components.puml) | Full component/layer map |
| [`05-request-lifecycle.puml`](../docs/diagrams/05-request-lifecycle.puml) | Middleware ordering and why Logging must be outermost |
| [`06-create-container.puml`](../docs/diagrams/06-create-container.puml) | Async create: placeholder → job → auto-pull → stages → poll |
| [`07-async-job.puml`](../docs/diagrams/07-async-job.puml) | The shared submit/worker/poll shape behind create/start/stop/delete |
| [`08-concurrency-control.puml`](../docs/diagrams/08-concurrency-control.puml) | The per-container lock, and the real (not 409) outcome of a start-vs-delete race |
| [`09-graceful-shutdown.puml`](../docs/diagrams/09-graceful-shutdown.puml) | Signal → HTTP drain → job drain (timeout-raced) → logger close |
