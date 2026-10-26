// Package worker 实现任务领取、租约心跳、执行与恢复（需求 §6.4）。
// 并发正确性全部由数据库保证：SKIP LOCKED 领取、lease_version fencing、条件更新。
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agentops/platform/internal/events"
	"github.com/agentops/platform/internal/gateway"
	"github.com/agentops/platform/internal/retry"
	"github.com/agentops/platform/internal/statemachine"
	"github.com/agentops/platform/internal/task"
)

// Config Worker 运行参数。
type Config struct {
	ID             string
	Concurrency    int
	PollInterval   time.Duration
	LeaseTTL       time.Duration
	RetryBase      time.Duration
	RetryMaxWait   time.Duration
	ReaperInterval time.Duration
}

// ExecError 执行器返回的分类错误；错误类别驱动重试决策（state-machine.md §5）。
type ExecError struct {
	Class retry.Class
	Code  string
	Msg   string
}

func (e *ExecError) Error() string { return string(e.Class) + "/" + e.Code + ": " + e.Msg }

// ErrCancelled 执行器主动确认停止（取消检查点命中）。
var ErrCancelled = errors.New("execution cancelled at checkpoint")

// Executor 任务执行器；阶段 B 为内置 Mock，阶段 C 增加 Agent 执行器。
type Executor interface {
	TaskTypes() []string
	Execute(ctx context.Context, h *Handle) error
}

// TaskSnapshot 领取到的任务上下文（传给执行器的只读视图）。
type TaskSnapshot struct {
	ID             string
	TraceID        string
	Type           string
	Input          json.RawMessage
	Params         json.RawMessage
	Attempt        int
	MaxAttempts    int
	TimeoutSeconds int
	LeaseVersion   int64
}

// Manager 管理领取循环、心跳与恢复。
type Manager struct {
	cfg   Config
	pool  *pgxpool.Pool
	gw    *gateway.Gateway
	execs map[string]Executor
	mu    sync.Mutex
	rng   *rand.Rand
}

// NewManager 构建执行管理器。
func NewManager(cfg Config, pool *pgxpool.Pool, gw *gateway.Gateway, executors ...Executor) *Manager {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 2
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = 30 * time.Second
	}
	if cfg.RetryBase <= 0 {
		cfg.RetryBase = 2 * time.Second
	}
	if cfg.RetryMaxWait <= 0 {
		cfg.RetryMaxWait = 60 * time.Second
	}
	if cfg.ReaperInterval <= 0 {
		cfg.ReaperInterval = 5 * time.Second
	}
	m := &Manager{cfg: cfg, pool: pool, gw: gw, execs: map[string]Executor{}, rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
	for _, e := range executors {
		for _, t := range e.TaskTypes() {
			m.execs[t] = e
		}
	}
	return m
}

// SupportedTypes 返回本 Manager 可领取的任务类型（领取 SQL 的过滤条件）。
func (m *Manager) SupportedTypes() []string {
	types := make([]string, 0, len(m.execs))
	for t := range m.execs {
		types = append(types, t)
	}
	return types
}

// Start 启动 N 个领取循环与恢复器；ctx 取消时全部退出。
func (m *Manager) Start(ctx context.Context) {
	go m.reaperLoop(ctx)
	for i := 0; i < m.cfg.Concurrency; i++ {
		go m.loop(ctx)
	}
}

func (m *Manager) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		claimed, err := m.RunOnce(ctx)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			time.Sleep(m.cfg.PollInterval) // 出错退避，避免打爆数据库
		}
		if !claimed {
			time.Sleep(m.cfg.PollInterval)
		}
	}
}

