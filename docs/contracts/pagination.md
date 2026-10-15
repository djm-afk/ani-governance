# Gov 前端 HTTP 接口规范与 VPC 参考实现

本规范定义经 Gov 向前端提供的 HTTP JSON 接口的统一目标规则。当前仅 VPC R01 完成本批页码协议切换；其他存量接口仍按各自当前合同执行，协议、迁移和历史验收继续在[接口集成登记](../interface-integration-register.md)维护，不因本文生效而视为已迁移。各 owner 保留自己的领域模型和 Go 接口，不要求实现相同的 Repository 接口，也不以建立跨仓公共库为前提。

## 请求约定

- 查询参数放在 URL 中，创建和修改的业务数据使用 JSON 请求体。
- 业务字段和请求体结构以接口定义为准，前端不自行增加或删除 `data` 包装。
- 公共分页参数使用 `page`、`pageSize`；筛选和排序参数由各接口声明。

## 请求与列表响应

| 项目 | 规则 |
| --- | --- |
| 请求 | 顶层 `page`、`pageSize`，分别默认 `1`、`20`；只传一个时独立补足另一个 |
| 数值 | 规范十进制正整数，无符号、空白、前导零或小数；VPC page 为 `1..4294967295`，pageSize 为 `1..100` |
| 错误 | 显式零、负数、非规范整数、重复、溢出或新旧窗口混用返回 400；校验先于空结果返回，不静默修正 |
| 查询 | `limit=pageSize`、`offset=(int64(page)-1)*int64(pageSize)`；直接 LIMIT/OFFSET，首次请求可查询第 3 页 |
| 响应 | 公共字段 `items`、`total`；items 始终为数组，total 为非负十进制字符串，不能用本页长度代替 |
| 空结果 | 无匹配：`{"items":[],"total":"0"}`；超出末页：空数组，total 保留真实总数 |
| 范围 | List 和 Count 共享可信身份、权限、租户和全部业务筛选，total 不受窗口或 fieldMask 影响 |
| 排序 | 截窗前采用确定排序，主排序追加唯一键；资源声明允许的排序及 NULL 顺序，不接受任意 SQL 列 |
| 一致性 | 单次数据库响应的 Count/List 使用同一只读快照；内存有界目录从同一次读取结果 count/slice；不承诺跨请求冻结 |

每次请求都重新经过当前认证、权限、租户状态和套餐检查；入站 tenant/actor/owner 不能替代可信上下文。关系筛选也须在授权租户内解析。列表可保留有业务含义的 observation、tenantId 等元数据；导航、权限、容量、统计、日志等完整快照按各自合同处理。

## VPC 请求与响应

```http
GET /api/v1/networks/vpcs?page=3&pageSize=2&name=研发网络
```

```json
{
  "items": [
    {"id":"vpc_0123456789abcdef0123456789abcdef","name":"研发网络","cidr":"10.20.0.0/16"}
  ],
  "total": "5"
}
```

示例只展示部分业务字段。VPC 保留 `name` 的去首尾空白后区分大小写精确匹配、现有 `state` 筛选；不传 state 时排除 deleted。稳定排序为 `created_at DESC, vpc_id DESC`，两列均非 NULL。Gov 保留当前 JWT/AK、Casbin、NETWORK 套餐和持久租户 UUID 映射，Resource 验证 mTLS 委托并在 List/Count SQL 同时约束 tenant。

普通详情直接返回资源对象：

```http
GET /api/v1/networks/vpcs/vpc_0123456789abcdef0123456789abcdef
```

```json
{"id":"vpc_0123456789abcdef0123456789abcdef","name":"研发网络","cidr":"10.20.0.0/16"}
```

不增加 `vpc` 或 `data` 包装。资源与 operation 等有业务含义的组合结果继续保留，分页改造不删减这些字段或扩大其他响应的迁移范围。

## 创建修改和删除

写入接口需要返回单个资源时，采用与详情相同的对象结构；没有业务结果需要返回时，返回 `{}`。使用 `204` 的接口不返回响应体。

部分更新按接口声明的更新字段处理：未指定更新的字段保持原值；清空字段必须显式表达，不能把省略字段当成清空。

异步受理保留任务或操作信息；请求受理成功不代表创建、删除等业务操作已经完成。

## 错误响应

通过 HTTP 状态码区分请求成功或失败，不把失败统一包装成 `200`。错误体包含 `code`、`reason`、`message`。例如，分页参数不合法时返回 HTTP `400`：

```json
{
  "code": 400,
  "reason": "INVALID_NETWORK_REQUEST",
  "message": "分页参数不合法",
  "metadata": {
    "request_id": "req-example"
  }
}
```

前端根据 HTTP 状态和 `reason` 处理错误；`message` 用于展示说明，不依赖其固定文案判断业务分支。响应提供 `X-Request-ID` 或 `metadata.request_id` 时保留该值用于排查，不要求每个错误都包含它们。

## 公共字段与特殊结果

- 64 位整数在 JSON 中使用十进制字符串，例如 `"total": "3"`、`"version": "1"`；沿用正式 Proto JSON 编码，不改全局编码器。
- 时间使用 RFC 3339 字符串，例如 `"2026-10-15T08:30:00Z"`。
- 字段名、枚举值和 ID 类型按接口定义使用，不自行转换大小写或把所有 ID 强制转换成数字。
- 普通详情直接返回对象，普通列表不增加外层 `data`；成功响应不统一添加 `code/message/data` 外壳。
- 有业务含义的组合结果保留完整结构，例如资源与操作、凭证与密钥、制品与下载地址；必要的列表元数据也应保留。
- 导航树、权限快照、容量快照、统计和日志按各自业务结构返回，不因为内部有数组就强行改成普通分页列表。

