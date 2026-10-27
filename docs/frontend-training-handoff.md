# 模型训练与微调任务前端对接文档

本文件面向前端联调。字段以 Governance BFF 的 `api/protos/modeldev/service/v1/execution.proto`、`api/protos/admin/service/v1/i_modeldev.proto`、`api/protos/compute/service/v1/compute.proto` 与严格共享 Intent parser 为准。内部 ModelDev mTLS 地址不向前端开放。

联调环境：`http://10.10.1.66:30085`（NodePort 30085），租户 `tenant-a`。登录沿用现有接口 `POST /api/v1/auth/password/login`，之后所有请求带 `Authorization: Bearer <access_token>`。

**本文件范围**：接口合同——任务创建（CPU / GPU）、列表、详情（含流水线步骤与训练曲线）、日志快照与日志流、容器组、事件、产物、停止、恢复模型登记，覆盖普通训练与微调两条链路。**只描述接口的请求/响应/错误语义，不含页面布局与交互设计建议。**

**当前唯一不可用**：产物真实下载（签名 URL 集群外不可达，见第 13 节）。其余全部可联调。

**详情页四类诊断能力（容器组 / 日志 / 事件 / 资源曲线）的完整参数、字段与错误语义见 [frontend-training-detail-diagnostics.md](frontend-training-detail-diagnostics.md)**，本文对应章节只保留摘要。

## 1. 当前闭环与前端可观察点

```
① 目录/输入就绪
   GET 实现目录 + GET 算力目录(TRAINING) + GET 输入版本(READY)
        ↓
② 创建受理（同步，HTTP 202）
   POST /api/v1/modeldev/executions
   Gov: 当前用户权限 + 租户映射 + ALL 数据范围
        → Resource 解析算力（CPU / GPU）
        → GPU 时：ACCELERATOR 资格 + ACC 最终执行参数解析 + Gov 额度占用
        → ModelDev 冻结候选（Prepare/Finalize）
        → Gov 单事务写入“受理 + 额度占用”
        ↓
③ 投递（Gov 异步 worker，前端不可见）
   AcceptExecution → ModelDev 持久 ACK → 额度操作 QUEUED→ACKED
        ↓
④ 执行（KFP Run/Workflow/Pod，前端只读状态）
   compute_state / delivery_state / current_step 变化
   → 详情可读 steps（流水线步骤）、metrics（训练曲线）、pods、events
   → 日志流 /logs/stream（SSE）可持续跟随
        ↓
⑤ 产物发布
   delivery_state=PUBLISHED，artifacts 可列，下载换短期 URL
        ↓
⑥ 微调可选：模型登记（只有填了 target_model_id 或 new_model_name 才有）
   current_step=REGISTER → model_registration=PENDING / READY / DEPENDENCY_UNAVAILABLE
        ↓
⑦ 关闭
   停止 202 / 自然终止 / 超时 / 步骤失败 → close_state=CLOSING → CLOSED
        ↓
⑧ GPU 释放与退额（内部，无前端接口）
   CLOSED + 计算清理完成后封存 → 观测 ACC 释放 → Gov 按原账全额退回 → ACC 容量恢复
```

前端需要理解的三条关键规则：

1. **创建 202 只代表“受理已落库”**，不代表已开始训练，也不代表额度已交付。需轮询详情看四个状态。
2. **GPU 任务在受理时即占额度，关闭并完成释放后才退回**。调用停止不会立刻退额。
3. **`CLOSED` 是终态**；`CLOSING` 期间仍可能看到 GPU 占用。

## 2. 认证、租户与权限

- 身份只来自登录态。所有接口要求**用户主体**（JWT 登录用户）；API Key 主体一律 `403`。
- 请求体和 URL 中**不得**携带 tenant/actor/owner 等身份字段，服务端会忽略或拒绝。
- 除独立接口权限外，当前用户角色必须具有 **ALL 数据范围**（仅本部门/自定义范围会 `403`）；租户需为启用状态，用户需为正常状态。
- 权限码矩阵：

| 接口 | 方法 | 权限码 |
| --- | --- | --- |
| `/api/v1/modeldev/implementations` | GET | `modeldev:implementation:list` |
| `/api/v1/modeldev/input-versions` | GET | `modeldev:input:list` |
| `/api/v1/modeldev/input-versions/{id}` | GET | `modeldev:input:get` |
| `/api/v1/compute-presets?usage=TRAINING` | GET | `modeldev:compute-preset:read`（与其他用途权限并存，缺一不可） |
| `/api/v1/modeldev/executions` | POST | `modeldev:execution:create` |
| `/api/v1/modeldev/executions` | GET | `modeldev:execution:list` |
| `/api/v1/modeldev/executions/{id}` | GET | `modeldev:execution:get` |
| `/api/v1/modeldev/executions/{id}/pods` | GET | `modeldev:execution:get` |
| `/api/v1/modeldev/executions/{id}/events` | GET | `modeldev:execution:get` |
| `/api/v1/modeldev/executions/{id}/logs` | GET | `modeldev:execution:logs` |
| `/api/v1/modeldev/executions/{id}/logs/stream` | GET | `modeldev:execution:logs` |
| `/api/v1/modeldev/executions/{id}/artifacts` | GET | `modeldev:artifact:list` |
| `/api/v1/modeldev/artifacts/{id}/content` | GET | `modeldev:artifact:download` |
| `/api/v1/modeldev/executions/{id}:stop` | POST | `modeldev:execution:stop` |
| `/api/v1/modeldev/executions/{id}:resume-model-registration` | POST | `modeldev:manage_execution` + `modeldev:execution:get` + Model 权限（见下） |

微调的额外权限（创建时和恢复时都实时校验，不会沿用历史授权）：

| 场景 | 额外要求 |
| --- | --- |
| 微调但**不登记模型**（target 与 new_model_name 都省略） | `GET /api/v1/model-versions/{id}`、`GET /api/v1/model-versions/{id}/download-url` |
| 微调并登记到**已有模型**（`target_model_id`） | 上面两项 + `POST /api/v1/model-versions` + `GET /api/v1/models/{model_id}` |
| 微调并登记为**新模型**（`new_model_name`） | 上面两项 + `POST /api/v1/model-versions` + `POST /api/v1/models` |

对应权限码（现网 `sys_apis` 实际绑定）：

| 接口 | 权限码 |
| --- | --- |
| `GET /api/v1/models`、`/models/{id}`、`/models/{id}/versions` | `modeldev:v21:model:read` |
| `GET /api/v1/model-versions/{id}`、`/model-versions/{id}/download-url` | `modeldev:v21:model:read` |
| `POST /api/v1/models`、`POST /api/v1/model-versions` | `modeldev:v21:model:manage` |

## 3. 通用约定

- 列表统一 `page` / `pageSize`（camelCase），响应 `items` / `total`。默认 `page=1`、`pageSize=20`，`pageSize` 上限 100。显式传 `0`、负数、前导零、小数、重复参数或未声明的参数，一律 `400`，不静默修正。
- **响应里的 `int64`/`uint64` 字段是十进制字符串**：`total`、`cpu_millis`、`memory_mib`、`size_bytes`、`close_generation`、`execution_timeout_*_seconds`、`step_timeout_seconds`、`explicit_parameters` 里的整数参数等。`uint32` 的 `seed` 在响应里是数字。
- **请求体里的整型参数是 JSON 数字，不是字符串**——这条与上面的响应规则相反，容易踩坑，详见 7.5。传成字符串会 `400 INVALID_MODELDEV_CREATE`（2026-10-26 现网实测）。
- 时间字段是 RFC3339Nano UTC 字符串（如 `2026-10-23T08:35:54.775435Z`）。
- 所有 modeldev 读接口响应带 `Cache-Control: no-store`、`Pragma: no-cache`、`Referrer-Policy: no-referrer`；不要做本地缓存或共享缓存。
- 详情接口直接返回资源对象，**没有 `data` 包装**。错误响应统一为：

```json
{"code":412,"reason":"INPUT_NOT_READY","message":"input version is not ready","metadata":{"request_id":"7abb6015-539b-44d5-9447-e2d9b05921aa"}}
```

前端用 `code` + `reason` 分支，不依赖 `message` 文案。`metadata.request_id` 存在时请保留用于排查。

创建接口（`POST /executions`）的 `reason` 全集：

| HTTP | `reason` | 含义 | 可重试性 |
| --- | --- | --- | --- |
| 400 | `INVALID_MODELDEV_CREATE` | 请求体/URL 不合规（重复键、未知键、类型错、query、超大等） | 不可重试，需改请求 |
| 403 | `FORBIDDEN` | 缺少创建权限 / 租户或数据范围不满足 | 不可重试 |
| 403 | `GPU_USE_DENIED` | 当前用户无 ACCELERATOR（GPU）使用资格 | 不可重试 |
| 409 | `IDEMPOTENCY_CONFLICT` | 同一幂等键配了不同意图 | 需换新键或查原任务 |
| 409 | `QUOTA_EXCEEDED` | GPU 额度不足 | 可稍后重试 |
| 403 | `QUOTA_NOT_CONFIGURED` | 租户未配置 GPU 额度 | 不可重试 |
| 403 | `QUOTA_ADMISSION_DENIED` | 额度准入被拒 | 不可重试 |
| 412 | `INPUT_NOT_READY` | 数据集不是 READY | 不可重试，需换数据集 |
| 412 | `EXECUTION_DEADLINE_EXCEEDED` | 受理窗口已过（极少见） | 可重试 |
| 404 | `RESOURCE_NOT_FOUND` | 引用的数据集/实现/规格不存在 | 不可重试，需刷新目录 |
| 503 | `ENVIRONMENT_NOT_READY` | 受管准入环境未就绪 | 可重试 |
| 503 | `UPSTREAM_UNAVAILABLE` | 上游解析不可用 | 可重试 |
| 503 | `GPU_USE_AUTHORIZATION_UNAVAILABLE` | GPU 资格校验服务不可用 | 可重试 |
| 503 | `MODELDEV_GPU_NOT_READY` | 受管 GPU 准入尚未就绪 | 可重试 |
| 503 | `MODELDEV_CREATE_UNAVAILABLE` | 兜底不可用（含发布抖动） | 可退避重试 |
| 499 / 504 | `MODELDEV_CREATE_CANCELED` / `MODELDEV_CREATE_TIMEOUT` | 请求被取消 / 超时 | **须用同一幂等键重试** |

