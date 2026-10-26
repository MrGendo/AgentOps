//go:build integration

// AgentExecutor 集成测试：httptest 桩 Python + 真实 PG。
// T9 的 L4 双进程用例在文件末尾（依赖本地 uvicorn，未启动时显式 SKIP）。
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentops/platform/internal/gateway"
	"github.com/agentops/platform/internal/httpapi"
	"github.com/agentops/platform/internal/task"
	"github.com/agentops/platform/internal/testutil"
)

func setupWithAgent(t *testing.T, agentHandler http.HandlerFunc) (*Manager, *task.Store, *httptest.Server) {
	t.Helper()
	st := testutil.NewStore(t)
	testutil.Truncate(t, st)
	tools, err := gateway.DefaultTools(st.Pool(), "../../knowledge")
	if err != nil {
		t.Fatal(err)
	}
	gw := gateway.New(st.Pool(), tools...)
	ts := httptest.NewServer(agentHandler)
	t.Cleanup(ts.Close)
	m := NewManager(fastCfg(), st.Pool(), gw,
		NewAgentExecutor(ts.URL, "test", 20*time.Millisecond, st.Pool()))
	return m, task.NewStore(st.Pool()), ts
}

func agentTask(t *testing.T, ts *task.Store, allowed []string) *task.Task {
	t.Helper()
	tk, _, err := ts.Create(context.Background(), task.CreateInput{
		TaskType:     "agent.ops_diagnosis",
		Input:        json.RawMessage(`{"service":"order-api","描述":"响应变慢 诊断"}`),
		AllowedTools: allowed,
	})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

// 成功路径：RUNNING→SUCCEEDED，agent_runs 落库，任务成功。
func TestAgentExecutorSuccess(t *testing.T) {
	var calls int32
	runID := "11111111-1111-4111-8111-111111111111"
	m, ts, _ := setupWithAgent(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/internal/v1/agent-runs" && r.Method == "POST":
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"data":{"id":"`+runID+`","status":"PENDING"}}`)
		case r.URL.Path == "/internal/v1/agent-runs/"+runID:
			if atomic.AddInt32(&calls, 1) < 2 {
				fmt.Fprint(w, `{"data":{"id":"`+runID+`","status":"RUNNING"}}`)
				return
			}
			fmt.Fprint(w, `{"data":{"id":"`+runID+`","task_id":"x","status":"SUCCEEDED",
				"finish_reason":"COMPLETED","final_output":{"report":{"root_cause":"下游劣化"}},
				"plan":[{"name":"检索知识库"}],"plan_valid":true,
				"trajectory_digest":{"model_calls":5,"tool_calls":3,"iterations":4},
				"token_usage":{"prompt_tokens":600,"completion_tokens":300,"total_tokens":900}}}`)
		}
	})

	tk := agentTask(t, ts, []string{"knowledge_search", "calculator", "mock_http"})
	if _, err := m.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := waitStatus(t, ts, tk.ID, "SUCCEEDED", 5*time.Second)

	var runStatus, finish string
	var toolCalls, tokens int
	err := m.Pool().QueryRow(context.Background(), `
		SELECT status, COALESCE(finish_reason,''), tool_call_count,
		      (token_usage->>'total_tokens')::int
		FROM agent_runs WHERE task_id=$1`, tk.ID).
		Scan(&runStatus, &finish, &toolCalls, &tokens)
	if err != nil {
		t.Fatalf("agent_runs 未落库: %v", err)
	}
	if runStatus != "SUCCEEDED" || finish != "COMPLETED" || toolCalls != 3 || tokens != 900 {
		t.Fatalf("agent_runs 摘要异常: %s/%s/%d/%d", runStatus, finish, toolCalls, tokens)
	}
	_ = got
}

// 服务不可用：归类 AGENT_SERVICE_UNAVAILABLE（可重试），任务退避重排。
func TestAgentExecutorUnavailableRetries(t *testing.T) {
	m, ts, _ := setupWithAgent(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	tk := agentTask(t, ts, nil)

	if _, err := m.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := ts.Get(context.Background(), tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "PENDING" {
		t.Fatalf("服务不可用应退避重排，当前 %s（err=%v）", got.Status, got.ErrorCode)
	}
	if got.ErrorCode == nil || *got.ErrorCode != "TRANSIENT_NETWORK" {
		t.Fatalf("错误类别应为 TRANSIENT_NETWORK: %v", got.ErrorCode)
	}
	var n int
	_ = m.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM task_events WHERE task_id=$1 AND event_type='task.retry_scheduled'`,
		tk.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("应留 1 条重排事件，实际 %d", n)
	}
}

