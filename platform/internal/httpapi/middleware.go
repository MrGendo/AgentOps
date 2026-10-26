package httpapi

// 请求标识中间件与统一响应包络（api-contract.md §1）。

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const ctxRequestID = "request_id"

// RequestID 为每个请求生成关联 ID（缺失时生成），回写响应头供调用方排障。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}
		c.Set(ctxRequestID, id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

// Data 标准成功包络：{data, meta}。
func Data(c *gin.Context, status int, data any, meta any) {
	if meta == nil {
		meta = gin.H{}
	}
	c.JSON(status, gin.H{"data": data, "meta": meta})
}

// apiErr 稳定错误码定义（api-contract.md §4 错误码表）。
type apiErr struct {
	Status    int
	Code      string
	Retryable bool
}

var (
	errValidation       = apiErr{400, "VALIDATION_ERROR", false}
	errTaskNotFound     = apiErr{404, "TASK_NOT_FOUND", false}
	errToolNotFound     = apiErr{404, "TOOL_NOT_FOUND", false}
	errAlreadyFinished  = apiErr{409, "TASK_ALREADY_FINISHED", false}
	errNotRetriable     = apiErr{409, "TASK_NOT_RETRIABLE", false}
	errIdemConflict     = apiErr{409, "IDEMPOTENCY_CONFLICT", false}
	errUnauthorized     = apiErr{401, "UNAUTHORIZED", false}
	errPermission       = apiErr{403, "PERMISSION_DENIED", false}
	errRateLimited      = apiErr{429, "RATE_LIMITED", true}
	errAgentUnavailable = apiErr{503, "AGENT_SERVICE_UNAVAILABLE", true}
	errDBUnavailable    = apiErr{503, "DB_UNAVAILABLE", true}
	errInternal         = apiErr{500, "INTERNAL_ERROR", false}
)

// Fail 标准错误包络：{error:{code,message,retryable,request_id,details}}。
func Fail(c *gin.Context, e apiErr, message string, details any) {
	if details == nil {
		details = gin.H{}
	}
	c.JSON(e.Status, gin.H{"error": gin.H{
		"code": e.Code, "message": message,
		"retryable":  e.Retryable,
		"request_id": c.GetString(ctxRequestID),
		"details":    details,
	}})
}
