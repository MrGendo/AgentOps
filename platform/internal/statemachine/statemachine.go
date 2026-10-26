// Package statemachine 以纯函数实现任务状态机的转换裁决（docs/state-machine.md）。
// 不做任何 IO；SQL 条件更新由 task/worker 层负责，本包只回答"允许吗、走哪条分支"。
package statemachine

// Status 任务状态（与 tasks.status CHECK 约束一致）。
type Status string

const (
	Pending         Status = "PENDING"
	Running         Status = "RUNNING"
	CancelRequested Status = "CANCEL_REQUESTED"
	Succeeded       Status = "SUCCEEDED"
	Failed          Status = "FAILED"
	Cancelled       Status = "CANCELLED"
)

// Terminal 判断是否终态。终态不可被普通更新覆盖，重试必须走显式 retry 流程。
func Terminal(s Status) bool {
	return s == Succeeded || s == Failed || s == Cancelled
}

// Retriable 终态中允许手动重试的状态。
func Retriable(s Status) bool {
	return s == Failed || s == Cancelled
}

// canGo 为合法转换矩阵（state-machine.md §2）。
// 键为当前状态，值为允许到达的状态集合。
var canGo = map[Status]map[Status]bool{
	Pending: {Running: true, Cancelled: true}, // 领取；未开始任务直接取消
	Running: { // 用户取消请求；三种结束；退避重排
		CancelRequested: true, Succeeded: true, Failed: true, Cancelled: true, Pending: true,
	},
	CancelRequested: { // 竞争裁决允许执行侧落终态；执行侧确认停止
		Succeeded: true, Failed: true, Cancelled: true,
	},
	Succeeded: {},
	Failed:    {Pending: true}, // 仅显式手动重试
	Cancelled: {Pending: true}, // 仅显式手动重试
}

// CanTransition 判断 from→to 是否为合法转换。
func CanTransition(from, to Status) bool {
	return canGo[from][to]
}

// CompletionOutcome 是执行器报告结果时的竞争裁决分支（state-machine.md §3.1）。
type CompletionOutcome int

const (
	// CompleteNormally 当前仍 RUNNING，正常落 SUCCEEDED。
	CompleteNormally CompletionOutcome = iota
	// CompleteOverCancel 已变为 CANCEL_REQUESTED：完成事实优先，落 SUCCEEDED 并打竞争标记。
	CompleteOverCancel
	// DiscardResult 任务已被接管/重试/终结，本次结果必须丢弃并留事件。
	DiscardResult
)

// ResolveCompletion 依据完成时刻的实时状态选择竞争分支。
func ResolveCompletion(current Status) CompletionOutcome {
	switch current {
	case Running:
		return CompleteNormally
	case CancelRequested:
		return CompleteOverCancel
	default: // PENDING（被恢复重排）或其余终态
		return DiscardResult
	}
}
