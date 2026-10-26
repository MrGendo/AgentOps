package gateway

// 四个内置工具：calculator / knowledge_search / task_status / mock_http。
// 全部只读或可控 Mock，不提供任意 Shell 或代码执行（§6.6 安全边界）。

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CalculatorTool 数值计算：自研递归下降求值器，杜绝任何形式的 eval（决策 D-B2）。
func CalculatorTool() *Tool {
	return &Tool{
		Name: "calculator", Version: "1.0.0",
		Description: "四则运算求值：支持 + - * / 与括号",
		RiskLevel:   "LOW", Timeout: 2 * time.Second, Idempotent: true,
		Schema: &Schema{
			Type:     "object",
			Required: []string{"expression"},
			Properties: map[string]*Schema{
				"expression": {Type: "string", MinLength: 1, MaxLength: 200,
					// 仅允许数字、运算符、括号与空格；拒绝任何其它字符注入
					Pattern: "[0-9+\\-*/(). ]+"},
			},
		},
		Exec: func(ctx context.Context, p Params) (any, error) {
			v, err := EvalExpression(p["expression"].(string))
			if err != nil {
				return nil, &ToolError{Code: "TOOL_EXECUTION_ERROR", Msg: err.Error()}
			}
			return map[string]any{"expression": p["expression"], "value": v}, nil
		},
	}
}

// KnowledgeSearchTool 检索本地知识库（data 目录由调用方注入，默认 platform/knowledge）。
type KnowledgeIndex struct {
	sections []kbSection
}

type kbSection struct {
	File  string
	Title string
	Text  string
}

// LoadKnowledge 读取目录下全部 *.md，按二级标题切段。
func LoadKnowledge(dir string) (*KnowledgeIndex, error) {
	idx := &KnowledgeIndex{}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return idx, nil // 目录不存在视为空知识库，不报错（可运行性优先）
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		for _, sec := range splitSections(string(raw)) {
			idx.sections = append(idx.sections, kbSection{File: e.Name(), Title: sec.title, Text: sec.text})
		}
	}
	return idx, nil
}

func splitSections(md string) []struct{ title, text string } {
	var out []struct{ title, text string }
	lines := strings.Split(md, "\n")
	var cur struct{ title, text string }
	flush := func() {
		if cur.title != "" {
			out = append(out, cur)
		}
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "## ") {
			flush()
			cur = struct{ title, text string }{title: strings.TrimSpace(l[3:]), text: ""}
		} else {
			cur.text += l + "\n"
		}
	}
	flush()
	return out
}

// KnowledgeSearchTool 构建检索工具；返回命中的段落与来源（演示场景 §3.2）。
func KnowledgeSearchTool(idx *KnowledgeIndex) *Tool {
	return &Tool{
		Name: "knowledge_search", Version: "1.0.0",
		Description: "检索本地运维诊断知识库，返回相关条目与来源",
		RiskLevel:   "LOW", Timeout: 2 * time.Second, Idempotent: true,
		Schema: &Schema{
			Type:     "object",
			Required: []string{"query"},
			Properties: map[string]*Schema{
				"query": {Type: "string", MinLength: 1, MaxLength: 200},
				"top_k": {Type: "integer", Minimum: ptrFloat(1), Maximum: ptrFloat(10)},
			},
		},
		Exec: func(ctx context.Context, p Params) (any, error) {
			query := strings.ToLower(p["query"].(string))
			topK := 3
			if v, ok := p["top_k"]; ok {
				if f, ok := toFloat(v); ok {
					topK = int(f)
				}
			}
			tokens := tokenize(query)
			type hit struct {
				Section kbSection
				Score   int
			}
			var hits []hit
			for _, sec := range idx.sections {
				lower := strings.ToLower(sec.Title + "\n" + sec.Text)
				score := 0
				for _, tk := range tokens {
					score += strings.Count(lower, tk)
				}
				if score > 0 {
					hits = append(hits, hit{sec, score})
				}
			}
			sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
			if len(hits) > topK {
				hits = hits[:topK]
			}
			results := make([]map[string]any, 0, len(hits))
			for _, h := range hits {
				snippet := strings.TrimSpace(h.Section.Text)
				if len(snippet) > 240 {
					snippet = snippet[:240] + "…"
				}
				results = append(results, map[string]any{
					"file": h.Section.File, "title": h.Section.Title,
					"snippet": snippet, "score": h.Score,
				})
			}
			return map[string]any{"query": p["query"], "results": results}, nil
		},
	}
}

