# conTogether

Two independent Go modules sharing one `go.mod`, plus a frontend:

- **[`logsys/`](logsys/README.md)** — a dependency-injected, asynchronous logging system with pluggable storage backends (memory, file, SQLite).
- **[`container-api/`](container-api/README.md)** — a RESTful API for managing per-user Docker containers, built on top of `logsys`.
- **[`web/`](web/README.md)** — a React dashboard for `container-api`: containers, uploads, and both log views. Not a separate deployable — `container-api` embeds the built output directly into its own binary (`internal/webui`), so it's one process, one port, in production.

`container-api` imports `logsys` for its logging middleware, which is why both live in one module rather than two.

## Requirements

- Go 1.24+
- A running Docker daemon (only needed to actually create/start/stop/delete containers — the API server itself starts fine without one)
- Postgres, only if you choose `DB_DRIVER=postgres` over the SQLite default — see [`container-api/README.md`](container-api/README.md#switching-the-database-backend)
- `buf` + `protoc-gen-go` + `protoc-gen-connect-go`, only if you change `proto/logsys/v1/logsys.proto` — generated code is committed, so a fresh clone doesn't need these otherwise
- Node.js, only if you want the real embedded frontend instead of the checked-in placeholder page — `go build`/`go test` work without it either way (see [`container-api/README.md#frontend`](container-api/README.md#frontend))

## Setup

```bash
go mod download
```

## Running the tests

```bash
go test ./... -race
```

Every package builds and passes under the race detector, including the concurrency-control test in `container-api/internal/service`, the async-close tests in `logsys` and `container-api/internal/job`, and real round-trip tests through actual HTTP/WebSocket servers for the gRPC/Connect and WebSocket log transports (`container-api/internal/rpc`, `container-api/internal/wsstream`) — see [`container-api/README.md#multi-protocol-log-delivery`](container-api/README.md#multi-protocol-log-delivery) for why logs are available over REST, SSE, gRPC/gRPC-Web/Connect, and WebSocket.

## Running the server

Same codebase, same features — these are three alternative ways to *run*
it, not three different things to run together. Pick one.

| | What's running | When to use it |
|---|---|---|
| **Docker Compose** | Everything containerized — the Go binary with the UI embedded, in one container | Simplest, most production-like. Only Docker needed locally. |
| **Single Go binary** | The server runs natively on your machine, UI baked into that same binary | Native (non-Docker) run, still one process/port. |
| **Two dev servers** | Go backend (`:8080`) + Vite dev server (`:5173`), proxying to each other | Only when actively editing the frontend and you want hot-reload. |

All three end up at the same place: open the printed URL, paste in an API
key (`dev-key` by default; override with `DEV_API_KEY`), and use the
dashboard. If you just want to try it with the least setup, use Docker
Compose.

### Docker Compose

```bash
docker compose up --build
```

Open http://localhost:8080. This builds `container-api` into a small Alpine
image (with the frontend built fresh in its own Docker stage — no separate
step needed) and mounts the *host's* Docker socket into it
(`/var/run/docker.sock`), since the API's job is to manage containers and
needs to talk to the same daemon it's running alongside, not a nested one.
The SQLite DB, log file, and uploads persist in a named volume across
restarts.

To use Postgres instead of the SQLite default:

```bash
DB_DRIVER=postgres docker compose --profile postgres up --build
```

Stop with `docker compose down` (add `-v` to also drop persisted volumes).
See [`container-api/README.md`](container-api/README.md#running-in-docker)
for more (env var overrides, etc.).

### Single Go binary

```bash
cd container-api
make frontend   # builds web/ and embeds it — skip for a placeholder page instead
go run ./cmd/server
```

Open http://localhost:8080. Needs Node.js (for `make frontend`) and a
running Docker daemon (for container operations) directly on your machine.

### Two dev servers (frontend hot-reload)

```bash
# terminal 1
cd container-api
go run ./cmd/server        # :8080 — frontend doesn't need to be built for this

# terminal 2
cd web
npm install                 # first time only
npm run dev                 # :5173, proxies API/WebSocket calls to :8080
```

Open http://localhost:5173.

See [`container-api/README.md`](container-api/README.md) for endpoints, auth, and Swagger UI.

## Design diagrams

[`docs/diagrams/`](docs/diagrams/) contains PlantUML sequence/component diagrams for both modules (log write/read flow, request middleware chain, async job processing, concurrency control, graceful shutdown). View them with a PlantUML renderer (e.g. the VS Code PlantUML extension, or plantuml.com).
