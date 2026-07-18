# conTogether frontend

A React + TypeScript + Vite dashboard for `container-api`: container
list/create/start/stop/delete, per-user file uploads, and two log views
(container-api's own operational logs, and a managed container's live
stdout/stderr).

See [`../container-api/README.md`](../container-api/README.md#multi-protocol-log-delivery)
for the backend transports this talks to.

## Running it

```bash
npm install
npm run dev
```

Opens on http://localhost:5173, proxying API/WebSocket requests to
`container-api` on `:8080` (see `vite.config.ts`) — start that separately
(`cd ../container-api && go run ./cmd/server`).

In production this isn't run standalone at all: `container-api` embeds
the built output directly into its own binary (see
[`../container-api/internal/webui`](../container-api/internal/webui) and
`make frontend` in `container-api/Makefile`), so the API and UI are one
process on one port with no CORS/proxy concerns.

## Structure

```
src/api/        fetch wrapper + one module per resource (containers, logs, uploads)
src/context/    API key state, shared across the app via React context
src/hooks/      useApiKey, and the job-status poll loop (waitForJob)
src/components/ Layout (nav + API key input), StatusBadge
src/pages/      one page per route
```

## A routing gotcha worth knowing

Client-side routes here must not exactly match a real `container-api`
REST path (see `App.tsx`) — the app's own router does exact-path
matching, so a same-named frontend route would always hit the real API
handler instead of ever reaching the SPA fallback. That's why the pages
are at `/upload` and `/app-logs`, not `/uploads` or `/logs` (those are
real API endpoints). `/containers/:id/logs` is fine as a frontend route
even though it shares the `/containers` *prefix* with several API
routes — exact matching means there's no real backend route at that
exact path, so it always falls through correctly in production.

`vite.config.ts`'s dev-only proxy has the equivalent problem in the
other direction (it matches by *prefix*, not exact path) and works around
it with an `Accept: text/html` bypass — see the comment there.
