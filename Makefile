# AgentOps 本地开发命令（README 与本文件保持一致）
.PHONY: build test migrate-up migrate-down migrate-status test-py build-console compose-up compose-down

build:
	cd platform && go build ./...

test:
	cd platform && go test ./...

migrate-up:
	cd platform && go run ./cmd/migrate -up

migrate-down:
	cd platform && go run ./cmd/migrate -down 1

migrate-status:
	cd platform && go run ./cmd/migrate -status

test-py:
	cd agent && .venv/bin/pytest

build-console:
	cd console && npm run build

compose-up:
	cd deploy && docker compose up -d

compose-down:
	cd deploy && docker compose down
