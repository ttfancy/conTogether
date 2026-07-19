# container-api

A RESTful API for managing per-user Docker containers: create/start/stop/delete,
file uploads, async job status, all behind API-key auth.

## Architecture

```
cmd/server        composition root — the only place every concrete type is known
internal/handler   HTTP layer: bind/validate requests, call a service, shape responses
internal/service   business logic: ownership checks, per-container concurrency lock
internal/job       async job queue + worker pool (start/stop/delete run here)
internal/repository  persistence — SQLite and Postgres, chosen at runtime via config
internal/migrations  versioned schema migrations, embedded, applied by both repos
internal/container   Docker Engine SDK wrapper
internal/upload      per-user file upload validation + storage
internal/middleware  auth, request logging, panic recovery + error mapping
internal/domain      shared data types (Container, Job, ContainerSpec) with no behavior
internal/rpc         gRPC/gRPC-Web/Connect-JSON transport for log operations
internal/wsstream    WebSocket transport for the same log operations
internal/genproto    generated code from proto/logsys/v1/logsys.proto (buf generate)
internal/webui       embeds the built frontend (../web), serves it with SPA fallback
```

Diagrams in [`../docs/diagrams/`](../docs/diagrams/): `04-container-api-components.puml`
(full layer map), `05-request-lifecycle.puml` (middleware chain), `06-create-container.puml`,
`07-async-job.puml`, `08-concurrency-control.puml`, `09-graceful-shutdown.puml`.

### Dependency injection

Every interface (`service.ContainerRepository`, `service.DockerClient`,
`job.ContainerOperator`, `job.Store`, `middleware.APIKeyStore`, ...) is defined
in the package that *consumes* it, not the package that implements it —
`internal/service` never imports `internal/repository` or `internal/container`.
Everything is wired together once, in `cmd/server/main.go`. This is what makes
the database backend a runtime config choice rather than a compile-time one:
`openContainerRepo` in `main.go` is the only place `DBDriver` is branched on,
constructing either `repository.SQLiteContainerRepo` or
`repository.PostgresContainerRepo` — both satisfy the same
`service.ContainerRepository` interface, so `ContainerService` and everything
above it never knows which one it's talking to. See `internal/*/​*_test.go`
for how this same seam makes unit testing possible without a real database or
Docker daemon.

### Why start/stop/delete are asynchronous

`POST /containers/{id}/start`, `/stop`, and `DELETE /containers/{id}` all
submit a job and return `202` with a Job ID immediately — the actual Docker
call happens on a worker pool (`internal/job`), and the client polls
`GET /jobs/{jobId}` for completion. Ownership/existence errors (403/404) are
still checked *synchronously* inside `Submit`, before the job is even queued
— those are already known at submission time, so returning them immediately
is both correct and better UX than making the client poll to discover a
request was invalid all along.

### Concurrency control

`ContainerService` holds one `sync.Mutex` per container ID (created
exactly-once even under a concurrent first access, guarded by a registry
mutex) so a start and a delete racing on the same container serialize instead
of both proceeding against Docker at once. See
`internal/service/container_service_test.go`'s
`TestConcurrentStartAndDelete`, run under `-race`.

### Graceful shutdown

