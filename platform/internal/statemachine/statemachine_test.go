package statemachine

import "testing"

// 全矩阵穷举：不在此表的转换必须被拒绝。
func TestCanTransitionMatrix(t *testing.T) {
	legal := map[Status][]Status{
		Pending:         {Running, Cancelled},
		Running:         {CancelRequested, Succeeded, Failed, Cancelled, Pending},
		CancelRequested: {Succeeded, Failed, Cancelled},
		Succeeded:       {},
		Failed:          {Pending},
		Cancelled:       {Pending},
	}
	all := []Status{Pending, Running, CancelRequested, Succeeded, Failed, Cancelled}
	for _, from := range all {
		for _, to := range all {
			want := false
			for _, w := range legal[from] {
				if w == to {
					want = true
				}
			}
			if got := CanTransition(from, to); got != want {
				t.Fatalf("CanTransition(%s→%s)=%v, 期望 %v", from, to, got, want)
			}
		}
	}
}

// 终态识别与可重试终态。
func TestTerminalAndRetriable(t *testing.T) {
	for _, s := range []Status{Succeeded, Failed, Cancelled} {
		if !Terminal(s) {
			t.Fatalf("%s 应为终态", s)
		}
	}
	for _, s := range []Status{Pending, Running, CancelRequested} {
		if Terminal(s) {
			t.Fatalf("%s 不应为终态", s)
		}
	}
	if !Retriable(Failed) || !Retriable(Cancelled) || Retriable(Succeeded) {
		t.Fatal("可重试终态应为 FAILED/CANCELLED")
	}
}

// 竞争裁决三分支。
func TestResolveCompletion(t *testing.T) {
	cases := map[Status]CompletionOutcome{
		Running:         CompleteNormally,
		CancelRequested: CompleteOverCancel,
		Pending:         DiscardResult, // 已被恢复器重排
		Succeeded:       DiscardResult,
		Failed:          DiscardResult,
		Cancelled:       DiscardResult,
	}
	for cur, want := range cases {
		if got := ResolveCompletion(cur); got != want {
			t.Fatalf("ResolveCompletion(%s)=%v, 期望 %v", cur, got, want)
		}
	}
}
