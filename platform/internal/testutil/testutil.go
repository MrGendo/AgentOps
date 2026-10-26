//go:build integration

// Package testutil 提供集成测试的数据库环境（agentops_test 库 + 全量清理）。
package testutil

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/agentops/platform/internal/store"
)

// DSN 测试库连接串，可用环境变量覆盖。
func DSN() string {
	if v := os.Getenv("AGENTOPS_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://localhost:5432/agentops_test?sslmode=disable"
}

// NewStore 建立到测试库的连接；失败即终止测试。
func NewStore(t *testing.T) *store.Store {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := store.New(ctx, DSN())
	if err != nil {
		t.Fatalf("连接测试库失败（先运行 make migrate-up AGENTOPS_DATABASE_URL=%s）: %v", DSN(), err)
	}
	t.Cleanup(st.Close)
	return st
}

// Truncate 清空全部核心表（含事件序列重置），每个测试开始时调用。
func Truncate(t *testing.T, st *store.Store) {
	t.Helper()
	_, err := st.Pool().Exec(context.Background(),
		`TRUNCATE tasks, task_steps, step_attempts, tool_calls, task_events, agent_runs
		 RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("清理测试表失败: %v", err)
	}
}
