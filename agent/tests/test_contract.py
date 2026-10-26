"""T8 契约测试：docs/openapi/internal-v1.yaml 与实现的结构一致性（防漂移）。"""

from pathlib import Path

import yaml

YAML_PATH = Path(__file__).resolve().parents[2] / "docs" / "openapi" / "internal-v1.yaml"


def load_spec() -> dict:
    with open(YAML_PATH, encoding="utf-8") as f:
        return yaml.safe_load(f)


def test_expected_paths_present():
    spec = load_spec()
    expected = {"/internal/v1/agent-runs", "/internal/v1/agent-runs/{id}",
                "/internal/v1/evaluations", "/internal/v1/evaluations/{id}",
                "/internal/v1/diagnoses", "/internal/v1/health"}
    assert expected == set(spec["paths"].keys())


def test_agent_runs_post_is_202_with_id_and_status():
    spec = load_spec()
    responses = spec["paths"]["/internal/v1/agent-runs"]["post"]["responses"]
    assert "202" in responses
    props = (responses["202"]["content"]["application/json"]["schema"]
             ["properties"]["data"]["properties"])
    assert set(props.keys()) == {"id", "status"}


def test_agent_run_get_fields_cover_implementation():
    """GET 返回字段与实现的 digest 键一致（阶段 C 实现的字段集）。"""
    spec = load_spec()
    props = (spec["paths"]["/internal/v1/agent-runs/{id}"]["get"]["responses"]["200"]
             ["content"]["application/json"]["schema"]["properties"]["data"]["properties"])
    documented = set(props.keys())
    implemented = {"id", "task_id", "status", "finish_reason", "final_output",
                   "trajectory_digest", "token_usage", "error"}
    # 契约允许超集；实现不得出现契约未定义的字段
    assert implemented <= documented


def test_agent_runs_secured_by_service_token():
    """除 health 外的全部内部端点必须声明服务令牌。"""
    spec = load_spec()
    for path, path_item in spec["paths"].items():
        if path.endswith("/health"):
            continue
        for method in ("post", "get"):
            if method in path_item:
                assert "serviceToken" in [list(s.keys())[0] for s in
                                          path_item[method].get("security", [])], path
