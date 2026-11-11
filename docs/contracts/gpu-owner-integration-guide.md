# GPU owner 集成合同

当前活动合同为加速器整改 v1.1：本期新 GPU Owner 仅 Resource 容器实例。ModelDev/Inference 新 GPU 在占额前明确未接入，CPU 保留原受理和原 canonical 重放。静态业务、执行与一致性校验使用 ACC `pkg/gpuprotocol` 的唯一普通 Go 内核及边界 protobuf wrapper，不复制厂商换算。

## 1. 主链与写入职责

ACC Profile → Res Preset → Gov 当前权限与套餐 → 原键重放查找 → 新键 Res 解析静态引用 → ACC Resolve → 完整引用逐项核对 → Gov 同事务真实占额与持久命令 → Res owner 接纳 → Kubernetes/Volcano → owner 清理并封闭迟到 CREATE → 原释放事件 → Gov 原 charge 全退。

ACC 维护静态供给/目录、设备事实和只读容量；Gov 拥有真实配额、幂等与可靠投递；Res 拥有业务、运行副作用、清理和退款 outbox。容量、库存、监控或 exporter 可用性不授权创建，不阻止删除与退款。不再存在平台 Binding、UsageSync、签名绑定、平台设备账本或释放证明。

## 2. 公共协议与发布顺序

公共协议按 Obs API → ACC → Res → Gov 锁定正式精确模块。使用 `GOWORK=off`，不提交本地 replace。Gov API 使用固定 `make api`，Ent 使用 `make ent`；不手改生成代码。独立 `api/quota` 的 `QuotaReleaseService/ReportResourceReleased` 合同保持。

## 3. 创建合同

`AcceptCreate` 仅包含可信 context 和冻结 spec。CPU 保持原 canonical 字节/摘要，`gpu_display:null` 的历史规范不变。GPU 冻结唯一 `GpuBusinessSpec` 和 `GpuExecution`，清除只用于解析响应的临时 `gpu_selection`；Res 不接收原账或完整 Plan。

BusinessSpec 包括 profile/version/name/spec_digest、mode/model、卡数、每卡 memory/core/CU、allocation_type/resource_key 和 adapter_id。Gov 将其与 Res 原静态引用逐项核对，调用公共内核验证规格、执行及配额。NVIDIA F/K 编码来自登记配置；海光整卡及预配置固定片按登记资源键执行，不反推 NVIDIA 显存、不做动态切卡。实际 F/K 读取可用于展示或配置核对，不建立安装认证门槛。

## 4. 删除与完整释放

`AcceptDelete` 只包含可信 context、原 create operation、expected version、instance kind 和 compute type。没有 GPU Plan 或 charges。持久 DELETE 封闭原 CREATE；DELETE 先到、迟到 CREATE、ACK 丢失和未知写确认仍由 Res 真实持久化和副作用逻辑保证。

Res 只有确认计算资源已清理、没有残留 GPU 访问、没有在途或待确认创建，才持久化释放 outbox 并调用：

```json
{
  "release_event_id": "<stable UUID>",
  "create_operation_id": "<original CREATE UUID>",
  "resource_id": "<resource UUID>",
  "reason": "RESOURCE_RELEASED"
}
```

失败清理使用相同四字段及 `ABORTED_CLEANED`。普通退出、Stop、重启、NotFound、超时、durable ACK、Usage ENDED 和单次 binding 缺失都不能触发完整退额。从未分配 GPU 的 Pending 实例可以在可靠封闭创建并确认无残留后走同一合同。

`QuotaReleaseService/ReportResourceReleased` 仅在内部 TLS 1.3 双向认证 listener 注册。客户端证书必须含唯一精确 DNS SAN，映射到已登记 owner，且只允许登记方法。请求没有可信 tenant/owner 字段；Gov 用认证 owner 和原 CREATE 查租户，锁定租户、原操作及 charges，再核对 resource。退款数量只取原账剩余量，不按现有目录重算。

Inference 接入通过 `ANI_QUOTA_INFERENCE_ENABLED=true` 显式注册唯一 DNS
`ani-inference-service` 到固定原账本 owner `ani-inference`。该开关不接受任意 owner 或别名；
与 Resource 身份冲突时启动失败。证书使用现有 cert-manager ClusterIssuer 签发，
部署、信任切换和续期加载见 [内部服务证书配置](../../scripts/deploy/service-mtls/README.md)。

同 event 不同内容拒绝；同资源换 event 重报不重复退款。新完整事件使用现有全局唯一 receipt ID 约束封闭跨租户或跨 owner 重用。响应回同 event/create/resource，status 为 `APPLIED` 或 `ALREADY_APPLIED`；Res 仅接受引用全部匹配的确认。

旧 `ReportQuotaRelease` 仅保留非 GPU 的独立累计释放语义，新的 GPU code 不能走该入口。本期不新增 CPU、主机内存或 GPU 核心 quota。

## 5. 监控与容量

Gov→ACC UsageSync 和 Binding 投影已退役。Gov 从不可变原 CREATE/charge 读取完整业务身份并直接查询 Obs；当前资源读授权仍必需，实时 owner 对象、Pod 或未退 charge 不是历史查询前置。

新 Profile/Group 容量为平台管理只读诊断，区分实际 Node/Pod 请求与 exporter 观测，不用于创建筛选。普通租户目录只以静态合同与权限形成候选；TRAINING/INFERENCE GPU 标为未接入，不调用容量。64 位 optional 字段经 HTTP 保持未知 `null` 与已知 `"0"` 的区别。

## 6. 切换与验证边界

按[切换步骤](../operations/gpu-contract-v11-cutover.md)盘点并闭合旧 owner、命令和原 charge。原 operation/canonical/hash/ACK、charge/receipt 和必要 outbox 保留，旧终态快照由 Res 清单归档。旧 GPU canonical 不转换、不重新派发；CPU 原键继续重放。部署必须核对目标真实 Atlas revisions 与现存列，不能假定已应用源码 main 全部迁移。

只执行本次受影响的生成、包/二进制定向编译、静态合同/授权/幂等及 owner/原 charge 数据库测试；用户明确排除完整 make verify 与全仓测试。真实设备计算、部署版本和闭环验收另行记录，不能用 fixture 或 HTTP 200 代替。
