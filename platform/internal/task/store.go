package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agentops/platform/internal/events"
	"github.com/agentops/platform/internal/statemachine"
)

// Store 任务表的数据访问；所有状态变更与事件写入保持同事务。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 构建任务存储。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func scanTask(row pgx.Row) (*Task, error) {
	var t Task
	var allowed []byte
	err := row.Scan(&t.ID, &t.TaskType, &t.Input, &t.Params, &allowed, &t.Status, &t.IdempotencyKey,
		&t.TraceID, &t.AttemptCount, &t.MaxAttempts, &t.TimeoutSeconds,
		&t.ErrorCode, &t.ErrorMessage, &t.FinishReason, &t.CreatedAt, &t.StartedAt, &t.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(allowed, &t.AllowedTools); err != nil {
		return nil, err
	}
	return &t, nil
}

// Pool 暴露连接池（集成测试断言与 httpapi 只读查询用）。
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Create 幂等创建（database.md §3.1）：同幂等键重复提交返回已存在任务；
// 同键不同内容返回 ErrIdempotencyConflict。
func (s *Store) Create(ctx context.Context, in CreateInput) (*Task, bool, error) {
	if in.Params == nil {
		in.Params = json.RawMessage(`{}`)
	}
	if in.AllowedTools == nil {
		in.AllowedTools = []string{}
	}
	allowed, _ := json.Marshal(in.AllowedTools)
	maxAttempts, timeout := in.MaxAttempts, in.TimeoutSeconds
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	if timeout <= 0 {
		timeout = 600
	}

	row := s.pool.QueryRow(ctx,
		`INSERT INTO tasks (task_type, input, params, allowed_tools, idempotency_key, max_attempts, timeout_seconds)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (idempotency_key) DO NOTHING
		 RETURNING `+cols,
		in.TaskType, in.Input, in.Params, allowed, nullIfEmpty(in.IdempotencyKey), maxAttempts, timeout)

	t, err := scanTask(row)
	if err == nil {
		if err := events.Emit(ctx, s.pool, events.Event{
			TaskID: t.ID, TraceID: t.TraceID, Type: events.TaskCreated,
			Payload: map[string]any{"task_type": t.TaskType, "attempt": 1},
		}); err != nil {
			return nil, false, err
		}
		return t, true, nil
	}
	if !errors.Is(err, ErrNotFound) { // scanTask 用 ErrNotFound 表达 0 行
		return nil, false, err
	}
	// 0 行：命中幂等键唯一约束
	if in.IdempotencyKey == "" {
		return nil, false, fmt.Errorf("insert returned no rows without idempotency key: %w", err)
	}
	existing, gerr := s.GetByIdempotencyKey(ctx, in.IdempotencyKey)
	if gerr != nil {
		return nil, false, gerr
	}
	if existing.TaskType != in.TaskType || !jsonEqual(existing.Input, in.Input) {
		return nil, false, ErrIdempotencyConflict
	}
	return existing, false, nil
}

// Get 按 ID 查询。
func (s *Store) Get(ctx context.Context, id string) (*Task, error) {
	return scanTask(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM tasks WHERE id=$1`, id))
}

// GetByIdempotencyKey 按幂等键查询。
func (s *Store) GetByIdempotencyKey(ctx context.Context, key string) (*Task, error) {
	return scanTask(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM tasks WHERE idempotency_key=$1`, key))
}

// ListFilter 列表过滤与游标分页（api-contract.md §1.4）。
type ListFilter struct {
	Status      string
	TaskType    string
	CreatedFrom *time.Time
	CreatedTo   *time.Time
	Limit       int
	Cursor      string // "c_<RFC3339Nano>_<uuid>"
}

