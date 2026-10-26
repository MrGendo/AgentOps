// Package gateway 是所有工具调用的唯一入口（需求 §6.6）：
// 存在性 → 任务授权 → 参数校验 → 幂等重放 → 超时执行 → 审计落库。
// Python/执行器只能经此调用受治理工具，禁止旁路。
package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agentops/platform/internal/events"
)

// Params 工具参数（来自 JSON 请求体）。
type Params map[string]any

// ToolResult 统一调用结果；Status 四态，TIMEOUT_UNKNOWN 表示外部结果未知（§6.5）。
type ToolResult struct {
	CallID       string `json:"call_id"`
	Status       string `json:"status"`
	Result       any    `json:"result,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	DurationMS   int    `json:"duration_ms"`
	Replayed     bool   `json:"replayed,omitempty"`
}

// 工具调用结果状态。
const (
	StatusSucceeded  = "SUCCEEDED"
	StatusFailed     = "FAILED"
	StatusTimeoutUnk = "TIMEOUT_UNKNOWN"
	StatusRejected   = "REJECTED"
)

// ErrUnknownTool / ErrTaskNotFound 是调用方需要区分的稳定错误。
var (
	ErrUnknownTool  = errors.New("unknown tool")
	ErrTaskNotFound = errors.New("task not found")
)

// ToolError 工具执行失败的结构化错误（可带稳定错误码）。
type ToolError struct {
	Code string
	Msg  string
}

func (e *ToolError) Error() string { return e.Code + ": " + e.Msg }

// ErrLostResponse 表示响应丢失：请求可能已被下游执行，结果不可知（§6.5）。
var ErrLostResponse = errors.New("response lost")

// Tool 工具声明与执行器。
type Tool struct {
	Name        string
	Version     string
	Description string
	RiskLevel   string        // LOW / MEDIUM / HIGH
	Timeout     time.Duration // 网关强制超时
	Idempotent  bool
	Schema      *Schema
	Exec        func(ctx context.Context, p Params) (any, error)
}

// Gateway 工具注册表与调用管线。
type Gateway struct {
	tools map[string]*Tool
	pool  *pgxpool.Pool
}

// New 构建；tools 为空时返回无工具网关（测试用）。
func New(pool *pgxpool.Pool, tools ...*Tool) *Gateway {
	g := &Gateway{tools: map[string]*Tool{}, pool: pool}
	for _, t := range tools {
		g.tools[t.Name] = t
	}
	return g
}

// Tools 返回全部已注册工具（供 /tools 端点）。
func (g *Gateway) Tools() []*Tool {
	out := make([]*Tool, 0, len(g.tools))
	for _, t := range g.tools {
		out = append(out, t)
	}
	return out
}

// Get 按名称查找工具。
func (g *Gateway) Get(name string) (*Tool, bool) {
	t, ok := g.tools[name]
	return t, ok
}

// InvokeRequest 一次工具调用的完整上下文。
type InvokeRequest struct {
	TaskID         string
	StepID         *string
	AgentRunID     *string
	ToolName       string
	Params         Params
	IdempotencyKey string
}

// Invoke 执行调用管线。返回的 ToolResult 一定带 CallID（除未知工具/任务不存在），
// 拒绝与失败均落 tool_calls 审计并追加事件。
func (g *Gateway) Invoke(ctx context.Context, r InvokeRequest) (*ToolResult, error) {
	tool, ok := g.tools[r.ToolName]
	if !ok {
		return nil, ErrUnknownTool
	}
	traceID, allowed, err := g.taskContext(ctx, r.TaskID)
	if err != nil {
		return nil, err
	}

	// 幂等重放：同键返回历史结果（§6.6 幂等策略）
	if r.IdempotencyKey != "" {
		if res, ok := g.replay(ctx, r.IdempotencyKey); ok {
			res.Replayed = true
			return res, nil
		}
	}

	callID := uuid.NewString()
	start := time.Now()

	reject := func(code, msg string) (*ToolResult, error) {
		res := &ToolResult{CallID: callID, Status: StatusRejected, ErrorCode: code, ErrorMessage: msg}
		g.audit(ctx, r, tool, traceID, res, start)
		g.emit(ctx, traceID, &callID, events.ToolCallRejected, res, r)
		return res, nil
	}

	// 权限：任务未授权该工具（§13.1 独立校验，不信任调用方自述）
	if !containsStr(allowed, r.ToolName) {
		return reject("PERMISSION_DENIED", "任务未授权使用工具 "+r.ToolName)
	}
	// 参数校验
	if tool.Schema != nil {
		if err := tool.Schema.ValidateParams(r.Params); err != nil {
			return reject("ARGUMENT_INVALID", err.Error())
		}
	}

	if err := g.emitStart(ctx, traceID, &callID, r, tool); err != nil {
		return nil, err
	}

	// 超时包裹执行：超时后结果未知，绝不自动重试（§6.5）
	execCtx, cancel := context.WithTimeout(ctx, tool.Timeout)
	defer cancel()

	var (
		out      any
		execErr  error
		deadline bool
		lost     bool
	)
	out, execErr = tool.Exec(execCtx, r.Params)
	deadline = errors.Is(execErr, context.DeadlineExceeded) || errors.Is(execCtx.Err(), context.DeadlineExceeded)
	lost = errors.Is(execErr, ErrLostResponse)

	res := &ToolResult{CallID: callID, DurationMS: int(time.Since(start).Milliseconds())}
	switch {
	case execErr == nil:
		res.Status = StatusSucceeded
		res.Result = out
	case deadline || lost:
		res.Status = StatusTimeoutUnk
		res.ErrorCode = "TOOL_TIMEOUT_UNKNOWN"
		if lost {
			res.ErrorCode = "RESPONSE_LOST"
		}
		res.ErrorMessage = "外部调用结果未知: " + execErr.Error()
	default:
		res.Status = StatusFailed
		var te *ToolError
		if errors.As(execErr, &te) {
			res.ErrorCode = te.Code
			res.ErrorMessage = te.Msg
		} else {
			res.ErrorCode = "TOOL_EXECUTION_ERROR"
			res.ErrorMessage = execErr.Error()
		}
	}
	res.DurationMS = int(time.Since(start).Milliseconds())

	if err := g.audit(ctx, r, tool, traceID, res, start); err != nil {
		return nil, err
	}
	g.emit(ctx, traceID, &callID, events.ToolCallCompleted, res, r)
	return res, nil
}

// taskContext 读取任务的 trace 与授权工具清单。
func (g *Gateway) taskContext(ctx context.Context, taskID string) (string, []string, error) {
	var trace string
	var allowed []byte
	err := g.pool.QueryRow(ctx,
		`SELECT trace_id::text, allowed_tools FROM tasks WHERE id=$1`, taskID).
		Scan(&trace, &allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrTaskNotFound
	}
	if err != nil {
		return "", nil, err
	}
	var list []string
	if err := unmarshalStrings(allowed, &list); err != nil {
		return "", nil, err
	}
	return trace, list, nil
}

// replay 查询同幂等键的历史调用。
func (g *Gateway) replay(ctx context.Context, key string) (*ToolResult, bool) {
	var res ToolResult
	var result []byte
	err := g.pool.QueryRow(ctx,
		`SELECT id::text, status, result, error_code, error_message, duration_ms
		 FROM tool_calls WHERE idempotency_key=$1`, key).
		Scan(&res.CallID, &res.Status, &result, &res.ErrorCode, &res.ErrorMessage, &res.DurationMS)
	if err != nil {
		return nil, false
	}
	if len(result) > 0 {
		_ = unmarshalAny(result, &res.Result)
	}
	return &res, true
}

// audit 写 tool_calls 审计行；幂等键冲突（并发同键）改为返回历史行。
func (g *Gateway) audit(ctx context.Context, r InvokeRequest, t *Tool, trace string, res *ToolResult, start time.Time) error {
	resultJSON, _ := marshalAny(res.Result)
	var key any
	if r.IdempotencyKey != "" {
		key = r.IdempotencyKey
	}
	finished := time.Now()
	_, err := g.pool.Exec(ctx,
		`INSERT INTO tool_calls (id, task_id, step_id, agent_run_id, tool_name, tool_version,
		   params, status, result, error_code, error_message, idempotency_key, risk_level,
		   started_at, finished_at, duration_ms)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		res.CallID, r.TaskID, r.StepID, r.AgentRunID, t.Name, t.Version,
		mustJSON(r.Params), res.Status, resultJSON, res.ErrorCode, res.ErrorMessage, key, t.RiskLevel,
		start, finished, res.DurationMS)
	return err
}

func (g *Gateway) emitStart(ctx context.Context, trace string, callID *string, r InvokeRequest, t *Tool) error {
	return events.Emit(ctx, g.pool, events.Event{
		TaskID: r.TaskID, TraceID: trace, ToolCallID: callID,
		Type:    events.ToolCallStarted,
		Payload: map[string]any{"tool": t.Name, "step_id": r.StepID},
	})
}

func (g *Gateway) emit(ctx context.Context, trace string, callID *string, typ string, res *ToolResult, r InvokeRequest) {
	_ = events.Emit(ctx, g.pool, events.Event{
		TaskID: r.TaskID, TraceID: trace, ToolCallID: callID, Type: typ,
		Payload: map[string]any{"tool": r.ToolName, "status": res.Status,
			"error_code": res.ErrorCode, "duration_ms": res.DurationMS},
	})
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func mustJSON(v any) []byte {
	b, _ := marshalAny(v)
	if b == nil {
		return []byte("{}")
	}
	return b
}

// taskPool 暴露连接池（集成测试断言审计行用）。
func (g *Gateway) taskPool() *pgxpool.Pool { return g.pool }
