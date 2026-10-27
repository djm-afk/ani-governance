# 训练 / 微调任务详情 · 容器组、日志、事件、监控 前端对接

面向 Governance BFF 前端联调。覆盖任务详情页的四类诊断能力：**容器组（Pod）、日志（快照 + SSE 流）、事件、资源曲线**。普通训练与微调共用同一套接口，差异只在流水线步骤数量与容器组的分层内容。

字段以 `api/protos/modeldev/service/v1/execution.proto`、`api/protos/admin/service/v1/i_modeldev.proto` 为准。整体任务生命周期（创建 / 列表 / 详情 / 产物 / 停止）见 [frontend-training-handoff.md](frontend-training-handoff.md)，本文只讲这四类能力，可单独使用。

联调环境：`http://10.10.1.66:30085`（NodePort），租户 `tenant-a`。

---

## 1. 四类能力状态

| 能力 | 接口 | 实测 |
| --- | --- | --- |
| 容器组 | `GET /api/v1/modeldev/executions/{id}/pods` | **200** |
| 事件 | `GET /api/v1/modeldev/executions/{id}/events` | **200**（保留窗口 `1h`） |
| 日志快照 | `GET /api/v1/modeldev/executions/{id}/logs` | **200** |
| 日志流 | `GET /api/v1/modeldev/executions/{id}/logs/stream` | **200**（SSE） |
| 资源曲线 | `GET /api/v1/modeldev/executions/{id}/resource-metrics` | **200**（CPU/内存；GPU 未实测） |

四类能力都按"执行冻结的运行时计划"取数：调用方不需要、也不允许传集群、命名空间、Pod 名。

---

## 2. 通用约定

### 2.1 认证与权限

- 全部要求**登录用户**（JWT）。请求头带 `Authorization: Bearer <access_token>`。API Key 主体一律 `403`。
- 请求与 URL **不得**携带 tenant / actor / owner；服务端会忽略并删除这类入站头。
- 权限码：

| 接口 | 权限码 |
| --- | --- |
| `/pods` | `modeldev:execution:get` |
| `/events` | `modeldev:execution:get` |
| `/resource-metrics` | `modeldev:execution:get` |
| `/logs` | `modeldev:execution:logs` |
| `/logs/stream` | `modeldev:execution:logs` |

### 2.2 响应头

所有 modeldev 读接口返回 `Cache-Control: no-store`、`Pragma: no-cache`、`Referrer-Policy: no-referrer`。不要做本地缓存、Service Worker 缓存或共享缓存。

### 2.3 错误响应

```json
{"code":412,"reason":"ARTIFACT_NOT_PUBLISHED","message":"artifact unavailable","metadata":{"request_id":"7abb6015-539b-44d5-9447-e2d9b05921aa"}}
```

用 `code` + `reason` 分支，不要依赖 `message` 文案。`metadata.request_id` 请保留，便于后端排查。

### 2.4 ⚠️ query 参数命名不统一（最容易踩的坑）

四个接口的参数命名风格不一致，**照抄本文表格，不要凭直觉推导**：

| 接口 | 参数 | 风格 |
| --- | --- | --- |
| `/pods` | `layer`、`includeRecycled` | 驼峰 |
| `/events` | `scope` | 单词 |
| `/logs` | `tail_lines`、`max_bytes`、`logId`、`podUid`、`container`、`previous` | **snake + 驼峰混用** |
| `/logs/stream` | `podUid`、`container`、`previous`、`sinceBytes` | 驼峰 |
| `/resource-metrics` | `pod_uid`、`container`、`start`、`end`、`step_seconds`、`metric_codes` | **全 snake** |

对所有接口统一适用：

- **未知参数一律 `400 INVALID_MODELDEV_QUERY`**，不会静默忽略。
- **重复参数一律 `400`**（`?scope=key&scope=all` 也是 400）。
- 不会自动纠正大小写或写法：`since_bytes` 不是 `sinceBytes`，`podUid` 不是 `pod_uid`。
- 请求体必须为空；URL 末尾的裸 `?` 也会 `400`。

### 2.5 时间与整数

- 时间字段统一 `RFC3339Nano` UTC 字符串，如 `2026-10-27T04:25:00.123456Z`。
- 响应中的 `int64`/`uint64` 是**十进制字符串**（如 `size_bytes`）。
- 请求里的整型参数是**数字**，不是字符串。

