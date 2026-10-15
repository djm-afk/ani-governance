# 存量游标协议

本文件保留尚未迁移接口的游标合同。VPC R01 已由[前端 HTTP 接口规范](frontend-http-api.md)替代；其他接口的迁移状态和历史验收只在[接口登记](../interface-integration-register.md#list-cursor-01)维护。本文件不新增迁移范围或改变任何运行行为。

### 请求与响应

- 游标列表请求使用 `limit` 和不透明 `cursor`。首次不传 cursor，后续原样回传上一页 `next_cursor`，并保留相同筛选与排序；客户端不解析或改写游标。
- Governance 自管游标基线的 limit 默认 20，合法范围 1～100。非法、零或越界值返回 400，不能静默截断。领域专用协议沿用其已接受的默认值与上限。
- cursor 与 `page/pageSize`、`offset`、`token` 互斥，冲突返回 400；显式旧页码兼容属于独立分支。`noPaging` 的支持及冲突处理必须在该接口登记，不能用它绕过游标参数校验。
- 游标响应为 `{items, next_cursor, total}`。只在确实存在下一页时给出非空 next_cursor；末页为空。HTTP 编码器可能省略空字段或输出空串，调用者以非空值判断是否继续。
- total 是当前可信租户、当前权限及相同筛选条件下的匹配总数，不受 cursor、limit、page 或 offset 窗口影响，不能用本页 items 长度代替。空结果 total 为零；Proto int64/uint64 的 HTTP JSON 值为十进制字符串。
- 未承诺 total 的领域列表保持原响应，不伪造零或页内计数。新增 total 时同时实现同租户、同权限、同筛选的计数路径并验收。

### 过滤、身份与授权

每次翻页重新经过该接口当前的认证、权限、租户状态和套餐检查，分页凭证不代替授权。租户与身份来自认证上下文和服务端可信 viewer/Principal；入站租户字段只在接口明确允许时作为受约束的筛选条件，不能切换调用者身份。

列表查询和 total 查询使用同一组租户、权限与业务过滤条件。关系筛选也必须在可信租户内解析；ID 使用明确的精确条件，文本搜索使用 contains，不能将 ID 放入模糊搜索。筛选值通过参数化查询进入数据库，HTTP 参数白名单与未知、重复或歧义参数的处理按该接口明确合同落实。

### 排序与游标签名

- 使用稳定的键集排序：确定主排序列及方向，追加唯一 ID 兜底，排序与续页谓词保持一致。允许排序列、NULL 位置及值类型由资源声明，不直接采用任意客户端列名。
- 可空列区分 SQL NULL、空串和零值。游标取值来自数据库实体；时间、数字还原为数据库可比较的类型。`fieldMask` 只裁剪输出，生成游标需要的排序列与 ID 可额外读取，输出前按原 mask 清理。
- 签名游标绑定资源、可信上下文类型、可信租户及原始筛选与有效排序的指纹。指纹在业务扩展关系筛选之前计算，防止内部展开的 ID 集合改变客户端筛选的含义。
- 使用受控密钥签名。签名失败、格式/版本不支持、超长或绑定不匹配返回 400，不降级为首页；缺可信 viewer 或签名配置时 fail-closed。所有副本使用兼容的密钥配置，轮换后的旧游标失效与客户端重启首页一起处理。
- 本仓 `c1` 游标与历史 token 格式独立，不能互换，也不能继承旧 token 在缺密钥时的未签名兼容行为。

稳定键集排序保证固定结果集中的续页顺序，不自动提供跨请求快照。并发写入、删除、排序值或授权变化可能改变后续页与 total；需要快照语义的接口必须另行明确一致性合同并验证，不能从游标签名推导出该能力。

## 自管 users 的既有约束

普通分页请求未显式提供 `page`、`pageSize`、`offset` 或 `token` 时进入游标分支，limit 默认 20、范围 1～100；存在非空 cursor 且同时提供上述窗口字段或 `noPaging` 时报错。显式提供旧窗口字段时仍走 `ListWithPaging`，例如 `?page=1&pageSize=20`；该分支返回 items/total，不具备新游标续页语义。

users 的默认排序为 ID 降序，等价于 `orderBy=["-id"]`。客户端 `sorting/orderBy` 只接受一个有效主排序字段并追加同方向唯一 ID。允许列为 `id/tenant_id/username/nickname/realname/email/mobile/status/gender/created_at/updated_at/last_login_at/locked_until`；降序 NULL 在前、升序 NULL 在后，键集谓词分别处理 NULL 区段。具体字段类型和取值以 `userCursorSortColumns` 为准。

[cursor codec](../../pkg/localdeps/go-crud/pagination/cursor_codec.go) 使用 HMAC-SHA256，格式为 `c1.<base64url(payload)>.<hex(hmac)>`；base64 载荷长度上限 2048 字节。绑定资源为 `identity.user`，上下文为 tenant/platform/system，平台及系统上下文租户绑定值为 0，租户上下文取服务端 viewer。当前绑定不包含具体用户 ID 或角色版本；每次请求仍须重新授权。

[启动装配](../../app/admin/service/cmd/server/wiring_ent.go) 从 `ANI_ACCESS_KEY_ENCRYPTION_KEY_FILE` 加载的 AK/SK 主密钥，以 `ani-governance/pagination-cursor/v1` 域分离派生游标签名子密钥，并设置 `SetCursorSecret`。所有副本需使用同一主密钥；轮换会使旧游标失效。它不复用 `SetTokenSecret`。

total 为 uint64，HTTP JSON 为字符串。查询侧 Ent TenantPrivacy 继续施加租户边界；未新增路由或权限点。当前页查询与 Count 分别执行，没有显式绑定同一个数据库快照，也没有跨页快照令牌。

其他资源的具体窗口、字段、授权、排序和 token 格式沿用各自当前 Proto 与领域合同；不得从共享 PagingRequest 推断已接入的能力。LIST-CURSOR-01/02 的后续迁移须按新页码规范安排兼容与退出，当前不删除公共游标和密钥设施。
