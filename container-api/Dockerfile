# Build context is the repo root (see docker-compose.yml) since this
# module also contains the sibling logsys package container-api imports,
# and the frontend (web/) container-api embeds into its own binary.
# syntax=docker/dockerfile:1

FROM node:22-bookworm AS frontend
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY logsys ./logsys
COPY container-api ./container-api
# go:embed can only reach files inside the embedding .go file's own
# module subtree — container-api/internal/webui/webui.go embeds
# ./dist, so the frontend build has to land there before `go build`
# runs, not just exist somewhere in the image. This overwrites the
# checked-in placeholder (internal/webui/dist/index.html) with the real
# build for every image; see internal/webui/webui.go and
# container-api/Makefile's `frontend` target for the equivalent local
# dev step.
COPY --from=frontend /web/dist ./container-api/internal/webui/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/server ./container-api/cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /data
COPY --from=build /out/server /usr/local/bin/server
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
    CMD wget -qO- http://localhost:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/server"]
