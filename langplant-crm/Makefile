VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X langplant-crm/internal/server.Version=$(VERSION) -X langplant-crm/internal/node.Version=$(VERSION)

.PHONY: all web build test check dist docker-server docker-node clean dev

all: build

web:
	cd web && npm ci --no-audit --no-fund && npm run build

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/crm-server ./cmd/crm-server
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/crm-node ./cmd/crm-node

test:
	go vet ./...
	go test ./...

check: test
	cd web && npm run check

# Static binaries for the VPS (linux amd64/arm64) and the node (incl. windows/macos)
dist: web
	@for t in linux/amd64 linux/arm64; do \
		os=$${t%/*}; arch=$${t#*/}; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/crm-server-$$os-$$arch ./cmd/crm-server; \
	done
	@for t in linux/amd64 linux/arm64 windows/amd64 darwin/arm64 darwin/amd64; do \
		os=$${t%/*}; arch=$${t#*/}; ext=$$( [ $$os = windows ] && echo .exe ); \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/crm-node-$$os-$$arch$$ext ./cmd/crm-node; \
	done
	@ls -la dist

docker-server:
	docker build -f deploy/server/Dockerfile --build-arg VERSION=$(VERSION) -t langplant-crm-server:$(VERSION) -t langplant-crm-server:latest .

docker-node:
	docker build -f deploy/node/Dockerfile --build-arg VERSION=$(VERSION) -t langplant-crm-node:$(VERSION) -t langplant-crm-node:latest .

# Local development: API on :8080 (data in ./data), UI with hot reload on :5173
dev:
	@echo "terminal 1: CRM_NODE_TOKEN=dev-token-dev-token-dev-token go run ./cmd/crm-server"
	@echo "terminal 2: NODE_SERVER_URL=http://localhost:8080 NODE_TOKEN=dev-token-dev-token-dev-token go run ./cmd/crm-node"
	@echo "terminal 3: cd web && npm run dev   →  http://localhost:5173"

clean:
	rm -rf bin dist internal/webui/dist/assets internal/webui/dist/*.html internal/webui/dist/*.png internal/webui/dist/*.svg internal/webui/dist/*.webmanifest
