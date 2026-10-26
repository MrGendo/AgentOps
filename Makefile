# AgentOps 本地开发命令（README 与本文件保持一致）
.PHONY: build test migrate-up migrate-down migrate-status test-py build-console compose-up compose-down demo metrics bench test-integration

build:
	cd platform && go build ./...

test:
	cd platform && go test ./...

# 集成测试：串行执行包（-p 1），共享 agentops_test 库的测试不得并行
test-integration:
	cd platform && go test -tags integration -p 1 -count=1 -timeout 180s ./internal/...

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

demo:
	bash scripts/demo.sh

metrics:
	bash scripts/metrics.sh

bench:
	bash scripts/bench.sh
