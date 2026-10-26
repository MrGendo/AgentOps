package worker

// Handle 是执行器与平台交互的唯一入口：
// 步骤/尝试记录、经网关的工具调用、取消检查点。
// 执行器不得绕过 Handle 直接写表或直接执行受治理工具（架构文档 §4）。

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/agentops/platform/internal/events"
	"github.com/agentops/platform/internal/gateway"
)

// Handle 一次执行的生命周期上下文。
type Handle struct {
	m    *Manager
	snap *TaskSnapshot
	seq  int
}

// Task 返回任务只读快照。
func (h *Handle) Task() TaskSnapshot { return *h.snap }

// checkCancel 取消检查点（state-machine.md §3.2）：在步骤与工具调用边界检查。
func (h *Handle) checkCancel(ctx context.Context) error {
	var status string
	if err := h.m.pool.QueryRow(ctx,
		`SELECT status FROM tasks WHERE id=$1`, h.snap.ID).Scan(&status); err != nil {
		return err
	}
	if status == "CANCEL_REQUESTED" {
		return ErrCancelled
	}
	return nil
}

// RunStep 执行并记录一个普通步骤（非工具调用）。
func (h *Handle) RunStep(ctx context.Context, name, stepType string,
	fn func(ctx context.Context) (any, error)) error {
	if err := h.checkCancel(ctx); err != nil {
		return err
	}
	stepID, attemptID := h.beginStep(ctx, name, stepType, nil)
	out, err := fn(ctx)
	h.finishStep(ctx, stepID, attemptID, out, err)
	return err
}

// CallTool 经网关调用工具并完整记录步骤、尝试与调用审计。
// 同一次尝试内同工具调用带稳定幂等键（task:attempt:tool），网关可安全重放。
func (h *Handle) CallTool(ctx context.Context, tool string, params gateway.Params) (*gateway.ToolResult, error) {
	if err := h.checkCancel(ctx); err != nil {
		return nil, err
	}
	stepID, attemptID := h.beginStep(ctx, tool, "tool", params)

	res, err := h.m.gw.Invoke(ctx, gateway.InvokeRequest{
		TaskID:         h.snap.ID,
		StepID:         &stepID,
		ToolName:       tool,
		Params:         params,
		IdempotencyKey: fmt.Sprintf("%s:%d:%s", h.snap.ID, h.snap.Attempt, tool),
	})
	if err != nil {
		h.finishStep(ctx, stepID, attemptID, nil, err)
		return nil, err
	}

	stepErr := error(nil)
	if res.Status != gateway.StatusSucceeded {
		stepErr = fmt.Errorf("工具 %s %s: %s", tool, res.Status, res.ErrorMessage)
	}
	h.finishStep(ctx, stepID, attemptID, res.Result, stepErr)
	return res, nil
}

// beginStep 建步骤与首次尝试记录（RUNNING），同事务写 step.started 事件。
// 失败时返回空串，调用方以空串跳过后续更新（不影响主流程落终态）。
func (h *Handle) beginStep(ctx context.Context, name, stepType string, input any) (stepID, attemptID string) {
	h.seq++
	digest := []byte("{}")
	if input != nil {
		digest, _ = json.Marshal(input)
	}
	tx, err := h.m.pool.Begin(ctx)
	if err != nil {
		return "", ""
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, `
		WITH s AS (
			INSERT INTO task_steps (task_id, seq, name, step_type, status, input_digest, attempt_count, started_at)
			VALUES ($1::uuid, $2, $3, $4, 'RUNNING', $5, 1, now())
			RETURNING id
		), a AS (
			INSERT INTO step_attempts (step_id, task_id, attempt_no, status, input_digest, started_at)
			SELECT id, $1::uuid, 1, 'RUNNING', $5, now() FROM s
			RETURNING id
		)
		SELECT (SELECT id::text FROM s), (SELECT id::text FROM a)`,
		h.snap.ID, h.seq, name, stepType, digest).Scan(&stepID, &attemptID)
	if err != nil {
		return "", ""
	}
	if err := events.Emit(ctx, tx, events.Event{TaskID: h.snap.ID, TraceID: h.snap.TraceID,
		StepID: &stepID, AttemptID: &attemptID, Type: events.StepStarted,
		Payload: map[string]any{"name": name, "type": stepType}}); err != nil {
		return "", ""
	}
	if err := tx.Commit(ctx); err != nil {
		return "", ""
	}
	return stepID, attemptID
}

// finishStep 结束步骤与尝试（小结果内联，duration 由 SQL 从 started_at 推导）。
func (h *Handle) finishStep(ctx context.Context, stepID, attemptID string, out any, err error) {
	if stepID == "" || attemptID == "" {
		return
	}
	status := "SUCCEEDED"
	var class, errCode, errMsg any
	if err != nil {
		status = "FAILED"
		class, errCode, errMsg = "INTERNAL", "INTERNAL", err.Error()
	}

	// 小结果内联到 output_ref；阶段 B 不会出现超大产物
	var ref any
	if out != nil {
		if b, merr := json.Marshal(out); merr == nil && len(b) <= 4096 {
			ref = string(b)
		}
	}

	_, _ = h.m.pool.Exec(ctx,
		`UPDATE task_steps SET status=$2, output_ref=$3, error_code=$4, error_message=$5, finished_at=now()
		 WHERE id=$1::uuid`, stepID, status, ref, errCode, errMsg)
	_, _ = h.m.pool.Exec(ctx,
		`UPDATE step_attempts SET status=$2, output_ref=$3, error_class=$4, error_code=$5,
		   error_message=$6, finished_at=now(),
		   duration_ms = (EXTRACT(EPOCH FROM (now() - started_at)) * 1000)::int
		 WHERE id=$1::uuid`, attemptID, status, ref, class, errCode, errMsg)
	_ = events.Emit(ctx, h.m.pool, events.Event{TaskID: h.snap.ID, TraceID: h.snap.TraceID,
		StepID: &stepID, AttemptID: &attemptID, Type: events.StepFinished,
		Payload: map[string]any{"status": status}})
}