// Agent 失败映射：RATE_LIMITED 可重试；其余（如 INVALID_PLAN）直接失败。
func TestAgentExecutorFailureMapping(t *testing.T) {
	runID := "22222222-2222-4222-8222-222222222222"
	newStub := func(status, reason, errCode string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == "POST" && r.URL.Path == "/internal/v1/agent-runs" {
				w.WriteHeader(http.StatusAccepted)
				fmt.Fprint(w, `{"data":{"id":"`+runID+`","status":"PENDING"}}`)
				return
			}
			fmt.Fprintf(w, `{"data":{"id":"%s","status":"%s","finish_reason":"%s",
				"error":{"code":"%s","message":"注入"}}}`, runID, status, reason, errCode)
		}
	}

	// RATE_LIMITED → 重排
	m, ts, _ := setupWithAgent(t, newStub("FAILED", "MODEL_ERROR", "RATE_LIMITED"))
	tk := agentTask(t, ts, nil)
	if _, err := m.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := ts.Get(context.Background(), tk.ID)
	if got.Status != "PENDING" {
		t.Fatalf("RATE_LIMITED 应可重试，当前 %s", got.Status)
	}

	// INVALID_PLAN → 直接失败
	m2, ts2, _ := setupWithAgent(t, newStub("FAILED", "INVALID_PLAN", "INVALID_OUTPUT"))
	tk2 := agentTask(t, ts2, nil)
	if _, err := m2.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got2 := waitStatus(t, ts2, tk2.ID, "FAILED", 5*time.Second)
	if got2.AttemptCount != 1 {
		t.Fatalf("不可重试失败应 1 次尝试，实际 %d", got2.AttemptCount)
	}
}