---

## 3. 容器组 `GET .../pods`

```
GET /api/v1/modeldev/executions/{execution_id}/pods?layer=all&includeRecycled=true
```

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `layer` | 否 | `all`（默认）/ `trainer`（训练容器）/ `pipeline`（流水线步骤）。其它值 `400` |
| `includeRecycled` | 否 | `true` / `false`，默认 `false`。**默认不返回已回收的 Pod**；要在容器组列表里看到已回收记录必须传 `true` |

### 3.1 响应

```json
{
  "pods": [
    {
      "pod_name": "md-rtttx-2081272040",
      "pod_uid": "8d0e4f4c-5a5f-4a1f-9c3d-2b1a0e6f7c11",
      "pod_ip": "10.244.3.51",
      "phase": "Succeeded",
      "ready": false,
      "node_name": "node3",
      "restarts": 0,
      "created_at": "2026-10-23T09:55:20Z",
      "start_time": "2026-10-23T09:55:22Z",
      "layer": "TRAINER",
      "availability": "LIVE",
      "step_name": "",
      "attempt": 0,
      "job_index": 0,
      "log_id": "…不透明串…",
      "log_available": true,
      "containers": [
        {"name": "node", "type": "container", "state": "terminated", "reason": "Completed",
         "message": "", "restart_count": 0, "exit_code": 0,
         "started_at": "2026-10-23T09:55:22Z", "finished_at": "2026-10-23T10:12:01Z"}
      ],
      "init_containers": [],
      "ephemeral_containers": [],
      "api_present": true,
      "exit_code": 0,
      "metric_instance_id": "23da0705-7fd7-44f0-89e4-e40fc2400115"
    }
  ],
  "observed_at": "2026-10-27T04:25:00Z",
  "availability": "LIVE"
}
```

> 未赋值的字段会被省略（不是 `null`）。例如 Pod 还在运行时没有 `exit_code`；`deletion_timestamp`、`step_name` 为空时不出现。前端按"字段缺失"处理，不要当成 `0` 或 `""` 之外的特殊值。

### 3.2 字段说明

| 字段 | 说明 |
| --- | --- |
| `layer` | `TRAINER`（训练容器，容器名恒为 `node`）/ `PIPELINE`（流水线步骤，容器名恒为 `main`） |
| `availability` | 单个 Pod 的存在状态，见下表 |
| `phase` | **仅** Kubernetes 原始 phase；已回收或从未创建的 Pod 由 `availability` 表达，`phase` 可能为空或历史值。不要把 `phase` 当状态机 |
| `restarts` | Pod 重启次数 |
| `exit_code` | 训练容器退出码；未终止时字段缺省，不要当成 0 |
| `api_present` | 该 Pod 的 API 对象当前是否还存在；`false` 表示只能看到持久化记录 |
| `step_name` | 仅 `PIPELINE` 层有效，取值见 §3.4，是它与详情 `steps[].role` 的连接键 |
| `job_index` / `attempt` | 多机训练的 rank 与重启次数（来自 JobSet 标签） |
| `log_id` | 该 Pod 的不透明日志凭证；调用 `/logs` 时可回传 |
| `log_available` | 该 Pod 当前是否可读日志。**调用 `/logs` 前先看它，避免无谓 412** |
| `containers` / `init_containers` / `ephemeral_containers` | 三类容器分列。诊断 prepare 阶段失败要看 `init_containers`，`ephemeral_containers` 是临时调试容器 |
| `metric_instance_id` | 该 Pod 在可观测侧上报的实例 ID。**前端不需要传**，`/resource-metrics` 内部会用；早于指标标签的历史 Pod 为空 |

容器对象字段：

| 字段 | 说明 |
| --- | --- |
| `type` | `container` / `init_container` / `ephemeral` |
| `state` | `running` / `waiting` / `terminated` / `unknown`（Kubernetes 原始词） |
| `reason` | 如 `Completed`、`OOMKilled`、`CrashLoopBackOff`、`ImagePullBackOff` |
| `message` | 容器状态附带信息 |
| `restart_count` | 单容器重启次数 |
| `exit_code` | 缺失表示未终止 |
| `last_state` | 上次终止实例的容器状态（结构同上），用于展示"上一次为什么退出" |

### 3.3 `availability` 取值

