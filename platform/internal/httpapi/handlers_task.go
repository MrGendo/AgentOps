package httpapi

// 任务管理端点：创建/列表/详情/取消/重试/事件/轨迹（api-contract.md §2）。

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agentops/platform/internal/gateway"
	"github.com/agentops/platform/internal/task"
)

type taskAPI struct {
	tasks *task.Store
	pool  *pgxpool.Pool
}

type createTaskReq struct {
	TaskType       string         `json:"task_type" binding:"required"`
	Input          map[string]any `json:"input" binding:"required"`
	Params         map[string]any `json:"params"`
	AllowedTools   []string       `json:"allowed_tools"`
	IdempotencyKey string         `json:"idempotency_key"`
	MaxAttempts    int            `json:"max_attempts"`
	TimeoutSeconds int            `json:"timeout_seconds"`
}

// createTask POST /api/v1/tasks：幂等创建（§6.1）。
func (a *taskAPI) createTask(c *gin.Context) {
	var req createTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, errValidation, "请求体不合法: "+err.Error(), nil)
		return
	}
	in := task.CreateInput{
		TaskType:       req.TaskType,
		Input:          mustMarshalRaw(req.Input),
		Params:         mustMarshalRaw(orEmpty(req.Params)),
		AllowedTools:   req.AllowedTools,
		IdempotencyKey: req.IdempotencyKey,
		MaxAttempts:    req.MaxAttempts,
		TimeoutSeconds: req.TimeoutSeconds,
	}
	t, created, err := a.tasks.Create(c.Request.Context(), in)
	switch {
	case errors.Is(err, task.ErrIdempotencyConflict):
		Fail(c, errIdemConflict, "幂等键已绑定不同内容", nil)
		return
	case err != nil:
		Fail(c, errInternal, "创建任务失败: "+err.Error(), nil)
		return
	}
	status := http.StatusCreated
	meta := gin.H{}
	if !created {
		status = http.StatusOK
		meta["already_exists"] = true
	}
	Data(c, status, t, meta)
}

// listTasks GET /api/v1/tasks：游标分页 + 过滤。
func (a *taskAPI) listTasks(c *gin.Context) {
	f := task.ListFilter{
		Status:   c.Query("status"),
		TaskType: c.Query("task_type"),
		Cursor:   c.Query("cursor"),
	}
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	if v := c.Query("created_from"); v != "" {
		if ts, err := time.Parse(time.RFC3339, v); err == nil {
			f.CreatedFrom = &ts
		}
	}
	if v := c.Query("created_to"); v != "" {
		if ts, err := time.Parse(time.RFC3339, v); err == nil {
			f.CreatedTo = &ts
		}
	}
	list, next, total, err := a.tasks.List(c.Request.Context(), f)
	if err != nil {
		Fail(c, errValidation, "查询失败: "+err.Error(), nil)
		return
	}
	Data(c, http.StatusOK, list, gin.H{"next_cursor": next, "total": total})
}

// getTask GET /api/v1/tasks/{id}。
func (a *taskAPI) getTask(c *gin.Context) {
	t, err := a.tasks.Get(c.Request.Context(), c.Param("id"))
	if errors.Is(err, task.ErrNotFound) {
		Fail(c, errTaskNotFound, "任务不存在", nil)
		return
	}
	if err != nil {
		Fail(c, errInternal, err.Error(), nil)
		return
	}
	Data(c, http.StatusOK, t, nil)
}

// cancelTask POST /api/v1/tasks/{id}/cancel：非终态可取消（§6.2）。
func (a *taskAPI) cancelTask(c *gin.Context) {
	t, err := a.tasks.Cancel(c.Request.Context(), c.Param("id"))
	switch {
	case errors.Is(err, task.ErrNotFound):
		Fail(c, errTaskNotFound, "任务不存在", nil)
	case errors.Is(err, task.ErrAlreadyFinished):
		Fail(c, errAlreadyFinished, "任务已处于终态，无法取消", nil)
	case err != nil:
		Fail(c, errInternal, err.Error(), nil)
	default:
		Data(c, http.StatusAccepted, t, nil)
	}
}

// retryTask POST /api/v1/tasks/{id}/retry：仅 FAILED/CANCELLED（§6.2）。
func (a *taskAPI) retryTask(c *gin.Context) {
	t, err := a.tasks.Retry(c.Request.Context(), c.Param("id"))
	switch {
	case errors.Is(err, task.ErrNotFound):
		Fail(c, errTaskNotFound, "任务不存在", nil)
	case errors.Is(err, task.ErrNotRetriable):
		Fail(c, errNotRetriable, "当前状态不可重试（仅 FAILED/CANCELLED）", nil)
	case err != nil:
		Fail(c, errInternal, err.Error(), nil)
	default:
		Data(c, http.StatusAccepted, t, nil)
	}
}

// --- 工具端点 ---

// listTools GET /api/v1/tools：工具清单（名称/版本/风险/超时/幂等）。
func listTools(gw *gateway.Gateway) gin.HandlerFunc {
	return func(c *gin.Context) {
		out := make([]gin.H, 0, 8)
		for _, t := range gw.Tools() {
			out = append(out, gin.H{
				"name": t.Name, "version": t.Version, "description": t.Description,
				"risk_level": t.RiskLevel, "timeout_ms": t.Timeout.Milliseconds(),
				"idempotent": t.Idempotent,
			})
		}
		Data(c, http.StatusOK, out, nil)
	}
}

type invokeToolReq struct {
	TaskID         string         `json:"task_id" binding:"required"`
	StepID         string         `json:"step_id"`
	AgentRunID     string         `json:"agent_run_id"`
	ToolName       string         `json:"tool_name" binding:"required"`
	Params         map[string]any `json:"params"`
	IdempotencyKey string         `json:"idempotency_key"`
}

// invokeTool POST /api/v1/tool-calls：外部（Python）经网关调用工具的唯一入口。
func invokeTool(gw *gateway.Gateway) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req invokeToolReq
		if err := c.ShouldBindJSON(&req); err != nil {
			Fail(c, errValidation, "请求体不合法: "+err.Error(), nil)
			return
		}
		var stepID, runID *string
		if req.StepID != "" {
			stepID = &req.StepID
		}
		if req.AgentRunID != "" {
			runID = &req.AgentRunID
		}
		res, err := gw.Invoke(c.Request.Context(), gateway.InvokeRequest{
			TaskID: req.TaskID, StepID: stepID, AgentRunID: runID,
			ToolName: req.ToolName, Params: req.Params, IdempotencyKey: req.IdempotencyKey,
		})
		switch {
		case errors.Is(err, gateway.ErrUnknownTool):
			Fail(c, errToolNotFound, "工具未注册: "+req.ToolName, nil)
		case errors.Is(err, gateway.ErrTaskNotFound):
			Fail(c, errTaskNotFound, "任务不存在", nil)
		case err != nil:
			Fail(c, errInternal, err.Error(), nil)
		case res.Status == gateway.StatusRejected && res.ErrorCode == "PERMISSION_DENIED":
			Fail(c, errPermission, res.ErrorMessage, res)
		default:
			Data(c, http.StatusOK, res, nil)
		}
	}
}

func mustMarshalRaw(v any) []byte {
	if v == nil {
		return []byte(`{}`)
	}
	b, err := jsonMarshal(v)
	if err != nil || len(b) == 0 {
		return []byte(`{}`)
	}
	return b
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