> `499` / `504` 时后端可能已经落库。**必须用原 `idempotency_key` 重试**，重试会命中 `replayed=true` 拿到原记录；换新键会创建第二条任务并二次占额。

## 4. 实现目录

```
GET /api/v1/modeldev/implementations?page=1&pageSize=20
GET /api/v1/modeldev/implementations?kind=FINETUNING
```

只接受 `page` / `pageSize` / `kind`。真实响应（节选）：

```json
{
  "items": [
    {
      "name": "MLP training",
      "kind": "GENERAL_TRAINING",
      "implementation_id": "builtin.mlp",
      "version": "1",
      "parameter_contract_version": "ani.mlp.parameters.v2.1",
      "supported_device_modes": ["cpu", "gpu"],
      "available": true,
      "execution_timeout_default_seconds": "10800",
      "execution_timeout_max_seconds": "14400",
      "step_timeout_seconds": "14400",
      "parameter_rules": [
        {
          "name": "learning_rate",
          "type": "DECIMAL",
          "required": false,
          "default_value": {"name": "learning_rate", "type": "DECIMAL", "value": "0.01"},
          "allowed_values": [],
          "minimum": {"name": "learning_rate", "type": "DECIMAL", "value": "0"},
          "maximum": null,
          "minimum_inclusive": false,
          "maximum_inclusive": null,
          "editable": true,
          "non_editable_reason": "",
          "numeric_representation": "float32_positive_finite",
          "validation_description": "non-exponent decimal string, at most 32 characters, positive and finite after float32 conversion"
        }
      ]
    }
  ],
  "total": "2"
}
```

要点：

- 响应是**扁平列表，不按 `kind` 分组**，也没有 `kind` 分组字段。要按类型取子集用 `kind` 查询参数。
- 当前只有两条，且 `kind` 与 `implementation_id` 是**固定一对一**：

  | `kind` | 唯一对应的 `implementation_id` | `name` |
  | --- | --- | --- |
  | `GENERAL_TRAINING` | `builtin.mlp` | `MLP training` |
  | `FINETUNING` | `builtin.sft_lora` | `LoRA supervised finetuning` |

  两者都是 `version="1"`。当前不存在“同一 `kind` 下多个实现”的情况。
- 一对一关系是服务端强制的：`kind=FINETUNING` 配 `implementation_id=builtin.mlp`（或反向错配）在创建时返回 `400 INVALID_MODELDEV_CREATE`（2026-10-26 实测）。因此创建时 `implementation_id` 可由 `kind` 唯一推导，但仍必须显式提交该字段。
- 可选 `kind` 取值为 `GENERAL_TRAINING` / `FINETUNING`，与响应 `kind` 同词汇。筛选在分页前执行，`total` 为筛选后条数，无匹配返回 `items: []`／`total: "0"`。
- `kind` 表达任务类型而不是实现标识；同一 `kind` 后续可能对应多个 `implementation_id`，前端须按返回项渲染，不能把 `kind` 当 `implementation_id` 的别名。
- `kind` 只在本接口接受，且只认大写枚举值：小写、`UNSPECIFIED`、空值、重复 `kind`、与 `state` 混用，以及 `implementation_id` 等未声明参数都返回 `400 INVALID_MODELDEV_QUERY`（2026-10-26 实测口径）。
- 分页对本接口生效，**页边界随筛选后的 `total` 收缩**：
  - 不带 `kind`：`total` 为 `"2"`，`pageSize=1` 时 `page=1` 返回 `GENERAL_TRAINING`、`page=2` 返回 `FINETUNING`、`page>=3` 返回空数组。
  - 带 `kind=FINETUNING`：`total` 为 `"1"`，`page=1` 返回 `builtin.sft_lora`，`page>=2` 即为空数组。
  - 带 `kind=GENERAL_TRAINING`：`total` 为 `"1"`，`page=1` 返回 `builtin.mlp`，`page>=2` 即为空数组。
  - 因此翻页循环请以**响应里的 `total`** 为准，不要按固定页数或写死 `page<=3`。要拿全量请一次请求 `pageSize>=total`。
- `parameter_rules` 里的 `default_value` / `minimum` / `maximum` / `allowed_values` / `numeric_representation` / `validation_description` 都是运行时值，不要硬编码默认值和边界。
- **区间开闭必须与 `minimum` / `maximum` 成对解读**，否则会放过后端拒绝的值：

  | 参数 | `minimum` | `minimum_inclusive` | `maximum` | `maximum_inclusive` | 真实区间 |
  | --- | --- | --- | --- | --- | --- |
  | `learning_rate` | `"0"` | `false` | `null` | `null` | `(0, +∞)` |
  | `lora_dropout` | `"0"` | `true` | `"1"` | `false` | `[0, 1)` |
  | `epochs` / `batch_size` / `cutoff_len` / `max_steps` / `seed` 等整数 | 见返回 | `true` | 见返回 | `true` | `[min, max]` |

  典型陷阱：`lora_dropout` 的 `maximum` 是 `"1"`，但 `maximum_inclusive=false`，**填 1 会被后端拒绝**；`learning_rate` 的 `minimum` 是 `"0"`，但 `minimum_inclusive=false`，**填 0 也会被拒绝**。前端的输入校验请按上表判定，不要只比较 `minimum` / `maximum` 的数值。
- `minimum_inclusive` / `maximum_inclusive` 是**可为 null** 的布尔：`null` 表示服务端没有给出该侧区间语义（通常同时 `minimum` 或 `maximum` 也是 `null`），**不要当作 `true` 处理**。
- `editable` 表示该参数当前是否允许用户改写，`non_editable_reason` 在 `editable=false` 时给出原因。当前两个已发布实现的所有参数都是 `editable=true`，此时 `non_editable_reason` 为 `""`。前端应按 `editable` 决定是否置灰，不要按参数名硬编码。
- 不传某个参数与显式传默认值，在服务端是**两种不同原意图**（会影响幂等哈希）。未填写的参数请保持“未提交”，不要补默认值。
- `execution_timeout_max_seconds` 是 `timeout_seconds` 的上限；`step_timeout_seconds` 是只读值。

## 5. 算力目录（TRAINING）

```
GET /api/v1/compute-presets?usage=TRAINING&page=1&pageSize=100
```

- `usage` 必填，`TRAINING` 覆盖普通训练与微调。可选 `page` / `pageSize` / `name` / `code` / `compute_type` / `gpu_mode`。
- 响应比实现目录多两个顶层字段：`gpu_catalog_status`、`gpu_catalog_reason`。

```json
{
  "items": [
    {
      "id": "1a832105-c69e-47ba-a423-915e436c2fb4",
      "name": "ModelDev v2.1 Fine CPU 8C 16GiB",
      "code": "modeldev-v21-fine-cpu-8c-16g",
      "compute_type": "cpu",
      "cpu_millis": "8000",
      "memory_mib": "16384",
      "enabled": true,
      "gpu": null,
      "selectable": true,
      "unavailable_reason": ""
    },
    {
      "id": "a240cb09-0816-4d19-8c04-c8fd36ded552",
      "name": "ModelDev v2.1 node3 Shared 4C 8GiB",
      "code": "mdv21-newenv-shared2-4c8g-20261023",
      "compute_type": "gpu",
      "cpu_millis": "4000",
      "memory_mib": "8192",
      "enabled": true,
      "gpu": {
        "profile_id": "ac998f7a-c39a-49aa-8aae-d932d567116a",
        "profile_version": "1",
        "mode": "SHARED_FIXED",
        "model_key": "nvidia-nvidia-geforce-rtx-4090",
        "devices_per_instance": 1,
        "memory_mib_per_device": "24570",
        "memory_mib_total": "24570",
        "core_limit_percent": 50
      },
      "selectable": false,
      "unavailable_reason": "GPU_CAPACITY_UNKNOWN"
    }
  ],
  "total": "9",
  "gpu_catalog_status": "OK",
  "gpu_catalog_reason": ""
}
```

**`selectable=false` 表示该规格当前不可提交**，调用方应在提交前据此排除，不要依赖服务端拦截。`unavailable_reason` 取值：

| 值 | 含义 |
| --- | --- |
| `GPU_CAPACITY_UNKNOWN` | 容量数据不完整，无法判断 |
| `GPU_TEMPORARILY_FULL` | 暂时无空闲设备 |
| `GPU_SUPPLY_PAUSED` | 供给已暂停受理 |
| `GPU_PROFILE_DISABLED` | 规格被停用 |
| `GPU_PROFILE_DELETED` | 规格已删除 |
| `GPU_PROFILE_NOT_ACTIVE` | 规格未处于可用状态 |
| `GPU_SUPPLY_CONFIG_MISMATCH` | 供给配置与节点不一致 |
| `GPU_CATALOG_UNAVAILABLE` | 目录服务不可用 |

注意：**目录可见 ≠ 有资格创建**。即使 `selectable=true`，创建时仍会重新校验当前权限、套餐和额度。

### 5.1 GPU 规格当前全部不可选

现网状态（2026-10-26）：9 条规格中 4 条 CPU 可选，5 条 GPU **全部** `selectable=false / GPU_CAPACITY_UNKNOWN`。

- 原因不是代码没实现，而是 **GPU 容量观测不完整**：集群 3 个节点都有 vGPU 可分配（node1/node2 各 2 卡、node3 4 卡），但 Accelerator 上报的容量数据不完整，目录侧只能判定为未知。
- 用 `selectable=false` 的 GPU 规格直接提交创建，当前返回 `503 MODELDEV_CREATE_UNAVAILABLE`（2026-10-26 实测），不是 400。
- GPU 链路本身已有真实的历史执行记录（含训练与微调、额度占用与全额退还），所以本文档的 GPU 字段、额度语义、释放规则都按已验证行为编写；**只要后端补齐容量观测、`selectable` 变 true，调用方无需改动即可用**。
- 目录接口始终返回全部 GPU 规格（含 `selectable=false`），不会隐藏；`selectable` 恢复后同一份响应即可提交。

## 6. 输入版本

