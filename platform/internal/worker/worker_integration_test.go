//go:build integration

// Worker 可靠性集成测试（真实 PG / agentops_test）：覆盖 testing-strategy.md §2
// R1 并发领取、R2 崩溃恢复、R3 fencing、R4 双超时区分、R5/R6 重试边界、
// R10 取消竞争、L4 mock.tools 全链路冒烟。
package worker

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agentops/platform/internal/gateway"
	"github.com/agentops/platform/internal/task"
	"github.com/agentops/platform/internal/testutil"
)

// fastCfg 毫秒级参数，保证测试快速且仍覆盖真实时序。
func fastCfg() Config {
	return Config{
		ID: "test-worker", Concurrency: 4, PollInterval: 20 * time.Millisecond,
		LeaseTTL: 400 * time.Millisecond, RetryBase: 50 * time.Millisecond,
		RetryMaxWait: 200 * time.Millisecond, ReaperInterval: 50 * time.Millisecond,
	}
}

func setup(t *testing.T) (*Manager, *task.Store) {
	st := testutil.NewStore(t)
	testutil.Truncate(t, st)
	tools, err := gateway.DefaultTools(st.Pool(), "../../knowledge")
	if err != nil {
		t.Fatal(err)
	}
	gw := gateway.New(st.Pool(), tools...)
	m := NewManager(fastCfg(), st.Pool(), gw, DefaultExecutors()...)
	return m, task.NewStore(st.Pool())
}

// create 建任务的快捷方式。
func create(t *testing.T, ts *task.Store, taskType string, params map[string]any,
	allowed []string, maxAttempts, timeoutSec int) *task.Task {
	t.Helper()
	var p json.RawMessage
	if params != nil {
		p, _ = json.Marshal(params)
	}
	in := task.CreateInput{
		TaskType: taskType, Input: []byte(`{"goal":"测试"}`), Params: p,
		AllowedTools: allowed, MaxAttempts: maxAttempts, TimeoutSeconds: timeoutSec,
	}
	tk, _, err := ts.Create(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

// waitStatus 轮询等待任务到达期望状态。
func waitStatus(t *testing.T, ts *task.Store, id, want string, timeout time.Duration) *task.Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tk, err := ts.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if tk.Status == want {
			return tk
		}
		time.Sleep(20 * time.Millisecond)
	}
	tk, _ := ts.Get(context.Background(), id)
	t.Fatalf("等待状态 %s 超时，当前 %s（finish=%v err=%v）", want, tk.Status, tk.FinishReason, tk.ErrorCode)
	return nil
}