// List 按创建时间倒序分页；返回本页数据与下一页游标。
func (s *Store) List(ctx context.Context, f ListFilter) ([]*Task, string, int, error) {
	where := []string{"TRUE"}
	args := []any{} // 仅 WHERE 条件的参数；LIMIT 单独占位，避免 count 查询误用
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }

	if f.Status != "" {
		where = append(where, "status = "+arg(f.Status))
	}
	if f.TaskType != "" {
		where = append(where, "task_type = "+arg(f.TaskType))
	}
	if f.CreatedFrom != nil {
		where = append(where, "created_at >= "+arg(*f.CreatedFrom))
	}
	if f.CreatedTo != nil {
		where = append(where, "created_at <= "+arg(*f.CreatedTo))
	}
	if f.Cursor != "" {
		ts, id, err := parseCursor(f.Cursor)
		if err != nil {
			return nil, "", 0, err
		}
		where = append(where, "(created_at, id) < ("+arg(ts)+", "+arg(id)+")")
	}
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	whereSQL := strings.Join(where, " AND ")
	limitPlaceholder := fmt.Sprintf("$%d", len(args)+1)

	rows, err := s.pool.Query(ctx,
		`SELECT `+cols+` FROM tasks WHERE `+whereSQL+`
		 ORDER BY created_at DESC, id DESC LIMIT `+limitPlaceholder, append(append([]any{}, args...), limit+1)...)
	if err != nil {
		return nil, "", 0, err
	}
	defer rows.Close()

	var list []*Task
	for rows.Next() {
		var t Task
		var allowed []byte
		if err := rows.Scan(&t.ID, &t.TaskType, &t.Input, &t.Params, &allowed, &t.Status, &t.IdempotencyKey,
			&t.TraceID, &t.AttemptCount, &t.MaxAttempts, &t.TimeoutSeconds,
			&t.ErrorCode, &t.ErrorMessage, &t.FinishReason, &t.CreatedAt, &t.StartedAt, &t.FinishedAt); err != nil {
			return nil, "", 0, err
		}
		if err := json.Unmarshal(allowed, &t.AllowedTools); err != nil {
			return nil, "", 0, err
		}
		list = append(list, &t)
	}
	if rows.Err() != nil {
		return nil, "", 0, rows.Err()
	}

	next := ""
	if len(list) > limit {
		list = list[:limit]
		last := list[len(list)-1]
		next = makeCursor(last.CreatedAt, last.ID)
	}

	var total int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM tasks WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, "", 0, err
	}
	return list, next, total, nil
}

// Cancel 请求取消（state-machine.md §2 规则 3）：
// PENDING 直接取消；RUNNING 转取消请求；CANCEL_REQUESTED 幂等返回；终态报错。
// 行级锁（FOR UPDATE）串行化并发取消与完成竞争，分支内再按状态机裁决。
func (s *Store) Cancel(ctx context.Context, id string) (*Task, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	t, err := scanTask(tx.QueryRow(ctx, `SELECT `+cols+` FROM tasks WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return nil, err
	}

	cur := statemachine.Status(t.Status)
	switch {
	case cur == statemachine.Pending:
		_, err = tx.Exec(ctx,
			`UPDATE tasks SET status='CANCELLED', finished_at=now(), finish_reason='cancelled_before_start', lease_version=lease_version+1
			 WHERE id=$1`, id)
		if err != nil {
			return nil, err
		}
		err = events.Emit(ctx, tx, events.Event{TaskID: t.ID, TraceID: t.TraceID,
			Type: events.TaskCancelled, Payload: map[string]any{"phase": "before_start"}})
	case cur == statemachine.Running:
		_, err = tx.Exec(ctx, `UPDATE tasks SET status='CANCEL_REQUESTED' WHERE id=$1`, id)
		if err != nil {
			return nil, err
		}
		err = events.Emit(ctx, tx, events.Event{TaskID: t.ID, TraceID: t.TraceID,
			Type: events.TaskCancelRequested, Payload: map[string]any{}})
	case cur == statemachine.CancelRequested:
		// 幂等：取消请求已受理，直接返回当前状态
	default:
		return nil, ErrAlreadyFinished
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Retry 手动重试（state-machine.md §2 规则 2）：仅 FAILED/CANCELLED；
// 状态回 PENDING、attempt 历史保留，写 task.retry_requested 事件。
func (s *Store) Retry(ctx context.Context, id string) (*Task, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	t, err := scanTask(tx.QueryRow(ctx, `SELECT `+cols+` FROM tasks WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return nil, err
	}
	if !statemachine.Retriable(statemachine.Status(t.Status)) {
		return nil, ErrNotRetriable
	}

	_, err = tx.Exec(ctx,
		`UPDATE tasks SET status='PENDING', scheduled_at=now(),
		 error_code=NULL, error_message=NULL, finish_reason=NULL,
		 lease_owner=NULL, lease_expires_at=NULL, lease_version=lease_version+1
		 WHERE id=$1`, id)
	if err != nil {
		return nil, err
	}
	if err := events.Emit(ctx, tx, events.Event{TaskID: t.ID, TraceID: t.TraceID,
		Type:    events.TaskRetryRequested,
		Payload: map[string]any{"previous_status": t.Status, "attempt": t.AttemptCount}}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// --- 游标编解码 ---

func makeCursor(ts time.Time, id string) string {
	return "c_" + ts.UTC().Format(time.RFC3339Nano) + "_" + id
}

func parseCursor(c string) (time.Time, string, error) {
	if len(c) < 2 || c[0:2] != "c_" {
		return time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	rest := c[2:]
	i := strings.LastIndex(rest, "_")
	if i < 0 {
		return time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	ts, err := time.Parse(time.RFC3339Nano, rest[:i])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor: %w", err)
	}
	return ts, rest[i+1:], nil
}

// --- 小工具 ---

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func jsonEqual(a, b json.RawMessage) bool {
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		return false
	}
	return fmt.Sprintf("%v", va) == fmt.Sprintf("%v", vb)
}