```
GET /api/v1/modeldev/input-versions?page=1&pageSize=20&state=READY
GET /api/v1/modeldev/input-versions?page=1&pageSize=20&format=CSV
GET /api/v1/modeldev/input-versions/{input_version_id}
```

- `state` 可选，取值 `FIXING` / `VALIDATING` / `READY` / `FAILED`；其他值 `400`。
- `format` 可选，只接受 `CSV` / `JSON`（大写）；其他值（含小写 `csv`、空值、`PARQUET`）一律 `400`，不会静默返回空页。
- `state` 与 `format` 可同时使用，两个条件取交集。
- 筛选在分页前生效，`total` 是筛选后的条数，翻页仍以 `total` 为准。
- 格式与任务的匹配是硬约束：`CSV` 只能用于普通训练，`JSON` 只能用于微调。选数据集时按当前任务类型带对应的 `format`，比在返回里再过滤更可靠。
- 详情接口不接受任何 query（包括裸 `?`），且直接返回对象本体。

```json
{
  "input_version_id": "04790fcc-dcd6-46e1-869e-84da77a975a8",
  "state": "READY",
  "format": "CSV",
  "size_bytes": "211325",
  "sha256": "2faa51063bb4aaf10d0e7848b547ab63cd8557cd4372d16c5666f90d083c3f27",
  "row_count": 1379,
  "feature_count": 16,
  "created_at": "2026-10-22T10:14:31.288028Z"
}
```

**当前没有可用的数据集上传/导入接口**，数据集由运维侧通过 CLI 登记（`admin modeldev-import-csv`、`admin modeldev-import-finetuning-dataset`）。只能选择 `state=READY` 的版本；`FIXING` / `VALIDATING` / `FAILED` 均不可用于创建。

## 7. 创建训练任务

```
POST /api/v1/modeldev/executions
Content-Type: application/json
```

成功返回 **HTTP 202**，不是 200/201。

### 7.1 请求体（普通训练）

```json
{
  "name": "cpu-smoke-01",
  "kind": "GENERAL_TRAINING",
  "implementation_id": "builtin.mlp",
  "compute_preset_id": "84547768-be1b-45dd-bdb9-be824f58d583",
  "dataset_version_id": "04790fcc-dcd6-46e1-869e-84da77a975a8",
  "parameters": {
    "epochs": 5,
    "batch_size": 32
  },
  "idempotency_key": "3f2b9c14-8e5a-4a0e-9a6f-1d2c3b4a5e60"
}
```

- `parameters` 必填对象，允许 `{}`（全用实现默认值）。
- 普通训练参数只允许：`learning_rate`（非指数十进制字符串）、`epochs`、`batch_size`（正整数）、`seed`（uint32）。
- `timeout_seconds` 可省略；省略才使用冻结材料默认值（10800）。传值必须 `> 0` 且不超过目录的 `execution_timeout_max_seconds`。
- 普通训练不得出现 `base_model_version_id` / `target_model_id` / `new_model_name`。

### 7.2 请求体（微调）

```json
{
  "name": "lora-sft-01",
  "kind": "FINETUNING",
  "implementation_id": "builtin.sft_lora",
  "compute_preset_id": "1a832105-c69e-47ba-a423-915e436c2fb4",
  "dataset_version_id": "04790fcc-dcd6-46e1-869e-84da77a975a8",
  "base_model_version_id": "9a1f0f43-0f6e-4f45-9a34-1f1d4b0e2c31",
  "target_model_id": "712515ef-1eab-4bad-968f-7c661677f688",
  "parameters": {"max_steps": 50},
  "idempotency_key": "c1a2b3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d"
}
```

- `base_model_version_id` 必填（微调）。
- `target_model_id` 与 `new_model_name` **二选一或都省略**；同时提交 `400`。
  - 都省略 → 只训练并发布产物，不写 Model 登记。
  - `target_model_id` → 登记到已有模型的版本。
  - `new_model_name` → 新建模型，格式 `^[a-z0-9.-]{1,63}$`。
- 微调参数：`learning_rate`、`max_steps`、`per_device_train_batch_size`、`gradient_accumulation_steps`、`cutoff_len`、`lora_rank`、`lora_alpha`、`lora_dropout`、`seed`。
- 即使不登记模型，也会校验基模真实 `READY` 且当前用户有基模版本读取与下载权限。

**基模与目标模型的数据来源**（微调创建页需要用，均在 Model 服务域）：

```bash
GET /api/v1/models?page=1&pageSize=20                          # 模型列表（含 status）
GET /api/v1/models/{model_id}/versions?page=1&pageSize=20      # 某模型的版本列表 → 取 base_model_version_id
GET /api/v1/model-versions/{model_version_id}                  # 版本详情（校验 ready）
GET /api/v1/model-versions/{model_version_id}/download-url     # 受控基模需要可下载
GET /api/v1/models/{model_id}                                  # 登记到已有模型时校验
```

注意：**没有 `GET /api/v1/model-versions` 列表路由**（实测 404），只能先列模型再列版本。现网 `GET /api/v1/models` 返回 `total=3`（2026-10-26）。

### 7.3 请求硬约束

| 约束 | 违反结果 |
| --- | --- |
| `Content-Type` 必须是 `application/json`（`; charset=utf-8` 也接受，`text/plain` 等被拒） | 400 `INVALID_MODELDEV_CREATE` |
| URL 不得带任何 query，包括裸 `?` | 400 |
| body ≤ 16 KiB 且为单个 JSON 对象 | 400 |
| 顶层重复键、嵌套重复键、`null`、未知键、类型错误 | 400 |
| `idempotency_key` 必填、非空、不含控制字符、不能是孤立 UTF-16 代理项 | 400 |
| `name` 1–80 字符、去首尾空白后不变、不含控制字符 | 400 |
| `compute_preset_id` / `dataset_version_id` / `base_model_version_id` / `target_model_id` 必须是规范小写 UUID | 400 |
| `epochs` / `batch_size` / 其他正整数参数 ≥ 1（且必须是 JSON 数字，见 7.5） | 400 |
| `learning_rate` 非指数十进制、≤32 字符、float32 正有限 | 400 |
| `lora_dropout` ∈ `[0,1)` | 400 |

### 7.4 响应

```json
{
  "operation_id": "35c4212e-7930-4e6d-80b6-681f7c2d4a95",
  "execution_id": "9707c369-0d71-4152-b546-874511762189",
  "replayed": false,
  "compute_state": "ACCEPTED",
  "delivery_state": "PENDING",
  "close_state": "OPEN",
  "resource_state": "NOT_APPLICABLE"
}
```

- `replayed=true` 表示同一幂等键的原意图被复用，后端返回的是**原记录**（原 execution_id、原冻结参数），不是创建失败。
- 同一 `idempotency_key` 配不同意图 → `409 IDEMPOTENCY_CONFLICT`。
- 幂等键建议用「提交时生成的 UUID v4」，并在整次重试过程中保持不变（改参数后重新生成）。

### 7.5 请求与响应的类型差异（重要）

**同一个参数，请求里是 JSON 数字，响应里是字符串。** 这是最容易写错的一处，请按下面两张表分别处理。

**创建请求 `parameters`（必须是 JSON 数字）**：

| 参数 | 请求类型 | 响应 `explicit_parameters` 类型 |
| --- | --- | --- |
| `epochs` | 数字 `5` | 字符串 `"5"` |
| `batch_size` | 数字 `32` | 字符串 `"32"` |
| `max_steps` | 数字 `50` | 字符串 `"50"` |
| `per_device_train_batch_size` | 数字 `1` | 字符串 `"1"` |
| `gradient_accumulation_steps` | 数字 `2` | 字符串 `"2"` |
| `cutoff_len` | 数字 `96` | 字符串 `"96"` |
| `lora_rank` | 数字 `4` | 字符串 `"4"` |
| `lora_alpha` | 数字 `12` | 字符串 `"12"` |
| `seed` | 数字 `73` | 数字 `73`（唯一例外） |
| `learning_rate` | 字符串 `"0.007"` | 字符串 `"0.007"`（两侧都是字符串） |
| `lora_dropout` | 字符串 `"0.05"` | 字符串 `"0.05"`（两侧都是字符串） |

**照抄响应再回传会 400。** 不要把详情接口拿到的 `explicit_parameters` 原样作为创建参数提交——响应里的 `"5"` 要转成数字 `5`。2026-10-26 现网实测：`parameters` 里传 `{"epochs":"3","batch_size":"8"}` → `400 INVALID_MODELDEV_CREATE`。

顶层 `timeout_seconds` 同理：请求传数字 `3600`，响应 `timing.execution_timeout_seconds` 是字符串 `"3600"`。

## 8. 任务列表

```
GET /api/v1/modeldev/executions?page=1&pageSize=20
GET /api/v1/modeldev/executions?page=1&pageSize=20&compute_state=SUCCEEDED
GET /api/v1/modeldev/executions?page=1&pageSize=20&compute_state=FAILED&close_state=NEEDS_REVIEW
GET /api/v1/modeldev/executions?page=1&pageSize=20&name=lora
```

**支持的 query 参数**

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `page` | 整数 ≥1 | 页码，缺省 1 |
| `pageSize` | 整数 1–100 | 每页条数，缺省 20 |
| `compute_state` | 字符串 | 可选，计算状态筛选 |
| `delivery_state` | 字符串 | 可选，交付状态筛选 |
| `close_state` | 字符串 | 可选，关闭状态筛选 |
| `name` | 字符串 | 可选，任务名筛选，不区分大小写的子串匹配 |