| 值 | 含义 |
| --- | --- |
| `LIVE` | Pod 仍存在 |
| `RECYCLED` | 曾存在，已被回收 |
| `NEVER_CREATED` | 从未创建（任务通常没走到该阶段） |
| `REJECTED` | 创建被拒绝 |
| `ORPHANED` | 已不属于当前执行但仍可观测（只读） |
| `CREATION_PENDING` | 创建中 |
| `UNSPECIFIED` | 无法判定 |

顶层 `availability` 描述**本次查询整体能证明什么**，不要和单个 Pod 的 `availability` 混用。当所有 worker Pod 都来自持久化观测时，顶层为 `RECYCLED`，即使你没传 `includeRecycled`。

### 3.4 `step_name` 取值（与详情 `steps[].role` 同词汇）

普通训练 5 步：

`prepare` → `train-wait` → `collect` → `publish` → `close`

微调在上述 `publish` 后追加 3 步：`export` → `verify` → `register`（未填登记目标时跳过）。

前端可以把 `/pods?layer=pipeline` 的结果按 `step_name` 与详情 `steps[]` 对齐，做成"时间线 + 每步容器状态/日志入口"。

---

## 4. 事件 `GET .../events`

```
GET /api/v1/modeldev/executions/{execution_id}/events?scope=key
```

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `scope` | 否 | `key`（默认）/ `all` / `warning`。其它值 `400` |

`scope` 语义：

| 值 | 返回内容 |
| --- | --- |
| `key`（默认） | 所有 `Warning` 事件，加上非 Pod 对象（Job / JobSet / TrainJob / Workflow）的 `Normal` 事件。默认值刻意滤掉 driver Pod 的 Normal 噪声，避免淹没失败信号 |
| `warning` | 只返回 `Warning` |
| `all` | 不过滤 |

### 4.1 响应

```json
{
  "events": [
    {
      "type": "Warning",
      "reason": "FailedScheduling",
      "message": "0/3 nodes are available: 3 Insufficient nvidia.com/gpu.",
      "first_timestamp": "2026-10-23T09:56:30Z",
      "last_timestamp": "2026-10-23T09:58:10Z",
      "count": 5,
      "source": "default-scheduler",
      "involved_kind": "Pod",
      "involved_name": "md-rtttx-2081272040",
      "involved_uid": "8d0e4f4c-…",
      "field_path": "spec.containers{node}",
      "reporting_instance": "node3"
    }
  ],
  "observed_at": "2026-10-27T04:25:00Z",
  "retention_hint": "1h",
  "truncated": false
}
```

| 字段 | 说明 |
| --- | --- |
| `type` | `Normal` / `Warning` |
| `count` | 同一事件被聚合的次数；与 `first_timestamp`/`last_timestamp` 一起展示 |
| `involved_kind` / `involved_name` | 事件对象类型与名字（Pod / Job / JobSet / TrainJob / Workflow） |
| `reporting_instance` | 上报组件所在节点，可为空 |
| `retention_hint` | 当前 `1h` |
| `truncated` | 本次结果是否被截断 |

### 4.2 必须知道的边界

- 事件来自 Kubernetes Event 对象，**默认保留约 1 小时**。历史执行看到 `events: []` 是**正常结果**，不是错误，也不是"从未发生"的证明。
- 想看更长历史需要另建 event-exporter / 落库方案，本期不提供。前端在空结果时展示"保留窗口 1h，更早的事件不再提供"即可。
- 事件不是执行日志的替代品，两者要分开展示。
- PodGroup / Volcano 排队事件按执行冻结的运行时计划条件化：计划里配了 PodGroup 才会出现，没配不会报错。

---

## 5. 日志

### 5.1 快照 `GET .../logs`

```
GET /api/v1/modeldev/executions/{execution_id}/logs?tail_lines=200&max_bytes=16384
```

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `tail_lines` | 否 | 1–1000，默认 200 |
| `max_bytes` | 否 | 1–65536，默认 16384 |
| `podUid` | 否 | 规范 UUID；取自 `/pods` 的 `pod_uid`。**选 Pod 时必须与 `container` 一起传** |
| `container` | 否 | 小写 Kubernetes 容器名（worker=`node`，step=`main`） |
| `logId` | 否 | 不透明日志凭证，取自 `/pods` 的 `log_id`；传了就必须与该 Pod 匹配，否则 `404` |
| `previous` | 否 | `true` 读**上一次**终止实例的日志（容器重启后排查上一次崩溃）；没有上次实例时 `404` |

