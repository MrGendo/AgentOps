// Package events 负责任务事件追加写（append-only，永不更新，docs/database.md §2.5）。
// 排序以 seq bigserial 为准；时间戳仅用于展示。
package events

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgconn"
)

// 事件类型常量（§6.7）。新增类型只增不改。
const (
	TaskCreated         = "task.created"
	TaskClaimed         = "task.claimed"
	TaskCancelRequested = "task.cancel_requested"
	TaskCancelled       = "task.cancelled"
	TaskSucceeded       = "task.succeeded"
	TaskFailed          = "task.failed"
	TaskRetryRequested  = "task.retry_requested"
	TaskRescheduled     = "task.retry_scheduled"
	LeaseRevoked        = "lease.revoked"
	ResultDiscarded     = "task.result_discarded"
	TaskTimeoutHit      = "task.timeout"
	StepStarted         = "step.started"
	StepFinished        = "step.finished"
	ToolCallStarted     = "tool.call.started"
	ToolCallCompleted   = "tool.call.completed"
	ToolCallRejected    = "tool.call.rejected"
)

// Execer 是事务与连接池共同满足的最小执行接口，
// 使事件写入可以嵌进调用方的事务里（与状态变更原子提交）。
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Event 一条事件的结构化内容。
type Event struct {
	TaskID     string // 必填
	TraceID    string // 必填
	StepID     *string
	AttemptID  *string
	ToolCallID *string
	Type       string
	Payload    map[string]any
}

// Emit 在给定执行器（事务或池）中追加一条事件。
func Emit(ctx context.Context, q Execer, e Event) error {
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return err
	}
	if e.Payload == nil {
		payload = []byte("{}")
	}
	_, err = q.Exec(ctx,
		`INSERT INTO task_events (task_id, trace_id, step_id, attempt_id, tool_call_id, event_type, payload)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		e.TaskID, e.TraceID, e.StepID, e.AttemptID, e.ToolCallID, e.Type, payload,
	)
	return err
}
