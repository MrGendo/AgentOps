// Package diagnosis 组装证据包、触发 Python 诊断并落库（需求 §9）。
// 只读分析：不修改任务状态、不调用工具；证据不足由诊断器标注 LOW。
package diagnosis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Report 落库后的诊断报告视图。
type Report struct {
	ID           string          `json:"id"`
	TaskID       string          `json:"task_id"`
	FaultCode    string          `json:"fault_code"`
	Confidence   string          `json:"confidence"`
	Hypothesis   string          `json:"root_cause_hypothesis"`
	Evidence     json.RawMessage `json:"evidence"`
	Suggestions  json.RawMessage `json:"suggestions"`
	NeedHuman    bool            `json:"need_human"`
	DiagnoserVer string          `json:"diagnoser_version"`
	CreatedAt    time.Time       `json:"created_at"`
}

// Service 诊断服务。
type Service struct {
	pool     *pgxpool.Pool
	agentURL string
	token    string
	client   *http.Client
	timeout  time.Duration
}

// NewService 构建诊断服务。
func NewService(pool *pgxpool.Pool, agentURL, token string) *Service {
	return &Service{pool: pool, agentURL: agentURL, token: token,
		client: &http.Client{Timeout: 10 * time.Second}, timeout: 15 * time.Second}
}

// buildEvidence 汇总真实执行证据：任务、步骤、工具调用、关键事件与 Agent 运行。
func (s *Service) buildEvidence(ctx context.Context, taskID string) (map[string]any, string, error) {
	var status string
	var errCode, errMsg, finishReason *string
	var traceID string
	if err := s.pool.QueryRow(ctx, `
		SELECT status, error_code, error_message, finish_reason, trace_id::text
		FROM tasks WHERE id=$1::uuid`, taskID).
		Scan(&status, &errCode, &errMsg, &finishReason, &traceID); err != nil {
		return nil, "", err
	}

	task := map[string]any{"id": taskID, "status": status,
		"error_code": errCode, "error_message": errMsg, "finish_reason": finishReason}

	var steps []map[string]any
	srows, err := s.pool.Query(ctx, `
		SELECT id::text, name, status, error_code FROM task_steps WHERE task_id=$1::uuid ORDER BY seq`, taskID)
	if err == nil {
		for srows.Next() {
			var id, name, st string
			var ec *string
			if srows.Scan(&id, &name, &st, &ec) == nil {
				steps = append(steps, map[string]any{"id": id, "name": name, "status": st, "error_code": ec})
			}
		}
		srows.Close()
	}

	var calls []map[string]any
	crows, err := s.pool.Query(ctx, `
		SELECT id::text, tool_name, status, error_code FROM tool_calls WHERE task_id=$1::uuid ORDER BY created_at`, taskID)
	if err == nil {
		for crows.Next() {
			var id, name, st string
			var ec *string
			if crows.Scan(&id, &name, &st, &ec) == nil {
				calls = append(calls, map[string]any{"id": id, "tool_name": name, "status": st, "error_code": ec})
			}
		}
		crows.Close()
	}

	var events []map[string]any
	erows, err := s.pool.Query(ctx, `
		SELECT event_type, payload FROM task_events
		WHERE task_id=$1::uuid AND event_type IN
		  ('lease.revoked','task.failed','tool.call.rejected','task.result_discarded','task.timeout')
		ORDER BY seq LIMIT 50`, taskID)
	if err == nil {
		for erows.Next() {
			var typ string
			var payload json.RawMessage
			if erows.Scan(&typ, &payload) == nil {
				events = append(events, map[string]any{"event_type": typ, "payload": payload})
			}
		}
		erows.Close()
	}

	var runs []map[string]any
	arows, err := s.pool.Query(ctx, `
		SELECT id::text, status, COALESCE(finish_reason,''), plan_valid,
		       COALESCE(error_code,''), COALESCE(error_message,'')
		FROM agent_runs WHERE task_id=$1::uuid ORDER BY created_at`, taskID)
	if err == nil {
		for arows.Next() {
			var id, st, reason, ec, em string
			var planValid *bool
			if arows.Scan(&id, &st, &reason, &planValid, &ec, &em) == nil {
				runs = append(runs, map[string]any{
					"id": id, "status": st, "finish_reason": reason,
					"plan_valid": planValid, "error_code": ec,
					"error": map[string]any{"code": ec, "message": em}})
			}
		}
		arows.Close()
	}

	return map[string]any{
		"task": task, "steps": steps, "tool_calls": calls,
		"events": events, "agent_runs": runs,
	}, status, nil
}

