// Package httpapi 提供 /api/v1 对外路由（api-contract.md 契约实现）。
package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agentops/platform/internal/gateway"
	"github.com/agentops/platform/internal/task"
)

// Pinger 抽象依赖连通性检查，测试可注入假实现而不依赖真实数据库。
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps 汇总路由层依赖；Pool/Tasks/GW 为 nil 时不注册对应路由（骨架/测试模式）。
type Deps struct {
	DB           Pinger
	Pool         *pgxpool.Pool
	Tasks        *task.Store
	GW           *gateway.Gateway
	ServiceToken string
	Eval         *EvalAPI
}

// New 构建 Gin 引擎并注册对外路由。
func New(d Deps) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), RequestID())
	r.GET("/api/v1/health", healthHandler(d))

	if d.Tasks != nil && d.Pool != nil {
		a := &taskAPI{tasks: d.Tasks, pool: d.Pool}
		r.POST("/api/v1/tasks", a.createTask)
		r.GET("/api/v1/tasks", a.listTasks)
		r.GET("/api/v1/tasks/:id", a.getTask)
		r.POST("/api/v1/tasks/:id/cancel", a.cancelTask)
		r.POST("/api/v1/tasks/:id/retry", a.retryTask)
		r.GET("/api/v1/tasks/:id/events", a.listEvents)
		r.GET("/api/v1/tasks/:id/trace", a.getTrace)
	}
	if d.GW != nil {
		r.GET("/api/v1/tools", listTools(d.GW))
		r.POST("/api/v1/tool-calls", serviceToken(d.ServiceToken), invokeTool(d.GW))
	}
	if d.Eval != nil {
		r.POST("/api/v1/datasets", d.Eval.importDataset)
		r.GET("/api/v1/datasets", d.Eval.listDatasets)
		r.POST("/api/v1/evaluations", d.Eval.createEvaluation)
		r.GET("/api/v1/evaluations", d.Eval.listEvaluations)
		r.GET("/api/v1/evaluations/:id", d.Eval.getEvaluation)
		r.GET("/api/v1/tasks/:id/diagnosis", d.Eval.getDiagnosis)
		r.GET("/api/v1/agents", d.Eval.listAgents)
	}
	return r
}

// serviceToken 校验内部服务令牌（/tool-calls 供 Python 执行期调用）。
func serviceToken(required string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if required == "" || c.GetHeader("X-Service-Token") != required {
			Fail(c, errUnauthorized, "服务令牌缺失或不正确", nil)
			c.Abort()
			return
		}
		c.Next()
	}
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
			"request_id":   uuid.NewString(),
		})
	}
}
