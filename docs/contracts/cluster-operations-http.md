# Gov / BOSS 集群运维共同接口合同

版本：v1.0 · 日期：2026-11-08

权威下游：[Res PR #67 Proto](https://github.com/zhangzhe-ctrl/ani-resource-service/blob/0837ee61ca4e486841932a18e424ac74ad92ede5/api/clusterops/v1/clusterops.proto)。本文件固定新增 HTTP 和页面协议，不重新定义 Kubernetes 行为。

## 1. 路由和权限

统一前缀 `P=/api/v1/admin/cluster-ops`。Gov operation 前缀为 `/admin.service.v1.PlatformClusterOpsService/`；Res 为 `/clusterops.v1.PlatformClusterOpsService/`。下表 Method 是两边一致的方法名。

| ID | HTTP | P 之后的路径 | Method | 权限 |
|---|---|---|---|---|
| H01 | GET | /clusters | ListClusters | cluster:ops:read |
| H02 | GET | /clusters/{cluster_id}/overview | GetClusterOverview | cluster:ops:read |
| H03 | GET | /clusters/{cluster_id}/resource-types | ListResourceTypes | cluster:ops:read |
| H04 | GET | /clusters/{cluster_id}/resources | ListResources | cluster:ops:read |
| H05 | GET | /clusters/{cluster_id}/resources/{name} | GetResource | cluster:ops:read |
| H06 | POST | /clusters/{cluster_id}/resources | CreateResource | cluster:ops:create |
| H07 | PUT | /clusters/{cluster_id}/resources/{name} | UpdateResource | cluster:ops:update |
| H08 | DELETE | /clusters/{cluster_id}/resources/{name} | DeleteResource | cluster:ops:delete |
| H09 | POST | /clusters/{cluster_id}/resources/{name}/scale | ScaleWorkload | cluster:ops:scale |
| H10 | POST | /clusters/{cluster_id}/resources/{name}/restart | RestartWorkload | cluster:ops:restart |
| H11 | GET | /clusters/{cluster_id}/namespaces/{namespace}/pods/{pod_name}/logs | StreamPodLogs | cluster:ops:logs |

只有十一条固定 HTTP 路由，不开放任意 Kubernetes URL、HTTP 方法、子资源或后端地址。H01–H10 返回普通 JSON；H11 返回 SSE。

只允许现有平台用户 JWT：SubjectUser，平台 tenant=0、现有 platform 角色、对应权限及精确 API 授权。拒绝租户身份、API Key、客户端自报 actor/scope/tenant。Secret 展开仍使用 read，不新增第二套审批或凭证。

## 2. 通用字段、编码与请求位置

### 2.1 JSON 编码

- 字段统一为下面规定的 snake_case。Gov 公共 Proto 每个字段显式设置 `json_name` 为该字段名，沿用现有编码器，不修改全局 JSON 策略。
- `int64`、`map<string,int64>` 的 HTTP JSON 值是十进制字符串；`uint32/int32` 是 JSON number；不把 resource_version、UID 转数字。
- repeated 空集合返回 `[]`；map 空集合返回 `{}`。布尔默认 false、字符串默认空串按现有 ProtoJSON 输出；Timestamp 为 RFC 3339 字符串，未取得值时允许 null。
- 成功响应直接使用本文件的结构，不增加 `code/message/data` 外壳。GetResource 的 `resource + manifest_yaml` 是有业务含义的组合结果，必须保留。
- `object_json` 和 `manifest_yaml` 均是字符串。Gov 不解析、重写或裁剪其中的 Kubernetes 对象；BOSS 不从 object_json 重建可写配置。

### 2.2 路径和查询

路径字段只从已匹配路径取得；BOSS 对每个路径段使用 encodeURIComponent。接受合法单段编码（例如 ClusterRole 的 `system:...`），不得把名称限定为 UUID、DNS 名称或 Network 产品 ID。拒绝解码后含 `/`、控制字符、空段、`.`/`..` 的目标；不对路径反复解码。

查询只允许各方法列出的字段，每个字段最多一个值。bool 只接受 `true/false`；整数使用规范十进制，无空值、前导加号、小数或科学计数法。允许 `group` 省略或空字符串，表示 core group。URL 编码交给 URLSearchParams/现有 serializer，不能拼接 selector 或游标。

H01、H02、H03 无 query/body；H04、H05、H08、H11 无 body；H06、H07、H09、H10 无 query，使用 `Content-Type: application/json` 与恰好一个 `{ "data": {...} }` 对象。顶层只允许 data，不接受 null、数组或第二个 JSON 对象。

写请求 data 不含 cluster_id；H07/H09/H10 也不含 name。H06 因路径没有 name，必须在 data 中提供 name。不允许通过外层或内层重复路径字段产生覆盖顺序。

未知查询/JSON 字段返回 400 `INVALID_CLUSTEROPS_REQUEST`，不静默忽略 tenant_id、actor、permissions、kubeconfig、url 等字段。Gov 只验证 HTTP 合同，不验证 Kubernetes manifest schema。

HTTP 写入 JSON 沿用既有 `decodeStrictJSONHTTPWithLimit` 的有界读取和 protojson 严格解码方式，在本域使用 32 MiB 传输预算，避免复制网络接口的 64 KiB 上限。该旧 helper 还会拒绝合法的 RawPath 编码，因此本域解码器按上文的单路径段规则实现，不直接调用其错误适用的路径检查，也不改旧接口。这个预算给 JSON 字符串转义留出空间，不扩大 Res 服务端请求接收或 Kubernetes 单对象的原有上限；不新增 manifest 业务大小规则、不全局调大其他入口。

## 3. 公共对象字段

下面 TypeScript 是字段和 HTTP 类型合同，可作为 BOSS `src/api/cluster-ops/types.ts` 的基础。Gov 公共消息统一加 `ClusterOps` 前缀，防止与既有目录消息重名。

```ts
export type ClusterOpsDecimal64 = string;
export interface ClusterOpsCluster {
  cluster_id: string;
  display_name: string;
}
export interface ClusterOpsResourceTypeRef {
  group: string;
  version: string;
  resource: string;
}
export interface ClusterOpsResourceRef {
  cluster_id: string;
  type: ClusterOpsResourceTypeRef;
  namespace: string;
  name: string;
}
export type ClusterOpsAction =
  | "list" | "get" | "create" | "update" | "delete"
  | "scale" | "restart" | "logs";
export interface ClusterOpsResourceDescriptor {
  type: ClusterOpsResourceTypeRef;
  kind: string;
  namespaced: boolean;
  actions: ClusterOpsAction[];
  custom_resource: boolean;
  crd_name: string;
}
export interface ClusterOpsResourceDocument {
  ref: ClusterOpsResourceRef;
  uid: string;
  resource_version: string;
  object_json: string;
  read_only: boolean;
  read_only_reason: string;
  secret_data_redacted: boolean;
  secret_keys: string[];
}
export interface ClusterOpsClusterOverview {
  cluster_id: string;
  kubernetes_version: string;
  nodes_total: ClusterOpsDecimal64;
  nodes_ready: ClusterOpsDecimal64;
  namespaces_total: ClusterOpsDecimal64;
  pods_total: ClusterOpsDecimal64;
  pod_phase_counts: Record<string, ClusterOpsDecimal64>;
  capacity: Record<string, string>;
  allocatable: Record<string, string>;
  sampled_at: string | null;
}
export interface ClusterOpsListClustersResponse { items: ClusterOpsCluster[]; }
export interface ClusterOpsListResourceTypesResponse {
  items: ClusterOpsResourceDescriptor[];
}
export interface ClusterOpsListResourcesResponse {
  items: ClusterOpsResourceDocument[];
  next_continue_token: string;
  list_resource_version: string;
}
export interface ClusterOpsGetResourceResponse {
  resource: ClusterOpsResourceDocument;
  manifest_yaml: string;
}
export interface ClusterOpsMutationResponse {
  resource: ClusterOpsResourceDocument;
  dry_run: boolean;
}
export interface ClusterOpsDeleteResourceResponse {
  deletion_requested: boolean;
  already_absent: boolean;
}
export interface ClusterOpsScaleWorkloadResponse {
  ref: ClusterOpsResourceRef;
  uid: string;
  resource_version: string;
  desired_replicas: number;
  observed_replicas: number;
  dry_run: boolean;
}
export interface ClusterOpsError {
  code: number;
  reason: string;
  message: string;
  metadata: Record<string, string>;
}
```

### 3.1 Gov Proto 字段编号和类型

公共对象沿用 Res 对应消息的编号/类型，新增前缀只避免公共包重名，不改字段语义。

| Gov 消息 | 字段（编号:type） |
|---|---|
| ClusterOpsCluster | cluster_id(1:string), display_name(2:string) |
| ClusterOpsResourceTypeRef | group(1:string), version(2:string), resource(3:string) |
| ClusterOpsResourceRef | cluster_id(1:string), type(2:ClusterOpsResourceTypeRef), namespace(3:string), name(4:string) |
| ClusterOpsResourceDescriptor | type(1:ClusterOpsResourceTypeRef), kind(2:string), namespaced(3:bool), actions(4:repeated string), custom_resource(5:bool), crd_name(6:string) |
| ClusterOpsResourceDocument | ref(1:ClusterOpsResourceRef), uid(2:string), resource_version(3:string), object_json(4:string), read_only(5:bool), read_only_reason(6:string), secret_data_redacted(7:bool), secret_keys(8:repeated string) |
| ClusterOpsClusterOverview | cluster_id(1:string), kubernetes_version(2:string), nodes_total(3:int64), nodes_ready(4:int64), namespaces_total(5:int64), pods_total(6:int64), pod_phase_counts(7:map<string,int64>), capacity(8:map<string,string>), allocatable(9:map<string,string>), sampled_at(10:Timestamp) |
| ClusterOpsListClustersResponse | items(1:repeated ClusterOpsCluster) |
| ClusterOpsListResourceTypesResponse | items(1:repeated ClusterOpsResourceDescriptor) |
| ClusterOpsListResourcesResponse | items(1:repeated ClusterOpsResourceDocument), next_continue_token(2:string), list_resource_version(3:string) |
| ClusterOpsGetResourceResponse | resource(1:ClusterOpsResourceDocument), manifest_yaml(2:string) |
| ClusterOpsMutationResponse | resource(1:ClusterOpsResourceDocument), dry_run(2:bool) |
| ClusterOpsDeleteResourceResponse | deletion_requested(1:bool), already_absent(2:bool) |
| ClusterOpsScaleWorkloadResponse | ref(1:ClusterOpsResourceRef), uid(2:string), resource_version(3:string), desired_replicas(4:int32), observed_replicas(5:int32), dry_run(6:bool) |

此表是新增目录 Proto 的精确要求，不代表需要复制 Res 生成文件。Gov data 使用已发布的 Res Go module；公共消息通过字段映射产生。

## 4. 各方法请求和返回

### H01–H03：集群、概览、类型

H01 返回 ClusterOpsListClustersResponse；H02 返回 ClusterOpsClusterOverview；H03 返回 ClusterOpsListResourceTypesResponse。没有额外 data 包装，不在 Gov 增加集群数据库。当前 Res 只有已配置的一个集群，未知 cluster_id 由 Res 明确拒绝。

H03 的 actions 只表达本模块支持的动作，不承诺 Kubernetes 实际凭据一定允许。BOSS 不补造发现结果中缺少的资源类型。

### H04：ListResources

| query | 类型/缺省 | 映射到 Res |
|---|---|---|
| group | string，缺省空串 | type.group |
| version | 必填 string | type.version |
| resource | 必填 string，plural | type.resource |
| namespace | string，缺省空串 | namespace |
| all_namespaces | bool，缺省 false | all_namespaces |
| page_size | 可选 uint32；省略沿用 Res 的 50；显式值 1..200 | page_size；省略传 0 |
| continue_token | string，缺省空串 | continue_token |
| name | string，缺省空串，精确匹配 | name |
| label_selector | string，缺省空串 | label_selector |
| field_selector | string，缺省空串 | field_selector |

cluster_id 来自 path。Namespaced 类型必须指定 namespace 或 all_namespaces=true，不能两者同时提供；Cluster 类型 namespace 为空且 all_namespaces=false。该类型语义最终由 Res 校验，Gov 不额外查询类型目录才能转发。

返回 ClusterOpsListResourcesResponse。没有 total、page、hasMore 或伪造顺序号。next_continue_token 非空才存在下一批，不能根据 items 长度推断。

Gov 仅对下游 `ListResources` RPC 使用 `clusterOpsListMaxRecvBytes=64 MiB`（67,108,864 字节）的 protobuf 接收预算，不更改共享连接或其他 RPC。该预算为默认 50 条、每项约 1 MiB 的常见大对象列表留出元数据空间；最大 200 条仍只是条数上限，不承诺任意 200 个对象都能装入预算。超出时整次响应失败，不截断正文/条目，不自动缩页、重试、扫描或缓存。这不是 HTTP 响应或 Kubernetes 业务大小规则，也不预分配该大小的缓冲区；HTTP 写入的 32 MiB 输入预算保持不变。

筛选、cluster、GVR、namespace 或 page_size 改变后，BOSS 清空本地游标历史，从首批重新查询。LIST_EXPIRED(409) 显示“列表快照已过期，请刷新”；用户刷新后从空游标开始，不把新快照接到旧记录尾部。

### H05：GetResource

query 为 group、version、resource、namespace，以及 include_secret_data（bool，缺省 false）。name/cluster_id 来自 path，构造 Res.ref；返回 ClusterOpsGetResourceResponse。

Secret 默认返回 secret_data_redacted=true、secret_keys，manifest_yaml 为空。显式 true 才取得真实编辑正文。其他类型传 true 不增加权限。

### H06–H10：写操作

共同 JSON 形式：

```json
{
  "data": {
    "type": { "group": "apps", "version": "v1", "resource": "deployments" },
    "namespace": "platform-system",
    "expected_uid": "由详情取得的原生UID",
    "expected_resource_version": "由详情取得的不透明版本",
    "replicas": 3,
    "dry_run": false
  }
}
```

上例仅是 H09 Scale 请求。每个方法只接受其下表字段，不接受 union 大对象中的无关字段。

| 方法 | data/query 精确字段 | 返回 |
|---|---|---|
| H06 Create | data.type、data.namespace、data.name、data.manifest_yaml；data.dry_run 可选 false | ClusterOpsMutationResponse |
| H07 Update | data.type、data.namespace、data.manifest_yaml、data.expected_uid、data.expected_resource_version；data.dry_run 可选 false | ClusterOpsMutationResponse |
| H08 Delete | 无 body；query group、version、resource、namespace、expected_uid | ClusterOpsDeleteResourceResponse |
| H09 Scale | data.type、data.namespace、data.expected_uid、data.expected_resource_version、data.replicas；data.dry_run 可选 false | ClusterOpsScaleWorkloadResponse |
| H10 Restart | data.type、data.namespace、data.expected_uid、data.expected_resource_version；data.dry_run 可选 false | ClusterOpsMutationResponse |

namespace 对 Namespaced 单对象必填，对 Cluster scope 为空串。type.version/resource 必填；group 可为空。UID/RV 均作为字符串透传。replicas 必须存在，是 0..2147483647 的整数；0 有效，不能用 truthy 判断遗漏。

Create/Update 的 manifest_yaml 必须是非空字符串；单文档、身份一致、系统字段、Secret 与 schema 校验全部由 Res/Kubernetes 执行。Gov 不 parse YAML、不补 namespace、不改资源名称、不转换成 apply。

dry_run=true 的响应只显示“校验通过，未保存”，不当成真实变更。BOSS 的“校验”按钮按同一请求改变 dry_run；“提交”重新发送 dry_run=false，不产生审批票、操作ID或提交凭证。

### 4.1 写结果处理

| 情况 | BOSS 必须表达的结果 |
|---|---|
| Create/Update 成功 | 已保存/已创建对象，刷新查询；不承诺 Ready |
| Restart 成功 | 已提交重启，刷新工作负载/Pod；不显示重启完成 |
| Scale 成功 | 已设置期望副本数，分别显示 desired/observed；observed 不代表 Ready |
| Delete.deletion_requested=true | 已提交删除，刷新；Terminating 对象仍可显示 |
| Delete.already_absent=true | 资源已不存在，关闭详情并刷新列表 |
| dry_run=true | 仅校验，未保存；不改变列表缓存 |
| HTTP 超时、断网、结果不确定 | 提示“结果尚未确认，请刷新资源查看”；不自动重放写入，不伪造成功或失败回滚 |
| RESOURCE_VERSION_CONFLICT / RESOURCE_UID_CHANGED | 保留未提交草稿，提示刷新目标；不自动替换 expected_* 再提交 |

Gov 不创建 operation_id、任务记录、幂等表或重试队列。请求 ID 仅沿用现有追踪用途。

## 5. Gov 公共请求消息

命名固定如下；每个字段显式 json_name=本字段名。用于 HTTP 的 path 字段由生成 handler 绑定；严格 filter 在此之前拒绝 body 重复路径字段。

| 消息 | 字段（编号:type） |
|---|---|
| ClusterOpsListClustersRequest | 空消息 |
| ClusterOpsGetClusterOverviewRequest | cluster_id(1:string) |
| ClusterOpsListResourceTypesRequest | cluster_id(1:string) |
| ClusterOpsListResourcesRequest | cluster_id(1:string), group(2:string), version(3:string), resource(4:string), namespace(5:string), all_namespaces(6:bool), page_size(7:optional uint32), continue_token(8:string), name(9:string), label_selector(10:string), field_selector(11:string) |
| ClusterOpsGetResourceRequest | cluster_id(1:string), name(2:string), group(3:string), version(4:string), resource(5:string), namespace(6:string), include_secret_data(7:bool) |
| ClusterOpsCreateResourceData | type(1:ClusterOpsResourceTypeRef), namespace(2:string), name(3:string), manifest_yaml(4:string), dry_run(5:bool) |
| ClusterOpsCreateResourceRequest | cluster_id(1:string), data(2:ClusterOpsCreateResourceData) |
| ClusterOpsUpdateResourceData | type(1:ClusterOpsResourceTypeRef), namespace(2:string), manifest_yaml(3:string), expected_uid(4:string), expected_resource_version(5:string), dry_run(6:bool) |
| ClusterOpsUpdateResourceRequest | cluster_id(1:string), name(2:string), data(3:ClusterOpsUpdateResourceData) |
| ClusterOpsDeleteResourceRequest | cluster_id(1:string), name(2:string), group(3:string), version(4:string), resource(5:string), namespace(6:string), expected_uid(7:string) |
| ClusterOpsScaleWorkloadData | type(1:ClusterOpsResourceTypeRef), namespace(2:string), expected_uid(3:string), expected_resource_version(4:string), replicas(5:optional int32), dry_run(6:bool) |
| ClusterOpsScaleWorkloadRequest | cluster_id(1:string), name(2:string), data(3:ClusterOpsScaleWorkloadData) |
| ClusterOpsRestartWorkloadData | type(1:ClusterOpsResourceTypeRef), namespace(2:string), expected_uid(3:string), expected_resource_version(4:string), dry_run(5:bool) |
| ClusterOpsRestartWorkloadRequest | cluster_id(1:string), name(2:string), data(3:ClusterOpsRestartWorkloadData) |
| ClusterOpsStreamPodLogsRequest | cluster_id(1:string), namespace(2:string), pod_name(3:string), container(4:string), expected_pod_uid(5:string), follow(6:bool), previous(7:bool), timestamps(8:bool), tail_lines(9:optional int64), since_seconds(10:optional int64), since_time(11:Timestamp), limit_bytes(12:optional int64) |

为 Gov Proto 的日志方法声明增加 ClusterOpsPodLogFrameType 和 ClusterOpsPodLogFrame，枚举值/字段号/类型与 Res PodLogFrame 完全对应并加 ClusterOps 前缀。该 Proto 日志方法不加 google.api.http 注解：H11 由一个手写 SSE handler 注册，OpenAPI 通过 Gov 既有后处理脚本登记，不生成第二个同路径 handler。对外实际协议是下一节 SSE。

## 6. 日志查询与 SSE

### 6.1 请求

H11 的 cluster_id、namespace、pod_name 来自 path。

| query | 规则 |
|---|---|
| container | 可选 string；单普通容器时可省，多普通容器时 BOSS 要求选择 |
| expected_pod_uid | 可选 string；从 Pod 详情打开时必须携带已取得 UID |
| follow、previous、timestamps | bool；省略均 false；BOSS 初始视图显式 follow=true、timestamps=true、previous=false |
| tail_lines | 可选 int64 十进制字符串，>=0；0 与省略不同；BOSS 初始显式 `"200"` |
| since_seconds | 可选 int64 十进制字符串，>0 |
| since_time | 可选有效 RFC 3339 时间；与 since_seconds 互斥 |
| limit_bytes | 可选 int64 十进制字符串，>0；默认不设置 |

Gov 保留 optional presence；不能把 0 清成未设置。BOSS 的初始 200 行是可修改的显示选择，不是后端固定截断规则。previous/follow 组合按 Res 原生行为处理，不另加互斥限制。

拒绝 Last-Event-ID，不支持续传游标。使用现有 Bearer/会话请求客户端，不把 token 放 URL，不使用无法附带现有认证头的裸 EventSource。

### 6.2 握手与帧

Gov 完成现有认证/权限检查，并从 Res 收到首个 READY 后，才写 HTTP 200。此前任何失败使用第 7 节的普通 HTTP 错误 JSON。

响应头：`Content-Type: text/event-stream; charset=utf-8`、`Cache-Control: no-store`、`X-Accel-Buffering: no`；沿用 X-Request-ID。不要持久化流正文。

| SSE event | JSON data | 下游映射 |
|---|---|---|
| ready | `{ "pod_uid": "...", "container": "..." }` | READY；必须为首个业务事件 |
| data | `{ "data_base64": "..." }` | DATA 的 bytes 原样标准 base64；不是日志行 |
| end | `{ "reason": "EOF" }` 或 `{ "reason": "STREAM_LIMIT_REACHED" }` | END；收到后正常结束 |
| error | `{ "code": 503, "reason": "LOG_READ_FAILED", "message": "...", "metadata": {} }` | Res ERROR 或打开后 RPC/协议故障，使用稳定原因 |

每个事件由 `event: ...\ndata: <单行JSON>\n\n` 编码；JSON 编码负责转义，不将原始日志直接拼到 SSE 语法中。无 `id`、`retry`、全局序列号或确认协议。Gov 可每 15 秒发送 `: heartbeat\n\n` 注释以维持空闲链路；BOSS 忽略注释。

Res 每个 DATA 最多 16 KiB，SSE 经 base64 约增大三分之一。BOSS 单个未完成 SSE 事件缓冲上限 64 KiB，超出按协议错误终止；不对完整日志响应做 readAll。

### 6.3 取消、期限和单一转发

| 位置 | 规定 |
|---|---|
| Gov H01–H10 HTTP | 本域精确路由预算 35 秒；较短上游 deadline 优先 |
| Gov → Res unary | 每次 30 秒；不自动重试写操作 |
| H11 HTTP 与 Res stream context | 本次处理最长 10 分 15 秒；继承浏览器断开/取消；不套 35/30 秒预算 |
| 日志 READY 等待 | 最多 35 秒；用 timer 监督同一流，READY 后停止 timer；不能取消一个被整条 stream 继承的临时开流 context |
| SSE 每次 Write/Flush | 使用现有 http.ResponseController 的 5 秒写期限；失败立即 cancel 下游并退出 |
| Res 本身 | 原生 10 分钟读取、发送/结束预算仍由 Res 执行，Gov 不延长或替代 |

使用一个 Recv goroutine、容量最多 1 的接收交接通道和 handler 内唯一 HTTP 写者。写者同时处理帧、heartbeat 和取消；发送完成前不无界积压数据。退出时 cancel 下游，接收 goroutine 随 stream context 退出，不另建流管理服务或进程外会话库。

收到 END/ERROR 后只发一次对应终止事件，停止转发并 cancel；不能再把随后 gRPC 非 OK 状态重复转成第二个 error。没有终止帧就发生 EOF/网络中断，且非用户主动取消时，尽可能发 error `LOG_STREAM_INTERRUPTED`（503），不能当正常 END。

首帧不是 READY、重复 READY、未知帧类型属于 `LOG_STREAM_PROTOCOL_ERROR`（502）。已经写入 SSE 后不再调用普通 JSON 错误编码器，防止往流末尾追加第二种协议。若客户端已断开/写失败，无需补发错误帧。

BOSS 分别使用两个增量解码器：一个解析 HTTP 的 UTF-8 SSE 文本，一个对每个 data_base64 解码后的连续 bytes 执行 `TextDecoder.decode(bytes,{stream:true})`。保留换行、空行、重复日志；帧边界不等于 UTF-8 字符或行边界。正常终止时 flush 解码器，用户停止时释放 reader。

换 Pod/容器/参数、关闭抽屉、离开页面或退出登录时 AbortController 取消当前请求。END/ERROR/断流后均不自动重连；继续查看由管理员明确点击“重新连接”。

## 7. 错误映射

普通错误统一使用 `{code:number,reason:string,message:string,metadata:map<string,string>}` 和真实 HTTP 状态，不返回 HTTP 200 错误壳。

Gov 只提取 Res `google.rpc.ErrorInfo.domain=clusterops.ani.io` 的稳定 reason 和允许 metadata；保留安全 message，不直接拼接 raw status、证书、地址、manifest、Secret 或日志正文。

| 来源/reason | HTTP | BOSS 行为 |
|---|---|---|
| Gov 用户 JWT 无效/失效 | 401 | 沿用当前客户端单次刷新/登录处理 |
| Gov 非平台用户或缺权限：PLATFORM_CLUSTEROPS_DENIED | 403 | 显示无权访问，不当作空数据 |
| INVALID_CLUSTEROPS_REQUEST、INVALID_ARGUMENT、SYSTEM_FIELD_READ_ONLY、KUBERNETES_INVALID、POD_CONTAINER_REQUIRED | 400 | 展示原因，保留用户输入 |
| CLUSTEROPS_REQUEST_TOO_LARGE | 413 | 本域 HTTP JSON 超过32MiB传输预算，未向Res提交；不截断正文 |
| RESOURCE_READ_ONLY、TENANT_NAMESPACE_READ_ONLY、KUBERNETES_FORBIDDEN | 403 | 显示具体只读/权限原因，不自动退出登录 |
| CLUSTER_NOT_CONFIGURED、RESOURCE_NOT_FOUND | 404 | 对象/集群不存在；不能隐式选择其他集群 |
| RESOURCE_ALREADY_EXISTS、RESOURCE_UID_CHANGED、RESOURCE_VERSION_CONFLICT、POD_CHANGED | 409 | 冲突/目标已变化，刷新确认，不重放 |
| RESOURCE_TYPE_UNAVAILABLE、ACTION_NOT_SUPPORTED、RESTART_STRATEGY_UNSUPPORTED、CRD_HAS_PROTECTED_NAMESPACE_INSTANCES、CRD_INSPECTION_FAILED | 409 | 显示当前条件不允许，不能替代为其他动作 |
| LIST_EXPIRED | 409 | 清晰提示刷新快照；不自动拼接不同快照 |
| 可信 clusterops.ani.io ErrorInfo 的 RESOURCE_EXHAUSTED | 429 | 显示请求受限，不自动重放写操作 |
| KUBERNETES_UNAUTHORIZED、Res PLATFORM_SCOPE_REQUIRED | 502 | 下游凭据/委托故障；保留原 reason，不触发用户401刷新 |
| NAMESPACE_POLICY_UNAVAILABLE、KUBERNETES_UNAVAILABLE、LOG_READ_FAILED | 503 | 依赖暂不可用；读取或操作按实际错误显示 |
| LOG_SEND_TIMEOUT、非用户取消的 DeadlineExceeded | 504 | 超时；写请求结果可能不确定，建议刷新查看 |
| INTERNAL_ERROR | 500 | 简短内部错误，保留 request_id |
| Gov 未配置 Res client / RPC Unimplemented：CLUSTEROPS_UNAVAILABLE | 503 | Res 接入或部署版本未就绪，不回退其他 API |
| 无可信 ErrorInfo 的上游认证失败：CLUSTEROPS_UPSTREAM_AUTH_FAILED | 502 | 不把下游认证失败当用户未登录 |
| 已建立日志无终止帧：LOG_STREAM_INTERRUPTED | 503 | 提示中断，手工重连 |
| 日志协议异常：LOG_STREAM_PROTOCOL_ERROR | 502 | 终止流并提示协议错误 |
| 无可信领域 ErrorInfo 的 gRPC ResourceExhausted（含接收超限）、其他未分类上游错误：CLUSTEROPS_UPSTREAM_ERROR | 502 | 使用通用安全说明，不输出原始响应 |

允许 metadata：cluster_id、group、version、resource、namespace、name、kubernetes_reason、kubernetes_http_status、field_paths，以及 Gov 的 request_id。上游 request_id 不覆盖 Gov 已建立的请求身份。

浏览器取消是正常释放，不显示操作失败 toast；服务端继承取消，不改为 background context。超时后 Gov 不发第二次写调用。

## 8. 页面能力与只读状态

已有对象的写操作按钮（update/delete/scale/restart）要求当前 descriptor.actions 包含该动作且 ResourceDocument.read_only=false。创建要求create能力，并按目标Namespace的只读状态处理。查看、事件和日志不因read_only=true禁用，继续按对应能力与既有授权处理。以上交互均不替代服务器鉴权。

只读原因展示固定为：

| read_only_reason | 提示 |
|---|---|
| TENANT_NAMESPACE_READ_ONLY | 租户命名空间资源，本入口只读 |
| NAMESPACE_POLICY_UNAVAILABLE | 租户命名空间登记暂不可用，写操作暂不可用 |
| RESOURCE_READ_ONLY | 本期仅支持查看该类型 |
| 空 | 不显示只读原因；仍以实际请求结果为准 |
| 未来未知值 | 显示“当前资源只读”及安全原因码；不默认解锁 |

Namespaced 创建必须选择一个具体 Namespace。可通过该 Namespace 的 GetResource 返回判断只读；选择器支持原生分页/精确名称定位，不先拉齐全集。Cluster 资源创建不伪造 Namespace。Namespace 本体的创建/删除保护由 Res 执行，页面不自己查登记数据库。

Gov 不重新查询 Owner、IAM 租户目录或 Kubernetes 来判断归属；BOSS 不看名称前缀/labels 猜归属。通用 CR 和已安装的 Higress 等 CR 都适用同一套规则。

## 9. 两项局部规范例外

1. **cluster-ops 资源列表正式使用游标**，仅本域不要求 page/pageSize/total。两仓文档明确登记，不把它伪装成业务旧接口迁移，不放宽其他列表规则。
2. **BOSS 本域写函数返回类型化结果**，保留 Mutation/Delete/Scale 的真实字段；仍在成功后刷新查询，不用 mutation 响应直接改造缓存。其他业务的 Promise<void> 约定不改。

写请求继续沿用 Gov 的 data 包装；登录、平台角色、Casbin、普通错误和 Secret 展开没有额外协议例外。

## 10. 依据

- [Res 实际接口](https://github.com/zhangzhe-ctrl/ani-resource-service/blob/0837ee61ca4e486841932a18e424ac74ad92ede5/api/clusterops/v1/clusterops.proto)
- [Res 实际语义与预算](https://github.com/zhangzhe-ctrl/ani-resource-service/blob/0837ee61ca4e486841932a18e424ac74ad92ede5/docs/contracts/cluster-operations-v1.md)
- [Gov HTTP 规则](https://github.com/zhangzhe-ctrl/ani-governance/blob/61b7b6c74bd16d32f327aef42182566a87fe024f/docs/contracts/frontend-http-api.md)
- [Gov 现有 platform 路由](https://github.com/zhangzhe-ctrl/ani-governance/blob/61b7b6c74bd16d32f327aef42182566a87fe024f/api/protos/admin/service/v1/i_platform_network.proto)
- [BOSS 请求与401处理](https://github.com/zhangzhe-ctrl/ani-boss-console/blob/4f4024049a9f848138702bb93c9ffee719aad2b3/src/api/request.ts)
