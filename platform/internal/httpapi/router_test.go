package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type stubPinger struct{ err error }

func (s stubPinger) Ping(ctx context.Context) error { return s.err }

func setupRouter(p Pinger) *gin.Engine {
	gin.SetMode(gin.TestMode)
	return New(Deps{DB: p})
}

// 依赖健康时整体应为 ok。
func TestHealthUp(t *testing.T) {
	srv := httptest.NewServer(setupRouter(stubPinger{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200", resp.StatusCode)
	}
	var body struct {
		Status       string `json:"status"`
		Dependencies struct {
			Postgres string `json:"postgres"`
		} `json:"dependencies"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "ok" || body.Dependencies.Postgres != "up" {
		t.Fatalf("健康体不符合预期: %+v", body)
	}
}

// 依赖故障时接口仍应 200 返回 degraded（§13.5 故障隔离），而不是 5xx。
func TestHealthDegraded(t *testing.T) {
	srv := httptest.NewServer(setupRouter(stubPinger{err: errors.New("db down")}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Status       string `json:"status"`
		Dependencies struct {
			Postgres string `json:"postgres"`
		} `json:"dependencies"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "degraded" || body.Dependencies.Postgres != "down" {
		t.Fatalf("降级健康体不符合预期: %+v", body)
	}
}
