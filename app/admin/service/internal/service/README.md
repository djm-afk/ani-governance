# Service

治理业务服务负责可信主体、权限与业务输入校验，并调用仓储或下游客户端。新增服务先读 [业务服务接入指南](../../../../../docs/service-integration.md)，装配在 [wiring_ent.go](../../cmd/server/wiring_ent.go) 手工维护。分页、关联更新及租户隔离遵循根 [AGENTS.md](../../../../../AGENTS.md) 与对应接口合同。