// Claim 原子领取一个到期任务（database.md §3.3）：
// FOR UPDATE SKIP LOCKED + 同事务置 RUNNING/租约，无重复领取窗口。
// 返回 pgx.ErrNoRows 表示当前无可领任务。
func (m *Manager) Claim(ctx context.Context) (*TaskSnapshot, error) {
	types := m.SupportedTypes()
	if len(types) == 0 {
		return nil, pgx.ErrNoRows
	}
	ttlMs := m.cfg.LeaseTTL.Milliseconds()

	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var snap TaskSnapshot
	err = tx.QueryRow(ctx, `
		WITH picked AS (
			SELECT id FROM tasks
			WHERE status='PENDING' AND scheduled_at <= now() AND task_type = ANY($1)
			ORDER BY created_at
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE tasks t SET
			status='RUNNING',
			attempt_count = t.attempt_count + 1,
			lease_version = t.lease_version + 1,
			lease_owner = $2,
			lease_expires_at = now() + ($3 * interval '1 millisecond'),
			started_at = COALESCE(t.started_at, now())
		FROM picked
		WHERE t.id = picked.id
		RETURNING t.id::text, t.trace_id::text, t.task_type, t.input, t.params,
		          t.attempt_count, t.max_attempts, t.timeout_seconds, t.lease_version`,
		types, m.cfg.ID, ttlMs,
	).Scan(&snap.ID, &snap.TraceID, &snap.Type, &snap.Input, &snap.Params,
		&snap.Attempt, &snap.MaxAttempts, &snap.TimeoutSeconds, &snap.LeaseVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, pgx.ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	if err := events.Emit(ctx, tx, events.Event{
		TaskID: snap.ID, TraceID: snap.TraceID, Type: events.TaskClaimed,
		Payload: map[string]any{"worker": m.cfg.ID, "attempt": snap.Attempt, "lease_version": snap.LeaseVersion},
	}); err != nil {
		return nil, err
	}
	return &snap, tx.Commit(ctx)
}

// RunOnce 领取并完整执行一个任务；返回是否领到任务。
func (m *Manager) RunOnce(ctx context.Context) (bool, error) {
	snap, err := m.Claim(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	m.execute(ctx, snap)
	return true, nil
}

// execute 执行一个已领取任务：心跳保活、超时控制、竞争裁决落终态。
func (m *Manager) execute(ctx context.Context, snap *TaskSnapshot) {
	exec, ok := m.execs[snap.Type]
	if !ok {
		// 不应发生：领取 SQL 已按类型过滤；防御性落失败
		m.failTask(ctx, snap, &ExecError{Class: retry.Internal, Code: "NO_EXECUTOR", Msg: "无可用执行器"})
		return
	}

	// 任务超时：执行侧硬性时长上限（R4：与租约超时语义分离）
	execCtx, cancelExec := context.WithTimeout(ctx, time.Duration(snap.TimeoutSeconds)*time.Second)
	defer cancelExec()

	hbCtx, cancelHb := context.WithCancel(context.Background())
	defer cancelHb()
	h := &Handle{m: m, snap: snap}

	// 心跳续租：丢失租约立即停止执行（fencing 兜底，即使本协程异常）
	lost := make(chan struct{}, 1)
	go m.heartbeat(hbCtx, snap, lost)

	execErr := exec.Execute(execCtx, h)
	cancelHb()

	// 取消/超时/失联的归类
	select {
	case <-lost:
		// 租约已丢失：结果注定被 fencing 拒绝，直接走完成路径让条件更新裁决
		if execErr == nil {
			execErr = &ExecError{Class: retry.LeaseExpired, Code: "LEASE_STOLEN", Msg: "租约已被接管"}
		}
	default:
	}
	if execErr == nil && execCtx.Err() != nil {
		execErr = &ExecError{Class: retry.TaskTimeout, Code: "TASK_TIMEOUT", Msg: "任务超过最大执行时长"}
	}
	if errors.Is(execErr, context.DeadlineExceeded) {
		execErr = &ExecError{Class: retry.TaskTimeout, Code: "TASK_TIMEOUT", Msg: "任务超过最大执行时长"}
	}
	if errors.Is(execErr, ErrCancelled) {
		execErr = &ExecError{Class: retry.Cancelled, Code: "CANCELLED", Msg: "执行侧确认停止"}
	}

	if execErr == nil {
		m.completeTask(ctx, snap)
		return
	}
	var ee *ExecError
	if !errors.As(execErr, &ee) {
		ee = &ExecError{Class: retry.Internal, Code: "INTERNAL", Msg: execErr.Error()}
	}
	if ee.Class == retry.Cancelled {
		m.cancelTask(ctx, snap)
		return
	}
	m.failTask(ctx, snap, ee)
}

// heartbeat 周期续租；续不上（被接管/已终态）时通知执行侧尽快停止。
func (m *Manager) heartbeat(ctx context.Context, snap *TaskSnapshot, lost chan struct{}) {
	interval := m.cfg.LeaseTTL / 3
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	ttlMs := m.cfg.LeaseTTL.Milliseconds()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tag, err := m.pool.Exec(ctx,
				`UPDATE tasks SET lease_expires_at = now() + ($3 * interval '1 millisecond')
				 WHERE id=$1 AND lease_owner=$4 AND lease_version=$2
				   AND status IN ('RUNNING','CANCEL_REQUESTED')`,
				snap.ID, snap.LeaseVersion, ttlMs, m.cfg.ID)
			if err != nil || tag.RowsAffected() == 0 {
				select { // 非阻塞通知
				case lost <- struct{}{}:
				default:
				}
				return
			}
		}
	}
}

