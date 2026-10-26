package retry

import (
	"math/rand"
	"testing"
	"time"
)

// 类别可重试性必须与 state-machine.md §5 表一致。
func TestRetryableTable(t *testing.T) {
	retryable := []Class{ModelRateLimited, TransientNetwork, ToolFailedClean, LeaseExpired}
	noRetry := []Class{ArgumentInvalid, PermissionDenied, BusinessRejected, TaskTimeout, TimeoutUnknown, Cancelled, Internal}
	for _, c := range retryable {
		if !Retryable(c) {
			t.Fatalf("%s 应可重试", c)
		}
	}
	for _, c := range noRetry {
		if Retryable(c) {
			t.Fatalf("%s 不应可重试", c)
		}
	}
}

// 退避必须指数增长、含抖动、且被上限截断。
func TestBackoff(t *testing.T) {
	base := 2 * time.Second
	maxWait := 60 * time.Second
	r := rand.New(rand.NewSource(42))

	// 下界：attempt>=1 时至少 base
	if w := Backoff(0, base, maxWait, r); w < base {
		t.Fatalf("attempt=0 退避 %v 不应小于 base %v", w, base)
	}
	// 上界：任何情况不超过 maxWait
	for a := 1; a <= 20; a++ {
		if w := Backoff(a, base, maxWait, nil); w > maxWait {
			t.Fatalf("attempt=%d 退避 %v 超过上限", a, w)
		}
	}
	// 指数性：无抖动时 2 的幂次
	if w := Backoff(1, base, maxWait, nil); w != base {
		t.Fatalf("attempt=1 无抖动退避 = %v, 期望 %v", w, base)
	}
	if w := Backoff(3, base, maxWait, nil); w != 8*time.Second {
		t.Fatalf("attempt=3 无抖动退避 = %v, 期望 8s", w)
	}
	// 截断：attempt 很大时恰为 maxWait（无抖动）
	if w := Backoff(10, base, maxWait, nil); w != maxWait {
		t.Fatalf("attempt=10 退避 = %v, 期望上限 %v", w, maxWait)
	}
}
