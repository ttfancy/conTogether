.PHONY: docs build test run frontend

# Regenerate Swagger docs from the @-annotations in cmd/server/main.go
# and internal/handler/*.go. Run this after changing any handler's
# annotations — docs.go is committed, but it's generated, not hand-edited.
docs:
	swag init -g cmd/server/main.go --output docs --parseDependency --parseInternal

# Builds the React frontend (../web) and copies the output into
# internal/webui/dist for go:embed to pick up. go:embed can only reach
# files inside the embedding .go file's own module subtree, and web/
# sits alongside container-api/, not inside it — hence the copy rather
# than embedding web/dist directly. Run this before `go build`/`go run`
# if you want the real UI instead of the placeholder page; the
# Dockerfile does the equivalent in its own build stage.
frontend:
	cd ../web && npm ci && npm run build
	rm -rf internal/webui/dist
	cp -r ../web/dist internal/webui/dist

build:
	go build ./...

test:
	go test ./... -race

run:
	go run ./cmd/server
