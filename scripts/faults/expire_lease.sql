-- 手动把运行中任务的租约置为过期（复现/调试入口）：
-- 执行后等待 reaper（默认 5s 扫描）回收，观察任务被重排或按上限失败。
UPDATE tasks
SET lease_expires_at = now() - interval '1 millisecond'
WHERE id = :'task_id' AND status IN ('RUNNING', 'CANCEL_REQUESTED');
