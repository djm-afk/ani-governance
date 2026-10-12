# 数据库备份与恢复

本页描述现有 `pg_dump` 备份脚本和本地审计归档；是否满足部署环境的备份要求，需结合实际调度、保存范围和恢复演练验证。

## 1. 物理库定期备份（pg_dump）

`pg_backup.sh` 每次生成一份自定义格式（`-Fc`，自带压缩）全量备份，并按 `KEEP_COPIES`（默认 30）轮换。

```bash
# 先显式核对并设置脚本所需连接、容器、备份目录和保留份数。
# docker 容器匹配时走 docker exec，否则用本地 pg_dump。
./scripts/backup/pg_backup.sh

# cron 每日 02:00（生产）
0 2 * * * /opt/ani-governance/scripts/backup/pg_backup.sh >> /var/log/pg_backup.log 2>&1
```

环境变量见 [pg_backup.sh](pg_backup.sh) 头部注释。脚本仍有历史默认容器、账号、库名和密码，不能把无配置执行当作当前部署用法；通过受保护的任务环境显式提供连接与备份目录。cron 也需加载同一份受管配置，不能把密码写入 crontab 或日志。

## 恢复（pg_restore）

由迁移 owner 显式建立与运行库不同的空目标库，通过受管 libpq 凭据恢复。具体命令、版本检查、权限恢复和账本核对见 [另库恢复步骤](../../docs/operations/governance-accelerator-v1.2.md#7-备份另库恢复与回退)。备份文件存在不等于可恢复；按部署要求在隔离目标演练并记录实际结果。

## 2. 应用内 OSS 备份的历史边界

当前服务没有注册上传 OSS 的 `backup` 任务。[Asynq 装配](../../app/admin/service/internal/server/asynq_server.go) 仅注册租户到期扫描、审计归档和站内信广播。不要依赖任务调度页完成数据库或对象存储备份；需要的外部备份调度与转存另行配置、验证。

## 3. 审计日志留存（≥6 个月）

`audit_log_archive` 系统级定时任务（每天 03:30）把超过保留期的六类审计日志行
导出为本地 JSONL 归档文件后从库中删除：

- 归档目录：`AUDIT_ARCHIVE_DIR`（默认 `./data/audit-archive`）——**纳入服务器备份范围**（脚本备份的是库，归档文件在磁盘上，需随主机/卷一起备份）；
- 库内保留天数：`AUDIT_RETENTION_DAYS`（默认 180，即 ≥6 个月留在库内，更早的行在归档文件中长期保存，按需另行转存）。

## 口令策略（等保"身份鉴别"）

阈值自 2026-09-12 起存于 `sys_configs` 平台参数表，经管理台「参数管理」页调整。当前键由 [首次种子](../../sql/bootstrap/001_initial.sql) 在显式 `admin init` 时插入；[ConfigService 构造](../../app/admin/service/internal/service/config_service.go) 不播种，启动不会缺一补一。旧库缺键需独立审查的数据补丁，不能重跑首次初始化。环境变量 `PASSWORD_MIN_LEN` / `PASSWORD_MAX_AGE_DAYS` / `PASSWORD_HISTORY_COUNT` 已废弃；参数行随数据库备份导出/恢复。各口令写路径的 Proto 校验另有 8 字符硬下限，参数调低于 8 不会放宽该边界。

| 项 | 默认 | 平台参数键 |
|---|---|---|
| 最小长度 | 8 | `sys.password.minLen` |
| 复杂度 | 四类字符（大小写/数字/符号）至少三类 | 固定规则（不可配） |
| 有效期 | 90 天（超期拒绝登录，走重置流程） | `sys.password.maxAgeDays`（≤0 关闭） |
| 历史口令 | 最近 3 条不可复用（存 credential.extra_info） | `sys.password.historyCount`（≤0 关闭） |
