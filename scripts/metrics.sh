#!/bin/bash
# 平台观测指标（§14.2）：全部来自真实运行记录。
DSN="${AGENTOPS_DATABASE_URL:-postgres://localhost:5432/agentops_dev?sslmode=disable}"
echo "== 任务 =="
psql "$DSN" -c "
SELECT count(*) AS 任务总量,
  count(*) FILTER (WHERE status='SUCCEEDED') AS 成功,
  count(*) FILTER (WHERE status='FAILED') AS 失败,
  round(100.0*count(*) FILTER (WHERE status='SUCCEEDED')/GREATEST(count(*),1),1) AS 成功率_百分比,
  count(*) FILTER (WHERE status='PENDING') AS 待执行,
  count(*) FILTER (WHERE status='RUNNING') AS 执行中,
  round(avg(EXTRACT(EPOCH FROM (finished_at-started_at))*1000) FILTER (WHERE finished_at IS NOT NULL)) AS 平均耗时_ms
FROM tasks"
echo "== 重试与租约 =="
psql "$DSN" -c "
SELECT (SELECT count(*) FROM tasks WHERE attempt_count>1) AS 发生重试的任务,
       (SELECT count(*) FROM task_events WHERE event_type='task.retry_scheduled') AS 重试调度次数,
       (SELECT count(*) FROM task_events WHERE event_type='lease.revoked') AS 租约过期次数"
echo "== 工具调用 =="
psql "$DSN" -c "
SELECT count(*) AS 总调用,
  count(*) FILTER (WHERE status='SUCCEEDED') AS 成功,
  count(*) FILTER (WHERE status='FAILED') AS 失败,
  count(*) FILTER (WHERE status='TIMEOUT_UNKNOWN') AS 结果未知,
  count(*) FILTER (WHERE status='REJECTED') AS 被拒
FROM tool_calls"
echo "== Agent 运行 =="
psql "$DSN" -c "
SELECT count(*) AS 运行数,
  round(avg(iteration_count),1) AS 平均迭代,
  coalesce(sum((token_usage->>'total_tokens')::bigint),0) AS 累计Token
FROM agent_runs"
echo "== 评测 =="
psql "$DSN" -c "
SELECT count(*) AS 评测数,
  count(*) FILTER (WHERE status='SUCCEEDED') AS 完成,
  round(avg(EXTRACT(EPOCH FROM (finished_at-created_at))) FILTER (WHERE finished_at IS NOT NULL),1) AS 平均耗时_s
FROM evaluation_runs"