// Diagnose 触发诊断并落库；仅对非 SUCCEEDED 的终态任务有意义。
func (s *Service) Diagnose(ctx context.Context, taskID string) (*Report, error) {
	evidence, status, err := s.buildEvidence(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if status == "SUCCEEDED" {
		return nil, fmt.Errorf("成功任务无需诊断")
	}
	body, _ := json.Marshal(map[string]any{"task_id": taskID, "evidence": evidence})
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodPost,
		s.agentURL+"/internal/v1/diagnoses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Token", s.token)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("诊断服务不可用: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("诊断受理失败: HTTP %d", resp.StatusCode)
	}

	// 诊断是纯函数，通常即刻可取；仍按轮询取回报告
	greq, _ := http.NewRequestWithContext(cctx, http.MethodGet,
		s.agentURL+"/internal/v1/diagnoses/"+taskID, nil)
	greq.Header.Set("X-Service-Token", s.token)
	gresp, err := s.client.Do(greq)
	if err != nil {
		return nil, err
	}
	defer gresp.Body.Close()
	if gresp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("诊断报告获取失败: HTTP %d", gresp.StatusCode)
	}
	var env struct {
		Data struct {
			FaultCode   string          `json:"fault_code"`
			Confidence  string          `json:"confidence"`
			Hypothesis  string          `json:"root_cause_hypothesis"`
			Evidence    json.RawMessage `json:"evidence"`
			Suggestions json.RawMessage `json:"suggestions"`
			NeedHuman   bool            `json:"need_human"`
			Version     string          `json:"diagnoser_version"`
		} `json:"data"`
	}
	if err := json.NewDecoder(gresp.Body).Decode(&env); err != nil {
		return nil, err
	}
	d := env.Data
	if len(d.Evidence) == 0 {
		d.Evidence = json.RawMessage("[]")
	}
	if len(d.Suggestions) == 0 {
		d.Suggestions = json.RawMessage("[]")
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO diagnosis_reports (task_id, fault_code, confidence, root_cause_hypothesis,
		  evidence, suggestions, need_human, diagnoser_version)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8)`,
		taskID, d.FaultCode, d.Confidence, d.Hypothesis, d.Evidence, d.Suggestions,
		d.NeedHuman, d.Version)
	if err != nil {
		return nil, err
	}
	return s.GetLatest(ctx, taskID)
}

// ListReports 任务的诊断报告（倒序）。
func (s *Service) ListReports(ctx context.Context, taskID string) ([]*Report, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, task_id::text, fault_code, confidence, root_cause_hypothesis,
		       evidence, suggestions, need_human, diagnoser_version, created_at
		FROM diagnosis_reports WHERE task_id=$1::uuid ORDER BY created_at DESC LIMIT 20`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Report
	for rows.Next() {
		var r Report
		if err := rows.Scan(&r.ID, &r.TaskID, &r.FaultCode, &r.Confidence, &r.Hypothesis,
			&r.Evidence, &r.Suggestions, &r.NeedHuman, &r.DiagnoserVer, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// GetLatest 最新报告。
func (s *Service) GetLatest(ctx context.Context, taskID string) (*Report, error) {
	list, err := s.ListReports(ctx, taskID)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return list[0], nil
}
