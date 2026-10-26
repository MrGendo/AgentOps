// cmd/server 是 AgentOps Go 核心执行平台入口。
// 模块化阶段 Server 与 Worker 同二进制（架构文档 D-2），角色由 AGENTOPS_ROLE 控制。
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
	"github.com/agentops/platform/internal/httpapi"
	"github.com/agentops/platform/internal/store"
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

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           httpapi.New(httpapi.Deps{DB: st}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("AgentOps Go 平台监听 :%s（role=%s）", cfg.Port, cfg.Role)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP 服务异常: %v", err)
		}
	}()

	// 优雅退出：收到信号后给在途请求 5 秒收尾。
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}