- **筛选条件可任意叠加**（AND 语义），都能在分页前生效，`total` 是筛选后的条数。
- 取值**不带枚举前缀，且只认大写**：`compute_state` 用 `ACCEPTED` / `SUBMITTING` / `SUBMISSION_UNCERTAIN` / `QUEUED` / `PREPARING` / `TRAINING` / `SUCCEEDED` / `FAILED` / `CANCELED`（以及 `SUBMISSION_NOT_SENT`）；`delivery_state` 用 `PENDING` / `PUBLISHING` / `PUBLISHED` / `FAILED`；`close_state` 用 `OPEN` / `CLOSING` / `CLOSED` / `NEEDS_REVIEW`。词汇与响应里的同名字段完全一致。
- 非法取值一律 `400 INVALID_MODELDEV_QUERY`，**不会静默退化成不筛选**：小写（`succeeded`）、空值、重复（`compute_state=A&compute_state=B`）、`UNSPECIFIED`、以及把某个枚举的成员传给另一个枚举（如 `delivery_state=SUCCEEDED`）都会被拒。
- `name` 匹配的是创建时提交的 `name`，即响应里的同名字段。空串、纯空白、超过 80 字、含控制字符都返回 `400`——这些值不可能命中任何已存在的任务，拒绝比返回空页更诚实。**服务端不做模糊拼写纠正**，前端做搜索框时按用户输入原样传即可。
- `keyword`、`sort`、`limit`、`cursor` 仍未支持，传了返回 `400`。
- **排序是 `accepted_at` 倒序（最新的在前）**，`accepted_at` 相同时用 `execution_id` 倒序稳定并列，所以翻页不会重复或漏行。可以直接用第一页展示"最近的任务"，不需要本地排序。
- 响应：

```json
{"items":[{"operation_id":"...","execution_id":"...","name":"cpu-smoke-01","kind":"GENERAL_TRAINING","compute_state":"SUCCEEDED","delivery_state":"PUBLISHED","close_state":"CLOSED","resource_state":"NOT_APPLICABLE","current_step":"CLOSE","stop_requested":false,"close_generation":"1","accepted_at":"2026-10-23T08:36:57.790931Z","deadline_at":"2026-10-23T11:36:57.790931Z","observed_at":"2026-10-23T09:10:12.345678Z"}],"total":"16"}
```

列表项是详情对象的**同构裁剪**，字段含义见第 9 节。

## 9. 任务详情

```
GET /api/v1/modeldev/executions/{execution_id}
```

不接受任何 query。直接返回执行对象本体。

```json
{
  "operation_id": "2559f36c-022f-4c3c-8a8e-c1aa2741c0c3",
  "execution_id": "23da0705-7fd7-44f0-89e4-e40fc2400115",
  "name": "lora-sft-01",
  "kind": "FINETUNING",
  "implementation_id": "builtin.sft_lora",
  "compute_preset_id": "a240cb09-0816-4d19-8c04-c8fd36ded552",
  "compute": {
    "compute_preset_id": "a240cb09-0816-4d19-8c04-c8fd36ded552",
    "selection_digest": "…",
    "cpu_millis": "4000",
    "memory_mib": "8192",
    "device_mode": "gpu"
  },
  "input_version_id": "04790fcc-dcd6-46e1-869e-84da77a975a8",
  "image_version_id": "df1bff1b-351f-5fe4-a6cf-5124b1c0ac82",
  "compute_state": "SUCCEEDED",
  "delivery_state": "PUBLISHED",
  "close_state": "CLOSED",
  "resource_state": "NOT_APPLICABLE",
  "current_step": "CLOSE",
  "stop_requested": true,
  "close_generation": "1",
  "accepted_at": "2026-10-23T16:26:52.176710Z",
  "deadline_at": "2026-10-23T19:26:52.176710Z",
  "observed_at": "2026-10-23T18:02:27.125401Z",
  "parameter_contract_version": "ani.sft-lora.parameters.v2.1",
  "resolved_configuration_sha256": "…",
  "resolved_parameters": [
    {"name": "max_steps", "type": "INTEGER", "value": "50"}
  ],
  "explicit_parameters": {
    "max_steps": "50"
  },
  "timing": {
    "execution_timeout_seconds": "10800",
    "execution_timeout_max_seconds": "14400",
    "step_timeout_seconds": "14400"
  },
  "model_registration": {
    "state": "READY",
    "registration_id": "…",
    "model_id": "712515ef-1eab-4bad-968f-7c661677f688",
    "model_version_id": "…",
    "failure_code": ""
  },
  "history": {
    "run_id": "…",
    "workflow_uid": "…",
    "observed_at": "2026-10-23T18:02:20Z",
    "run_state": "SUCCEEDED",
    "run_finished_at": "2026-10-23T18:01:55Z",
    "workflow_phase": "SUCCEEDED",
    "workflow_finished_at": "2026-10-23T18:02:10Z",
    "workflow_resource_version": "…",
    "workflow_nodes_sha256": "…"
  },
  "steps": [
    {
      "role": "prepare",
      "state": "SUCCEEDED",
      "started_at": "2026-10-23T09:55:32.051273Z",
      "finished_at": "2026-10-23T09:56:58Z",
      "exit_code": 0
    },
    {"role": "train-wait", "state": "SUCCEEDED", "started_at": "…", "finished_at": "…", "exit_code": 0},
    {"role": "export", "state": "SUCCEEDED", "started_at": "…", "finished_at": "…", "exit_code": 0}
  ],
  "metrics": [
    {"name": "train.loss", "decimal_value": "0.7341379523277283", "step": "1"}
  ],
  "failure": {
    "reason": "UPSTREAM_UNAVAILABLE",
    "safe_message": "the trainer exited before writing the export",
    "violations": [
      {"field": "parameters.epochs", "reason": "out of range"}
    ],
    "correlation_id": "0f3a1c2e-…"
  },
  "managed_settings": {
    "stage": "sft",
    "finetuning_type": "lora",
    "lora_target": "all",
    "lora_rank": "8",
    "lora_alpha": "16",
    "cutoff_len": "128",
    "per_device_train_batch_size": "1",
    "gradient_accumulation_steps": "1",
    "seed": "42",
    "data_seed": "42",
    "device": "cpu",
    "fp16": false,
    "bf16": false,
    "flash_attn": "disabled",
    "report_to": "none",
    "save_strategy": "no",
    "template": "qwen",
    "train_dtype": "float32",
    "export_dtype": "float32",
    "verify_dtype": "float32",
    "lr_scheduler_type": "linear",
    "warmup_ratio": "0",
    "weight_decay": "0",
    "lora_dropout": "0",
    "num_train_epochs": "3",
    "optim": "adamw_torch",
    "logging_steps": "1"
  },
  "base_model": {
    "model_id": "5f0d0c1e-8f0a-4b7e-9a2c-1d3e5f7a9b0c",
    "version_id": "0b1c2d3e-4f5a-6b7c-8d9e-0a1b2c3d4e5f",
    "family": "Qwen2.5-0.5B-Instruct",
    "format": "safetensors"
  }
}
```

字段说明：

| 字段 | 说明 |
| --- | --- |
| `operation_id` | 受理操作 ID，与额度账本对应 |
| `compute.compute_preset_id` / `selection_digest` | 受理时冻结的算力选择；**不随目录变化** |
| `compute.device_mode` | `cpu` 或 `gpu`，由算力规格决定 |
| `compute.cpu_millis` / `memory_mib` | 受理时冻结值（字符串） |
| `explicit_parameters` | 用户**显式提交**的参数，仅包含提交过的键；省略即未出现。**整型值在此处是字符串**（`"5"`），与创建请求的数字类型不同，见 7.5 |
| `resolved_parameters` | 冻结后的最终参数（含实现默认值），`value` 一律字符串 |
| `timing` | 受理时冻结的时长预算 |
| `model_registration` | 仅微调且指定了登记目标时出现；普通训练为 `null` |
| `history` | 关闭时持久化的控制器证据；缺失字段表示“当时无该证据”，不要补猜 |
| `steps` | 流水线步骤时间线，按执行顺序排列。普通训练 5 步，微调 8 步；步集由 `kind` 决定，所以 `role` / `state` 是字符串不是枚举 |
| `metrics` | 训练曲线点，来自已发布的 `METRICS` 产物。目前只有 `train.loss`；`decimal_value` 是字符串，`step` 也是字符串（int64 序列化为十进制文本，如 `"1"`），两者都需按十进制解析。点数等于实际写入的点数（实测 7~1379 点，最高单次响应约 108 KB）；没有曲线时是**空数组 `[]`**，不是缺字段 |
| `failure` | 失败诊断，**只有确实失败过才有值**，正常结束或仍在运行时为 `null`。见下方 `failure` 字段 |
| `managed_settings` | 服务端托管的训练超参，**只读展示**。只有微调才有值，普通训练为 `null`。见下方 `managed_settings` 字段 |
| `base_model` | 微调所用的基座模型身份。只有微调才有值，普通训练为 `null`。见下方 `base_model` 字段 |

`steps[]` 字段：

| 字段 | 说明 |
| --- | --- |
| `role` | 步骤角色，如 `prepare`、`train-wait`、`export`、`collect` |
| `state` | 步骤状态，如 `PENDING` / `RUNNING` / `SUCCEEDED` / `FAILED` |
| `started_at` / `finished_at` | RFC3339；未开始或未结束时为 `null` |
| `exit_code` | 进程退出码；未结束为 0 |
| `deadline_at` | 该步的超时截止时间 |

`steps` 与 `metrics` 都随详情一起返回，**不需要额外请求**。`metrics` 为空数组表示该执行没有已发布的 `METRICS` 产物（例如仍在训练或已失败）。

`failure` 字段（失败时才有）：

| 字段 | 说明 |
| --- | --- |
| `reason` | 失败原因枚举名，不带前缀。`STEP_FAILED` 表示某个流水线步骤失败；其它取值（`INVALID_ARGUMENT` / `RESOURCE_NOT_FOUND` / `UPSTREAM_UNAVAILABLE` / `ENVIRONMENT_NOT_READY` 等）来自服务端记录的原始诊断 |
| `safe_message` | 面向人的安全描述，**可直接展示**。服务端已剔除凭据、存储密钥与隐藏的对象事实 |
| `violations[]` | 字段级明细，`{field, reason}`。`reason=STEP_FAILED` 时形如 `{field:"steps.train-wait", reason:"FAILED"}`，指出哪个步骤失败；空数组表示没有字段级信息 |
| `correlation_id` | 服务端关联 ID，用于向后端追日志；**不是**业务标识，不要展示为主键 |

`failure` 是**执行结果的一部分**，与 HTTP 错误不同：接口仍然返回 `200`，只是这次训练失败了。`failure=null` 只代表“没有失败诊断”，不代表成功——成功仍需看 `compute_state=SUCCEEDED`。

