// cmd/server 是 AgentOps Go 核心执行平台入口。
// 模块化阶段 Server 与 Worker 同二进制（架构文档 D-2），角色由 AGENTOPS_ROLE 控制：
// all = HTTP + Worker；server = 仅 HTTP；worker = 仅 Worker。
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/agentops/platform/internal/config"
	"github.com/agentops/platform/internal/diagnosis"
	"github.com/agentops/platform/internal/eval"
	"github.com/agentops/platform/internal/gateway"
	"github.com/agentops/platform/internal/httpapi"
	"github.com/agentops/platform/internal/store"
	"github.com/agentops/platform/internal/task"
	"github.com/agentops/platform/internal/worker"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("server 退出: %v", err)
	}
}

func run() error {
	cfg := config.Load()

	// 启动期带短重试连接数据库，避免 compose 环境下 PG 尚未就绪即退出。
	var st *store.Store
	var err error
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		st, err = store.New(ctx, cfg.DatabaseURL)
		cancel()
		if err == nil {
			break
		}
		log.Printf("等待数据库就绪（第 %d 次）: %v", i+1, err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return err
	}
	defer st.Close()

	// 工具网关：四内置工具 + 知识库目录注入
	tools, err := gateway.DefaultTools(st.Pool(), cfg.KnowledgeDir)
	if err != nil {
		return err
	}
	gw := gateway.New(st.Pool(), tools...)

	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Worker：按角色启动
	if cfg.Role != "server" {
		m := worker.NewManager(worker.Config{
			ID:             "worker-" + hostname(),
			Concurrency:    cfg.WorkerConcurrency,
			LeaseTTL:       time.Duration(cfg.LeaseTTLSeconds) * time.Second,
			RetryBase:      time.Duration(cfg.RetryBaseSeconds) * time.Second,
			RetryMaxWait:   time.Duration(cfg.RetryMaxWaitSecond) * time.Second,
			ReaperInterval: time.Duration(cfg.ReaperIntervalMS) * time.Millisecond,
		}, st.Pool(), gw, append(worker.DefaultExecutors(),
			worker.NewAgentExecutor(cfg.AgentBaseURL, cfg.ServiceToken, time.Duration(cfg.AgentPollIntervalMS)*time.Millisecond, st.Pool()))...)
		m.Start(rootCtx)
		log.Printf("Worker 已启动（并发=%d，类型=%v）", cfg.WorkerConcurrency, m.SupportedTypes())
	}

	if cfg.Role == "worker" {
		<-rootCtx.Done()
		return nil
	}

	evalStore := eval.NewStore(st.Pool(), cfg.AgentBaseURL, cfg.ServiceToken)
	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: httpapi.New(httpapi.Deps{
			DB: st, Pool: st.Pool(), Tasks: task.NewStore(st.Pool(), cfg.AgentBaseURL, cfg.ServiceToken), GW: gw,
			ServiceToken: cfg.ServiceToken,
			Eval: httpapi.NewEvalAPI(
				evalStore,
				eval.NewRunService(st.Pool(), evalStore, cfg.AgentBaseURL, cfg.ServiceToken,
					time.Duration(cfg.AgentPollIntervalMS)*time.Millisecond),
				diagnosis.NewService(st.Pool(), cfg.AgentBaseURL, cfg.ServiceToken),
				st.Pool(), cfg.AgentBaseURL, cfg.ServiceToken,
			),
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("AgentOps Go 平台监听 :%s（role=%s）", cfg.Port, cfg.Role)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP 服务异常: %v", err)
		}
	}()

	// 优雅退出：收到信号后给在途请求 5 秒收尾
	<-rootCtx.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "local"
	}
	return h
}
