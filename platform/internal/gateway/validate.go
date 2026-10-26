package gateway

// 内置 JSON Schema 校验器，覆盖工具声明所需的子集：
// type/required/properties/enum/minLength/maxLength/minimum/maximum/pattern。
// 选择手写而非引入完整 draft 2020-12 实现：四工具 schema 固定且简单，
// 零依赖、行为完全可控；阶段 C 若出现复杂 schema 再评估引入标准库（设计决策 D-B3 分支）。

import (
	"fmt"
	"regexp"
	"strings"
)

// Schema 描述一个工具参数模式的校验子集。
type Schema struct {
	Type       string
	Required   []string
	Properties map[string]*Schema
	Enum       []string
	MinLength  int
	MaxLength  int
	Minimum    *float64
	Maximum    *float64
	Pattern    string
	// ItemObject 表示数组元素应为 object（仅用于 allowed_tools 这类简单数组）。
	ItemObject bool
	// FreeForm 为 true 时（Type=object）允许任意键值：用于 mock_http.body 这类透传对象。
	FreeForm bool
}

// ValidateParams 校验参数映射；返回首个违反项的可读错误。
func (s *Schema) ValidateParams(p Params) error {
	if s.Type != "object" {
		return fmt.Errorf("schema 根类型必须为 object")
	}
	if s.FreeForm { // 自由对象：不做键级校验
		return nil
	}
	for _, req := range s.Required {
		if _, ok := p[req]; !ok {
			return fmt.Errorf("缺少必填参数 %s", req)
		}
	}
	for k, v := range p {
		sub, ok := s.Properties[k]
		if !ok {
			return fmt.Errorf("未知参数 %s", k)
		}
		if err := sub.validateValue(k, v); err != nil {
			return err
		}
	}
	return nil
}

func (s *Schema) validateValue(name string, v any) error {
	switch s.Type {
	case "string":
		str, ok := v.(string)
		if !ok {
			return fmt.Errorf("参数 %s 应为字符串", name)
		}
		if len(str) < s.MinLength {
			return fmt.Errorf("参数 %s 长度不足 %d", name, s.MinLength)
		}
		if s.MaxLength > 0 && len(str) > s.MaxLength {
			return fmt.Errorf("参数 %s 超过最大长度 %d", name, s.MaxLength)
		}
		if s.Pattern != "" {
			re, err := regexp.Compile("^(?:" + s.Pattern + ")$")
			if err != nil {
				return fmt.Errorf("参数 %s 的 pattern 非法: %w", name, err)
			}
			if !re.MatchString(str) {
				return fmt.Errorf("参数 %s 含有不允许的字符", name)
			}
		}
		if len(s.Enum) > 0 {
			for _, e := range s.Enum {
				if e == str {
					return nil
				}
			}
			return fmt.Errorf("参数 %s 必须为 %s 之一", name, strings.Join(s.Enum, "/"))
		}
	case "number", "integer":
		num, ok := toFloat(v)
		if !ok {
			return fmt.Errorf("参数 %s 应为数字", name)
		}
		if s.Type == "integer" && num != float64(int(num)) {
			return fmt.Errorf("参数 %s 应为整数", name)
		}
		if s.Minimum != nil && num < *s.Minimum {
			return fmt.Errorf("参数 %s 小于最小值 %v", name, *s.Minimum)
		}
		if s.Maximum != nil && num > *s.Maximum {
			return fmt.Errorf("参数 %s 大于最大值 %v", name, *s.Maximum)
		}
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("参数 %s 应为对象", name)
		}
		sub := Params(m)
		if err := s.ValidateParams(sub); err != nil {
			return fmt.Errorf("参数 %s: %w", name, err)
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("参数 %s 应为数组", name)
		}
		if s.ItemObject {
			for _, it := range arr {
				if _, ok := it.(map[string]any); !ok {
					return fmt.Errorf("参数 %s 的元素应为对象", name)
				}
			}
		}
	default:
		return fmt.Errorf("参数 %s 类型 %s 不受支持", name, s.Type)
	}
	return nil
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