**关于失败原因的精度**：本期后端只保留到“哪个步骤失败”，没有自由文本的训练日志级原因。`reason=STEP_FAILED` 时的 `safe_message` 形如 `step train-wait failed`，`violations` 列出所有 `state=FAILED` 的步骤。要看具体报错，请用第 11 节的日志接口读 `train-wait` 步骤的日志。更细的原因文本需 ModelDev 新增字段，本期不具备。

`managed_settings` 字段（微调才有）：

服务端托管的训练超参，**创建请求不接受这些字段**，只能读取展示。`stage` 本期恒为 `"sft"`（当前只有 SFT，不存在 DPO/PPO，**不要做成可选项**）。`lora_rank` / `lora_alpha` / `cutoff_len` / `per_device_train_batch_size` / `gradient_accumulation_steps` / `seed` / `data_seed` / `num_train_epochs` / `logging_steps` 是整数；`warmup_ratio` / `weight_decay` / `lora_dropout` 是十进制字符串；`fp16` / `bf16` 是布尔。

不要把 `delivery_state=PUBLISHED` 当成训练成功；判断成功只看 `compute_state`。

`base_model` 字段（微调才有）：

| 字段 | 说明 |
| --- | --- |
| `model_id` | 基座模型 ID |
| `version_id` | 基座模型版本 ID |
| `family` | 模型族名，如 `Qwen2.5-0.5B-Instruct`，可直接展示 |
| `format` | 权重格式，如 `safetensors` |

`base_model` **只有身份信息**：没有对象键、存储桶、存储 scope 和文件清单，因此**不能**据此拼下载地址。要下载请走第 13 节的产物接口。普通训练恒为 `null`；微调且登记了基座时才有值。

### 9.1 容器组

```
GET /api/v1/modeldev/executions/{execution_id}/pods
```

可选参数 `layer`（`all` 默认 / `trainer` / `pipeline`）与 `includeRecycled`（`true` / `false`，默认 `false`）。**默认不返回已回收的容器组**；要在列表里看到它们必须显式传 `true`。

```json
{
  "pods": [
    {
      "pod_name": "md-rtttx-2081272040",
      "pod_uid": "…",
      "pod_ip": "10.244.3.51",
      "phase": "Succeeded",
      "ready": false,
      "node_name": "node3",
      "restarts": 0,
      "created_at": "2026-10-23T09:55:20Z",
      "start_time": "2026-10-23T09:55:22Z",
      "layer": "PIPELINE",
      "availability": "ORPHANED",
      "log_id": "…",
      "log_available": true,
      "containers": [{"name": "main", "type": "container", "state": "terminated", "exit_code": 0}],
      "init_containers": [],
      "ephemeral_containers": [],
      "api_present": true,
      "metric_instance_id": "…"
    }
  ],
  "observed_at": "2026-10-27T04:25:00Z",
  "availability": "LIVE"
}
```

**所有字段都会输出，空值不会省略**（BFF 用 `UseProtoNames + EmitUnpopulated` 编码）：字符串为空是 `""`、`null` 只用于消息型字段（如 `last_state`）、数值为 `0`、布尔为 `false`、数组为 `[]`、Map 为 `{}`。所以"Pod 还在运行"时 `exit_code` 是 `0` 而不是缺失，`deletion_timestamp` 是 `""`。前端必须靠 `phase`/`availability` 判断状态，不能靠字段是否存在。

| 字段 | 说明 |
| --- | --- |
| `layer` | `TRAINER`（训练容器）或 `PIPELINE`（流水线控制器） |
| `availability` | 容器组的存在状态，见下表 |
| `phase` | **仅** Kubernetes 原始 phase；已回收或从未创建的容器组由 `availability` 表达，`phase` 可能仍是历史值 |
| `restarts` | 重启次数 |
| `exit_code` | worker 容器退出码；未终止时字段缺省，不要当成 0 |
| `containers` / `init_containers` / `ephemeral_containers` | 三类容器状态分列；诊断 prepare 阶段失败时看 `init_containers` |
| `metric_instance_id` | 该 Pod 在可观测侧上报的实例 ID：worker 为执行 ID，step 为 KFP run ID。是 9.3 的内部依据，前端不需要传；标签早于该字段的历史 Pod 为空串 |

`availability` 取值：

| 值 | 含义 |
| --- | --- |
| `LIVE` | 容器组仍存在 |
| `RECYCLED` | 曾经存在，已被回收 |
| `NEVER_CREATED` | 从未创建（通常任务未走到该阶段） |
| `REJECTED` | 创建被拒绝 |
| `ORPHANED` | 已不属于当前执行但仍可观测 |
| `CREATION_PENDING` | 创建中 |

顶层 `availability` 描述**本次查询**能否取到数据，不要和单个容器组的 `availability` 混用。

### 9.2 事件

```
GET /api/v1/modeldev/executions/{execution_id}/events[?scope=key|all|warning]
```

`scope` 省略等价于 `key`；传其它值返回 `400 INVALID_MODELDEV_QUERY`。

```json
{
  "events": [
    {
      "type": "Warning",
      "reason": "BackOff",
      "message": "Back-off restarting failed container",
      "first_timestamp": "2026-10-23T09:56:30Z",
      "last_timestamp": "2026-10-23T09:58:10Z",
      "count": 5,
      "source": "kubelet",
      "involved_kind": "Pod",
      "involved_name": "md-rtttx-2081272040",
      "involved_uid": "…",
      "field_path": "",
      "reporting_instance": "node3"
    }
  ],
  "observed_at": "2026-10-27T04:25:00Z",
  "retention_hint": "1h",
  "truncated": false
}
```

- `retention_hint`：Kubernetes 事件的保留窗口，当前为 `1h`。**超过窗口的事件不会返回**，`events` 为空是正常结果，不代表出错。
- `truncated=true` 表示本次结果被截断。
- 事件来自 Kubernetes Event 对象，不是执行日志的替代品。

### 9.3 资源曲线（CPU/内存）

```
GET /api/v1/modeldev/executions/{execution_id}/resource-metrics
    ?pod_uid=&container=&start=&end=&step_seconds=&metric_codes=
```

**一次请求只返回一个 Pod 的一个容器**：可观测侧的 target 合同一次只解析一个物理工作负载，
并行 step 的曲线不能合并成一次调用。前端按 Pod 分页懒加载。

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `pod_uid` | 是 | 取自 9.1 `pods[].podUid`；用 Pod UID 而非名字，重建后的 Pod 不会继承旧曲线 |
| `container` | 是 | worker 层只能 `node`，step 层只能 `main`；传错返回 `400`，不会静默返回空曲线 |
| `start` / `end` | 是 | RFC3339，窗口 ≤ 24h，`end` 不得晚于当前时间 |
| `step_seconds` | 否 | 默认 `30`；允许 30–3600，且必须是 30 的整数倍；点数上限 3000 |
| `metric_codes` | 是 | 逗号分隔，只接受 `cpu.usage_cores` / `memory.working_set_bytes` |

Pod 的实例归属由服务端从 9.1 的 `metric_instance_id` 取得，不需要前端传；该字段为空（早于指标标签的 Pod）时，worker 回落到执行 ID，step 回落到运行时历史里的 run ID，两者都取不到时返回 `412`。

```json
{
  "execution_id": "…",
  "pod_uid": "…",
  "pod_name": "md-rtttx-2081272040",
  "container": "node",
  "start": "2026-10-27T00:00:00Z",
  "end": "2026-10-27T01:00:00Z",
  "step_seconds": 60,
  "observed_at": "2026-10-27T01:00:05Z",
  "source": "PROMETHEUS_CADVISOR_PROJECTION",
  "retention_hint": "24h",
  "device_mode": "cpu",
  "gpu_requested": false,
  "gpu_available": false,
  "series": [
    {
      "metric_code": "cpu.usage_cores",
      "unit": "cores",
      "status": "PARTIAL",
      "points": [
        { "timestamp": "2026-10-27T00:00:00Z", "value": "1.72" },
        { "timestamp": "2026-10-27T00:01:00Z", "gap_reason": "NO_DATA" }
      ]
    }
  ]
}
```

- `status`：`OK` / `PARTIAL` / `NO_DATA`，与 `points` 里真实值的个数一致。
- 缺口点**没有 `value`**，只有 `gap_reason`（`NO_DATA` / `AMBIGUOUS_SOURCE` / `INVALID_VALUE`）。不要渲染成 0。
- `source` 是 recording rule 对 cAdvisor 的投影，不是直读 cAdvisor。
- `retention_hint` 当前 `24h`；超出保留期返回空序列，接口不报错，前端不要承诺更长历史。
- `gpu_requested=true` 且 `gpu_available=false`：显示“GPU 指标不可用”，不要显示成 0。当前环境没有 GPU exporter。
- 曲线数据 30s 采集一次，前端轮询间隔不要低于 `step_seconds`。
- 每租户并发上限 4，BFF 会把超出部分排队；首屏（worker + 4 个 step）= 5 次调用，建议限制同时展开的详情页为 1。
- **`cpu.usage_cores` 对 step Pod 通常是整条 `NO_DATA`**：该指标用 `rate(...[5m])` 求值，要求同一 Pod 连续存活 ≥5 分钟，而 step Pod 一般只活 10–30 秒。这是采集规则的物理限制，不是接口缺陷；worker Pod 长时间运行可正常出点。前端对 step 曲线建议默认只展示内存。
- 2026-10-27 在 66 集群实测：`memory.working_set_bytes` 返回真实值（62.3 MiB），`status=PARTIAL`，未测到点的位置均为 `NO_DATA`；参数类负例（缺 `pod_uid`、非规范 UUID、未登记指标码、layer 与容器名不匹配、`step_seconds` 越界、窗口 >24h、`end` 晚于当前、重复指标码、未知 query 参数、Pod 不属于该执行、执行不存在、无 token）全部返回预期状态码。GPU 曲线形状尚未实测（环境中唯一的 GPU 执行 Pod 已被回收）。

## 10. 枚举取值

### 10.1 `compute_state`（计算状态）

