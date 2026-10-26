package httpapi

// 数据集、评测与诊断端点（api-contract.md §2）。

import (
	"bufio"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agentops/platform/internal/diagnosis"
	"github.com/agentops/platform/internal/eval"
)

// EvalAPI 评测与诊断处理器组（导出供路由装配）。
type EvalAPI struct {
	store *eval.Store
	runs  *eval.RunService
	diag  *diagnosis.Service
	pool  *pgxpool.Pool
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
func NewEvalAPI(store *eval.Store, runs *eval.RunService, diag *diagnosis.Service, pool *pgxpool.Pool) *EvalAPI {
	return &EvalAPI{store: store, runs: runs, diag: diag, pool: pool}
}
