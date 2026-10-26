// Package retry 定义错误分类、可重试性与指数退避（state-machine.md §5）。
package retry

import (
	"math"
	"math/rand"
	"time"
)

// Class 错误类别。可重试性由此表统一裁决，禁止在调用点散落判断。
type Class string

const (
	ArgumentInvalid  Class = "ARGUMENT_INVALID"   // 参数错误：不重试
	PermissionDenied Class = "PERMISSION_DENIED"  // 权限拒绝：不重试
	BusinessRejected Class = "BUSINESS_REJECTED"  // 业务规则拒绝：默认不重试
	ModelRateLimited Class = "MODEL_RATE_LIMITED" // 模型限流：退避重试
	TransientNetwork Class = "TRANSIENT_NETWORK"  // 临时网络错误：退避重试
	ToolFailedClean  Class = "TOOL_FAILED_CLEAN"  // 可确认未执行的工具失败：按策略重试
	LeaseExpired     Class = "LEASE_EXPIRED"      // Worker 崩溃/失联：租约过期后恢复
	TaskTimeout      Class = "TASK_TIMEOUT"       // 任务业务时长上限：不自动重试
	TimeoutUnknown   Class = "TIMEOUT_UNKNOWN"    // 外部结果未知：禁止自动重试（§6.5）
	Cancelled        Class = "CANCELLED"          // 取消：停止调度
	Internal         Class = "INTERNAL"           // 兜底：不重试
)

// Retryable 判断类别是否允许自动重试。
func Retryable(c Class) bool {
	switch c {
	case ModelRateLimited, TransientNetwork, ToolFailedClean, LeaseExpired:
		return true
	default:
		return false
	}
}

// Backoff 指数退避加抖动：wait = min(base*2^attempt + rand(0,base), max)。
// attempt 为已失败次数（从 1 起），确保首次退避 >= base。
func Backoff(attempt int, base, maxWait time.Duration, r *rand.Rand) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := float64(attempt - 1)
	raw := float64(base) * math.Pow(2, shift)
	if raw > float64(maxWait) {
		raw = float64(maxWait)
	}
	jitter := time.Duration(0)
	if r != nil && base > 0 {
		jitter = time.Duration(r.Int63n(int64(base)))
	}
	wait := time.Duration(raw) + jitter
	if wait > maxWait {
		wait = maxWait
	}
	return wait
}
