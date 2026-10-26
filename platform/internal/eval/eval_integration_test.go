//go:build integration

// 阶段 D 集成测试：数据集导入、评测协调器（桩 Python）、诊断（桩 Python）。
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentops/platform/internal/diagnosis"
	"github.com/agentops/platform/internal/task"
	"github.com/agentops/platform/internal/testutil"
)

func newEnv(t *testing.T) (*Store, *RunService) {
	t.Helper()
	st := testutil.NewStore(t)
	testutil.Truncate(t, st)
	store := NewStore(st.Pool())
	runs := NewRunService(st.Pool(), store, "http://stub.invalid", "test", 20*time.Millisecond)
	return store, runs
}

func twoCases() []CaseInput {
	return []CaseInput{
		{InputTask: map[string]any{"task_type": "mock.echo", "input": map[string]any{"m": "1"}},
			ScoringRules: []map[string]any{{"type": "finished_normally"}}},
		{InputTask: map[string]any{"task_type": "mock.echo", "input": map[string]any{"m": "2"}},
			ScoringRules: []map[string]any{{"type": "finished_normally"}}},
	}
}

// T2：导入、重复版本 409 语义、列表。
func TestDatasetImportAndConflict(t *testing.T) {
	store, _ := newEnv(t)
	ctx := context.Background()

	d, err := store.Import(ctx, "demo", 1, "", twoCases())
	if err != nil {
		t.Fatal(err)
	}
	if d.CaseCount != 2 {
		t.Fatalf("样例数 = %d", d.CaseCount)
	}
	if _, err := store.Import(ctx, "demo", 1, "", twoCases()); err != ErrDatasetExists {
		t.Fatalf("同版本应冲突: %v", err)
	}
	if _, err := store.Import(ctx, "demo", 2, "", twoCases()); err != nil {
		t.Fatalf("新版本应可导入: %v", err)
	}
	list, err := store.ListDatasets(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("列表异常: %v %d", err, len(list))
	}
	// 非法样例（缺 task_type）被拒
	if _, err := store.Import(ctx, "bad", 1, "", []CaseInput{{}}); err == nil {
		t.Fatal("缺 task_type 应被拒绝")
	}
}

// waitRun 轮询评测运行状态。
func waitRun(t *testing.T, runs *RunService, id, want string, timeout time.Duration) *Run {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r, _, err := runs.GetRun(context.Background(), id)
		if err == nil && r.Status == want {
			return r
		}
		time.Sleep(30 * time.Millisecond)
	}
	r, _, _ := runs.GetRun(context.Background(), id)
	t.Fatalf("等待评测 %s 超时，当前 %s", want, r.Status)
	return nil
}

// T6：协调器对桩 Python 的完整轮询与落库。
func TestCoordinatorPersistsResults(t *testing.T) {
	store, runs := newEnv(t)
	ctx := context.Background()
	d, err := store.Import(ctx, "demo", 1, "", twoCases())
	if err != nil {
		t.Fatal(err)
	}
	_, cases, err := store.DatasetWithCases(ctx, d.ID)
	if err != nil || len(cases) != 2 {
		t.Fatalf("样例读取失败: %v", err)
	}

	var polls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["eval_run_id"] == "" {
				w.WriteHeader(400)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"data":{"id":"x","status":"PENDING"}}`)
			return
		}
		if atomic.AddInt32(&polls, 3) < 6 { // 前两次 RUNNING
			fmt.Fprint(w, `{"data":{"status":"RUNNING"}}`)
			return
		}
		fmt.Fprintf(w, `{"data":{"status":"SUCCEEDED",
		  "metrics":{"task_success_rate":{"value":1.0,"caliber":"per-task"}},
		  "results":[
		    {"case_id":"%s","task_id":null,"status":"SUCCEEDED","scores":[],"all_rules_pass":true,"token_usage":{},"duration_ms":10,"error_code":""},
		    {"case_id":"%s","task_id":null,"status":"FAILED","scores":[],"all_rules_pass":false,"token_usage":{},"duration_ms":10,"error_code":"X"}]}}`,
			cases[0].ID, cases[1].ID)
	}))
	defer ts.Close()
	runs.agentURL = ts.URL

	run, err := runs.CreateRun(ctx, CreateRunInput{DatasetID: d.ID, AgentVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	got := waitRun(t, runs, run.ID, "SUCCEEDED", 5*time.Second)

	var m map[string]any
	if err := json.Unmarshal(got.Metrics, &m); err != nil || m["task_success_rate"] == nil {
		t.Fatalf("指标未落库: %s %v", got.Metrics, err)
	}
	var rows int
	if err := store.pool.QueryRow(ctx,
		`SELECT count(*) FROM evaluation_results WHERE eval_run_id=$1`, run.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("应落 2 条样例结果，实际 %d", rows)
	}
}

// T6：Python 不可用 → run 置 FAILED(AGENT_SERVICE_UNAVAILABLE)，可对比错误码。
func TestCoordinatorServiceUnavailable(t *testing.T) {
	store, runs := newEnv(t)
	d, err := store.Import(context.Background(), "demo", 1, "", twoCases())
	if err != nil {
		t.Fatal(err)
	}
	run, err := runs.CreateRun(context.Background(), CreateRunInput{DatasetID: d.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, runs, run.ID, "FAILED", 5*time.Second)
	r, _, _ := runs.GetRun(context.Background(), run.ID)
	if r.ErrorCode == nil || *r.ErrorCode != "AGENT_SERVICE_UNAVAILABLE" {
		t.Fatalf("错误码: %v", r.ErrorCode)
	}
}

// T9：诊断服务（桩 Python 即时报告）→ 落库与报告列表。
func TestDiagnosisPersistsReport(t *testing.T) {
	st := testutil.NewStore(t)
	testutil.Truncate(t, st)
	ctx := context.Background()
	ts := task.NewStore(st.Pool())

	tk, _, err := ts.Create(ctx, task.CreateInput{
		TaskType: "mock.fail", Input: []byte(`{}`), MaxAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 直接构造失败终态与一条参数拒绝的调用证据
	if _, err := st.Pool().Exec(ctx,
		`UPDATE tasks SET status='FAILED', error_code='ARGUMENT_INVALID', finished_at=now() WHERE id=$1`, tk.ID); err != nil {
		t.Fatal(err)
	}

	tsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"data":{"id":"x","status":"SUCCEEDED"}}`)
			return
		}
		fmt.Fprint(w, `{"data":{"fault_code":"TOOL_ARGUMENT_ERROR","confidence":"MEDIUM",
		  "root_cause_hypothesis":"参数非法","evidence":[{"type":"task","ref":"`+tk.ID+`","detail":"input"}],
		  "suggestions":["修正参数"],"need_human":false,"diagnoser_version":"rule-v1"}}`)
	}))
	defer tsrv.Close()

	svc := diagnosis.NewService(st.Pool(), tsrv.URL, "test")
	rep, err := svc.Diagnose(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.FaultCode != "TOOL_ARGUMENT_ERROR" || rep.Confidence != "MEDIUM" {
		t.Fatalf("报告字段异常: %+v", rep)
	}
	list, err := svc.ListReports(ctx, tk.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("报告列表: %v %d", err, len(list))
	}
}
