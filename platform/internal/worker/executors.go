package worker

// 内置 Mock 执行器：驱动阶段 B 验收与可靠性测试，不依赖 Python 与真实模型（决策 D-B1）。

import (
	"context"
	"encoding/json"
	"time"

	"github.com/agentops/platform/internal/gateway"
	"github.com/agentops/platform/internal/retry"
)

// EchoExecutor 单步成功回显输入：冒烟与演示。
type EchoExecutor struct{}

// TaskTypes 声明可执行的任务类型。
func (EchoExecutor) TaskTypes() []string { return []string{"mock.echo"} }

// Execute 执行逻辑。
func (EchoExecutor) Execute(ctx context.Context, h *Handle) error {
	return h.RunStep(ctx, "echo", "mock", func(ctx context.Context) (any, error) {
		return map[string]any{"echo": json.RawMessage(h.Task().Input)}, nil
	})
}

// FailExecutor 按参数注入失败：params = {fail_times, error_class}。
// attempt_count <= fail_times 时返回指定类别错误，驱动 R5/R6。
type FailExecutor struct{}

// TaskTypes 声明可执行的任务类型。
func (FailExecutor) TaskTypes() []string { return []string{"mock.fail"} }

// Execute 执行逻辑。
func (FailExecutor) Execute(ctx context.Context, h *Handle) error {
	var p struct {
		FailTimes  int    `json:"fail_times"`
		ErrorClass string `json:"error_class"`
	}
	_ = json.Unmarshal(h.Task().Params, &p)
	class := retry.Class(p.ErrorClass)
	if class == "" {
		class = retry.TransientNetwork
	}
	return h.RunStep(ctx, "fail", "mock", func(ctx context.Context) (any, error) {
		if h.Task().Attempt <= p.FailTimes {
			return nil, &ExecError{Class: class, Code: "INJECTED", Msg: "注入失败（模拟）"}
		}
		return map[string]any{"recovered_after": p.FailTimes}, nil
	})
}

// SleepExecutor 睡眠指定秒数后成功：驱动任务超时（R4）。
type SleepExecutor struct{}

// TaskTypes 声明可执行的任务类型。
func (SleepExecutor) TaskTypes() []string { return []string{"mock.sleep"} }

// Execute 执行逻辑。
func (SleepExecutor) Execute(ctx context.Context, h *Handle) error {
	var p struct {
		Seconds float64 `json:"seconds"`
	}
	_ = json.Unmarshal(h.Task().Params, &p)
	return h.RunStep(ctx, "sleep", "mock", func(ctx context.Context) (any, error) {
		if p.Seconds <= 0 {
			p.Seconds = 1
		}
		select {
		case <-time.After(time.Duration(p.Seconds * float64(time.Second))):
			return map[string]any{"slept_seconds": p.Seconds}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
}

// ToolsExecutor 依次调用四个内置工具：网关联动与 trace 验收（L4 冒烟）。
type ToolsExecutor struct{}

// TaskTypes 声明可执行的任务类型。
func (ToolsExecutor) TaskTypes() []string { return []string{"mock.tools"} }

// Execute 执行逻辑。
func (ToolsExecutor) Execute(ctx context.Context, h *Handle) error {
	if _, err := h.CallTool(ctx, "calculator", gateway.Params{
		"expression": "2*(3+4)",
	}); err != nil {
		return err
	}
	if _, err := h.CallTool(ctx, "knowledge_search", gateway.Params{
		"query": "服务响应变慢 慢查询", "top_k": 2,
	}); err != nil {
		return err
	}
	if _, err := h.CallTool(ctx, "task_status", gateway.Params{
		"task_id": h.Task().ID,
	}); err != nil {
		return err
	}
	if _, err := h.CallTool(ctx, "mock_http", gateway.Params{
		"mode": "success", "delay_ms": 10, "body": map[string]any{"demo": true},
	}); err != nil {
		return err
	}
	return nil
}

// DefaultExecutors 返回全部内置执行器。
func DefaultExecutors() []Executor {
	return []Executor{EchoExecutor{}, FailExecutor{}, SleepExecutor{}, ToolsExecutor{}}
}