func tokenize(q string) []string {
	var out []string
	for _, tk := range strings.Fields(q) {
		tk = strings.TrimSpace(tk)
		if len([]rune(tk)) >= 2 {
			out = append(out, tk)
		}
	}
	return out
}

// TaskStatusTool 只读查询任务状态概要（受限工具：不暴露任务输入等敏感内容）。
func TaskStatusTool(pool *pgxpool.Pool) *Tool {
	return &Tool{
		Name: "task_status", Version: "1.0.0",
		Description: "查询指定任务的当前状态概要（只读、受限字段）",
		RiskLevel:   "LOW", Timeout: 2 * time.Second, Idempotent: true,
		Schema: &Schema{
			Type:     "object",
			Required: []string{"task_id"},
			Properties: map[string]*Schema{
				"task_id": {Type: "string", MinLength: 36, MaxLength: 36},
			},
		},
		Exec: func(ctx context.Context, p Params) (any, error) {
			id := p["task_id"].(string)
			if _, err := uuid.Parse(id); err != nil {
				return nil, &ToolError{Code: "ARGUMENT_INVALID", Msg: "task_id 不是合法 UUID"}
			}
			var summary struct {
				ID       string  `json:"id"`
				Type     string  `json:"task_type"`
				Status   string  `json:"status"`
				Attempts int     `json:"attempt_count"`
				Error    *string `json:"error_code"`
			}
			err := pool.QueryRow(ctx,
				`SELECT id::text, task_type, status, attempt_count, error_code
				 FROM tasks WHERE id=$1`, id).
				Scan(&summary.ID, &summary.Type, &summary.Status, &summary.Attempts, &summary.Error)
			if err != nil {
				return nil, &ToolError{Code: "TASK_NOT_FOUND", Msg: "任务不存在: " + id}
			}
			return summary, nil
		},
	}
}

// MockHTTPTool 模拟外部 HTTP 服务，四种故障模式驱动可靠性测试（§6.6 / R7）。
func MockHTTPTool() *Tool {
	return &Tool{
		Name: "mock_http", Version: "1.0.0",
		Description: "模拟外部 HTTP 调用：success/error/timeout/lost_response",
		RiskLevel:   "MEDIUM", Timeout: 2 * time.Second, Idempotent: false,
		Schema: &Schema{
			Type:     "object",
			Required: []string{"mode"},
			Properties: map[string]*Schema{
				"mode":     {Type: "string", Enum: []string{"success", "error", "timeout", "lost_response"}},
				"delay_ms": {Type: "integer", Minimum: ptrFloat(0), Maximum: ptrFloat(5000)},
				"body":     {Type: "object", FreeForm: true},
			},
		},
		Exec: func(ctx context.Context, p Params) (any, error) {
			mode := p["mode"].(string)
			delay := time.Duration(0)
			if v, ok := toFloat(p["delay_ms"]); ok {
				delay = time.Duration(v) * time.Millisecond
			}
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			switch mode {
			case "success":
				body := map[string]any{"ok": true}
				if b, ok := p["body"].(map[string]any); ok {
					body = b
				}
				return map[string]any{"http_status": 200, "body": body}, nil
			case "error":
				return nil, &ToolError{Code: "TOOL_EXECUTION_ERROR", Msg: "模拟下游返回 500"}
			case "timeout":
				// 睡过网关超时，由网关判定 TIMEOUT_UNKNOWN
				select {
				case <-time.After(10 * time.Second):
					return map[string]any{"late": true}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			case "lost_response":
				// 响应丢失：请求已发出但结果不可知（§6.5）
				return nil, ErrLostResponse
			}
			return nil, &ToolError{Code: "ARGUMENT_INVALID", Msg: "未知模式 " + mode}
		},
	}
}

// DefaultTools 组装全部内置工具；knockDir 为知识库目录。
func DefaultTools(pool *pgxpool.Pool, knowledgeDir string) ([]*Tool, error) {
	idx, err := LoadKnowledge(knowledgeDir)
	if err != nil {
		return nil, err
	}
	return []*Tool{
		CalculatorTool(),
		KnowledgeSearchTool(idx),
		TaskStatusTool(pool),
		MockHTTPTool(),
	}, nil
}

func ptrFloat(f float64) *float64 { return &f }

// ---------- 表达式求值器：递归下降（D-B2，无 eval） ----------

// EvalExpression 解析并求值四则运算表达式。
// 语法：expr = term (("+"|"-") term)* ; term = factor (("*"|"/") factor)* ;
//
//	factor = number | "(" expr ")" | "-" factor
type exprParser struct {
	src []rune
	pos int
}

// EvalExpression 对外入口：词法校验由 pattern 保证，这里做语法与求值。
func EvalExpression(s string) (float64, error) {
	p := &exprParser{src: []rune(s)}
	v, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	p.skipSpace()
	if p.pos != len(p.src) {
		return 0, fmt.Errorf("表达式在位置 %d 有多余字符", p.pos)
	}
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, fmt.Errorf("计算结果溢出")
	}
	return v, nil
}