func eventCount(t *testing.T, pool *pgxpool.Pool, taskID, typ string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM task_events WHERE task_id=$1 AND event_type=$2`, taskID, typ).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// R1：8 goroutine 并发领取 16 个任务，每个任务恰好被领取一次。
func TestR1ConcurrentClaimNoDuplicate(t *testing.T) {
	m, ts := setup(t)
	ctx := context.Background()
	const total = 16
	ids := make([]string, total)
	for i := 0; i < total; i++ {
		tk := create(t, ts, "mock.echo", nil, nil, 3, 10)
		ids[i] = tk.ID
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				claimed, err := m.RunOnce(ctx)
				if err != nil {
					t.Errorf("RunOnce: %v", err)
					return
				}
				if !claimed {
					return
				}
			}
		}()
	}
	wg.Wait()

	for _, id := range ids {
		tk := waitStatus(t, ts, id, "SUCCEEDED", 3*time.Second)
		if tk.AttemptCount != 1 {
			t.Fatalf("任务 %s 应恰好 1 次尝试，实际 %d", id, tk.AttemptCount)
		}
		if n := eventCount(t, m.Pool(), id, "task.claimed"); n != 1 {
			t.Fatalf("任务 %s 应恰好 1 条领取事件，实际 %d", id, n)
		}
	}
}

// R2：Worker 失联（租约过期）后任务被回收并重新执行。
func TestR2LeaseExpiryRecovery(t *testing.T) {
	m, ts := setup(t)
	ctx := context.Background()
	tk := create(t, ts, "mock.echo", nil, nil, 3, 10)

	snap, err := m.Claim(ctx) // 模拟 Worker 领取后立即崩溃（无心跳、无完成）
	if err != nil || snap.ID != tk.ID {
		t.Fatalf("领取失败: %v", err)
	}
	// 直接把租约置为过期（等价于心跳停止超过 TTL）
	if _, err := m.PoolExec(ctx,
		`UPDATE tasks SET lease_expires_at = now() - interval '1 millisecond' WHERE id=$1`, tk.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.ReapOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := ts.Get(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "PENDING" {
		t.Fatalf("过期后应重排 PENDING，实际 %s", got.Status)
	}
	if n := eventCount(t, m.Pool(), tk.ID, "lease.revoked"); n != 1 {
		t.Fatalf("应留 lease.revoked 事件，实际 %d", n)
	}
	// 新 Worker 正常接管并完成
	if _, err := m.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, ts, tk.ID, "SUCCEEDED", 3*time.Second)
}

// R3：旧租约 Worker 无法覆盖新状态（fencing）。
func TestR3FencingStaleWorkerCannotWrite(t *testing.T) {
	m, ts := setup(t)
	ctx := context.Background()
	tk := create(t, ts, "mock.echo", nil, nil, 3, 10)

	snap, err := m.Claim(ctx) // 旧 Worker 持 v
	if err != nil {
		t.Fatal(err)
	}
	// 模拟任务已被接管：lease_version 被推进（reaper/新领取都会 +1）
	if _, err := m.PoolExec(ctx,
		`UPDATE tasks SET lease_version = lease_version + 1, lease_owner='newcomer' WHERE id=$1`, tk.ID); err != nil {
		t.Fatal(err)
	}
	// 旧 Worker 迟到的成功上报：应被条件更新拒绝并丢弃结果
	m.completeTask(ctx, snap)

	got, err := ts.Get(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == "SUCCEEDED" && got.FinishReason != nil && *got.FinishReason == "succeeded" {
		t.Fatal("旧租约不得写入 SUCCEEDED")
	}
	if n := eventCount(t, m.Pool(), tk.ID, "task.result_discarded"); n != 1 {
		t.Fatalf("迟到结果应留丢弃事件，实际 %d", n)
	}
}

// R4：任务超时与租约超时是两种明确区分的失败。
func TestR4TaskTimeoutVsLeaseTimeout(t *testing.T) {
	m, ts := setup(t)
	ctx := context.Background()

	// 4a 任务超时：业务时长上限触发
	tk1 := create(t, ts, "mock.sleep", map[string]any{"seconds": 5}, nil, 3, 1)
	if _, err := m.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got1 := waitStatus(t, ts, tk1.ID, "FAILED", 5*time.Second)
	if got1.ErrorCode == nil || *got1.ErrorCode != "TASK_TIMEOUT" {
		t.Fatalf("任务超时应为 TASK_TIMEOUT，实际 %v", got1.ErrorCode)
	}
	if got1.FinishReason == nil || *got1.FinishReason != "task_timeout" {
		t.Fatalf("finish_reason 应为 task_timeout: %v", got1.FinishReason)
	}

	// 4b 租约超时：Worker 失联且重试耗尽 → LEASE_EXPIRED
	tk2 := create(t, ts, "mock.echo", nil, nil, 1, 10)
	if _, err := m.Claim(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PoolExec(ctx,
		`UPDATE tasks SET lease_expires_at = now() - interval '1 millisecond' WHERE id=$1`, tk2.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.ReapOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got2 := waitStatus(t, ts, tk2.ID, "FAILED", 3*time.Second)
	if got2.ErrorCode == nil || *got2.ErrorCode != "LEASE_EXPIRED" {
		t.Fatalf("租约超时应为 LEASE_EXPIRED，实际 %v", got2.ErrorCode)
	}
	if got1.ErrorCode != nil && got2.ErrorCode != nil && *got1.ErrorCode == *got2.ErrorCode {
		t.Fatal("两种超时不得混淆")
	}
}

// R5：可重试错误遵守退避与次数上限后成功。
func TestR5RetryWithBackoffEventuallySucceeds(t *testing.T) {
	m, ts := setup(t)
	ctx := context.Background()
	// 失败 2 次后第 3 次成功
	tk := create(t, ts, "mock.fail",
		map[string]any{"fail_times": 2, "error_class": "TRANSIENT_NETWORK"}, nil, 3, 10)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		claimed, err := m.RunOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := ts.Get(ctx, tk.ID)
		if got.Status == "SUCCEEDED" || got.Status == "FAILED" {
			break
		}
		if !claimed { // 处于退避等待期
			time.Sleep(30 * time.Millisecond)
		}
	}
	got := waitStatus(t, ts, tk.ID, "SUCCEEDED", time.Second)
	if got.AttemptCount != 3 {
		t.Fatalf("应第 3 次尝试成功，实际 %d", got.AttemptCount)
	}
	if n := eventCount(t, m.Pool(), tk.ID, "task.retry_scheduled"); n != 2 {
		t.Fatalf("应留 2 条重排事件，实际 %d", n)
	}
}

// R6：不可重试错误一次即失败，绝不无限重试。
func TestR6NonRetryableFailsFast(t *testing.T) {
	m, ts := setup(t)
	ctx := context.Background()
	tk := create(t, ts, "mock.fail",
		map[string]any{"fail_times": 5, "error_class": "ARGUMENT_INVALID"}, nil, 3, 10)

	if _, err := m.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got := waitStatus(t, ts, tk.ID, "FAILED", 3*time.Second)
	if got.AttemptCount != 1 {
		t.Fatalf("不可重试错误应 1 次尝试即失败，实际 %d", got.AttemptCount)
	}
	if n := eventCount(t, m.Pool(), tk.ID, "task.retry_scheduled"); n != 0 {
		t.Fatalf("不应有重排事件，实际 %d", n)
	}
}

// R10：取消与完成的并发竞争——恰一个终态、事件可重建、无状态损坏。
func TestR10CancelCompleteRace(t *testing.T) {
	m, ts := setup(t)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		tk := create(t, ts, "mock.echo", nil, nil, 3, 10)
		snap, err := m.Claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); m.completeTask(ctx, snap) }()
		go func() {
			defer wg.Done()
			_, _ = ts.Cancel(ctx, tk.ID) // 终态后取消返回 AlreadyFinished 是合法结局
		}()
		wg.Wait()

		got, err := ts.Get(ctx, tk.ID)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case got.Status == "SUCCEEDED":
			if got.FinishReason == nil ||
				(*got.FinishReason != "succeeded" && *got.FinishReason != "succeeded_after_cancel_request") {
				t.Fatalf("竞争成功侧 finish_reason 异常: %v", got.FinishReason)
			}
		case got.Status == "CANCEL_REQUESTED":
			// 取消先落、完成方走了 OverCancel 或 discard——再补一步收尾验证可恢复
			m.completeTask(ctx, snap)
			got2, _ := ts.Get(ctx, tk.ID)
			if got2.Status != "SUCCEEDED" {
				t.Fatalf("CANCEL_REQUESTED 上的迟到完成应可裁决，实际 %s", got2.Status)
			}
		default:
			t.Fatalf("竞争后出现非法状态: %s", got.Status)
		}
	}
}

// 取消检查点：CANCEL_REQUESTED 的任务在步骤边界被确认停止。
func TestCancelCheckpoint(t *testing.T) {
	m, ts := setup(t)
	ctx := context.Background()
	tk := create(t, ts, "mock.echo", nil, nil, 3, 10)

	snap, err := m.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Cancel(ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	// 执行器在检查点发现取消 → 走确认停止路径
	h := &Handle{m: m, snap: snap}
	if err := (EchoExecutor{}).Execute(ctx, h); err != ErrCancelled {
		t.Fatalf("检查点应返回 ErrCancelled: %v", err)
	}
	m.cancelTask(ctx, snap)
	got := waitStatus(t, ts, tk.ID, "CANCELLED", 3*time.Second)
	if got.FinishReason == nil || *got.FinishReason != "cancelled" {
		t.Fatalf("finish_reason 应为 cancelled: %v", got.FinishReason)
	}
}

// L4：mock.tools 全链路——四工具、步骤、审计、事件完整可查。
func TestL4ToolsTaskEndToEnd(t *testing.T) {
	m, ts := setup(t)
	ctx := context.Background()
	tools := []string{"calculator", "knowledge_search", "task_status", "mock_http"}
	tk := create(t, ts, "mock.tools", nil, tools, 3, 10)

	if _, err := m.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, ts, tk.ID, "SUCCEEDED", 5*time.Second)

	var stepN, callN, startedN, doneN int
	pool := m.Pool()
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM task_steps WHERE task_id=$1 AND status='SUCCEEDED'`, tk.ID).Scan(&stepN)
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM tool_calls WHERE task_id=$1 AND status='SUCCEEDED'`, tk.ID).Scan(&callN)
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM task_events WHERE task_id=$1 AND event_type='tool.call.started'`, tk.ID).Scan(&startedN)
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM task_events WHERE task_id=$1 AND event_type='tool.call.completed'`, tk.ID).Scan(&doneN)
	if stepN != 4 || callN != 4 || startedN != 4 || doneN != 4 {
		t.Fatalf("全链路计数异常: steps=%d calls=%d started=%d done=%d", stepN, callN, startedN, doneN)
	}
	// 轨迹时间线可按 seq 全序重建
	var seqOK bool
	_ = pool.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM (
		    SELECT seq, lag(seq) OVER (ORDER BY seq) AS prev
		    FROM task_events WHERE task_id=$1
		  ) x WHERE prev IS NOT NULL AND seq <= prev
		) = false`, tk.ID).Scan(&seqOK)
	if !seqOK {
		t.Fatal("事件 seq 必须严格递增（可重建时间线）")
	}
}
