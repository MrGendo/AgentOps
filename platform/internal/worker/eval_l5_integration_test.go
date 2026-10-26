//go:build integration

// L5（§17-D 验收）：双进程真实评测与诊断。
// Go 测试进程提供 HTTP 网关 + Worker 循环；拉起真实 uvicorn 执行评测引擎与规则诊断器。
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentops/platform/internal/diagnosis"
	"github.com/agentops/platform/internal/eval"
	"github.com/agentops/platform/internal/gateway"
	"github.com/agentops/platform/internal/httpapi"
	"github.com/agentops/platform/internal/task"
	"github.com/agentops/platform/internal/testutil"
)

// importDemoMixed 以 JSON 形态导入与 agent/evaldata/demo-mixed.jsonl 等价的样例。
func importDemoMixed(t *testing.T, store *eval.Store) *eval.Dataset {
	t.Helper()
	cases := []eval.CaseInput{
		{InputTask: map[string]any{"task_type": "mock.echo", "input": map[string]any{"msg": "hello"}, "timeout_seconds": 30},
			ScoringRules: []map[string]any{{"type": "finished_normally"}}},
		{InputTask: map[string]any{"task_type": "mock.fail", "input": map[string]any{"goal": "retry-demo"},
			"params":       map[string]any{"fail_times": 1, "error_class": "TRANSIENT_NETWORK"},
			"max_attempts": 3, "timeout_seconds": 30},
			ScoringRules: []map[string]any{{"type": "finished_normally"}}},
		{InputTask: map[string]any{"task_type": "mock.tools", "input": map[string]any{"goal": "demo"},
			"allowed_tools":   []string{"calculator", "knowledge_search", "task_status", "mock_http"},
			"timeout_seconds": 60},
			ScoringRules: []map[string]any{{"type": "finished_normally"},
				{"type": "tools_used", "tools": []string{"calculator", "knowledge_search", "mock_http"}, "mode": "subset"},
				{"type": "contains_text", "text": "value"}}},
		{InputTask: map[string]any{"task_type": "mock.fail", "input": map[string]any{"goal": "bad-input"},
			"params": map[string]any{"fail_times": 5, "error_class": "ARGUMENT_INVALID"}, "timeout_seconds": 30},
			ScoringRules: []map[string]any{{"type": "finished_normally"}}},
	}
	d, err := store.Import(context.Background(), "demo-mixed", 1, "L5 验收集", cases)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestL5DualProcessEvaluationAndDiagnosis(t *testing.T) {
	const pyPort = "8902"
	agentURL := "http://127.0.0.1:" + pyPort
	st := testutil.NewStore(t)
	testutil.Truncate(t, st)
	ctx := context.Background()

	tools, err := gateway.DefaultTools(st.Pool(), "../../knowledge")
	if err != nil {
		t.Fatal(err)
	}
	gw := gateway.New(st.Pool(), tools...)

	evalStore := eval.NewStore(st.Pool())
	evalRuns := eval.NewRunService(st.Pool(), evalStore, agentURL, "dev-service-token", 50*time.Millisecond)
	diagSvc := diagnosis.NewService(st.Pool(), agentURL, "dev-service-token")

	apiSrv := httptest.NewServer(httpapi.New(httpapi.Deps{
		DB: st, Pool: st.Pool(), Tasks: task.NewStore(st.Pool()), GW: gw,
		ServiceToken: "dev-service-token",
		Eval:         httpapi.NewEvalAPI(evalStore, evalRuns, diagSvc, st.Pool()),
	}))
	defer apiSrv.Close()

	// 真实 uvicorn：评测引擎 + 诊断器
	pyBin, _ := filepath.Abs("../../../agent/.venv/bin/python3")
	pyDir, _ := filepath.Abs("../../../agent")
	if _, err := os.Stat(pyBin); err != nil {
		t.Skipf("SKIP（未执行）: 未找到 %s", pyBin)
	}
	var pyLogs bytes.Buffer
	cmd := exec.Command(pyBin, "-m", "uvicorn", "app.main:app", "--port", pyPort, "--log-level", "warning")
	cmd.Dir = pyDir
	cmd.Env = append(os.Environ(),
		"AGENTOPS_PLATFORM_URL="+apiSrv.URL,
		"AGENTOPS_SERVICE_TOKEN=dev-service-token",
		"PYTHONUNBUFFERED=1",
	)
	cmd.Stderr = &pyLogs
	if err := cmd.Start(); err != nil {
		t.Skipf("SKIP（未执行）: 启动 Python 失败: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	for i := 0; i < 50; i++ {
		if resp, err := http.Get(agentURL + "/internal/v1/health"); err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Worker 循环：处理评测创建的样例任务
	wctx, stop := context.WithCancel(context.Background())
	defer stop()
	m := NewManager(fastCfg(), st.Pool(), gw, DefaultExecutors()...)
	m.Start(wctx)

	ds := importDemoMixed(t, evalStore)

	waitEval := func(runID, want string) *eval.Run {
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			r, _, err := evalRuns.GetRun(ctx, runID)
			if err == nil && r.Status == want {
				return r
			}
			time.Sleep(100 * time.Millisecond)
		}
		r, _, _ := evalRuns.GetRun(ctx, runID)
		t.Fatalf("等待评测 %s 超时，当前 %s\nPython 日志:\n%s", want, r.Status, pyLogs.String())
		return nil
	}

	// ① 批量评测（v1）
	run1, err := evalRuns.CreateRun(ctx, eval.CreateRunInput{DatasetID: ds.ID, AgentVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	r1 := waitEval(run1.ID, "SUCCEEDED")

	var m1 struct {
		TaskSuccessRate   struct{ Value float64 } `json:"task_success_rate"`
		ResultCorrectness struct{ Value float64 } `json:"result_correctness"`
		RetryRate         struct{ Value float64 } `json:"retry_rate"`
		ToolCallAccuracy  struct{ Value float64 } `json:"tool_call_accuracy"`
	}
	if err := json.Unmarshal(r1.Metrics, &m1); err != nil {
		t.Fatalf("指标解析: %v %s", err, r1.Metrics)
	}
	if m1.TaskSuccessRate.Value != 0.75 {
		t.Fatalf("任务成功率 = %v，期望 0.75（4 样例 1 败）", m1.TaskSuccessRate.Value)
	}
	if m1.RetryRate.Value != 0.25 {
		t.Fatalf("重试率 = %v，期望 0.25", m1.RetryRate.Value)
	}
	if m1.ResultCorrectness.Value != 0.75 {
		t.Fatalf("正确率 = %v，期望 0.75", m1.ResultCorrectness.Value)
	}
	if m1.ToolCallAccuracy.Value != 1.0 {
		t.Fatalf("工具调用准确率 = %v，期望 1.0", m1.ToolCallAccuracy.Value)
	}

	// ② 版本对比：同数据集跑 v2，两次运行指标均可查
	run2, err := evalRuns.CreateRun(ctx, eval.CreateRunInput{DatasetID: ds.ID, AgentVersion: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	waitEval(run2.ID, "SUCCEEDED")
	list, err := evalRuns.ListRuns(ctx, ds.ID, "")
	if err != nil || len(list) != 2 {
		t.Fatalf("版本对比数据缺失: %v %d", err, len(list))
	}

	// ③ 失败任务诊断：引用真实证据
	var failedTaskID string
	if err := st.Pool().QueryRow(ctx, `
		SELECT t.id::text FROM tasks t
		JOIN evaluation_results er ON er.task_id = t.id
		WHERE er.eval_run_id=$1 AND er.status='FAILED' LIMIT 1`, run1.ID).Scan(&failedTaskID); err != nil {
		t.Fatalf("找失败样例: %v", err)
	}
	resp, err := http.Get(fmt.Sprintf("%s/api/v1/tasks/%s/diagnosis", apiSrv.URL, failedTaskID))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("诊断 HTTP %d", resp.StatusCode)
	}
	var env struct {
		Data []struct {
			FaultCode  string          `json:"fault_code"`
			Confidence string          `json:"confidence"`
			Evidence   json.RawMessage `json:"evidence"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil || len(env.Data) == 0 {
		t.Fatalf("诊断报告缺失: %v", err)
	}
	rep := env.Data[0]
	if rep.FaultCode != "TOOL_ARGUMENT_ERROR" {
		t.Fatalf("故障码 = %s，期望 TOOL_ARGUMENT_ERROR（任务 error_code=ARGUMENT_INVALID）", rep.FaultCode)
	}
	if len(rep.Evidence) < 4 { // 证据必须引用真实对象
		t.Fatalf("证据为空或过短: %s", rep.Evidence)
	}

	// ④ 复查：诊断有幂等的报告列表（第二次查询直接返回已有报告）
	resp2, _ := http.Get(fmt.Sprintf("%s/api/v1/tasks/%s/diagnosis", apiSrv.URL, failedTaskID))
	defer resp2.Body.Close()
	var env2 struct {
		Data []json.RawMessage `json:"data"`
	}
	_ = json.NewDecoder(resp2.Body).Decode(&env2)
	if len(env2.Data) != 1 {
		t.Fatalf("重复诊断应复用报告，当前 %d 份", len(env2.Data))
	}
}
