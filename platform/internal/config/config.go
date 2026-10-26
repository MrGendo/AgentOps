// Package config 负责从环境变量加载运行配置；全部提供安全默认值。
package config

import (
	"os"
	"strconv"
)

// Config 汇总平台运行所需配置；阶段 B 起逐步启用调度与租约相关字段。
type Config struct {
	DatabaseURL       string // PostgreSQL 连接串
	Port              string // HTTP 监听端口
	Role              string // 进程角色：all|server|worker（架构文档 D-2）
	WorkerConcurrency int    // Worker 并发领取数
	LeaseTTLSeconds   int    // 租约时长；心跳与过期检测共用
	ServiceToken      string // 内部接口服务令牌（Python 调用 Go 时校验）
}

// Load 按环境变量读取配置，缺省值满足本地开发（.env.example 与此对齐）。
func Load() Config {
	return Config{
		DatabaseURL:       env("AGENTOPS_DATABASE_URL", "postgres://localhost:5432/agentops_dev?sslmode=disable"),
		Port:              env("AGENTOPS_PORT", "8080"),
		Role:              env("AGENTOPS_ROLE", "all"),
		WorkerConcurrency: envInt("AGENTOPS_WORKER_CONCURRENCY", 2),
		LeaseTTLSeconds:   envInt("AGENTOPS_LEASE_TTL_SECONDS", 30),
		ServiceToken:      env("AGENTOPS_SERVICE_TOKEN", "dev-service-token"),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
