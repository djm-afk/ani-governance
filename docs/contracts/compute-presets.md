# 算力目录 HTTP 合同

唯一租户目录是 `GET /api/v1/compute-presets`，owner 为 `ComputePresetService.ListPresets`。
`/api/v1/instance-presets` 已删除，返回 404；没有别名、转发或默认用途。
平台 CRUD 仍由 `/api/v1/admin/compute-presets…` 和 `compute:preset:admin:*` 授权。

## 请求与业务授权

`usage` 必填且只接受下表三个大小写精确的字符串。普通训练和微调共用 TRAINING。

| usage | 当前套餐模块 | 当前精确权限 |
|---|---|---|
| CONTAINER_INSTANCE | INSTANCE | instance:preset:read |
| TRAINING | MODEL | modeldev:compute-preset:read |
| INFERENCE | INFERENCE | inference:compute-preset:read |

身份必须为认证上下文中的租户用户；不接受 query/header 提供的 tenant、actor 或平台用户。
每次请求在同一只读 Repeatable Read 授权快照中检查有效租户、用户、成员关系、角色、
ALL 数据范围、ALLOW 关系、启用权限、API 以及本 usage 套餐模块。只拥有另一 usage 的权限拒绝。
三个权限映射同一 API；其 SYSTEM 分类仅表示 ComputePresetService owner，不能代替业务套餐。
INFERENCE 模块在本次仅开放目录资格，不提供推理管理、受理或执行接口。

可选 query：`page`、`pageSize`、`name`、`code`、`compute_type`、`gpu_mode`。
分页为正 uint32，默认 1/20，pageSize 最多100；空值、零、前导零、溢出、重复键和未知键拒绝。
`compute_type` 为 cpu/gpu；`gpu_mode` 为 WHOLE_EXCLUSIVE/SHARED_FIXED。
name/code 去除首尾空白后最多128/64字符，字面匹配语义沿 Resource。

```http
GET /api/v1/compute-presets?usage=INFERENCE&compute_type=cpu&page=1&pageSize=20
Authorization: Bearer <tenant-user-token>
```

## 响应与边界

200 返回 `{items: [...], total: "N", gpu_catalog_status: "OK", gpu_catalog_reason: ""}`，
不包外层 data。Preset 字段及 CPU/GPU 单位沿[容器规格合同](container-instances.md)；
int64 按 ProtoJSON 十进制字符串，Cache-Control 为 no-store。
列表只含 enabled 规格。GPU 使用当前 ACC 目录权限和完整可信 profile/version/digest/cluster/pool
交集，在 Resource 最终 count/page 之前过滤。无 Profile 目录资格时 CPU 仍可返回，
gpu_catalog_status=NO_PERMISSION、gpu_catalog_reason=GPU_CATALOG_NO_PERMISSION；
ACC 故障返回 CPU 安全集合并标记 UNAVAILABLE/GPU_CATALOG_UNAVAILABLE。
显式 compute_type=cpu 不访问 ACC。selectable 和 unavailable_reason 只表示当次目录提示。

400：缺少/非法 usage、分页/过滤/未知/重复参数；401：无有效认证；403：当前业务权限、
租户/角色/套餐不满足或跨租户；503：当前授权存储或 Resource 等必要依赖不可用。
目录选择不会创建资源、占额或冻结执行。创建仍须执行相应业务完整授权和正式解析；
本次只提供 TRAINING/INFERENCE 目录，未接通它们的创建消费者。

升级与恢复使用[部署步骤](../deployment.md#统一租户算力目录切换)，保留原容器权限、
API ID、状态和角色关系；不自动给现有角色或套餐追加训练/推理授权。
