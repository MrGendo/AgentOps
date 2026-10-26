// Package eval 实现评测数据集管理与评测运行协调（需求 §8）。
// 全部数据库写入只发生在 Go（架构 D-1）；Python 只做计算。
package eval

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrDatasetExists 同名同版本数据集已存在。
var ErrDatasetExists = errors.New("dataset version already exists")

// Dataset 数据集元数据。
type Dataset struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Version     int       `json:"version"`
	Description string    `json:"description,omitempty"`
	CaseCount   int       `json:"case_count"`
	CreatedAt   time.Time `json:"created_at"`
}

// CaseInput 导入时的样例定义。
type CaseInput struct {
	InputTask    map[string]any   `json:"input_task"`
	Expected     map[string]any   `json:"expected,omitempty"`
	ScoringRules []map[string]any `json:"scoring_rules"`
	Tags         []string         `json:"tags,omitempty"`
	Difficulty   string           `json:"difficulty,omitempty"`
}

// CaseRow 落库后的样例。
type CaseRow struct {
	ID           string
	Seq          int
	InputTask    json.RawMessage
	Expected     json.RawMessage
	AllowedTools []string
	RunLimits    json.RawMessage
	ScoringRules json.RawMessage
	Tags         []string
	Difficulty   string
}

// Store 评测数据访问。
type Store struct{ pool *pgxpool.Pool }

// NewStore 构建评测存储。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// validateImport 导入前校验：每条样例必须声明 task_type。
func validateImport(cases []CaseInput) error {
	for _, c := range cases {
		if c.InputTask == nil || c.InputTask["task_type"] == nil {
			return errors.New("样例缺少 input_task.task_type")
		}
	}
	return nil
}

// Import 原子导入数据集与全部样例；同名同版本冲突返回 ErrDatasetExists。
func (s *Store) Import(ctx context.Context, name string, version int, desc string, cases []CaseInput) (*Dataset, error) {
	if name == "" {
		return nil, errors.New("数据集名称不能为空")
	}
	if version <= 0 {
		version = 1
	}
	if err := validateImport(cases); err != nil {
		return nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var d Dataset
	err = tx.QueryRow(ctx, `
		INSERT INTO evaluation_datasets (name, version, description, case_count)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (name, version) DO NOTHING
		RETURNING id::text, name, version, description, case_count, created_at`,
		name, version, desc, len(cases)).
		Scan(&d.ID, &d.Name, &d.Version, &d.Description, &d.CaseCount, &d.CreatedAt)
	// ON CONFLICT DO NOTHING 命中冲突时不返回行：Scan 得到 ErrNoRows → 版本已存在
	if errors.Is(err, errNoRows()) {
		return nil, ErrDatasetExists
	}
	if err != nil {
		return nil, err
	}

	for i, c := range cases {
		input, _ := json.Marshal(c.InputTask)
		expected := []byte("{}")
		if len(c.Expected) > 0 {
			expected, _ = json.Marshal(c.Expected)
		}
		rules, _ := json.Marshal(c.ScoringRules)
		if c.ScoringRules == nil {
			rules = []byte("[]")
		}
		difficulty := c.Difficulty
		if difficulty == "" {
			difficulty = "normal"
		}
		tags := c.Tags
		if tags == nil { // tags 列 NOT NULL：nil 兜底为空数组
			tags = []string{}
		}
		tools := extractTools(c.InputTask)
		if _, err := tx.Exec(ctx, `
			INSERT INTO evaluation_cases (dataset_id, seq, input_task, expected, allowed_tools, scoring_rules, tags, difficulty)
			VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8)`,
			d.ID, i+1, input, expected, tools, rules, tags, difficulty); err != nil {
			return nil, err
		}
	}
	return &d, tx.Commit(ctx)
}

// ListDatasets 数据集列表（按创建时间倒序）。
func (s *Store) ListDatasets(ctx context.Context) ([]*Dataset, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, name, version, description, case_count, created_at
		FROM evaluation_datasets ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Dataset
	for rows.Next() {
		var d Dataset
		if err := rows.Scan(&d.ID, &d.Name, &d.Version, &d.Description, &d.CaseCount, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &d)
	}
	return out, rows.Err()
}

// DatasetWithCases 取数据集与全部样例（协调器下发给 Python 用）。
func (s *Store) DatasetWithCases(ctx context.Context, id string) (*Dataset, []CaseRow, error) {
	var d Dataset
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, name, version, description, case_count, created_at
		FROM evaluation_datasets WHERE id=$1::uuid`, id).
		Scan(&d.ID, &d.Name, &d.Version, &d.Description, &d.CaseCount, &d.CreatedAt)
	if err != nil {
		return nil, nil, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id::text, seq, input_task, expected, allowed_tools, run_limits, scoring_rules, tags, difficulty
		FROM evaluation_cases WHERE dataset_id=$1::uuid ORDER BY seq`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var cases []CaseRow
	for rows.Next() {
		var c CaseRow
		var tools []byte
		if err := rows.Scan(&c.ID, &c.Seq, &c.InputTask, &c.Expected, &tools, &c.RunLimits,
			&c.ScoringRules, &c.Tags, &c.Difficulty); err != nil {
			return nil, nil, err
		}
		_ = json.Unmarshal(tools, &c.AllowedTools)
		cases = append(cases, c)
	}
	return &d, cases, rows.Err()
}

func extractTools(inputTask map[string]any) []string {
	if v, ok := inputTask["allowed_tools"].([]any); ok {
		out := make([]string, 0, len(v))
		for _, t := range v {
			if s, ok := t.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return []string{}
}