## 旧协议边界

本次 Gov VPC 采用明确切换：无窗口参数即 page=1/pageSize=20；`limit/cursor/offset/token/page_size/page_token/noPaging` 及其他未声明参数一律 400，不提供公网上的薄兼容响应，也不返回 next_cursor。旧 Proto 编号与名称已 reserved，页码使用新编号。这是对旧 VPC 调用者的协议变更；SDK、AK/运维调用者须升级后再部署，前端迁移和部署未由本次交付证明。

Resource RPC 保留旧 limit/cursor 字段及原无页码请求行为，供已发布的旧 Gov/内部调用者过渡；显式 page 或 page_size 选择新窗口，不能与非零 limit 或非空 cursor 混用。新 Gov 始终发送补齐默认值后的两个页码字段。Resource 旧 RPC 的退出须另行核查消费者并安排版本，当前不移除其他资源使用的共享游标设施。

<a id="users-current"></a>
<a id="existing-protocols"></a>

users、roles、api-keys 及 EIP/Subnet/LB/Image、ModelDev、Accelerator、Quota 等继续使用各自当前协议；尚未迁移的游标规则见[存量游标合同](cursor-pagination-legacy.md)。`LIST-CURSOR-01/02` 是历史批次，逐接口状态及遗留项仍见[登记](../interface-integration-register.md#list-cursor-01)，不能由共享 Proto 字段推断能力。

<a id="current-differences"></a>

其他 owner 迁移时明确自己的兼容期、无参数语义和退出条件；需要兼容就按声明保留旧响应，协调切换就明确拒绝旧字段。不要通过循环拉取前 N 页伪装随机跳页，不把旧分页器静默截断或 noPaging 上限带入新合同。

## 最终实现与测试入口

Resource 引用固定为 Gov [go.mod](../../go.mod) 中的正式模块版本；以下 Resource 相对路径均位于该版本源码。

| 层次 | Gov 参考位置 | Resource 参考位置 |
| --- | --- | --- |
| Proto / HTTP | [vpc.proto](../../api/protos/catalog/service/v1/vpc.proto)、[i_network.proto](../../api/protos/admin/service/v1/i_network.proto)；详情使用 response_body | [api/network/v1/network.proto](https://github.com/zhangzhe-ctrl/ani-resource-service/blob/98a534804231c7842acaa080fe1ebec066d921b2/api/network/v1/network.proto)：有 presence 的 page/page_size，旧 RPC 字段 deprecated |
| 参数与签名 | [network_policy.go](../../pkg/middleware/auth/network_policy.go)、[AK 客户端](../../scripts/ops/aksk_vpc_client.py)；白名单、单值、规范整数、canonical query | [internal/biz/network/page.go](https://github.com/zhangzhe-ctrl/ani-resource-service/blob/98a534804231c7842acaa080fe1ebec066d921b2/internal/biz/network/page.go)：简单 PageWindow，先转 int64 再计算 |
| Service / Client | [vpc_page.go](../../app/admin/service/internal/service/vpc_page.go)、[network_service.go](../../app/admin/service/internal/service/network_service.go)、[network_client.go](../../app/admin/service/internal/data/network_client.go) | [internal/service/network/network.go](https://github.com/zhangzhe-ctrl/ani-resource-service/blob/98a534804231c7842acaa080fe1ebec066d921b2/internal/service/network/network.go)、[internal/biz/network/list.go](https://github.com/zhangzhe-ctrl/ani-resource-service/blob/98a534804231c7842acaa080fe1ebec066d921b2/internal/biz/network/list.go)；保留业务筛选、身份与领域字段 |
| SQL / Repository | 通过正式 Resource 模块调用；无本地 replace | [internal/data/network/queries/vpcs.sql](https://github.com/zhangzhe-ctrl/ani-resource-service/blob/98a534804231c7842acaa080fe1ebec066d921b2/internal/data/network/queries/vpcs.sql)、[internal/data/network/postgres.go](https://github.com/zhangzhe-ctrl/ani-resource-service/blob/98a534804231c7842acaa080fe1ebec066d921b2/internal/data/network/postgres.go)；同一 Repeatable Read 只读事务 Count + List，sqlc OFFSET |
| 定向验证 | [参数测试](../../pkg/middleware/auth/network_policy_test.go)、[Service 测试](../../app/admin/service/internal/service/network_service_test.go)、[真实 HTTP 测试](../../app/admin/service/internal/service/network_joint_http_test.go) 的 `vpc_pages`、[AK 脚本测试](../../scripts/tests/test_aksk_vpc_client.py) | [internal/biz/network/page_test.go](https://github.com/zhangzhe-ctrl/ani-resource-service/blob/98a534804231c7842acaa080fe1ebec066d921b2/internal/biz/network/page_test.go)、[internal/data/network/vpc_page_integration_test.go](https://github.com/zhangzhe-ctrl/ani-resource-service/blob/98a534804231c7842acaa080fe1ebec066d921b2/internal/data/network/vpc_page_integration_test.go)；同快照并发验证和旧游标回归 |

正式入口：Resource `make config sql`，Gov `make api`；全部固定版本生成和格式化在本次指定的 Fedora 执行。复用[联调入口](../../scripts/network-joint-integration)，设置 `NETWORK_JOINT_TEST_RUN='^TestNetworkJointHTTP/vpc_pages$'` 与真实 Resource 测试二进制。该入口只建任务独占 PG/Redis，真实 HTTP、JWT/AK、Casbin、mTLS、业务查询和持久化均运行生产代码，替身只在 Provider 边界。验证结果与未验证边界记录在接口登记，本文不复制执行台账。
