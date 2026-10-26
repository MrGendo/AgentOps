package config

import "testing"

// 默认值应满足本地开发零配置启动。
func TestLoadDefaults(t *testing.T) {
	c := Load()
	if c.DatabaseURL == "" || c.Port != "8080" || c.Role != "all" {
		t.Fatalf("默认配置不符合预期: %+v", c)
	}
	if c.WorkerConcurrency != 2 || c.LeaseTTLSeconds != 30 {
		t.Fatalf("调度默认值不符合预期: %+v", c)
	}
}

// 环境变量覆盖应生效。
func TestLoadOverrides(t *testing.T) {
	t.Setenv("AGENTOPS_PORT", "9090")
	t.Setenv("AGENTOPS_WORKER_CONCURRENCY", "8")
	c := Load()
	if c.Port != "9090" || c.WorkerConcurrency != 8 {
		t.Fatalf("环境变量覆盖未生效: %+v", c)
	}
}
