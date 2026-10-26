package eval

// 评测运行协调器：建 run → 下发 Python → 轮询终态 → 由 Go 落库（架构 D-1）。
// Python 不可用/状态丢失 → run 置 FAILED(EVALUATION_ERROR)，可整体重跑。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Run 评测运行视图。
type Run struct {
	ID             string          `json:"id"`
	DatasetID      string          `json:"dataset_id"`
	DatasetName    string          `json:"dataset_name"`
	DatasetVersion int             `json:"dataset_version"`
	AgentVersion   string          `json:"agent_version"`
	Status         string          `json:"status"`
	Metrics        json.RawMessage `json:"metrics,omitempty"`
	ErrorCode      *string         `json:"error_code,omitempty"`
	ErrorMessage   *string         `json:"error_message,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
}

// RunService 协调器。
type RunService struct {
	pool   *pgxpool.Pool
	store  *Store
	client *http.Client
	agentURL,
	token string
	poll time.Duration
}

// NewRunService 构建协调器。
func NewRunService(pool *pgxpool.Pool, store *Store, agentURL, token string, poll time.Duration) *RunService {
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	return &RunService{pool: pool, store: store, client: &http.Client{Timeout: 10 * time.Second},
		agentURL: agentURL, token: token, poll: poll}
}

// CreateRunInput 创建评测入参。
type CreateRunInput struct {
	DatasetID    string
	AgentVersion string
	RunConfig    map[string]any
}

// CreateRun 建 run 并异步协调（POST 立即返回 202 语义由 handler 决定）。
func (s *RunService) CreateRun(ctx context.Context, in CreateRunInput) (*Run, error) {
	ds, cases, err := s.store.DatasetWithCases(ctx, in.DatasetID)
	if err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("数据集没有样例")
	}
	if in.AgentVersion == "" {
		in.AgentVersion = "default"
	}
	cfg, _ := json.Marshal(orEmptyMap(in.RunConfig))

	var r Run
	var finished *time.Time
	err = s.pool.QueryRow(ctx, `
		INSERT INTO evaluation_runs (dataset_id, dataset_version, agent_version, run_config)
		VALUES ($1::uuid, $2, $3, $4)
		RETURNING id::text, dataset_id::text, dataset_version, agent_version,
		          status, created_at, finished_at`,
		in.DatasetID, ds.Version, in.AgentVersion, cfg).
		Scan(&r.ID, &r.DatasetID, &r.DatasetVersion, &r.AgentVersion, &r.Status, &r.CreatedAt, &finished)
	if err != nil {
		return nil, err
	}
	r.DatasetName = ds.Name

	go s.coordinate(r.ID, in.AgentVersion, cases, in.RunConfig)
	return &r, nil
}

// coordinate 与 Python 的交互全流程；任何失败最终落 run 终态。
func (s *RunService) coordinate(runID, agentVersion string, cases []CaseRow, runConfig map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	fail := func(code, msg string) {
		_, _ = s.pool.Exec(context.Background(), `
			UPDATE evaluation_runs SET status='FAILED', error_code=$2, error_message=$3, finished_at=now()
			WHERE id=$1::uuid AND status IN ('PENDING','RUNNING')`, runID, code, msg)
	}

	// 组装样例载荷（沿用库内 seq，带 case_id 供结果回填）
	type payloadCase struct {
		CaseID       string          `json:"case_id"`
		Seq          int             `json:"seq"`
		InputTask    json.RawMessage `json:"input_task"`
		AllowedTools []string        `json:"allowed_tools"`
		ScoringRules json.RawMessage `json:"scoring_rules"`
	}
	pc := make([]payloadCase, 0, len(cases))
	for _, c := range cases {
		pc = append(pc, payloadCase{c.ID, c.Seq, c.InputTask, c.AllowedTools, c.ScoringRules})
	}
	body, _ := json.Marshal(map[string]any{
		"eval_run_id": runID, "agent_version": agentVersion,
		"run_config": orEmptyMap(runConfig), "cases": pc,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.agentURL+"/internal/v1/evaluations", bytes.NewReader(body))
	if err != nil {
		fail("EVALUATION_ERROR", err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Token", s.token)
	resp, err := s.client.Do(req)
	if err != nil || resp.StatusCode != http.StatusAccepted {
		status := 0
		if resp != nil {
			status = resp.StatusCode
			resp.Body.Close()
		}
		fail("AGENT_SERVICE_UNAVAILABLE", fmt.Sprintf("评测服务不可用: err=%v status=%d", err, status))
		return
	}
	resp.Body.Close()

	// 轮询至终态
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			fail("EVALUATION_TIMEOUT", "评测整体超时")
			return
		case <-time.After(s.poll):
		}
		greq, _ := http.NewRequestWithContext(ctx, http.MethodGet,
			s.agentURL+"/internal/v1/evaluations/"+runID, nil)
		greq.Header.Set("X-Service-Token", s.token)
		gresp, err := s.client.Do(greq)
		if err != nil || gresp.StatusCode == http.StatusNotFound {
			if gresp != nil {
				gresp.Body.Close()
			}
			fail("AGENT_SERVICE_UNAVAILABLE", "评测状态丢失（服务可能已重启）")
			return
		}
		if err != nil {
			fail("EVALUATION_ERROR", err.Error())
			return
		}
		var env struct {
			Data struct {
				Status  string          `json:"status"`
				Metrics json.RawMessage `json:"metrics"`
				Error   *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
				Results []struct {
					CaseID     string          `json:"case_id"`
					TaskID     *string         `json:"task_id"`
					Status     string          `json:"status"`
					Scores     json.RawMessage `json:"scores"`
					AllPass    bool            `json:"all_rules_pass"`
					TokenUsage json.RawMessage `json:"token_usage"`
					DurationMS *int            `json:"duration_ms"`
					ErrorCode  string          `json:"error_code"`
				} `json:"results"`
			} `json:"data"`
		}
		err = json.NewDecoder(gresp.Body).Decode(&env)
		gresp.Body.Close()
		if err != nil {
			fail("EVALUATION_ERROR", err.Error())
			return
		}
		switch env.Data.Status {
		case "RUNNING", "PENDING":
			continue
		case "SUCCEEDED":
			s.persist(runID, env.Data.Metrics, env.Data.Results)
			return
		default:
			code, msg := "EVALUATION_ERROR", "评测失败"
			if env.Data.Error != nil {
				code, msg = env.Data.Error.Code, env.Data.Error.Message
			}
			fail(code, msg)
			return
		}
	}
	fail("EVALUATION_TIMEOUT", "评测整体超时")
}

// persist 由 Go 写结果与指标（事务）。
func (s *RunService) persist(runID string, metrics json.RawMessage, results []struct {
	CaseID     string          `json:"case_id"`
	TaskID     *string         `json:"task_id"`
	Status     string          `json:"status"`
	Scores     json.RawMessage `json:"scores"`
	AllPass    bool            `json:"all_rules_pass"`
	TokenUsage json.RawMessage `json:"token_usage"`
	DurationMS *int            `json:"duration_ms"`
	ErrorCode  string          `json:"error_code"`
}) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)

	for _, r := range results {
		if r.CaseID == "" {
			continue
		}
		var taskID any
		if r.TaskID != nil {
			taskID = *r.TaskID
		}
		scores := orRaw(r.Scores, "[]")
		usage := orRaw(r.TokenUsage, "{}")
		_, err := tx.Exec(ctx, `
			INSERT INTO evaluation_results (eval_run_id, case_id, task_id, status, scores, evidence, token_usage, duration_ms, error_code)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, NULLIF($9,''))
			ON CONFLICT (eval_run_id, case_id) DO UPDATE SET
			  task_id=EXCLUDED.task_id, status=EXCLUDED.status, scores=EXCLUDED.scores,
			  evidence=EXCLUDED.evidence, token_usage=EXCLUDED.token_usage,
			  duration_ms=EXCLUDED.duration_ms, error_code=EXCLUDED.error_code`,
			runID, r.CaseID, taskID, r.Status, scores,
			[]byte(fmt.Sprintf(`{"all_rules_pass":%v}`, r.AllPass)), usage, r.DurationMS, r.ErrorCode)
		if err != nil {
			return
		}
	}
	if len(metrics) == 0 {
		metrics = []byte("{}")
	}
	if _, err := tx.Exec(ctx, `
		UPDATE evaluation_runs SET status='SUCCEEDED', metrics=$2, finished_at=now()
		WHERE id=$1::uuid`, runID, metrics); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}

// ListRuns 评测列表（可按数据集与版本过滤，供版本对比）。
func (s *RunService) ListRuns(ctx context.Context, datasetID, agentVersion string) ([]*Run, error) {
	where := "TRUE"
	args := []any{}
	if datasetID != "" {
		args = append(args, datasetID)
		where += fmt.Sprintf(" AND r.dataset_id = $%d::uuid", len(args))
	}
	if agentVersion != "" {
		args = append(args, agentVersion)
		where += fmt.Sprintf(" AND r.agent_version = $%d", len(args))
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.id::text, r.dataset_id::text, d.name, r.dataset_version, r.agent_version,
		       r.status, r.metrics, r.created_at, r.finished_at
		FROM evaluation_runs r JOIN evaluation_datasets d ON d.id = r.dataset_id
		WHERE `+where+` ORDER BY r.created_at DESC LIMIT 100`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.DatasetID, &r.DatasetName, &r.DatasetVersion,
			&r.AgentVersion, &r.Status, &r.Metrics, &r.CreatedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// GetRun 详情（含逐样例结果）。