// 轮询期间取消：检查点命中 → 通知 Python 取消 → 任务 CANCELLED。
func TestAgentExecutorCancelDuringPoll(t *testing.T) {
	runID := "33333333-3333-4333-8333-333333333333"
	cancelCalled := make(chan struct{}, 1)
	m, ts, _ := setupWithAgent(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/internal/v1/agent-runs":
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"data":{"id":"`+runID+`","status":"PENDING"}}`)
		case r.URL.Path == "/internal/v1/agent-runs/"+runID+"/cancel":
			cancelCalled <- struct{}{}
			fmt.Fprint(w, `{"data":{"id":"`+runID+`","cancel_requested":true}}`)
		default:
			fmt.Fprint(w, `{"data":{"id":"`+runID+`","status":"RUNNING"}}`) // 永远运行中
		}
	})
	tk := agentTask(t, ts, nil)

	go func() { // 并发请求取消，触发执行器轮询间隙的检查点
		time.Sleep(150 * time.Millisecond)
		_, _ = ts.Cancel(context.Background(), tk.ID)
	}()

	if _, err := m.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, ts, tk.ID, "CANCELLED", 5*time.Second)
	select {
	case <-cancelCalled:
	default:
		t.Fatal("应尽力通知 Python 侧取消")
	}
}

// L4（§17-C 验收）：双进程真实链路。
// 测试进程内起 httptest 网关（HTTP 形态），并拉起真实 uvicorn 子进程（Mock 模型），
// env 把 Python 的工具调用指回本测试的网关——不依赖任何手工准备的服务。
func TestL4DualProcessAgentTask(t *testing.T) {
	const pyPort = "8901"
	agentURL := "http://127.0.0.1:" + pyPort
	st := testutil.NewStore(t)
	testutil.Truncate(t, st)
	tools, err := gateway.DefaultTools(st.Pool(), "../../knowledge")
	if err != nil {
		t.Fatal(err)
	}
	gw := gateway.New(st.Pool(), tools...)

	// ① HTTP 形态的工具网关（Python 经此调用，等价于真实 Go 服务）
	apiSrv := httptest.NewServer(httpapi.New(httpapi.Deps{
		DB: st, Pool: st.Pool(), Tasks: task.NewStore(st.Pool()),
		GW: gw, ServiceToken: "dev-service-token",
	}))
	defer apiSrv.Close()

	// ② 真实 uvicorn 子进程；Python 服务缺失时显式 SKIP（§16.7）
	// 直接用 venv 的 python -m uvicorn 启动；exec 的相对路径以 cmd.Dir 为基准，
	// 这里统一转绝对路径，避免测试进程 cwd 与子进程 Dir 的双重解析陷阱
	pyBin, _ := filepath.Abs("../../../agent/.venv/bin/python3")
	pyDir, _ := filepath.Abs("../../../agent")
	if _, err := os.Stat(pyBin); err != nil {
		t.Skipf("SKIP（未执行）: 未找到 %s，请先构建 agent/.venv", pyBin)
	}
	var pyLogs bytes.Buffer
	cmd := exec.Command(pyBin, "-m", "uvicorn", "app.main:app", "--port", pyPort, "--log-level", "info")
	cmd.Dir = pyDir
	cmd.Env = append(os.Environ(),
		"AGENTOPS_PLATFORM_URL="+apiSrv.URL,
		"AGENTOPS_SERVICE_TOKEN=dev-service-token",
		"PYTHONUNBUFFERED=1",
	)
	cmd.Stderr = &pyLogs
	if err := cmd.Start(); err != nil {
		t.Skipf("SKIP（未执行）: 启动 Python 服务失败: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	up := false
	for i := 0; i < 50; i++ {
		if resp, err := http.Get(agentURL + "/internal/v1/health"); err == nil {
			resp.Body.Close()
			up = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !up {
		t.Fatalf("Python 服务 10s 未就绪:\n%s", pyLogs.String())
	}

	m := NewManager(fastCfg(), st.Pool(), gw,
		NewAgentExecutor(agentURL, "dev-service-token", 50*time.Millisecond, st.Pool()))
	ts := task.NewStore(st.Pool())

	tk, _, err := ts.Create(context.Background(), task.CreateInput{
		TaskType:     "agent.ops_diagnosis",
		Input:        json.RawMessage(`{"service":"order-api","描述":"近期响应变慢，请诊断"}`),
		AllowedTools: []string{"knowledge_search", "calculator", "mock_http"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// 轮询至终态；失败时转储 Python 日志与任务错误，便于定位
	deadline := time.Now().Add(30 * time.Second)
	final := ""
	for time.Now().Before(deadline) {
		got, _ := ts.Get(context.Background(), tk.ID)
		if got.Status == "SUCCEEDED" || got.Status == "FAILED" {
			final = got.Status
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if final != "SUCCEEDED" {
		got, _ := ts.Get(context.Background(), tk.ID)
		t.Fatalf("任务终态=%s err=%v finish=%v\nPython 日志:\n%s",
			got.Status, got.ErrorCode, got.FinishReason, pyLogs.String())
	}

	// 断言：agent_runs 落库、至少 3 次真实网关工具调用、任务轨迹完整
	var toolCalls, runRows int
	if err := m.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM tool_calls WHERE task_id=$1 AND status='SUCCEEDED'`,
		tk.ID).Scan(&toolCalls); err != nil {
		t.Fatal(err)
	}
	if toolCalls < 3 {
		t.Fatalf("Agent 应经网关至少 3 次工具调用，实际 %d", toolCalls)
	}
	if err := m.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM agent_runs WHERE task_id=$1 AND status='SUCCEEDED'`,
		tk.ID).Scan(&runRows); err != nil {
		t.Fatal(err)
	}
	if runRows != 1 {
		t.Fatalf("agent_runs 应有 1 行 SUCCEEDED，实际 %d", runRows)
	}
}
