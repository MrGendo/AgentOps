package gateway

// L1 单元测试：参数校验器、表达式求值器、无依赖工具（不触碰数据库）。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 求值器：合法表达式与运算优先级。
func TestEvalExpression(t *testing.T) {
	cases := map[string]float64{
		"1+2":           3,
		"1+2*3":         7,
		"2*(3+4)":       14,
		"-3+5":          2,
		"10/4":          2.5,
		"((1+2)*(3+4))": 21,
		"100 - 20 * 3":  40,
	}
	for expr, want := range cases {
		got, err := EvalExpression(expr)
		if err != nil {
			t.Fatalf("EvalExpression(%q) 报错: %v", expr, err)
		}
		if got != want {
			t.Fatalf("EvalExpression(%q) = %v, 期望 %v", expr, got, want)
		}
	}
}

// 求值器：非法表达式必须报错（模式校验之外的第二道防线）。
func TestEvalExpressionInvalid(t *testing.T) {
	for _, expr := range []string{"", "1+", "1 2", "(1+2", "1/0", "abc", "1.2.3"} {
		if _, err := EvalExpression(expr); err == nil {
			t.Fatalf("EvalExpression(%q) 应报错", expr)
		}
	}
}

// 校验器：必填/类型/枚举/长度边界。
func TestValidator(t *testing.T) {
	sch := &Schema{
		Type:     "object",
		Required: []string{"mode"},
		Properties: map[string]*Schema{
			"mode":     {Type: "string", Enum: []string{"success", "error"}},
			"delay_ms": {Type: "integer", Minimum: ptrFloat(0), Maximum: ptrFloat(5000)},
			"body":     {Type: "object"},
		},
	}
	if err := sch.ValidateParams(Params{"mode": "success"}); err != nil {
		t.Fatalf("合法参数被拒: %v", err)
	}
	if err := sch.ValidateParams(Params{}); err == nil {
		t.Fatal("缺必填应被拒绝")
	}
	if err := sch.ValidateParams(Params{"mode": "timeout"}); err == nil {
		t.Fatal("枚举外取值应被拒绝")
	}
	if err := sch.ValidateParams(Params{"mode": "success", "delay_ms": 9999}); err == nil {
		t.Fatal("超上限应被拒绝")
	}
	if err := sch.ValidateParams(Params{"mode": "success", "delay_ms": "10"}); err == nil {
		t.Fatal("字符串冒充整数应被拒绝")
	}
	if err := sch.ValidateParams(Params{"mode": "success", "extra": 1}); err == nil {
		t.Fatal("未知参数应被拒绝")
	}
	// 自由对象：body 内任意键合法，但 body 本身替换为字符串仍应被拒
	if err := (&Schema{Type: "object", FreeForm: true}).ValidateParams(Params{"any": 1}); err != nil {
		t.Fatalf("FreeForm 对象应放行任意键: %v", err)
	}

	// calculator 的 pattern：字母注入必须被拒
	calc := CalculatorTool()
	if err := calc.Schema.ValidateParams(Params{"expression": "1+2;rm -rf /"}); err == nil {
		t.Fatal("表达式含非法字符必须被拒绝")
	}
	if err := calc.Schema.ValidateParams(Params{"expression": "(1+2)*3"}); err != nil {
		t.Fatalf("合法表达式被拒: %v", err)
	}
}

// 知识库：分段、打分与 top_k。
func TestKnowledgeSearch(t *testing.T) {
	dir := t.TempDir()
	content := "# KB\n\n## 慢查询\n\n慢查询导致响应变慢，检查执行计划。\n\n## 内存泄漏\n\nRSS 持续上升。\n"
	if err := os.WriteFile(filepath.Join(dir, "kb.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := LoadKnowledge(dir)
	if err != nil {
		t.Fatal(err)
	}
	tool := KnowledgeSearchTool(idx)
	out, err := tool.Exec(context.Background(), Params{"query": "响应变慢 慢查询", "top_k": 1})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	results := res["results"].([]map[string]any)
	if len(results) != 1 {
		t.Fatalf("top_k=1 应只返回 1 条，得到 %d", len(results))
	}
	if results[0]["title"] != "慢查询" {
		t.Fatalf("命中的应是慢查询条目，得到 %v", results[0]["title"])
	}
	// 目录不存在：空知识库不报错
	idx2, err := LoadKnowledge(filepath.Join(dir, "not-exist"))
	if err != nil {
		t.Fatal(err)
	}
	if len(idx2.sections) != 0 {
		t.Fatal("不存在目录应为空知识库")
	}
}

// mock_http：success/error/lost_response 三模式；timeout 模式由网关超时判定（集成测试覆盖）。
func TestMockHTTPTool(t *testing.T) {
	tool := MockHTTPTool()
	ctx := context.Background()

	out, err := tool.Exec(ctx, Params{"mode": "success", "body": map[string]any{"x": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if out.(map[string]any)["http_status"].(int) != 200 {
		t.Fatal("success 模式应返回 200")
	}

	if _, err := tool.Exec(ctx, Params{"mode": "error"}); err == nil {
		t.Fatal("error 模式应返回错误")
	} else {
		var te *ToolError
		if !errors.As(err, &te) || te.Code != "TOOL_EXECUTION_ERROR" {
			t.Fatalf("error 模式应为 TOOL_EXECUTION_ERROR: %v", err)
		}
	}

	if _, err := tool.Exec(ctx, Params{"mode": "lost_response"}); !errors.Is(err, ErrLostResponse) {
		t.Fatal("lost_response 模式应返回 ErrLostResponse")
	}

	// timeout 模式：短超时 ctx 触发 DeadlineExceeded
	tctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := tool.Exec(tctx, Params{"mode": "timeout"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout 模式应触发 DeadlineExceeded: %v", err)
	}
}