选择规则（很重要）：

- **两个都不传**：走兼容路径，返回该执行训练 worker 的默认日志（`node` 容器），并优先使用归档 tail。适合"只想看训练日志"的简单场景。
- **两个都传**：读取指定 Pod 的指定容器。
- **只传 `container` 不传 `podUid`**：`412`，不会猜测 Pod。

**调用前先看 `/pods` 里该 Pod 的 `log_available`**。

响应：

```json
{
  "log_id": "…不透明串…",
  "lines": [
    {"timestamp": "2026-10-23T08:40:01.123456Z", "text": "epoch 1/5 loss=0.42"}
  ],
  "truncated": true,
  "observed_at": "2026-10-23T09:10:12.345678Z",
  "source_kind": "TRAINER",
  "container_name": "node",
  "previous": false,
  "available": true
}
```

| 字段 | 说明 |
| --- | --- |
| `source_kind` | `TRAINER`（训练日志）/ `KFP_STEP`（流水线步骤日志） |
| `container_name` | 实际读取的容器名 |
| `previous` | 回显请求 |
| `available` | 200 响应里恒为 `true`；"不可用"以 `412` 表达，不用这个字段判断 |
| `truncated` | 结果是否被 `tail_lines` / `max_bytes` 截断 |

错误：

| HTTP | `reason` | 场景 |
| --- | --- | --- |
| `400` | `INVALID_MODELDEV_QUERY` | 参数越界、未知参数、重复参数、非规范 UUID |
| `404` | `RESOURCE_NOT_FOUND` | 执行不存在；Pod 不属于该执行；`previous=true` 但没有上次实例；`logId` 不匹配 |
| `412` | `ARTIFACT_NOT_PUBLISHED` | **没有可读的日志来源**：日志尚未产出、容器已回收、或 step Pod 已消失且无归档 |
| `503` | `MODELDEV_QUERY_UNAVAILABLE` | 下游不可用 |

> 训练容器日志在容器回收后不再可读，返回 `412` 是**设计行为**，不是故障。前端展示成"容器已回收，日志不可读"，而不是红色报错。

### 5.2 日志流 `GET .../logs/stream`（SSE）

```
GET /api/v1/modeldev/executions/{execution_id}/logs/stream?podUid=…&container=main&previous=false&sinceBytes=115
```

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `podUid` | 否 | 规范 UUID |
| `container` | 否 | 小写容器名 |
| `previous` | 否 | `true` / `false` |
| `sinceBytes` | 否 | 续传偏移；`0` 到 `2^30` 的**规范十进制**（`01`、负数、超界 → `400`）。`0` 等同不传 |

响应头 `Content-Type: text/event-stream`。

#### 帧格式

```
: ready

: heartbeat

event: log
data: {"text":"epoch 1/5 loss=0.42","offset":115}

event: log
data: {"text":"epoch 2/5 loss=0.31","offset":268}

event: end
data: {"reason":"LOG_ENDED"}
```

| 帧 | 说明 |
| --- | --- |
| `: ready` | 注释帧，连接已建立并开始推送，可忽略 |
| `: heartbeat` | 注释帧，约每 15s 一次，保持连接；可忽略 |
| `event: log` | 一行日志。`offset` 是该行在日志流中的累计字节偏移，**前端要保存最后一个 offset，用于断线续传** |
| `event: end` | 流正常结束。`reason` 取值见下 |
| `event: error` | 连接建立后发生的终止性错误，`reason` 取值同下 |

`end` 帧的 `reason`：

| `reason` | 含义 | 前端动作 |
| --- | --- | --- |
| `LOG_ENDED` | 日志正常结束（容器退出） | 停止重连 |
| `TIME_LIMIT` | 单连接生命周期（10 分钟）到期 | **用最后的 offset 重新发起请求续传** |
| `LIFETIME_EXCEEDED` | 同上，服务端侧生命周期 | 同上 |
| `POD_GONE` | Pod 已消失 | 提示并停止重连 |

`error` 帧的 `reason`：

| `reason` | 含义 |
| --- | --- |
| `OFFSET_INVALID` | 传入的 `sinceBytes` 已失效（日志轮转或 Pod 重建） |
| `PREVIOUS_UNAVAILABLE` | 请求了上次实例但不可读 |
| `STEP_LOGS_UNAVAILABLE` | step 日志不可用（Pod 已消失且本期无归档读取） |
| `LOG_UNAVAILABLE` | 兜底不可读 |

