package worker

// reaper 周期回收过期租约：Worker 崩溃后的任务恢复（需求 §6.4、§6.5）。
// 全部走条件更新（lease_version 递增），与在跑 Worker、其他 reaper 天然互斥。

import (
	"context"
	"time"

	"github.com/agentops/platform/internal/events"
)

func (m *Manager) reaperLoop(ctx context.Context) {
	t := time.NewTicker(m.cfg.ReaperInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = m.ReapOnce(ctx)
		}
	}
}

// ReapOnce 执行一轮回收：RUNNING 过期→重排或失败；CANCEL_REQUESTED 过期→CANCELLED。
func (m *Manager) ReapOnce(ctx context.Context) error {
	// ① RUNNING 且租约过期：Worker 已失联
	rows, err := m.pool.Query(ctx, `
		UPDATE tasks SET
		  status = CASE WHEN attempt_count < max_attempts THEN 'PENDING' ELSE 'FAILED' END,
		  scheduled_at = now(),
		  lease_version = lease_version + 1,
		  lease_owner = NULL, lease_expires_at = NULL,
		  finished_at = CASE WHEN attempt_count < max_attempts THEN NULL ELSE now() END,
		  finish_reason = CASE WHEN attempt_count < max_attempts THEN NULL ELSE 'lease_expired' END,
		  error_code = CASE WHEN attempt_count < max_attempts THEN NULL ELSE 'LEASE_EXPIRED' END
		WHERE status='RUNNING' AND lease_expires_at IS NOT NULL AND lease_expires_at < now()
		RETURNING id::text, trace_id::text, attempt_count,
		          CASE WHEN attempt_count < max_attempts THEN 'PENDING' ELSE 'FAILED' END AS next`)
	if err != nil {
		return err
	}
	type reaped struct {
		id, trace, next string
		attempt         int
	}
	var list []reaped
	for rows.Next() {
		var r reaped
		if err := rows.Scan(&r.id, &r.trace, &r.attempt, &r.next); err != nil {
			rows.Close()
			return err
		}
		list = append(list, r)
	}
	rows.Close()

	for _, r := range list {
		_ = events.Emit(ctx, m.pool, events.Event{TaskID: r.id, TraceID: r.trace,
			Type:    events.LeaseRevoked,
			Payload: map[string]any{"attempt": r.attempt, "worker": m.cfg.ID}})
		if r.next == "PENDING" {
			_ = events.Emit(ctx, m.pool, events.Event{TaskID: r.id, TraceID: r.trace,
				Type:    events.TaskRescheduled,
				Payload: map[string]any{"attempt": r.attempt, "reason": "lease_expired"}})
		} else {
			_ = events.Emit(ctx, m.pool, events.Event{TaskID: r.id, TraceID: r.trace,
				Type: events.TaskFailed,
				Payload: map[string]any{"attempt": r.attempt, "error_class": "LEASE_EXPIRED",
					"finish_reason": "lease_expired"}})
		}
	}

	// ② CANCEL_REQUESTED 且租约过期：执行侧已失联，恢复流程直接确认取消（§3.2）
	rows2, err := m.pool.Query(ctx, `
		UPDATE tasks SET status='CANCELLED', finished_at=now(),
		  finish_reason='cancelled_after_lease_expiry',
		  lease_version = lease_version + 1, lease_owner=NULL, lease_expires_at=NULL
		WHERE status='CANCEL_REQUESTED' AND lease_expires_at IS NOT NULL AND lease_expires_at < now()
		RETURNING id::text, trace_id::text`)
	if err != nil {
		return err
	}
	defer rows2.Close()
	for rows2.Next() {
		var id, trace string
		if err := rows2.Scan(&id, &trace); err != nil {
			return err
		}
		_ = events.Emit(ctx, m.pool, events.Event{TaskID: id, TraceID: trace,
			Type:    events.TaskCancelled,
			Payload: map[string]any{"by": "reaper", "reason": "lease_expired"}})
	}
	return nil
}
