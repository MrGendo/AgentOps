//go:build integration

// 网关集成测试（真实 PG）：权限拒绝、参数拒绝、未注册拒绝、
// 超时未知（R7）、幂等重放、审计关联（§16.3）。
package gateway

import (
	"context"
	"testing"

	"github.com/agentops/platform/internal/task"
	"github.com/agentops/platform/internal/testutil"
)

type env struct {
	gw    *Gateway
	tasks *task.Store
}

func setup(t *testing.T) *env {
	st := testutil.NewStore(t)
	testutil.Truncate(t, st)
	tk := task.NewStore(st.Pool())
	tools, err := DefaultTools(st.Pool(), "../../knowledge")
	if err != nil {
		t.Fatal(err)
	}
	return &env{gw: New(st.Pool(), tools...), tasks: tk}
}

func (e *env) newTask(t *testing.T, tools ...string) *task.Task {
	t.Helper()
	in := task.CreateInput{
		TaskType:     "mock.tools",
		Input:        []byte(`{}`),
		AllowedTools: tools,
	}
	tk, _, err := e.tasks.Create(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func callCount(t *testing.T, e *env, taskID string) int {
	t.Helper()
	var n int
	if err := e.gw.taskPool().QueryRow(context.Background(),
		`SELECT count(*) FROM tool_calls WHERE task_id=$1`, taskID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// 合法调用成功且审计可关联任务。
func TestInvokeSucceedsAndAudits(t *testing.T) {
	e := setup(t)
	tk := e.newTask(t, "calculator")
	res, err := e.gw.Invoke(context.Background(), InvokeRequest{
		TaskID: tk.ID, ToolName: "calculator",
		Params: Params{"expression": "2*(3+4)"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusSucceeded {
		t.Fatalf("状态: %s %s", res.Status, res.ErrorMessage)
	}
	if res.Result.(map[string]any)["value"].(float64) != 14 {
		t.Fatalf("求值错误: %v", res.Result)
	}
	if n := callCount(t, e, tk.ID); n != 1 {
		t.Fatalf("应落 1 条审计，实际 %d", n)
	}
}

// §16.3：未注册工具被拒。
func TestInvokeUnknownTool(t *testing.T) {
	e := setup(t)
	tk := e.newTask(t, "calculator")
	if _, err := e.gw.Invoke(context.Background(), InvokeRequest{
		TaskID: tk.ID, ToolName: "shell", Params: Params{},
	}); err != ErrUnknownTool {
		t.Fatalf("未注册工具应返回 ErrUnknownTool: %v", err)
	}
}

// §16.3：任务未授权的工具被拒，且留 REJECTED 审计与事件。
func TestInvokePermissionDenied(t *testing.T) {
	e := setup(t)
	tk := e.newTask(t, "calculator") // 未授权 knowledge_search
	res, err := e.gw.Invoke(context.Background(), InvokeRequest{
		TaskID: tk.ID, ToolName: "knowledge_search",
		Params: Params{"query": "慢查询"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusRejected || res.ErrorCode != "PERMISSION_DENIED" {
		t.Fatalf("应 REJECTED/PERMISSION_DENIED: %+v", res)
	}
	var status string
	_ = e.gw.taskPool().QueryRow(context.Background(),
		`SELECT status FROM tool_calls WHERE task_id=$1 ORDER BY created_at DESC LIMIT 1`, tk.ID).Scan(&status)
	if status != "REJECTED" {
		t.Fatalf("拒绝应落审计，实际状态 %s", status)
	}
}

// §16.3：参数格式错误被拒（含注入尝试）。
func TestInvokeInvalidParams(t *testing.T) {
	e := setup(t)
	tk := e.newTask(t, "calculator")
	res, err := e.gw.Invoke(context.Background(), InvokeRequest{
		TaskID: tk.ID, ToolName: "calculator",
		Params: Params{"expression": "1+2; rm -rf /"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusRejected || res.ErrorCode != "ARGUMENT_INVALID" {
		t.Fatalf("应 REJECTED/ARGUMENT_INVALID: %+v", res)
	}
}

// R7：外部超时 → TIMEOUT_UNKNOWN 落库留痕（不自动重试由 worker 层测试覆盖）。
func TestR7TimeoutUnknown(t *testing.T) {
	e := setup(t)
	tk := e.newTask(t, "mock_http")
	res, err := e.gw.Invoke(context.Background(), InvokeRequest{
		TaskID: tk.ID, ToolName: "mock_http",
		Params: Params{"mode": "timeout", "delay_ms": 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusTimeoutUnk {
		t.Fatalf("超时应为 TIMEOUT_UNKNOWN: %+v", res)
	}
	var status string
	_ = e.gw.taskPool().QueryRow(context.Background(),
		`SELECT status FROM tool_calls WHERE task_id=$1`, tk.ID).Scan(&status)
	if status != "TIMEOUT_UNKNOWN" {
		t.Fatalf("审计状态应为 TIMEOUT_UNKNOWN: %s", status)
	}
}

// 幂等键：重复调用重放历史结果，不产生第二条审计。
func TestIdempotencyReplay(t *testing.T) {
	e := setup(t)
	tk := e.newTask(t, "calculator")
	ctx := context.Background()
	req := InvokeRequest{
		TaskID: tk.ID, ToolName: "calculator",
		Params: Params{"expression": "1+1"}, IdempotencyKey: "replay-1",
	}
	first, err := e.gw.Invoke(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.gw.Invoke(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || second.CallID != first.CallID {
		t.Fatalf("重放应返回同一调用: %+v vs %+v", first, second)
	}
	if n := callCount(t, e, tk.ID); n != 1 {
		t.Fatalf("幂等重放不应新增审计，实际 %d 条", n)
	}
}