#### 连接前的失败（HTTP 状态码，不是 SSE 帧）

流的失败语义分两段：**首帧 `: ready` 之前**的错误以 HTTP 状态码返回；**首帧之后**的错误以 `event: error` 返回。

| HTTP | `reason` | 场景 |
| --- | --- | --- |
| `412` | `OFFSET_INVALID` / `PREVIOUS_UNAVAILABLE` / `POD_GONE` / `STEP_LOGS_UNAVAILABLE` / `LOG_UNAVAILABLE` | 打开失败 |
| `404` | `RESOURCE_NOT_FOUND` | 执行不存在 / 无权限区分（不泄露资源是否存在） |
| `429` | `EXECUTION_LOG_CONNECTION_LIMIT` | 并发连接超限 |
| `400` | `INVALID_MODELDEV_QUERY` | 参数不合法 |
| `401` | — | 未认证 |
| `503` | `EXECUTION_LOG_UPSTREAM_UNAVAILABLE` | 上游不可用或打开超时 |

> 已建立的流不会回传 HTTP 错误；异常断开就是终止。前端按 `event: end` 或连接关闭结束读取。

#### 并发上限

| 维度 | 上限 |
| --- | --- |
| 单执行（execution） | 3 条流 |
| 单租户 | 10 条流 |
| 进程全局 | 100 条流 |

超限返回 `429 EXECUTION_LOG_CONNECTION_LIMIT`。前端应保证同一执行同一时刻只开 1 条流，并在页面切走时显式关闭连接。

#### 重连建议

1. 保存最后收到的 `offset`。
2. 收到 `end/TIME_LIMIT` 或异常断开时，用 `sinceBytes=<最后 offset>` 重连。
3. 收到 `412 OFFSET_INVALID` 时，丢弃 offset 从头重开（`sinceBytes` 不传），并在 UI 提示"中间可能有缺失"。
4. 收到 `end/LOG_ENDED` 或 `end/POD_GONE` 时停止重连。
5. 建议加退避（例如 1s → 2s → 5s，上限 30s），避免连接风暴撞 `429`。

> **注意：即使只开 1 条流，服务端每 10 分钟也会主动以 `TIME_LIMIT` 结束连接。**这是设计行为，不是错误。

---

## 6. 资源曲线 `GET .../resource-metrics`

```
GET /api/v1/modeldev/executions/{execution_id}/resource-metrics
    ?pod_uid=…&container=main&start=2026-10-27T00:00:00Z&end=2026-10-27T01:00:00Z
    &step_seconds=60&metric_codes=memory.working_set_bytes,cpu.usage_cores
```

### 6.1 一次请求只返回一个 Pod 的一个容器

上游可观测合同一次只解析一个物理工作负载，因此**并行 step 的曲线不能合并成一次调用**。前端按 Pod 逐个懒加载（例如容器组表格里展开某一行时才请求）。

### 6.2 参数

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `pod_uid` | **是** | 规范 UUID，取自 `/pods` 的 `pod_uid`。用 UID 而非名字：Pod 重建后不会继承旧曲线 |
| `container` | **是** | 必须与层匹配：`TRAINER` 层只能 `node`，`PIPELINE` 层只能 `main`。传错 `400`，不会静默返回空曲线 |
| `start` / `end` | **是** | RFC3339。要求**整秒**（带小数秒如 `00:00:00.5Z` 会被拒），窗口 > 0 且 ≤ 24h，`end` 不得晚于当前时间 |
| `step_seconds` | 否 | 默认 `30`；允许 30–3600 且必须是 30 的整数倍 |
| `metric_codes` | **是** | 逗号分隔，1–2 个，只能取 `cpu.usage_cores` / `memory.working_set_bytes`，不得重复 |

点数上限：`(end-start)/step_seconds + 1 ≤ 3000`。

参数错误一律 `400 INVALID_MODELDEV_QUERY`。

### 6.3 响应