| 值 | 含义 |
| --- | --- |
| `ACCEPTED` | 已受理，尚未投递 |
| `SUBMITTING` | 正在提交运行 |
| `SUBMISSION_UNCERTAIN` | 提交结果未知 |
| `SUBMISSION_NOT_SENT` | 原尝试未发出 |
| `SUBMISSION_CONFIRMED` | 已观测到运行创建 |
| `QUEUED` | 排队中 |
| `PREPARING` | 准备环境/数据 |
| `TRAINING` | 训练中 |
| `SUCCEEDED` | 计算成功 |
| `FAILED` | 计算失败 |
| `CANCELED` | 已取消 |

`SUBMISSION_NOT_SENT` 与 `SUBMISSION_UNCERTAIN` 都是**非终态且不可重发**，不存在“重新提交”的操作。

### 10.2 `delivery_state`（产物交付）

`PENDING` / `PUBLISHING` / `PUBLISHED` / `FAILED`。只有 `PUBLISHED` 才能列产物与下载。

### 10.3 `close_state`（关闭）

| 值 | 含义 |
| --- | --- |
| `OPEN` | 未进入关闭 |
| `CLOSING` | 关闭中（可能仍在占用 GPU） |
| `CLOSED` | 已关闭（终态） |
| `NEEDS_REVIEW` | 需人工核查（异常关闭），**不等于正常结束** |

### 10.4 `resource_state`

当前只有 `NOT_APPLICABLE`。

### 10.5 `current_step`（流水线步骤）

| 值 | 说明 |
| --- | --- |
| `PREPARE` | 准备工作区与输入 |
| `TRAIN_WAIT` | 训练执行/等待 |
| `COLLECT` | 收集产物 |
| `PUBLISH` | 发布产物 |
| `CLOSE` | 关闭与清理 |
| `EXPORT` | （仅微调）导出合并模型 |
| `VERIFY` | （仅微调）校验合并结果 |
| `REGISTER` | （仅微调）登记到 Model |

枚举里的先后**不是执行顺序**，本表不按流程排序。真实执行顺序以 `steps[]` 数组为准：

- 普通训练：`prepare → train-wait → collect → publish → close`
- 微调：`prepare → train-wait → export → verify → collect → publish → register → close`
  （未填登记目标时 `register` 不会成功执行）

`steps[]` 是**有序**数组，前端直接按数组顺序渲染时间线，不要按上面的枚举顺序重排。

### 10.6 `kind`

`GENERAL_TRAINING`（普通训练）、`FINETUNING`（微调）。

### 10.7 `model_registration.state`

| 值 | 含义 |
| --- | --- |
| `PENDING` | 已持久登记意图，外部写尚未有确定结果；此状态下 `registration_id` / `model_id` / `model_version_id` 不可用 |
| `READY` | 供应商回执已核验，`registration_id` / `model_id` / `model_version_id` 均为真实 UUID |
| `DEPENDENCY_UNAVAILABLE` | 依赖不可用，`failure_code` 固定为 `MODEL_DEPENDENCY_UNAVAILABLE` |

`READY` 与 `history.run_state=FAILED` 可以同时存在：训练 Run 失败但登记成功是合法组合，两者不冲突。

### 10.8 `history`

- `run_state`：`SUCCEEDED` / `FAILED` / `CANCELED`
- `workflow_phase`：`SUCCEEDED` / `FAILED` / `ERROR`
- 空字段表示原证据缺失，不要推断为成功。

### 10.9 `ArtifactView.role`

`CHECKPOINT`、`MODEL_CONFIG`、`METRICS`、`SUMMARY`、`INPUT_MANIFEST`、`EXECUTION_MANIFEST`、`LOG`、`HF_MODEL`、`LORA_ADAPTER`、`EXPORT_REPORT`、`VERIFY_REPORT`、`SOURCE_METADATA`。

普通训练实测为 `CHECKPOINT` / `MODEL_CONFIG` / `METRICS` / `SUMMARY` / `LOG` 一类；微调会额外出现 `HF_MODEL` / `LORA_ADAPTER` / `EXPORT_REPORT` / `VERIFY_REPORT` / `SOURCE_METADATA`。不要假设某个角色一定存在。

## 11. 日志（一次性快照，可用）

```
GET /api/v1/modeldev/executions/{execution_id}/logs?tail_lines=200&max_bytes=16384
```

> 本接口是**一次性快照**，可用（2026-10-27 实测 200）。流式日志见 11.1。
> 响应是一次性快照，不含增量或订阅语义。

- 只接受 `tail_lines`（1–1000）与 `max_bytes`（1–65536）。不传时服务端默认 200 行 / 16384 字节。
- 可选 `podUid`（规范 UUID）、`container`（小写容器名）、`previous`（`true` 读上一次终止实例）、`logId`（取自容器组的 `log_id`）。**`podUid` 与 `container` 必须同时传或同时不传**；只传 `container` 返回 `412`。不传任何选择器时走兼容路径：返回该执行训练 worker 的默认日志（`node` 容器）并优先使用归档 tail。
- 不接受 pod **名字**、namespace 等选择器；选 Pod 一律用 `podUid`。
- 响应：

```json
{
  "log_id": "…",
  "lines": [{"timestamp":"2026-10-23T08:40:01.123456Z","text":"epoch 1/5 loss=0.42"}],
  "truncated": true,
  "observed_at": "2026-10-23T09:10:12.345678Z",
  "source_kind": "TRAINER",
  "container_name": "trainer"
}
```

- 没有可用日志来源时返回 **`412 ARTIFACT_NOT_PUBLISHED`**（训练尚未产出日志，或日志已随容器回收）。
- 日志是**一次性快照**；需要持续跟随时用 11.1 的流式接口。

### 11.1 流式日志

```
GET /api/v1/modeldev/executions/{execution_id}/logs/stream
```

`Content-Type: text/event-stream`（SSE）。可选参数 `podUid`、`container`、`previous`、`sinceBytes`（续传偏移，规范十进制，上限 `2^30`）。服务端每连接最多保留 10 分钟，到期以 `event: end` + `reason: TIME_LIMIT` 正常收口，前端用最后的 `offset` 续传。

数据帧格式：

```
: ready

event: log
data: {"text":"epoch 1/5 loss=0.42","offset":115}

event: log
data: {"text":"epoch 2/5 loss=0.31","offset":268}

event: end
data: {"reason":"LOG_ENDED"}
```

| 帧 | 说明 |
| --- | --- |
| `: ready` | 注释帧，表示连接已建立并开始推送，可直接忽略 |
| `event: log` | 一行日志。`offset` 是该行在日志流中的累计字节偏移，用于断线续传判断 |
| `event: end` | 流正常结束。`reason` 取值 `LOG_ENDED` |

流的失败语义（HTTP 状态码在**首帧之前**返回，不是 SSE 帧）：

| 状态码 | `reason` | 含义 |
| --- | --- | --- |
| `412` | `LOG_UNAVAILABLE` | 该执行当前没有可读的日志来源 |
| `404` | `RESOURCE_NOT_FOUND` | 执行不存在 |
| `401` | — | 未认证 |

已连接的流不会回传错误帧，异常断开就是终止；调用方按 `event: end` 或连接关闭结束读取。

> 注意：训练容器的日志在容器回收后不再可读。已结束较久的执行会返回 `412 LOG_UNAVAILABLE`，这是设计行为，不是故障。

## 12. 停止任务

```
POST /api/v1/modeldev/executions/{execution_id}:stop
```

- **body 必须为空**，`{}` 也会 400；URL 不得带 query（裸 `?` 也 400）；不需要 `Content-Type`。
- 成功返回 **202**：

```json
{"operation_id":"…","execution_id":"…","stop_requested":true,"intent_generation":"1","replayed":false}
```

- 202 只表示**停止意图已持久化**。`close_state` 需继续轮询，未观察到 `CLOSED` 前不表示已关闭。
- 停止**不等于退额**；GPU 额度要等关闭并完成释放后才退回。
- 重复调用返回同一意图：`replayed=true`，`intent_generation` 保持 `"1"`（不会递增，2026-10-26 实测）。
- 执行不存在 → `404 RESOURCE_NOT_FOUND`；无权限 → `403 FORBIDDEN`。

## 13. 产物与下载

```
GET /api/v1/modeldev/executions/{execution_id}/artifacts?page=1&pageSize=20
GET /api/v1/modeldev/artifacts/{artifact_id}/content
```

产物列表（只返回 `delivery_state=PUBLISHED` 的文件）。真实响应（微调执行，`total=18`，此处节选）：

```json
{
  "items": [
    {
      "artifact_id": "0377b660-dd42-50a1-85a6-9a95c54b6bb9",
      "execution_id": "64320bf3-0a9a-41cc-8b03-557b6dc75d58",
      "filename": "export/merged/vocab.json",
      "role": "HF_MODEL",
      "size_bytes": "2776833",
      "sha256": "ca10d7e9fb3ed18575dd1e277a2579c16d108e32f27439684afa0e10b1440910",
      "delivery_state": "PUBLISHED",
      "verified_at": "2026-10-23T07:40:03.711965124Z"
    },
    {
      "artifact_id": "4cfce4c6-a3cd-5809-bb6f-328ed834e875",
      "execution_id": "64320bf3-0a9a-41cc-8b03-557b6dc75d58",
      "filename": "training/adapter/adapter_config.json",
      "role": "LORA_ADAPTER",
      "size_bytes": "855",
      "sha256": "54996d06b1333c5bc0232ed5bdeeed502bcd6d793164fab4f7df5093d927f14b",
      "delivery_state": "PUBLISHED",
      "verified_at": "2026-10-23T07:40:03.711965124Z"
    }
  ],
  "total": "18"
}
```

- **只接受 `page` / `pageSize`**。`role`、`filename` 等筛选传了也是 `400 INVALID_MODELDEV_QUERY`，需在本地过滤。
- `filename` 是**相对路径**（含 `training/…`、`export/merged/…`、`reports/…` 前缀），不是扁平文件名。
- 微调执行的真实角色分布：`HF_MODEL`（合并后的完整模型，含 `model.safetensors`）、`LORA_ADAPTER`、`EXPORT_REPORT`、`VERIFY_REPORT`、`METRICS`、`SUMMARY`、`SOURCE_METADATA`。

下载接口**不接受任何 query**，只做“换取短期 URL”：