// currentStatus 读取实时状态（竞争裁决依据）。
func (m *Manager) currentStatus(ctx context.Context, id string) (statemachine.Status, error) {
	var s string
	err := m.pool.QueryRow(ctx, `SELECT status FROM tasks WHERE id=$1`, id).Scan(&s)
	return statemachine.Status(s), err
}

// completeTask 成功完成路径（state-machine.md §3.1 三分支）。
func (m *Manager) completeTask(ctx context.Context, snap *TaskSnapshot) {
	cur, err := m.currentStatus(ctx, snap.ID)
	if err != nil {
		return
	}
	switch statemachine.ResolveCompletion(cur) {
	case statemachine.CompleteNormally:
		tag, err := m.pool.Exec(ctx,
			`UPDATE tasks SET status='SUCCEEDED', finished_at=now(), finish_reason='succeeded'
			 WHERE id=$1 AND status='RUNNING' AND lease_version=$2`, snap.ID, snap.LeaseVersion)
		if err != nil || tag.RowsAffected() == 0 {
			m.discardResult(ctx, snap, "concurrent_state_change")
			return
		}
		_ = events.Emit(ctx, m.pool, events.Event{TaskID: snap.ID, TraceID: snap.TraceID,
			Type: events.TaskSucceeded, Payload: map[string]any{"attempt": snap.Attempt}})
	case statemachine.CompleteOverCancel:
		tag, err := m.pool.Exec(ctx,
			`UPDATE tasks SET status='SUCCEEDED', finished_at=now(), finish_reason='succeeded_after_cancel_request'
			 WHERE id=$1 AND status='CANCEL_REQUESTED' AND lease_version=$2`, snap.ID, snap.LeaseVersion)
		if err != nil || tag.RowsAffected() == 0 {
			m.discardResult(ctx, snap, "concurrent_state_change")
			return
		}
		_ = events.Emit(ctx, m.pool, events.Event{TaskID: snap.ID, TraceID: snap.TraceID,
			Type:    events.TaskSucceeded,
			Payload: map[string]any{"attempt": snap.Attempt, "cancel_race": true}})
	default:
		m.discardResult(ctx, snap, string(cur))
	}
}

// cancelTask 执行侧确认停止：落 CANCELLED。
func (m *Manager) cancelTask(ctx context.Context, snap *TaskSnapshot) {
	tag, _ := m.pool.Exec(ctx,
		`UPDATE tasks SET status='CANCELLED', finished_at=now(), finish_reason='cancelled',
		   lease_owner=NULL, lease_expires_at=NULL
		 WHERE id=$1 AND status IN ('RUNNING','CANCEL_REQUESTED') AND lease_version=$2`,
		snap.ID, snap.LeaseVersion)
	if tag.RowsAffected() > 0 {
		_ = events.Emit(ctx, m.pool, events.Event{TaskID: snap.ID, TraceID: snap.TraceID,
			Type: events.TaskCancelled, Payload: map[string]any{"by": "worker_checkpoint", "attempt": snap.Attempt}})
	}
}