On `SIGINT`/`SIGTERM`: stop accepting new HTTP connections → let in-flight
requests finish (`http.Server.Shutdown`) → drain the job queue (with its own
timeout, so one stuck Docker call can't hang the process forever) → close
the logger last. See `cmd/server/main.go`.

## Configuration

Everything tunable is read from the environment by `internal/config`
(`config.Load()`, called once in `main.go`) — nothing else in the app reads
`os.Getenv` directly. All variables are optional; defaults are for local dev.

| Env var | Default | Meaning |
|---|---|---|
| `SERVER_PORT` | `8080` | HTTP listen port |
| `DEV_API_KEY` | `dev-key` | Static API key → `dev-user` owner (see limitations below) |
| `LOG_FILE_PATH` | `container-api.log` | JSON-lines application log |
| `DB_DRIVER` | `sqlite` | `sqlite` or `postgres` — selects the repository backend |
| `DB_PATH` | `container-api.db` | SQLite database file (used when `DB_DRIVER=sqlite`) |
| `DATABASE_URL` | *(none)* | Postgres connection string, e.g. `postgres://user:pass@host:5432/db?sslmode=disable` — **required** when `DB_DRIVER=postgres` |
| `UPLOADS_DIR` | `uploads` | Root directory for per-user uploads |
| `JOB_WORKERS` | `4` | Concurrent async-job worker count |
| `JOB_QUEUE_SIZE` | `100` | Pending-job queue size before `Submit` returns 503 |
| `SHUTDOWN_TIMEOUT` | `10s` | Bounds both HTTP graceful shutdown and job draining |

## Running it

```bash
go run ./cmd/server
# or, e.g.:
SERVER_PORT=9090 JOB_WORKERS=8 go run ./cmd/server
```

Requires a running Docker daemon to actually create/start/stop/delete
containers (the server itself starts fine without one — you'll only see
errors on the container endpoints).

Auth: every route except `/healthz` and `/swagger/*` requires an
`X-API-Key` header. For local use it defaults to `dev-key` (maps to a
`dev-user` owner); override with `DEV_API_KEY`.

```bash
curl -H "X-API-Key: dev-key" -H "Content-Type: application/json" \
  -d '{"image":"alpine","name":"web"}' http://localhost:8080/containers
```

### Switching the database backend

Defaults to SQLite (a local file). To use Postgres instead:

```bash
DB_DRIVER=postgres DATABASE_URL="postgres://user:pass@localhost:5432/contogether?sslmode=disable" \
  go run ./cmd/server
```

`config.Load()` rejects startup with a clear error if `DB_DRIVER=postgres` is
set without `DATABASE_URL`, or if `DB_DRIVER` is anything other than
`sqlite`/`postgres` — this fails fast at boot rather than at the first
request. The Postgres repository's own tests
(`internal/repository/postgres_container_repo_test.go`) skip themselves
unless `TEST_POSTGRES_DSN` points at a reachable instance; point it at any
Postgres (e.g. `docker run -e POSTGRES_PASSWORD=test -p 5433:5432 postgres:16-alpine`) to run them for real.

### Schema migrations

Both repository constructors call `migrations.Apply(db, driver)` before
returning, which replaced an earlier `CREATE TABLE IF NOT EXISTS` at
connection time. That approach only ever handles the very first deployment —
it can't express "add a column" or "add an index" against a database that
already has rows in it, which is the actual problem a migration system
solves. `internal/migrations` keeps one versioned `.up.sql`/`.down.sql` pair
per change under `sqlite/` and `postgres/` (e.g. `0001_init`, then
`0002_index_owner_id`), embedded into the binary via `go:embed` — nothing
extra to ship or mount at deploy time — and applies them in order via
[`golang-migrate`](https://github.com/golang-migrate/migrate), using its
`modernc.org/sqlite`- and `pgx/v5`-based drivers so no cgo dependency is
reintroduced.

This is safe to call on every process start: `golang-migrate` tracks applied
versions in a `schema_migrations` table it manages, so a database already at
the latest version is a no-op — there's no separate "run migrations" step to
remember before deploying a new version.

Adding a change means adding the next-numbered `.up.sql`/`.down.sql` pair to
*both* `sqlite/` and `postgres/` (dialects differ slightly — e.g. `INTEGER`
vs `BIGINT` for the timestamp columns) — see
`internal/migrations/migrations_test.go` for tests verifying both that a
fresh database ends up with the expected schema and that re-applying is a
no-op.

The `.down.sql` files are actually exercised in tests, not just
believed-correct: `migrations.Down`/`migrations.Steps` (test-only — see
below) drive a full up → down → up round trip proving the down migrations
are true inverses of the up ones, plus a check that rolling back the index
migration specifically preserves existing rows while rolling back the table
migration destroys them — those are different migrations with different
(correct) blast radii, not a contradiction.

### Running in Docker

```bash
docker compose up --build
```

(from the repo root — `docker-compose.yml` lives there; the Dockerfile
itself is `container-api/Dockerfile`.)

Two things worth understanding about how this is wired:

- **The Docker socket is mounted, not nested.** `container-api`'s whole job
  is to manage *other* containers, so the container it runs in needs to talk
  to the same Docker daemon it's sitting next to — mounting
  `/var/run/docker.sock` (the "sibling containers" pattern) does that,
  versus running a full Docker-in-Docker daemon inside the container, which
  is heavier and generally discouraged.
- **State persists in a named volume at `/data`**, which is also the
  image's `WORKDIR` — so the default relative paths (`container-api.log`,
  `container-api.db`, `uploads/`) land there automatically without needing
  path env vars set.

Override config via env vars (see [Configuration](#configuration) above),
e.g.:

```bash
SERVER_PORT=9090 DEV_API_KEY=my-key docker compose up --build
```

An optional `postgres` service is defined but only starts if you opt in via
the `postgres` Compose profile — otherwise `container-api` uses its SQLite
default. `container-api` waits for Postgres's healthcheck before starting, so
there's no startup race:

```bash
DB_DRIVER=postgres docker compose --profile postgres up --build
```

Tear down (and drop persisted state) with:

```bash
docker compose down -v
```

### Frontend

There's a React dashboard at [`../web`](../web) — container list/create/
start/stop/delete, uploads, and both log views. It's not a separate
service: `internal/webui` embeds the built frontend directly into this
binary via `go:embed`, so in production it's one process, one port, no
CORS.

`go build`/`go test` work out of the box without Node installed — a
placeholder page is checked in at `internal/webui/dist/index.html` for
exactly that reason. To get the real UI:

```bash
make frontend   # builds ../web and copies the output into internal/webui/dist
go run ./cmd/server
```

`docker compose up --build` always builds the real frontend fresh (see
the `frontend` stage in `Dockerfile`) — no separate step needed there.

For local frontend *development* (hot reload, etc.), run the Vite dev
server directly instead — see [`../web/README.md`](../web/README.md).

### API docs

Swagger UI: http://localhost:8080/swagger/index.html — generated from the
`@`-annotations on each handler via `swag`. Regenerate after changing any
handler's annotations:

```bash
make docs
```

### Endpoints

| Method | Path | Notes |
|---|---|---|
| GET | `/healthz` | No auth |
| POST | `/containers` | Create — synchronous |
| GET | `/containers` | List the authenticated owner's containers |
| GET | `/containers/{id}` | Get — synchronous |
| POST | `/containers/{id}/start` | Async — returns 202 + Job ID |
| POST | `/containers/{id}/stop` | Async — returns 202 + Job ID |
| DELETE | `/containers/{id}` | Async — returns 202 + Job ID |
| GET | `/jobs/{jobId}` | Poll job status |
| POST | `/uploads` | Multipart file upload |
| WS | `GET /ws/containers/{id}/exec` | Interactive terminal — see below |

## Interactive terminal

`GET /ws/containers/{id}/exec` bridges a WebSocket to a real shell
(`/bin/sh`, TTY-attached via `docker exec`) inside the container — owner-only,
regardless of visibility, since a shell is real control, not a read (see
`service.ContainerService.Exec`'s use of the strict `mustOwnContainer` check,
the same one start/stop/delete use — a public container's visibility never
extends to this). The frontend's `/containers/{id}/exec` page
(`web/src/pages/ContainerExecPage.tsx`) drives it with
[xterm.js](https://xtermjs.org/).

Wire protocol: binary WS frames carry raw PTY bytes in both directions
(keystrokes in, terminal output out — a TTY session is a single
unmultiplexed stream, unlike `StreamLogs`' non-TTY containers); text WS
frames from the client are JSON resize messages
(`{"type":"resize","cols":..,"rows":..}`), sent whenever the browser's
terminal element resizes so full-screen programs (`vim`, `top`, ...) render
at the right size — the same binary-data/text-control split tools like
`ttyd` and `gotty` use for the same reason.

## Multi-protocol log delivery

Log data (both container-api's own operational logs and a managed container's
stdout/stderr) is available over four transports. They all call into the
exact same service objects (`*applog.Manager`, `*service.ContainerService`) —
this is a difference in wire protocol, not four separate implementations of
the same feature.

| Transport | App logs (query/clear) | Container stdout/stderr (live) | Package |
|---|---|---|---|
| REST | `GET`/`DELETE /logs` | `GET /containers/{id}/logs/stream` (SSE) | `internal/handler` |
| gRPC / gRPC-Web / Connect-JSON | `LogService.ReadLogs`/`ClearLogs` | `LogService.StreamContainerLogs` (server-streaming) | `internal/rpc` |
| WebSocket | `GET /ws/logs` (live-tail only, no query/clear) | `GET /ws/containers/{id}/logs` | `internal/wsstream` |

A few things worth knowing about why each one looks the way it does:

- **One Protobuf schema, three wire formats.** `proto/logsys/v1/logsys.proto`
  defines `LogService` once; [Connect](https://connectrpc.com) generates a
  handler that serves gRPC, gRPC-Web, *and* its own simple JSON/HTTP protocol
  from that single schema and a single port — no separate grpc-web proxy, and
  a TypeScript client can be generated from the same `.proto` a Go service
  client uses. Regenerate after changing the schema:
  ```bash
  buf generate
  ```
  (needs `buf`, `protoc-gen-go`, and `protoc-gen-connect-go` — see
  `buf.gen.yaml`; generated code lives in `internal/genproto` and is
  committed, so a fresh clone doesn't need the toolchain unless the schema
  itself changes.)
- **Plain gRPC needs HTTP/2; the rest don't.** `main.go` wraps the server in
  `h2c` (plaintext HTTP/2) so a real gRPC client works locally without TLS.
  gRPC-Web and Connect's JSON protocol are fine over plain HTTP/1.1 either
  way.
- **Auth is one implementation, two entry points.** `middleware.OwnerID`
  works on a plain `context.Context`, not `*gin.Context` — that's what lets
  the Connect interceptor (`internal/rpc/auth_interceptor.go`) and the Gin
  middleware resolve the exact same identity model (API key → owner ID,
  never a client-supplied identity) without either transport reimplementing
  it. Streaming RPCs authenticate themselves directly rather than through
  the interceptor, since Connect's streaming interceptor hook wraps a
  different type than the unary path — see the comment in
  `internal/rpc/log_service.go`.
- **WebSocket auth can't use a header.** Browsers' `WebSocket` constructor
  can't set custom headers on the handshake request, so `internal/wsstream`
  accepts the API key as an `api_key` query parameter instead (falling back
  to `X-Api-Key` for non-browser clients that can set headers). Everything
  else uses the header.
- **The WebSocket app-log tail is a genuinely new capability, not just
  another transport for something that already existed.** It's built on
  `applog.Manager.RegisterLogHandler` — the exact extension point the
  original design called for — bridging live entries into the socket as
  they're written, rather than polling `GET /logs`. That handler must be
  removed when the client disconnects, or it leaks for the life of the
  process; `RegisterLogHandler` now returns an `unregister` func for exactly
  this (a backwards-compatible addition — existing callers that ignore the
  return value still compile). It's also *non-blocking*: a slow/stuck
  WebSocket client drops messages for itself rather than stalling the one
  shared write-loop goroutine every other log write in the process also
  goes through — see
  `internal/wsstream/applogs_test.go`'s
  `TestServeAppLogsDoesNotBlockOtherWrites` for a test that actually proves
  this under load, not just asserts it.
- **Not implemented, and why**: MQTT, a message queue (NATS/Kafka/Redis
  pub-sub), and Syslog would all suit shipping logs *out* to an external
  system, but that's a different problem from "watch logs live in a client
  you're talking directly to," which is what all four transports above
  solve. GraphQL subscriptions were skipped since standing up a whole
  GraphQL layer for one feature is disproportionate to what it buys here.

## Testing

```bash
go test ./... -race
```

Covers: service-layer business logic and the concurrency-control race test
(`internal/service`), the async job pipeline including fail-fast submission,
queue-full, and drain-on-close semantics (`internal/job`), upload validation
including path-traversal and content-sniffing rejection (`internal/upload`),
config validation for the DB driver switch (`internal/config`), migration
idempotency and ordering (`internal/migrations`), SQLite repository
round-trips (`internal/repository` — the Postgres repository's tests live
alongside them but skip unless `TEST_POSTGRES_DSN` is set, since they need a
real server), a full HTTP-level integration test exercising the real
middleware chain + async job pipeline against fakes for the repository/Docker
layer (`internal/handler`), the Connect/gRPC service including a genuine
round trip through a real HTTP server and generated client for the
server-streaming RPC (`internal/rpc`), and the WebSocket transports including
a real Dial/Read round trip and a test that specifically proves a stuck
client can't stall the shared applog write-loop (`internal/wsstream`), and
the embedded-frontend SPA fallback — proving an unmatched path serves the
same content as `/` rather than 404ing — without asserting on the specific
placeholder-vs-real-build content, since which one is embedded depends on
whether `make frontend` has run (`internal/webui`).

## Assumptions & known limitations

These are deliberate scope decisions for a project of this size, not
oversights — noted here so they're explicit rather than discovered:

- **Auth is a static API-key map** (`middleware.MapAPIKeyStore`), not
  JWT/OAuth. It demonstrates the middleware pattern and resolves a key to an
  owner ID (never trusting a client-supplied identity), but a real deployment
  would back `middleware.APIKeyStore` with a database-backed lookup — no
  other code would need to change.
- **Job status is in-memory only** (`job.MemoryStore`). A process restart
  loses a job's tracked status (the underlying Docker operation may have
  already completed) — the same interface swap to a durable store (e.g.
  SQLite-backed) would remove this without `job.Service` changing.
- **The per-container lock registry never evicts entries.** Fine at this
  scale; a long-running deployment managing many short-lived containers
  would want to garbage-collect locks for deleted containers.
- **Everything is single-instance.** The lock registry and job queue are
  in-process; running multiple API instances behind a load balancer would
  need a distributed lock and queue (e.g. Redis) instead.
- **File uploads only accept text (CSV/JSON/source code) and images**
  (PNG/JPEG/GIF), sniffed via content, not extension. `application/octet-stream`
  — Go's sniffing fallback for anything binary it doesn't recognize — is
  deliberately excluded, since allowing it would let a relabeled executable
  through.
- **Migrations only ever run forward (`Up`) automatically in the app itself.**
  `.down.sql` files exist, are tested (`migrations_test.go` proves a full
  up → down → up round trip), and are valid `golang-migrate` migrations —
  but there's no exposed rollback *command* in this app; reaching for one for
  real would mean running `golang-migrate`'s own CLI directly against the
  configured DSN, pointed at `internal/migrations/sqlite` or
  `internal/migrations/postgres`.
- **`middleware.Logging` logs every request, including static frontend
  asset fetches** (JS/CSS/favicon) once `internal/webui` is serving
  them — there's no path-based exclusion for "uninteresting" requests.
  Harmless at this scale (and arguably honest — "here's everything that
  came through the door"), but worth knowing before it looks like noise
  in the App Logs page.