```json
{
  "artifact": {
    "artifact_id": "0377b660-dd42-50a1-85a6-9a95c54b6bb9",
    "execution_id": "64320bf3-0a9a-41cc-8b03-557b6dc75d58",
    "filename": "export/merged/vocab.json",
    "role": "HF_MODEL",
    "size_bytes": "2776833",
    "sha256": "ca10d7e9fb3ed18575dd1e277a2579c16d108e32f27439684afa0e10b1440910",
    "delivery_state": "PUBLISHED",
    "verified_at": "2026-10-23T07:40:03.711965124Z"
  },
  "download_url": "https://ani-rustfs-svc.ani-platform.svc.cluster.local:9000/ani-md-v21-output-01a15f47/…?X-Amz-Algorithm=AWS4-HMAC-SHA256&…&X-Amz-Expires=60&…",
  "expires_at": "2026-10-26T08:16:44Z"
}
```

- **有效期只有 60 秒**（`X-Amz-Expires=60`）。拿到 URL 后必须立即使用，不要缓存或预先批量换取。
- **URL 主机是集群内域名**（`ani-rustfs-svc.ani-platform.svc.cluster.local:9000`）且使用自签证书，**从办公网直接访问必然失败**（DNS 不可解析 / 证书不受信）。2026-10-26 实测该 URL 连接超时。
  - 换 URL 这一步接口本身可用；真实下载需等对象存储提供对外入口，或由运维用 `kubectl port-forward` / 外层网关改写后再验证。
- 产物未发布 → `412 ARTIFACT_NOT_PUBLISHED`；不存在或无权限 → `404 RESOURCE_NOT_FOUND`。
- 不要把这个 URL 写入日志、埋点、快照或分享链接（含签名，等于临时凭证）。
- 不要经 BFF 中转二进制；URL 是对象存储直出。

## 14. 恢复模型登记（仅微调）

```
POST /api/v1/modeldev/executions/{execution_id}:resume-model-registration
```

- body 必须为空（`{}` 也拒绝），URL 不得带 query。
- 只有以下条件全部满足时，该接口才会返回 200：
  1. `kind=FINETUNING`
  2. 创建时指定了 `target_model_id` 或 `new_model_name`
  3. `close_state=CLOSED`
  4. `model_registration.state` 为 `PENDING` 或 `DEPENDENCY_UNAVAILABLE`
  5. 当前用户具备 `modeldev:manage_execution`、该执行的 GET 权限，以及对应 Model 权限
- 成功返回 **200 + 与详情同构的执行对象**，不新增第二套状态字段。
- 服务端行为：不重训、不重新导出、不重新受理，只恢复原登记或核实原未知结果；原目标、新模型名、基模、参数、登记键与冻结时长都不可覆盖。
- 未知响应（超时、5xx）时**不要自动重试**，先重新拉取详情确认事实。
- 权限绑定已于 2026-10-26 补齐并实测 `200`（见第 16 节）。

## 15. 轮询时序

### 15.1 创建后

```
POST → 202
  → 记下 execution_id / operation_id
  → 轮询 GET /executions/{id}：1s × 10 次 → 3s × 20 次 → 10s 持续
  → compute_state ∈ {TRAINING, SUCCEEDED, FAILED, CANCELED} 或 close_state=CLOSED 时降频
```

`delivery_state=PUBLISHED` 后可拉产物；`close_state=CLOSED` 后可停止轮询。

> `503` 时应退避重试，**不代表任务本身失败**——503 表示查询链路暂时不可用。

### 15.2 停止后

```
POST :stop → 202
  → 轮询详情直到 close_state ∈ {CLOSED, NEEDS_REVIEW}
  → 202 不代表已关闭，也不代表算力已释放
```

### 15.3 幂等与重试

- 只有 `replayed=true` 才是“原键复用”；网络超时后**用同一 `idempotency_key` 重试**，不要换新键。
- 换新键会创建第二条执行并二次占用额度。

## 16. 现网环境与联调前置

联调地址 `http://10.10.1.66:30085`。2026-10-27 全量逐条实测（Governance 已发布 `v21-detail-on-dev-20261027`，含执行详情合同）：

| 接口 | 实测 | 说明 |
| --- | --- | --- |
| `GET /api/v1/modeldev/implementations` | **200** | 2 条（`builtin.mlp` / `builtin.sft_lora`），规则完整 |
| `GET /api/v1/modeldev/input-versions` | **200** | 2 条 READY（CSV 1379×16、JSON 64 行） |
| `GET /api/v1/modeldev/input-versions/{id}` | **200** | 详情正常 |
| `GET /api/v1/compute-presets?usage=TRAINING` | **200** | 9 条：4 条 CPU 可选，5 条 GPU 全 `selectable=false / GPU_CAPACITY_UNKNOWN` |
| `GET /api/v1/models`、`/models/{id}/versions`、`/model-versions/{id}`、`/download-url` | **200** | 微调基模选择可用，`total=3` |
| `POST /api/v1/modeldev/executions` | **202** | CPU 与 GPU 均 202（GPU 仅限历史上 `selectable=true` 时），幂等重放正常，负例 400 |
| `GET /api/v1/modeldev/executions` | **200** | 22 条执行全部可列；只接受 `page`/`pageSize` |
| `GET /api/v1/modeldev/executions/{id}` | **200** | 四状态 + 参数 + `model_registration` + `history` + **`steps`** + **`metrics`** |
| `GET .../{id}/logs` | **200** | 一次性快照。无日志来源返回 `412 ARTIFACT_NOT_PUBLISHED`（见第 11 节） |
| `GET .../{id}/logs/stream` | **200** | SSE；`: ready` → `event: log` → `event: end`。无日志来源返回 `412 LOG_UNAVAILABLE`（见 11.1） |
| `GET .../{id}/artifacts` | **200** | 有产出的历史执行可正常列出（微调执行 `total=18`，`filename` 为嵌套相对路径） |
| `GET /api/v1/modeldev/artifacts/{id}/content` | **200** | 但 URL 是集群内地址 + 60 秒签名，集群外不可达（见第 13 节） |
| `GET .../{id}/pods` | **200** | 容器组，含 `layer` / `availability`（见 9.1） |
| `GET .../{id}/events` | **200** | Kubernetes 事件，保留窗口 `1h`（见 9.2）；`scope` 非法值 400 |
| `POST ...:stop` | **202** | 正常，重复调用 `replayed=true`、`intent_generation` 不变 |
| `POST ...:resume-model-registration` | **200** | 权限已绑定；但仅目标条件满足时 200，否则 `412 MODEL_REGISTRATION_UNAVAILABLE` |
| `GET /api/v1/model-versions`（列表） | **404** | 该路由不存在，只能通过 `/models/{id}/versions` |

实测补充：

- 22 条执行详情全部 200，`steps` 有值：普通训练 5 步，微调 8 步
- `steps` / `metrics` 随详情返回，不需要额外请求；微调实测 `metrics` 1000 点
- 未认证访问详情与子资源均为 **401**；不存在的 UUID 为 **404 `RESOURCE_NOT_FOUND`**（此前为 400，已修正）
- `page=0`、`pageSize=101`、非 UUID、未知参数仍是 **400 `INVALID_MODELDEV_QUERY`**

### `steps` / `metrics` 的验证程度（2026-10-27 复核）

上面"200"只说明**接口可达**，不代表内容已验收。2026-10-27 用集群全部 25 条执行做了一次内容核对，结论是主路径可用，但仍有未收敛项：

| 项 | 状态 |
| --- | --- |
| 步骤数量与顺序（训练 5 / 微调 8） | **已核实**，与 `steps[]` 实际数组一致 |
| 失败传播（失败步 FAILED、后续 PENDING、无误报 RUNNING） | **已核实** |
| `metrics` 解析（7~1379 点）与未发布时为 `[]` | **已核实** |
| `register` 成功路径（`model_registration.state=READY`） | **已核实** |
| `register` 在**未登记目标**的成功微调上显示 `STOPPED` | **缺陷，未修**（7 条微调中 4 条命中），前端不要把它当失败渲染 |
| stop / 步骤超时 / export 失败 / `DEPENDENCY_UNAVAILABLE` | **无样本，未验证** |
| `metrics` 点数上限 | 实测最高 1379 点、单次响应约 108 KB，**无上限无分页**，前端需自行降采样 |

细节、样本 id 与复现方式见工作区 [流水线进度与训练曲线验证计划](../../../docs/modeldev-流水线进度与训练曲线-验证计划-20261027.md)。

剩余唯一阻断：

**产物下载 URL 集群外不可达**：签名 URL 指向 `ani-rustfs-svc.ani-platform.svc.cluster.local:9000`（自签证书、有效期 60 秒），集群外无法解析与信任。需后端提供对外出口或改签外层域名；换 URL 这一步接口本身可用。

> 2026-10-26 已修复的三个问题（保留记录，便于排查历史现象）：
> 1. 执行列表 / 详情 / 日志曾全 **503**：Governance 依赖的 ModelDev 合同版本落后（`0814ae3`），新版 ModelDev 在 `ExecutionView` 增加 `steps` 字段后，严格未知字段校验 fail-closed。已升级依赖至 `687aa2f`。
> 2. 恢复登记曾 **403**：路由已注册但未绑定 permission。已补绑 `modeldev:manage_execution`（脚本 `scripts/ops/sql/register-modeldev-resume-registration-permission.sql`）。
> 3. 产物列表曾 **503**：Governance 侧校验要求 `filename` 是平铺文件名，而 ModelDev 实际返回嵌套相对路径（`export/merged/…`）。已改为允许合法相对路径（仍拒绝 `..`、绝对路径、反斜杠、控制字符），发布 `mdcontract-687aa2f-r2-20261026`。当前 21 条执行里 5 条微调执行各返回 18 个嵌套路径产物，均 **200**。
>
> 2026-10-27 已修复：
> 4. 列表 / 详情再次 **503**：dev 分支锁定的 ModelDev 合同回退到 `af7f9e98`（缺 `steps`），而线上 ModelDev 已是 detail 版。已将执行详情合同并入 dev 并发布 `v21-detail-on-dev-20261027`。
> 5. `/pods`、`/events`、`/logs/stream` 曾 **404**：这三条路由只在 detail 分支上。现已随同一镜像发布，三条均 **200**。
> 6. 不存在的执行曾返回 **400**，现为 **404 `RESOURCE_NOT_FOUND`**。
> 7. 资源曲线曾对新建执行的 step Pod 返回 **412**：实例归属取自运行时历史，而历史是异步写入的。现改为读 Pod 上的 `ani.io/instance-id`（由准入 webhook 写入），历史仅作回落。已在集群实测 `memory.working_set_bytes` 返回真实值。
>
> 2026-10-27 新增能力（第二批）：
> 8. 任务列表默认按 `accepted_at` 倒序，支持 `name` 子串筛选（见第 8 节）。
> 9. 输入版本支持 `format=CSV|JSON` 筛选（见第 6 节）。
> 10. 微调详情新增 `base_model` 基座身份（见第 9 节）。

