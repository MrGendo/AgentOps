package worker

// AgentExecutor：把 agent 任务下发到 Python Runtime 并轮询至终态（需求 §7.1）。
// Python 不可达/重启丢运行 → 归类可重试错误，由 Go 统一退避，任务不悬挂。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agentops/platform/internal/retry"
)

// AgentExecutor 通过 HTTP 驱动 Python /internal/v1/agent-runs。
type AgentExecutor struct {
	base         string
	token        string
	poll         time.Duration
	httpClient   *http.Client
	pool         *pgxpool.Pool
	agentVersion string
}

// NewAgentExecutor 构建执行器。
func NewAgentExecutor(baseURL, token string, poll time.Duration, pool *pgxpool.Pool) *AgentExecutor {
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	return &AgentExecutor{
		base: baseURL, token: token, poll: poll, pool: pool,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		agentVersion: "0.1.0",
	}
}

// TaskTypes 声明可执行的任务类型（演示场景 §3.2）。
func (a *AgentExecutor) TaskTypes() []string { return []string{"agent.ops_diagnosis", "swe.fix"} }

// agentRunDigest GET 端点返回的运行摘要（与 internal-v1.yaml 对齐）。
type agentRunDigest struct {
	ID           string          `json:"id"`
	TaskID       string          `json:"task_id"`
	Status       string          `json:"status"`
	FinishReason string          `json:"finish_reason"`
	FinalOutput  json.RawMessage `json:"final_output"`
	Plan         json.RawMessage `json:"plan"`
	Trajectory   struct {
		ModelCalls int `json:"model_calls"`
		ToolCalls  int `json:"tool_calls"`
		Iterations int `json:"iterations"`
	} `json:"trajectory_digest"`
	TokenUsage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"token_usage"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Execute 下发→轮询→落库 agent_runs；全程在 RunStep 内记录步骤。
func (a *AgentExecutor) Execute(ctx context.Context, h *Handle) error {
	task := h.Task()

	var goal map[string]any
	if err := json.Unmarshal(task.Input, &goal); err != nil {
		return &ExecError{Class: retry.ArgumentInvalid, Code: "INVALID_INPUT", Msg: "任务 input 不是对象"}
	}
	allowed, err := a.taskAllowedTools(ctx, task.ID)
	if err != nil {
		return &ExecError{Class: retry.Internal, Code: "DB_ERROR", Msg: err.Error()}
	}

	stepErr := h.RunStep(ctx, "agent-run", "agent", func(ctx context.Context) (any, error) {
		run, err := a.start(ctx, task, goal, allowed)
		if err != nil {
			return nil, err
		}
		digest, err := a.await(ctx, h, run.ID)
		if err != nil {
			return nil, err
		}
		a.persist(ctx, task.ID, digest, agentID(task))
		switch digest.Status {
		case "SUCCEEDED":
			return map[string]any{
				"finish_reason": digest.FinishReason,
				"tool_calls":    digest.Trajectory.ToolCalls,
				"total_tokens":  digest.TokenUsage.TotalTokens,
				"output":        digest.FinalOutput,
			}, nil
		case "CANCELLED":
			return nil, ErrCancelled
		default:
			class := retry.Internal
			if digest.Error != nil && digest.Error.Code == "RATE_LIMITED" {
				class = retry.ModelRateLimited // 唯一可重试的 Agent 侧失败
			}
			code := digest.FinishReason
			if code == "" {
				code = "AGENT_RUN_FAILED"
			}
			msg := code
			if digest.Error != nil {
				msg = digest.Error.Code + ": " + digest.Error.Message
			}
			return nil, &ExecError{Class: class, Code: code, Msg: msg}
		}
	})
	return stepErr
}

// start POST /internal/v1/agent-runs。
func (a *AgentExecutor) start(ctx context.Context, task TaskSnapshot, goal map[string]any, allowed []string) (*struct{ ID string }, error) {
	body := map[string]any{
		"task_id":       task.ID,
		"trace_id":      task.TraceID,
		"goal":          goal,
		"allowed_tools": allowed,
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.base+"/internal/v1/agent-runs", bytes.NewReader(raw))
	if err != nil {
		return nil, a.unavailable(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Token", a.token)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, a.unavailable(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return nil, a.unavailable(fmt.Errorf("agent-runs 受理失败: HTTP %d", resp.StatusCode))
	}
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Data.ID == "" {
		return nil, a.unavailable(err)
	}
	return &struct{ ID string }{ID: out.Data.ID}, nil
}

// await 轮询至终态；每轮先查取消检查点（长轮询期间也能响应取消）。
func (a *AgentExecutor) await(ctx context.Context, h *Handle, runID string) (*agentRunDigest, error) {
	for {
		if err := h.checkCancel(ctx); err != nil {
			a.cancelRun(ctx, runID)
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			a.base+"/internal/v1/agent-runs/"+runID, nil)
		if err != nil {
			return nil, a.unavailable(err)
		}
		req.Header.Set("X-Service-Token", a.token)
		resp, err := a.httpClient.Do(req)
		if err != nil {
			return nil, a.unavailable(err)
		}
		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			// Python 重启后运行丢失：按可重试处理，Go 重发整个运行
			return nil, a.unavailable(fmt.Errorf("agent run %s 不存在（服务可能已重启）", runID))
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, a.unavailable(fmt.Errorf("agent-runs 查询失败: HTTP %d", resp.StatusCode))
		}
		var envelope struct {
			Data agentRunDigest `json:"data"`
		}
		err = json.NewDecoder(resp.Body).Decode(&envelope)
		resp.Body.Close()
		if err != nil {
			return nil, a.unavailable(err)
		}
		switch envelope.Data.Status {
		case "SUCCEEDED", "FAILED", "CANCELLED":
			return &envelope.Data, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(a.poll):
		}
	}
}

// cancelRun 尽力通知 Python 取消（失败不影响本地取消路径）。
func (a *AgentExecutor) cancelRun(ctx context.Context, runID string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.base+"/internal/v1/agent-runs/"+runID+"/cancel", nil)
	if err != nil {
		return
	}
	req.Header.Set("X-Service-Token", a.token)
	if resp, err := a.httpClient.Do(req); err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// persist 将运行摘要落 agent_runs 表（架构 D-1：Python 不直连库，由 Go 写入）。
func (a *AgentExecutor) persist(ctx context.Context, taskID string, d *agentRunDigest, agentIDv string) {
	finalOut := string(d.FinalOutput)
	if finalOut == "" {
		finalOut = "{}"
	}
	usage, _ := json.Marshal(d.TokenUsage)
	var errCode, errMsg any
	if d.Error != nil {
		errCode, errMsg = d.Error.Code, d.Error.Message
	}
	_, _ = a.pool.Exec(ctx, `
		INSERT INTO agent_runs (id, task_id, agent_version, status, finish_reason,
		  plan, model_call_count, tool_call_count, iteration_count,
		  token_usage, final_output_ref, error_code, error_message, started_at, finished_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now(), now())
		ON CONFLICT (id) DO UPDATE SET
		  status=EXCLUDED.status, finish_reason=EXCLUDED.finish_reason,
		  plan=EXCLUDED.plan, model_call_count=EXCLUDED.model_call_count,
		  tool_call_count=EXCLUDED.tool_call_count, iteration_count=EXCLUDED.iteration_count,
		  token_usage=EXCLUDED.token_usage, final_output_ref=EXCLUDED.final_output_ref,
		  error_code=EXCLUDED.error_code, error_message=EXCLUDED.error_message,
		  finished_at=now()`,
		d.ID, taskID, agentIDv, d.Status, d.FinishReason,
		[]byte(orJSON(d.Plan)), d.Trajectory.ModelCalls, d.Trajectory.ToolCalls,
		d.Trajectory.Iterations, []byte(usage), finalOut, errCode, errMsg)
}

func (a *AgentExecutor) taskAllowedTools(ctx context.Context, taskID string) ([]string, error) {
	var raw []byte
	if err := a.pool.QueryRow(ctx,
		`SELECT allowed_tools FROM tasks WHERE id=$1`, taskID).Scan(&raw); err != nil {
		return nil, err
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// unavailable 统一归类为可重试的 Agent 服务错误。
func (a *AgentExecutor) unavailable(err error) error {
	return &ExecError{Class: retry.TransientNetwork, Code: "AGENT_SERVICE_UNAVAILABLE",
		Msg: fmt.Sprintf("Agent 服务不可用: %v", err)}
}

func orJSON(b json.RawMessage) string {
	if len(b) == 0 {
		return "[]"
	}
	return string(b)
}

// agentID 从任务输入取 agent 标识（swe 任务由前端/适配器写入），默认 miniswe。
func agentID(task TaskSnapshot) string {
	var g struct {
		Agent string `json:"agent"`
	}
	if json.Unmarshal(task.Input, &g) == nil && g.Agent != "" {
		return g.Agent
	}
	return "miniswe"
}
