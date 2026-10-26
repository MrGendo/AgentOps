// Package httpapi 提供 /api/v1 对外路由；阶段 B 起按契约逐模块扩展。
package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Pinger 抽象依赖连通性检查，测试可注入假实现而不依赖真实数据库。
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps 汇总路由层依赖。
type Deps struct {
	DB Pinger
}

// New 构建 Gin 引擎并注册对外路由。
func New(d Deps) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/api/v1/health", healthHandler(d))
	return r
}

// healthHandler 报告自身与依赖状态；依赖故障时整体降级为 degraded 而非报错，
// 以满足需求 §13.5「服务故障隔离：查询能力不因依赖故障而不可用」。
func healthHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		pg := "up"
		if d.DB == nil || d.DB.Ping(ctx) != nil {
			pg = "down"
		}
		status := "ok"
		if pg == "down" {
			status = "degraded"
		}
		c.JSON(http.StatusOK, gin.H{
			"status":       status,
			"dependencies": gin.H{"postgres": pg},
		})
	}
}