func (p *exprParser) skipSpace() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
}

func (p *exprParser) peek() (rune, bool) {
	p.skipSpace()
	if p.pos < len(p.src) {
		return p.src[p.pos], true
	}
	return 0, false
}

func (p *exprParser) parseExpr() (float64, error) {
	left, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		op, ok := p.peek()
		if !ok || (op != '+' && op != '-') {
			return left, nil
		}
		p.pos++
		right, err := p.parseTerm()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			left += right
		} else {
			left -= right
		}
	}
}

func (p *exprParser) parseTerm() (float64, error) {
	left, err := p.parseFactor()
	if err != nil {
		return 0, err
	}
	for {
		op, ok := p.peek()
		if !ok || (op != '*' && op != '/') {
			return left, nil
		}
		p.pos++
		right, err := p.parseFactor()
		if err != nil {
			return 0, err
		}
		if op == '*' {
			left *= right
		} else {
			if right == 0 {
				return 0, fmt.Errorf("除数为零")
			}
			left /= right
		}
	}
}

func (p *exprParser) parseFactor() (float64, error) {
	ch, ok := p.peek()
	if !ok {
		return 0, fmt.Errorf("表达式意外结束")
	}
	switch {
	case ch == '(':
		p.pos++
		v, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		if c, ok := p.peek(); !ok || c != ')' {
			return 0, fmt.Errorf("缺少右括号")
		}
		p.pos++
		return v, nil
	case ch == '-':
		p.pos++
		v, err := p.parseFactor()
		return -v, err
	default:
		return p.parseNumber()
	}
}

func (p *exprParser) parseNumber() (float64, error) {
	p.skipSpace()
	start := p.pos
	for p.pos < len(p.src) && (p.src[p.pos] >= '0' && p.src[p.pos] <= '9' || p.src[p.pos] == '.') {
		p.pos++
	}
	tok := string(p.src[start:p.pos])
	if start == p.pos {
		return 0, fmt.Errorf("位置 %d 期望数字", p.pos)
	}
	// 数字 token 必须形如 digits[.digits]：至多一个小数点且不能出现在首尾
	if strings.Count(tok, ".") > 1 || strings.HasPrefix(tok, ".") || strings.HasSuffix(tok, ".") {
		return 0, fmt.Errorf("非法数字 %q", tok)
	}
	v, err := strconv.ParseFloat(tok, 64)
	if err != nil {
		return 0, fmt.Errorf("非法数字 %q", tok)
	}
	return v, nil
}