```json
{
  "execution_id": "23da0705-7fd7-44f0-89e4-e40fc2400115",
  "pod_uid": "8d0e4f4c-5a5f-4a1f-9c3d-2b1a0e6f7c11",
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
      "metric_code": "memory.working_set_bytes",
      "unit": "bytes",
      "status": "PARTIAL",
      "points": [
        {"timestamp": "2026-10-27T00:00:00Z", "value": "65331200"},
        {"timestamp": "2026-10-27T00:01:00Z", "value": null, "gap_reason": "NO_DATA"}
      ]
    },
    {
      "metric_code": "cpu.usage_cores",
      "unit": "cores",
      "status": "NO_DATA",
      "points": [
        {"timestamp": "2026-10-27T00:00:00Z", "value": null, "gap_reason": "NO_DATA"}
      ]
    }
  ]
}
```

| 字段 | 说明 |
| --- | --- |
| `source` | `PROMETHEUS_CADVISOR_PROJECTION`：数据来自 recording rule 对 cAdvisor 的投影，不是直读 cAdvisor |
| `retention_hint` | 当前 `24h`。超出保留期的窗口返回空序列，接口不报错；**前端不要承诺更长历史** |
| `device_mode` | `cpu` / `gpu`，来自执行冻结的算力选择 |
| `gpu_requested` | 该执行是否申请了 GPU |
| `gpu_available` | 是否真有 GPU 指标序列。**当前环境没有 GPU exporter，恒为 `false`** |
| `series[].status` | `OK` / `PARTIAL` / `NO_DATA`，由真实值个数决定 |
| `points[].value` | 十进制字符串（`"65331200"`、`"1.72"`），避免大字节数失真 |
| `points[].gap_reason` | 缺口原因：`NO_DATA` / `AMBIGUOUS_SOURCE` / `INVALID_VALUE` / `EXPORTER_UNAVAILABLE` |

> 该接口开启了"输出所有字段"，所以缺口点的 `value` 会**显式为 `null`**、而有效点不会有 `gap_reason` 键。判断写 `point.value == null || point.value === ""`，不要写 `"value" in point`。

### 6.4 渲染规则（重要）

- **缺口点不能渲染成 0**。`value` 为 `null` 时按断裂处理（曲线断开或虚线段），hover 显示 `gap_reason` 的中文含义。
- `gpu_requested=true && gpu_available=false`：显示"GPU 指标不可用（当前环境未部署 GPU exporter）"，**不要画成 0 使用率**。
- 曲线采样间隔是 30s；轮询间隔不要低于 `step_seconds`。
- 建议默认只请求 `memory.working_set_bytes`，把 `cpu.usage_cores` 做成可选（原因见下）。

### 6.5 已知限制

- **`cpu.usage_cores` 对 step Pod 通常是整条 `NO_DATA`**：该指标用 `rate(...[5m])` 求值，要求同一 Pod 连续存活 ≥5 分钟，而 step Pod 一般只活 10–30 秒。这是采集规则的物理限制，不是接口缺陷。worker Pod 长时间运行可正常出点。
- 每租户并发上限 4，BFF 会把超出的请求排队等待（不是直接拒绝）。首屏"worker + 4 个 step"就有 5 次调用，建议限制同时展开的详情页为 1 个，并按 Pod 逐个懒加载。
- GPU 曲线形状尚未实测（环境里唯一的 GPU 执行 Pod 已被回收）。前端按 `gpu_available=false` 的降级分支实现即可。

### 6.6 错误

| HTTP | `reason` | 场景 |
| --- | --- | --- |
| `400` | `INVALID_MODELDEV_QUERY` | 参数不合法（含 layer 与容器名不匹配、Pod 不含该容器） |
| `403` | `FORBIDDEN` | 无权限 / 数据范围不足 |
| `404` | `RESOURCE_NOT_FOUND` | 执行不存在或 Pod 不属于该执行 |
| `412` | `MODELDEV_METRIC_SCOPE_UNAVAILABLE` | 执行缺少冻结的集群/命名空间，或无法确定该 Pod 的上报实例 |
| `429` | `MODELDEV_METRIC_CONCURRENCY_LIMIT` | 并发超限（正常情况下 BFF 会先排队） |
| `499` | `MODELDEV_METRIC_CANCELED` | 请求被取消 |
| `502` | `MODELDEV_METRIC_RESPONSE_INVALID` | 上游响应形状不符 |
| `503` | `MODELDEV_METRIC_UNAVAILABLE` | 指标服务不可用 |
| `504` | `MODELDEV_METRIC_TIMEOUT` | 查询超时（该接口服务端预算 10s） |

---