// failTask 失败路径：可重试则退避重排 PENDING，否则落 FAILED。
func (m *Manager) failTask(ctx context.Context, snap *TaskSnapshot, ee *ExecError) {
	if retry.Retryable(ee.Class) && snap.Attempt < snap.MaxAttempts {
		m.mu.Lock()
		wait := retry.Backoff(snap.Attempt, m.cfg.RetryBase, m.cfg.RetryMaxWait, m.rng)
		m.mu.Unlock()
		tag, err := m.pool.Exec(ctx,
			`UPDATE tasks SET status='PENDING',
			   scheduled_at = now() + ($3 * interval '1 millisecond'),
			   lease_owner=NULL, lease_expires_at=NULL, lease_version=lease_version+1,
			   error_code=$4, error_message=$5
			 WHERE id=$1 AND status IN ('RUNNING','CANCEL_REQUESTED') AND lease_version=$2`,
			snap.ID, snap.LeaseVersion, wait.Milliseconds(), string(ee.Class), ee.Msg)
		if err != nil || tag.RowsAffected() == 0 {
			m.discardResult(ctx, snap, "retry_race")
			return
		}
		_ = events.Emit(ctx, m.pool, events.Event{TaskID: snap.ID, TraceID: snap.TraceID,
			Type: events.TaskRescheduled,
			Payload: map[string]any{"attempt": snap.Attempt, "error_class": string(ee.Class),
				"wait_ms": wait.Milliseconds()}})
		return
	}

	reason := "failed"
	switch ee.Class {
	case retry.TaskTimeout:
		reason = "task_timeout"
	case retry.TimeoutUnknown:
		reason = "timeout_unknown"
	}
	tag, err := m.pool.Exec(ctx,
		`UPDATE tasks SET status='FAILED', finished_at=now(),
		   error_code=$3, error_message=$4, finish_reason=$5,
		   lease_owner=NULL, lease_expires_at=NULL
		 WHERE id=$1 AND status IN ('RUNNING','CANCEL_REQUESTED') AND lease_version=$2`,
		snap.ID, snap.LeaseVersion, string(ee.Class), ee.Msg, reason)
	if err != nil || tag.RowsAffected() == 0 {
		m.discardResult(ctx, snap, "fail_race")
		return
	}
	_ = events.Emit(ctx, m.pool, events.Event{TaskID: snap.ID, TraceID: snap.TraceID,
		Type: events.TaskFailed,
		Payload: map[string]any{"attempt": snap.Attempt, "error_class": string(ee.Class),
			"error_code": ee.Code, "finish_reason": reason}})
}

// discardResult 结果被丢弃（任务已被接管/重排/终结）；不改状态，只留证据。
func (m *Manager) discardResult(ctx context.Context, snap *TaskSnapshot, why string) {
	_ = events.Emit(ctx, m.pool, events.Event{TaskID: snap.ID, TraceID: snap.TraceID,
		Type: events.ResultDiscarded,
		Payload: map[string]any{"attempt": snap.Attempt, "reason": why,
			"lease_version": snap.LeaseVersion}})
}

// taskStore 简化对 task.Store 的依赖注入（trace 查询用）。
var _ = task.ErrNotFound // 保持包引用（trace 聚合在 httpapi 实现）

// Pool 暴露连接池（集成测试断言用）。
func (m *Manager) Pool() *pgxpool.Pool { return m.pool }

// PoolExec 测试辅助：暴露受限的池执行。
func (m *Manager) PoolExec(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := m.pool.Exec(ctx, sql, args...)
	return tag.RowsAffected(), err
}
