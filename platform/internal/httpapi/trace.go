package httpapi

// 事件时间线与执行轨迹聚合（只读查询，api-contract.md §2）。

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// listEvents GET /api/v1/tasks/{id}/events：seq 升序，游标为最后一条 seq。
func (a *taskAPI) listEvents(c *gin.Context) {
	id := c.Param("id")
	limit := 50
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	after := 0
	if v, err := strconv.Atoi(c.Query("cursor")); err == nil && v > 0 {
		after = v
	}
	if !a.taskExists(c, id) {
		return
	}
	rows, err := a.pool.Query(c.Request.Context(), `
		SELECT seq, event_id::text, COALESCE(step_id::text,''), COALESCE(attempt_id::text,''),
		       COALESCE(tool_call_id::text,''), event_type, payload, created_at
		FROM task_events WHERE task_id=$1::uuid AND seq > $2
		ORDER BY seq LIMIT $3`, id, after, limit+1)
	if err != nil {
		Fail(c, errInternal, err.Error(), nil)
		return
	}
	defer rows.Close()

	items := []gin.H{}
	last, more := 0, false
	for rows.Next() {
		if len(items) == limit {
			more = true
			break
		}
		var seq int
		var eid, step, attempt, tool, typ string
		var payload json.RawMessage
		var ts time.Time
		if err := rows.Scan(&seq, &eid, &step, &attempt, &tool, &typ, &payload, &ts); err != nil {
			Fail(c, errInternal, err.Error(), nil)
			return
		}
		last = seq
		h := gin.H{"seq": seq, "event_id": eid, "event_type": typ, "payload": payload, "created_at": ts}
		if step != "" {
			h["step_id"] = step
		}
		if attempt != "" {
			h["attempt_id"] = attempt
		}
		if tool != "" {
			h["tool_call_id"] = tool
		}
		items = append(items, h)
	}
	meta := gin.H{}
	if more && last > 0 {
		meta["next_cursor"] = strconv.Itoa(last)
	}
	Data(c, http.StatusOK, items, meta)
}

// getTrace GET /api/v1/tasks/{id}/trace：步骤(含尝试历史)+工具调用聚合。
func (a *taskAPI) getTrace(c *gin.Context) {
	id := c.Param("id")
	if !a.taskExists(c, id) {
		return
	}
	ctx := c.Request.Context()

	type stepRow struct {
		ID, Name, StepType, Status string
		Seq                        int
		Output, ErrCode, ErrMsg    *string
		StartedAt, FinishedAt      *time.Time
	}
	var steps []gin.H
	srows, err := a.pool.Query(ctx, `
		SELECT id::text, seq, name, step_type, status, output_ref,
		       error_code, error_message, started_at, finished_at
		FROM task_steps WHERE task_id=$1::uuid ORDER BY seq`, id)
	if err != nil {
		Fail(c, errInternal, err.Error(), nil)
		return
	}
	var stepList []stepRow
	for srows.Next() {
		var s stepRow
		if err := srows.Scan(&s.ID, &s.Seq, &s.Name, &s.StepType, &s.Status, &s.Output,
			&s.ErrCode, &s.ErrMsg, &s.StartedAt, &s.FinishedAt); err != nil {
			srows.Close()
			Fail(c, errInternal, err.Error(), nil)
			return
		}
		stepList = append(stepList, s)
	}
	srows.Close()

	attemptsByStep := map[string][]gin.H{}
	arows, err := a.pool.Query(ctx, `
		SELECT id::text, step_id::text, attempt_no, status, error_class, error_code, error_message,
		       started_at, finished_at, duration_ms
		FROM step_attempts WHERE task_id=$1::uuid ORDER BY step_id, attempt_no`, id)
	if err != nil {
		Fail(c, errInternal, err.Error(), nil)
		return
	}
	for arows.Next() {
		var aid, sid, status string
		var no int
		var class, ecode, emsg *string
		var started time.Time
		var finished *time.Time
		var dur *int
		if err := arows.Scan(&aid, &sid, &no, &status, &class, &ecode, &emsg, &started, &finished, &dur); err != nil {
			arows.Close()
			Fail(c, errInternal, err.Error(), nil)
			return
		}
		attemptsByStep[sid] = append(attemptsByStep[sid], gin.H{
			"id": aid, "attempt_no": no, "status": status,
			"error_class": class, "error_code": ecode, "error_message": emsg,
			"started_at": started, "finished_at": finished, "duration_ms": dur,
		})
	}
	arows.Close()

	for _, s := range stepList {
		steps = append(steps, gin.H{
			"id": s.ID, "seq": s.Seq, "name": s.Name, "step_type": s.StepType, "status": s.Status,
			"output_ref": s.Output, "error_code": s.ErrCode, "error_message": s.ErrMsg,
			"started_at": s.StartedAt, "finished_at": s.FinishedAt,
			"attempts": attemptsByStep[s.ID],
		})
	}

	toolCalls := []gin.H{}
	trows, err := a.pool.Query(ctx, `
		SELECT id::text, tool_name, tool_version, status, params, result,
		       error_code, error_message, duration_ms, created_at
		FROM tool_calls WHERE task_id=$1::uuid ORDER BY created_at`, id)
	if err != nil {
		Fail(c, errInternal, err.Error(), nil)
		return
	}
	for trows.Next() {
		var tid, name, ver, status string
		var params, result json.RawMessage
		var errCode, errMsg *string
		var dur *int
		var ts time.Time
		if err := trows.Scan(&tid, &name, &ver, &status, &params, &result, &errCode, &errMsg, &dur, &ts); err != nil {
			trows.Close()
			Fail(c, errInternal, err.Error(), nil)
			return
		}
		toolCalls = append(toolCalls, gin.H{
			"id": tid, "tool_name": name, "tool_version": ver, "status": status,
			"params": params, "result": result,
			"error_code": errCode, "error_message": errMsg,
			"duration_ms": dur, "created_at": ts,
		})
	}
	trows.Close()

	Data(c, http.StatusOK, gin.H{"steps": steps, "tool_calls": toolCalls}, nil)
}

// taskExists 校验任务存在，否则写 404 并返回 false。
func (a *taskAPI) taskExists(c *gin.Context, id string) bool {
	var exists bool
	if err := a.pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1::uuid)`, id).Scan(&exists); err != nil || !exists {
		Fail(c, errTaskNotFound, "任务不存在", nil)
		return false
	}
	return true
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