func (s *RunService) GetRun(ctx context.Context, id string) (*Run, []map[string]any, error) {
	var r Run
	err := s.pool.QueryRow(ctx, `
		SELECT r.id::text, r.dataset_id::text, d.name, r.dataset_version, r.agent_version,
		       r.status, r.metrics, r.error_code, r.error_message, r.created_at, r.finished_at
		FROM evaluation_runs r JOIN evaluation_datasets d ON d.id = r.dataset_id
		WHERE r.id=$1::uuid`, id).
		Scan(&r.ID, &r.DatasetID, &r.DatasetName, &r.DatasetVersion,
			&r.AgentVersion, &r.Status, &r.Metrics, &r.ErrorCode, &r.ErrorMessage,
			&r.CreatedAt, &r.FinishedAt)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT c.seq, er.status, er.scores, er.evidence, er.duration_ms, er.error_code
		FROM evaluation_results er JOIN evaluation_cases c ON c.id = er.case_id
		WHERE er.eval_run_id=$1::uuid ORDER BY c.seq`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var results []map[string]any
	for rows.Next() {
		var seq int
		var status string
		var scores, evidence json.RawMessage
		var dur *int
		var ecode *string
		if err := rows.Scan(&seq, &status, &scores, &evidence, &dur, &ecode); err != nil {
			return nil, nil, err
		}
		results = append(results, map[string]any{
			"case_seq": seq, "status": status, "scores": scores,
			"evidence": evidence, "duration_ms": dur, "error_code": ecode,
		})
	}
	return &r, results, rows.Err()
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func orRaw(b json.RawMessage, def string) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage(def)
	}
	return b
}