**联调前请先向后端确认当前镜像状态。** 后端做过多次滚动发布，发布窗口内可能短暂出现 `503 MODELDEV_CREATE_UNAVAILABLE`。这类 503 是发布抖动，重试即可；若持续超过几分钟再找后端。

建议联调顺序：实现目录 → 算力目录 → 输入版本 → 模型（基模）选择 → 创建（含各类 400 负例）→ 列表 → 详情（含 `steps`/`metrics`）→ 日志快照与日志流 → 容器组 → 事件 → 资源曲线 → 产物列表与换 URL → 停止 → 恢复登记。除产物真实下载外，全部可联调。

## 17. 当前不可用能力

### 17.1 已交付：详情页诊断能力

此前列为“另出文档”的四类详情能力，**已随 `v21-detail-on-dev-20261027` 一并交付**，可直接按本文件对接：

| 能力 | 位置 | 实测 |
| --- | --- | --- |
| 流水线步骤明细 | 详情响应的 `steps[]` | **200**，普通训练 5 步、微调 8 步（数量与顺序已核实；`register` 有已知缺陷，见上） |
| 训练曲线 | 详情响应的 `metrics[]` | **200**，实测 7~1379 点（内容已核实） |
| 失败诊断 | 详情响应的 `failure` | 见第 9 节字段说明 |
| 托管超参 | 详情响应的 `managed_settings` | 见第 9 节字段说明 |
| 容器组 `GET .../pods` | 见 9.1 | **200** |
| 事件 `GET .../events` | 见 9.2 | **200**（保留窗口 `1h`） |
| 资源曲线 `GET .../resource-metrics` | 见 9.3 | **200**（CPU/内存，GPU 未实测） |
| 日志流 `GET .../logs/stream` | 见 11.1 | **200**（SSE） |

注意事项：

- 事件来自 Kubernetes Event，保留窗口仅 `1h`，空结果是正常的。
- 日志与日志流在容器回收后不可读，返回 `412`，属设计行为。
- `metrics` 只在 `METRICS` 产物发布后才有点；训练中或已失败的执行可能为空数组。

### 17.2 其它确定不提供的

| 能力 | 状态 |
| --- | --- |
| 数据集上传/导入 | 无接口，只能运维 CLI 登记 |
| 训练 Release / 预设绑定 / 组合审批 | 已退出当前产品，不提供 |
| 任务重试 / 重新提交 | 无接口；`SUBMISSION_NOT_SENT` 等状态也不可重发 |

## 18. 最小可运行 curl 示例

以下命令在 bash / Git Bash / WSL 下可直接执行，`B` 为联调地址。每条的期望状态码已标注。

```bash
B=http://10.10.1.66:30085
```

### 18.1 登录换 token

```bash
TOK=$(curl -s -X POST "$B/api/v1/auth/password/login" \
  -H 'Content-Type: application/json' \
  -d '{"tenant_name":"tenant-a","username":"admin","password":"Correct@123"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["access_token"])')
```

- 请求体只有 `tenant_name` / `username` / `password` 三个字段；`tenant_name` 是租户编号（`sys_tenants.code`），不是显示名。
- 返回 `access_token`、`expires_in`，refresh token 走 HttpOnly Cookie，不在响应体里。
- 后续所有请求带 `-H "Authorization: Bearer $TOK"`。

> PowerShell 提示：`curl.exe -d '<json>'` 会把单引号内容原样传给进程，双引号被剥离，服务端会返回 `CODEC / invalid value xxx`。请把 JSON 写入文件后 `--data-binary "@body.json"`，或用 `Invoke-RestMethod`。

### 18.2 目录与输入（200）

```bash
curl -s -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/implementations?page=1&pageSize=20"
curl -s -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/input-versions?page=1&pageSize=20&state=READY"
curl -s -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/input-versions/04790fcc-dcd6-46e1-869e-84da77a975a8"
curl -s -H "Authorization: Bearer $TOK" "$B/api/v1/compute-presets?usage=TRAINING&page=1&pageSize=100"
```

微调创建所需的基模来源（**注意不是 `/model-versions`，那个列表路由不存在，会 404**）：

```bash
curl -s -H "Authorization: Bearer $TOK" "$B/api/v1/models?page=1&pageSize=20"
curl -s -H "Authorization: Bearer $TOK" "$B/api/v1/models/{model_id}/versions?page=1&pageSize=20"
curl -s -H "Authorization: Bearer $TOK" "$B/api/v1/model-versions/{model_version_id}"
curl -s -H "Authorization: Bearer $TOK" "$B/api/v1/model-versions/{model_version_id}/download-url"
```

### 18.3 创建任务（202）

```bash
curl -s -o resp.json -w '%{http_code}\n' -X POST "$B/api/v1/modeldev/executions" \
  -H "Authorization: Bearer $TOK" \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "cpu-smoke-01",
    "kind": "GENERAL_TRAINING",
    "implementation_id": "builtin.mlp",
    "compute_preset_id": "84547768-be1b-45dd-bdb9-be824f58d583",
    "dataset_version_id": "04790fcc-dcd6-46e1-869e-84da77a975a8",
    "parameters": {"epochs": 5, "batch_size": 32},
    "idempotency_key": "3f2b9c14-8e5a-4a0e-9a6f-1d2c3b4a5e60"
  }'
```

期望 `202`，body：

```json
{"operation_id":"3d3a883d-3289-4a6d-a935-a8578a3d6e29","execution_id":"2bdbb318-1538-4e57-835e-15c4baa30751","replayed":false,"compute_state":"ACCEPTED","delivery_state":"PENDING","close_state":"OPEN","resource_state":"NOT_APPLICABLE"}
```

用**完全相同的 body** 再发一次 → 仍是 `202`，`replayed=true`、`execution_id` 不变。改任意字段但保留同一 `idempotency_key` → `409 IDEMPOTENCY_CONFLICT`。

PC 上实测（2026-10-26）：`Content-Type: application/json; charset=utf-8` **也能通过**；`text/plain` 才被拒。

> 注意 `parameters` 里 `epochs` / `batch_size` 是**数字** `5` / `32`，不是字符串。传 `"5"` 会 `400`，见 7.5。

### 18.4 查询（200）

> 列表、详情、日志、容器组、事件均已可用。字段定义以第 9、11 节为准。

```bash
curl -s -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/executions?page=1&pageSize=20"
curl -s -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/executions/{execution_id}"
curl -s -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/executions/{execution_id}/logs?tail_lines=200&max_bytes=16384"
curl -s -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/executions/{execution_id}/pods"
curl -s -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/executions/{execution_id}/events?scope=all"
```

详情响应里的 `steps` / `metrics` 无需额外请求。校验 `steps` 有值：

```bash
curl -s -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/executions/{execution_id}" \
  | jq '{kind, steps: (.steps|length), metrics: (.metrics|length)}'
```

### 18.4.1 日志流（SSE，200）

```bash
curl -sN -H "Authorization: Bearer $TOK" \
  "$B/api/v1/modeldev/executions/{execution_id}/logs/stream"
```

期望逐行输出 `: ready`、若干 `event: log` + `data: {...}`，最后 `event: end`。
没有可读日志来源时会直接返回 `412 LOG_UNAVAILABLE`（不是流帧），不要当成连接失败。

### 18.5 产物（200）

```bash
curl -s -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/executions/64320bf3-0a9a-41cc-8b03-557b6dc75d58/artifacts?page=1&pageSize=20"
curl -s -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/artifacts/0377b660-dd42-50a1-85a6-9a95c54b6bb9/content"
```

第二条返回的 `download_url` 是集群内地址 + 60 秒签名，集群外不可达，见第 13 节。

### 18.6 停止（202）

```bash
curl -s -w '\n%{http_code}\n' -X POST "$B/api/v1/modeldev/executions/{execution_id}:stop" \
  -H "Authorization: Bearer $TOK"
```

- **不要带 body、不要带 `Content-Type`、不要带 query**，三者任一都会 `400 INVALID_MODELDEV_STOP`。
- 响应：`{"operation_id":"…","execution_id":"…","stop_requested":true,"intent_generation":"1","replayed":false}`。
- 不存在的执行 → `404 RESOURCE_NOT_FOUND`。

### 18.7 恢复模型登记（200）

```bash
curl -s -w '\n%{http_code}\n' -X POST "$B/api/v1/modeldev/executions/{execution_id}:resume-model-registration" \
  -H "Authorization: Bearer $TOK"
```

同样不允许 body / query。实测 `200`，返回与详情同结构的执行本体（含 `model_registration`）。

仅当执行是 **FINETUNING + 已 `CLOSED` + 原目标存在** 时才允许；其他情况返回 `412`。

### 18.8 参数负例自检（都应为 400）

```bash
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/implementations?page=0"
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/implementations?pageSize=101"
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/implementations?name=MLP"
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/implementations?kind=finetuning"
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/implementations?kind=UNSPECIFIED"
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/implementations?"
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/input-versions?state=ready"
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/input-versions?kind=FINETUNING"
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/input-versions/not-a-uuid"
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOK" "$B/api/v1/compute-presets?page=1"
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOK" "$B/api/v1/modeldev/executions/{execution_id}/artifacts?role=HF_MODEL"
```

最后一条说明：**产物列表只接受 `page` / `pageSize`**，`role`、`filename` 等筛选需在本地做。

未带 token 时统一 `401 UNAUTHORIZED / missing bearer token`（已在现网确认）。
