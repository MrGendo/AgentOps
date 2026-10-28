package httpapi

// 数据集、评测与诊断端点（api-contract.md §2）。

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agentops/platform/internal/diagnosis"
	"github.com/agentops/platform/internal/eval"
)

// EvalAPI 评测与诊断处理器组（导出供路由装配）。
type EvalAPI struct {
	store    *eval.Store
	runs     *eval.RunService
	diag     *diagnosis.Service
	pool     *pgxpool.Pool
	agentURL string
	token    string
}

var errDatasetExists = apiErr{409, "DATASET_VERSION_EXISTS", false}

// importDataset POST /api/v1/datasets：JSON 或 JSONL（x-ndjson，逐行一个样例）。
func (a *EvalAPI) importDataset(c *gin.Context) {
	var (
		name, desc string
		version    int
		cases      []eval.CaseInput
	)
	if strings.HasPrefix(c.GetHeader("Content-Type"), "application/x-ndjson") {
		name = c.Query("name")
		if name == "" {
			Fail(c, errValidation, "JSONL 导入必须通过 ?name= 指定数据集名", nil)
			return
		}
		scanner := bufio.NewScanner(c.Request.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var cs eval.CaseInput
			if err := bindJSONLine(line, &cs); err != nil {
				Fail(c, errValidation, "JSONL 行解析失败: "+err.Error(), nil)
				return
			}
			cases = append(cases, cs)
		}
		desc = c.Query("description")
	} else {
		var req struct {
			Name        string           `json:"name" binding:"required"`
			Version     int              `json:"version"`
			Description string           `json:"description"`
			Cases       []eval.CaseInput `json:"cases" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			Fail(c, errValidation, "请求体不合法: "+err.Error(), nil)
			return
		}
		name, version, desc, cases = req.Name, req.Version, req.Description, req.Cases
	}

	d, err := a.store.Import(c.Request.Context(), name, version, desc, cases)
	switch {
	case errors.Is(err, eval.ErrDatasetExists):
		Fail(c, errDatasetExists, "同名同版本数据集已存在", nil)
	case err != nil:
		Fail(c, errValidation, err.Error(), nil)
	default:
		Data(c, http.StatusCreated, d, nil)
	}
}

// listDatasets GET /api/v1/datasets。
func (a *EvalAPI) listDatasets(c *gin.Context) {
	list, err := a.store.ListDatasets(c.Request.Context())
	if err != nil {
		Fail(c, errInternal, err.Error(), nil)
		return
	}
	Data(c, http.StatusOK, list, nil)
}

type createEvalReq struct {
	DatasetID    string         `json:"dataset_id" binding:"required"`
	AgentVersion string         `json:"agent_version"`
	RunConfig    map[string]any `json:"run_config"`
}

// createEvaluation POST /api/v1/evaluations：建 run 并异步执行。
func (a *EvalAPI) createEvaluation(c *gin.Context) {
	var req createEvalReq
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, errValidation, "请求体不合法: "+err.Error(), nil)
		return
	}
	run, err := a.runs.CreateRun(c.Request.Context(), eval.CreateRunInput{
		DatasetID: req.DatasetID, AgentVersion: req.AgentVersion, RunConfig: req.RunConfig,
	})
	if err != nil {
		Fail(c, errValidation, "创建评测失败: "+err.Error(), nil)
		return
	}
	Data(c, http.StatusAccepted, run, nil)
}

// listEvaluations GET /api/v1/evaluations?dataset_id=&agent_version=。
func (a *EvalAPI) listEvaluations(c *gin.Context) {
	list, err := a.runs.ListRuns(c.Request.Context(), c.Query("dataset_id"), c.Query("agent_version"))
	if err != nil {
		Fail(c, errInternal, err.Error(), nil)
		return
	}
	Data(c, http.StatusOK, list, gin.H{"count": len(list)})
}

// getEvaluation GET /api/v1/evaluations/{id}。
func (a *EvalAPI) getEvaluation(c *gin.Context) {
	run, results, err := a.runs.GetRun(c.Request.Context(), c.Param("id"))
	if err != nil {
		Fail(c, errValidation, "评测不存在", nil)
		return
	}
	Data(c, http.StatusOK, gin.H{
		"id": run.ID, "dataset_id": run.DatasetID, "dataset_name": run.DatasetName,
		"agent_version": run.AgentVersion, "status": run.Status,
		"metrics": run.Metrics, "error_code": run.ErrorCode, "error_message": run.ErrorMessage,
		"created_at": run.CreatedAt, "finished_at": run.FinishedAt,
		"results": results,
	}, nil)
}

// getDiagnosis GET /api/v1/tasks/{id}/diagnosis：无报告且任务失败终态时自动生成。
func (a *EvalAPI) getDiagnosis(c *gin.Context) {
	id := c.Param("id")
	if !a.taskExistsForDiag(c, id) {
		return
	}
	reports, err := a.diag.ListReports(c.Request.Context(), id)
	if err != nil {
		Fail(c, errInternal, err.Error(), nil)
		return
	}
	meta := gin.H{}
	if len(reports) == 0 && a.taskFailed(c, id) {
		// 首次查询触发生成（15s 上限）；超时返回 generating 提示
		if _, derr := a.diag.Diagnose(c.Request.Context(), id); derr != nil {
			meta["generating"] = true
			meta["generate_error"] = derr.Error()
		} else {
			reports, _ = a.diag.ListReports(c.Request.Context(), id)
		}
	}
	if reports == nil {
		reports = []*diagnosis.Report{}
	}
	Data(c, http.StatusOK, reports, meta)
}

func (a *EvalAPI) taskExistsForDiag(c *gin.Context, id string) bool {
	var exists bool
	if err := a.pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1::uuid)`, id).Scan(&exists); err != nil || !exists {
		Fail(c, errTaskNotFound, "任务不存在", nil)
		return false
	}
	return true
}

func (a *EvalAPI) taskFailed(c *gin.Context, id string) bool {
	var status string
	if err := a.pool.QueryRow(c.Request.Context(),
		`SELECT status FROM tasks WHERE id=$1::uuid`, id).Scan(&status); err != nil {
		return false
	}
	return status == "FAILED" || status == "CANCELLED"
}

// bindJSONLine 解析单行 JSON（JSONL 导入用）。
func bindJSONLine(line string, out any) error {
	return jsonUnmarshalString(line, out)
}

// NewEvalAPI 构建评测/诊断处理器组。
func NewEvalAPI(store *eval.Store, runs *eval.RunService, diag *diagnosis.Service, pool *pgxpool.Pool, agentURL, token string) *EvalAPI {
	return &EvalAPI{store: store, runs: runs, diag: diag, pool: pool, agentURL: agentURL, token: token}
}

// listAgents GET /api/v1/agents：注册表（Python 适配器层）+ agent_runs 真实统计注入。
// 已注册/可运行/已测试三态分离；无运行记录的 success_rate 保持 null。
func (a *EvalAPI) listAgents(c *gin.Context) {
	// ① 真实统计（来自 agent_runs，agent_version 列即 agent_id）
	stats := map[string]map[string]any{}
	rows, err := a.pool.Query(c.Request.Context(), `
		SELECT ar.agent_version,
		       count(*) AS total,
		       round(1.0*count(*) FILTER (WHERE ar.status='SUCCEEDED')/GREATEST(count(*),1), 4) AS rate,
		       (SELECT ar2.status FROM agent_runs ar2
		         WHERE ar2.agent_version=ar.agent_version
		         ORDER BY ar2.created_at DESC LIMIT 1) AS last_status,
		       max(ar.finished_at)::text AS last_used
		FROM agent_runs ar
		JOIN tasks t ON t.id = ar.task_id AND t.task_type='swe.fix'
		GROUP BY ar.agent_version`)
	if err == nil {
		for rows.Next() {
			var id string
			var total int
			var rate float64
			var lastStatus any
			var lastUsed any
			if rows.Scan(&id, &total, &rate, &lastStatus, &lastUsed) == nil {
				stats[id] = map[string]any{"total_runs": total, "success_rate": rate,
					"last_run_status": lastStatus, "last_used_at": lastUsed}
			}
		}
		rows.Close()
	}

	// ② 透传 Python 注册表并注入统计
	registryBytes, err := a.fetchRegistry(c)
	if err != nil {
		Fail(c, errAgentUnavailable, "Agent 注册表服务不可用: "+err.Error(), nil)
		return
	}
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if json.Unmarshal(registryBytes, &env) != nil {
		Fail(c, errInternal, "注册表响应不可解析", nil)
		return
	}
	for _, item := range env.Data {
		id, _ := item["agent_id"].(string)
		if st, ok := stats[id]; ok {
			for k, v := range st {
				item[k] = v
			}
		}
	}
	Data(c, http.StatusOK, env.Data, nil)
}

// fetchRegistry 转发到 Python /internal/v1/agents（带服务令牌）。
func (a *EvalAPI) fetchRegistry(c *gin.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet,
		a.agentURL+"/internal/v1/agents", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Service-Token", a.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
