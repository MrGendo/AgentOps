"""T2 模型适配层：Mock 确定性、错误分类、工厂回退。"""

from app.config import Settings
from app.models.base import InvalidOutputError, ModelUsage, RateLimitedError
from app.models.mock import MockLLM
from app.models.openai_compat import build_model


def test_mock_ops_diagnosis_script_deterministic():
    """运维诊断脚本：4 轮动作（计划+3 工具+终答），输出确定且用量递增。"""
    m = MockLLM()
    prompt = '{"phase":"plan","goal":{"service":"order-api","描述":"响应变慢 诊断"}}'
    outs = [m.generate(prompt).content for _ in range(5)]
    assert '"type": "plan"' in outs[0].replace(" ", " ")
    assert outs.count(outs[0]) == 1  # 每轮内容不同（游标推进）
    assert '"final"' in outs[4]
    # 同一脚本重放完全一致
    m2 = MockLLM()
    outs2 = [m2.generate(prompt).content for _ in range(5)]
    assert outs == outs2


def test_mock_script_exhausted_raises():
    """脚本耗尽必须抛 INVALID_OUTPUT，不允许无限输出。"""
    m = MockLLM()
    prompt = '{"goal":{"topic":"通用"}}'
    for _ in range(3):
        m.generate(prompt)
    try:
        m.generate(prompt)
        raise AssertionError("应抛 InvalidOutputError")
    except InvalidOutputError:
        pass


def test_usage_accumulate():
    u1 = ModelUsage(10, 5, 15)
    u2 = ModelUsage(1, 2, 3)
    u1.add(u2)
    assert (u1.prompt_tokens, u1.completion_tokens, u1.total_tokens) == (11, 7, 18)


def test_build_model_fallback_to_mock():
    """未配置密钥的 openai 选择必须回退 Mock（安全默认）。"""
    s = Settings()
    s.model = "openai"
    s.model_api_key = ""
    assert isinstance(build_model(s), MockLLM)
    s2 = Settings()
    assert isinstance(build_model(s2), MockLLM)


def test_rate_limited_kind():
    assert RateLimitedError("限流").kind == "RATE_LIMITED"
