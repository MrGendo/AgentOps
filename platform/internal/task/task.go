// Package task 实现任务管理：幂等创建、查询、取消、重试（需求 §6.1）。
package task

import (
	"encoding/json"
	"errors"
	"time"
)

// ErrNotFound 任务不存在；ErrAlreadyFinished 终态不可取消；ErrNotRetriable 终态不可重试；
// ErrIdempotencyConflict 同幂等键不同内容。
var (
	ErrNotFound            = errors.New("task not found")
	ErrAlreadyFinished     = errors.New("task already finished")
	ErrNotRetriable        = errors.New("task not in retriable state")
	ErrIdempotencyConflict = errors.New("idempotency key conflicts with different content")
)

// Task 任务的完整业务视图（JSON 字段保持原始载荷）。
type Task struct {
	ID             string          `json:"id"`
	TaskType       string          `json:"task_type"`
	Input          json.RawMessage `json:"input"`
	Params         json.RawMessage `json:"params"`
	AllowedTools   []string        `json:"allowed_tools"`
	Status         string          `json:"status"`
	IdempotencyKey *string         `json:"idempotency_key,omitempty"`
	TraceID        string          `json:"trace_id"`
	AttemptCount   int             `json:"attempt_count"`
	MaxAttempts    int             `json:"max_attempts"`
	TimeoutSeconds int             `json:"timeout_seconds"`
	ErrorCode      *string         `json:"error_code,omitempty"`
	ErrorMessage   *string         `json:"error_message,omitempty"`
	FinishReason   *string         `json:"finish_reason,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
}

// CreateInput 创建任务的入参（API 与内部共用）。
type CreateInput struct {
	TaskType       string
	Input          json.RawMessage
	Params         json.RawMessage
	AllowedTools   []string
	IdempotencyKey string
	MaxAttempts    int
	TimeoutSeconds int
}

// cols 是统一的列清单，与 scanTask 一一对应。
const cols = `id, task_type, input, params, allowed_tools, status, idempotency_key,
	trace_id, attempt_count, max_attempts, timeout_seconds,
	error_code, error_message, finish_reason, created_at, started_at, finished_at`