## 7. 普通训练 vs 微调

四类接口完全共用，前端不需要分叉调用，只在这三处有差异：

| 差异点 | 普通训练 | 微调 |
| --- | --- | --- |
| 流水线步骤 | 5 步：`prepare` → `train-wait` → `collect` → `publish` → `close` | 8 步：在 `publish` 后加 `export` → `verify` → `register`（未填登记目标时跳过 6–8） |
| `layer=pipeline` 的容器组 | 5 个左右的 step Pod | 更多，且 `export`/`verify` 失败时要靠它们的 step 日志定位原因 |
| 详情 `steps[]` / `current_step` | 同上 5 个角色 | 同上 8 个角色 |

不变的部分：

- worker 容器名恒为 `node`，step 容器名恒为 `main`。
- 指标码只有 `cpu.usage_cores` 与 `memory.working_set_bytes` 两个。
- 事件、容器组、日志的字段与错误语义完全一致。

**微调排障要点**：微调失败时不要只看 worker 日志。`export` / `verify` / `register` 阶段失败时 worker 可能早就正常结束，必须切到 `layer=pipeline` 找到对应 `step_name` 的 Pod，读它 `main` 容器的日志。

---

## 8. 推荐的调用顺序

```
1) GET /pods?layer=all&includeRecycled=true
    → 渲染容器组列表（TRAINER / PIPELINE 分两个区块或两个 Tab）
    → 缓存每个 pod 的 pod_uid / container 名 / step_name / log_available / availability

2) 点某个容器组看日志：
   GET /logs?podUid=<pod_uid>&container=<node|main>&tail_lines=200&max_bytes=16384
   运行中的 Pod 改开 SSE：
   GET /logs/stream?podUid=<pod_uid>&container=<node|main>
   容器重启过且要看上一次：加 previous=true

3) 事件 Tab：
   GET /events?scope=key
   空结果时提示"Kubernetes 事件保留窗口约 1h"

4) 曲线（展开该容器组时才请求）：
   GET /resource-metrics?pod_uid=<pod_uid>&container=<node|main>
       &start=<窗口起>&end=<窗口止>&step_seconds=60
       &metric_codes=memory.working_set_bytes
```

首屏只调 1、3 即可；2、4 按用户展开懒加载。

---

## 9. 已知限制汇总

| 项 | 影响 | 前端处理 |
| --- | --- | --- |
| 事件保留窗口 `1h` | 历史执行事件为空 | 正常空态 + 文案说明，不报错 |
| 指标保留窗口 `24h` | 更早窗口返回空序列 | 不承诺更长历史 |
| 容器回收后日志不可读 | `/logs` 返回 `412` | 提示"容器已回收"，非红色错误 |
| step Pod 消失且无归档 | `/logs` / 流返回 `412 STEP_LOGS_UNAVAILABLE` | 同上 |
| step Pod 的 `cpu.usage_cores` 恒 `NO_DATA` | CPU 曲线对步骤无意义 | 步骤默认只展示内存 |
| 无 GPU exporter | `gpu_available=false` | 降级文案，不画 0 |
| 日志流 10 分钟生命周期 | 连接被服务端正常关闭 | 用 offset 续传 |
| 日志流单执行 3 条 / 单租户 10 条 | 超限 `429` | 同执行只开 1 条，页面隐藏时关闭 |
| 指标每租户并发 4 | 高峰排队变慢 | 逐个懒加载，同时只展开 1 个详情页 |

---

## 10. 与后端确认过的实测结论（2026-10-27，66 集群）

- `/pods`、`/events`、`/logs`、`/logs/stream` 均 **200**；不存在的执行返回 **404 `RESOURCE_NOT_FOUND`**。
- `/resource-metrics` 正例返回真实内存值 `65331200`（62.3 MiB），`status=PARTIAL`，其余点正确标 `NO_DATA`。
- 参数类负例（缺 `pod_uid`、非规范 UUID、未登记指标码、layer 与容器名不匹配、容器名大写、`step_seconds` 取 15 / 45、窗口 >24h、`end` 晚于当前、重复指标码、未知 query 参数、Pod 不属于该执行、执行不存在、无 token）全部返回预期状态码。
- 训练记录形状：`__name__` + 7 个标签，含 `pod_uid`，已确认无 `pod` 标签。
- **未验证**：GPU 曲线响应形状；500 Pod 规模的容量回归。
