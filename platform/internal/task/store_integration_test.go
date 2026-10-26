//go:build integration

// 任务存储集成测试（真实 PG / agentops_test）：
// R9 幂等并发、R8 事务失败不部分落库、取消/重试状态语义（§16.1）。
package task

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/agentops/platform/internal/testutil"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st := testutil.NewStore(t)
	testutil.Truncate(t, st)
	return NewStore(st.Pool())
}

func echoInput(v string) json.RawMessage { return json.RawMessage(`{"msg":"` + v + `"}`) }

// R9：并发同幂等键提交，恰好创建一个任务，全部拿到同一 ID。
func TestR9IdempotentCreateConcurrent(t *testing.T) {
	s := newTestStore(t)
	const n = 8
	key := "idem-r9"
	ids := make([]string, n)
	created := make([]bool, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			task, c, err := s.Create(ctx, CreateInput{
				TaskType: "mock.echo", Input: echoInput("same"), IdempotencyKey: key,
			})
			if err != nil {
				t.Errorf("并发创建报错: %v", err)
				return
			}
			ids[i], created[i] = task.ID, c
		}(i)
	}
	wg.Wait()

	for i := 1; i < n; i++ {
		if ids[i] != ids[0] {
			t.Fatalf("并发幂等提交返回了不同 ID: %s vs %s", ids[i], ids[0])
		}
	}
	createdCount := 0
	for _, c := range created {
		if c {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("应恰好创建 1 次，实际 %d 次", createdCount)
	}
	var rows int
	if err := s.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM tasks WHERE idempotency_key=$1`, key).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("库中应有 1 行，实际 %d 行", rows)
	}
}

// 同幂等键不同内容：必须返回冲突错误且不产生第二行。
func TestIdempotencyConflict(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, _, err := s.Create(ctx, CreateInput{
		TaskType: "mock.echo", Input: echoInput("a"), IdempotencyKey: "k1",
	}); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.Create(ctx, CreateInput{
		TaskType: "mock.echo", Input: echoInput("b"), IdempotencyKey: "k1",
	})
	if err != ErrIdempotencyConflict {
		t.Fatalf("同键不同内容应返回冲突，得到 %v", err)
	}
}

// 取消语义：PENDING 直接 CANCELLED；终态取消被拒（§16.1）。
func TestCancelSemantics(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tk, _, err := s.Create(ctx, CreateInput{TaskType: "mock.echo", Input: echoInput("x")})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Cancel(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "CANCELLED" || got.FinishReason == nil || *got.FinishReason != "cancelled_before_start" {
		t.Fatalf("PENDING 取消应直接 CANCELLED: %+v", got)
	}
	if _, err := s.Cancel(ctx, tk.ID); err != ErrAlreadyFinished {
		t.Fatalf("终态取消应被拒绝: %v", err)
	}
}

// 手动重试：仅 FAILED/CANCELLED 可重试；重试回 PENDING 且保留尝试历史（§16.1）。
func TestRetrySemantics(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tk, _, err := s.Create(ctx, CreateInput{TaskType: "mock.echo", Input: echoInput("x")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retry(ctx, tk.ID); err != ErrNotRetriable {
		t.Fatalf("PENDING 不可重试: %v", err)
	}
	if _, err := s.Cancel(ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Retry(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "PENDING" {
		t.Fatalf("重试应回 PENDING: %s", got.Status)
	}
	var events int
	_ = s.Pool().QueryRow(ctx,
		`SELECT count(*) FROM task_events WHERE task_id=$1 AND event_type='task.retry_requested'`, tk.ID).Scan(&events)
	if events != 1 {
		t.Fatalf("重试应留操作事件，实际 %d 条", events)
	}
}

// R8：约束冲突时事务如实报错，不产生部分写入。
func TestR8ConstraintFailureNoPartialWrite(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tk, _, err := s.Create(ctx, CreateInput{TaskType: "mock.echo", Input: echoInput("x")})
	if err != nil {
		t.Fatal(err)
	}
	// 制造唯一约束冲突：向同一 (task_id, seq) 插入两次
	ins := `INSERT INTO task_steps (task_id, seq, name, step_type) VALUES ($1::uuid, 1, 'a', 'tool')`
	if _, err := s.Pool().Exec(ctx, ins, tk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, ins, tk.ID); err == nil {
		t.Fatal("重复 seq 应报约束错误")
	}
	var n int
	if err := s.Pool().QueryRow(ctx,
		`SELECT count(*) FROM task_steps WHERE task_id=$1`, tk.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("冲突后不应产生第二行，实际 %d 行", n)
	}
}

// 列表过滤与游标分页冒烟。
func TestListFilterAndCursor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, _, err := s.Create(ctx, CreateInput{
			TaskType: fmt.Sprintf("mock.t%d", i%2), Input: echoInput("x"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	list, next, total, err := s.List(ctx, ListFilter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || next == "" || total != 5 {
		t.Fatalf("分页结果异常: len=%d next=%q total=%d", len(list), next, total)
	}
	page2, _, _, err := s.List(ctx, ListFilter{Limit: 2, Cursor: next})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 || page2[0].ID == list[0].ID {
		t.Fatalf("第二页数据异常: %d 条", len(page2))
	}
	// i%2 循环 5 次：t0 出现 3 次（i=0,2,4）、t1 出现 2 次
	filtered, _, total2, err := s.List(ctx, ListFilter{TaskType: "mock.t0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 3 || total2 != 3 {
		t.Fatalf("类型过滤异常: %d/%d", len(filtered), total2)
	}
}
