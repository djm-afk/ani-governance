# Gov 认证会话与 API 路径改造执行计划

日期：2026-10-12。状态：待执行的交接计划；相关业务改造尚未实现、测试或部署。本文说明实施范围，不代替执行人获得代码实施、业务库操作、推送或部署授权。

目标是接通一条可观察的业务主链：同一浏览器同时登录 Console 的租户账号和 BOSS 的平台账号，两端访问令牌只保存在内存；页面重载后通过各自的 HttpOnly Cookie 恢复会话；两端按原有权限访问共用的新管理地址；刷新、退出不会影响另一端；来源、CSRF 和跨应用令牌错误会被拒绝；路径迁移保留原 API ID、启停状态与权限关系。

## 1 范围和完成边界

用户选择的三个范围是：平台与租户会话隔离、浏览器安全边界、按功能统一 API 地址并允许 BOSS 与 Console 共用 path。默认采用本计划中的目标合同；执行负责人在阶段 0 核对部署输入后冻结它。默认值如需调整，先记录具体差异，再开展依赖该差异的实现。

纳入：

- 分离 Console 和 BOSS 的刷新 Cookie、令牌用途及服务端会话归属。
- 移除浏览器持久存储中的访问令牌，补齐内存会话恢复、CSRF、来源校验、生产 Cookie 配置和日志脱敏。
- 处理上述主链必需的前端刷新协调，以及当前会话退出与刷新竞争。
- 统一公开业务 HTTP 路由、生成产物、存量 API 目录、首次种子、启动检查、前端地址、运维脚本及代理规则。
- 使用真实业务代码、真实独占 PostgreSQL/Redis 和真实浏览器验证这条链。

明确排除：

- `PUT /admin/v1/me` 的自助角色写入风险修复。该入口可随本批迁移地址，业务可写字段与校验不在本批改动范围。风险继续保留，不能把本计划完成报告成全部认证安全问题已关闭。
- 完整角色降权即时吊销、所有设备退出、租户切换、Membership OneToMany 改造、通用 IAM 或 OAuth/OIDC 重建。
- 修改权限点 code、套餐业务模块、业务服务 RPC、服务间 mTLS 身份或服务所有权。
- Go 模块改名、手改生成实现、全量数据重新播种、新增独立认证服务或其他运行组件。
- 前端页面样式、非阻塞管理功能及本批无关历史缺陷。

## 2 基线和当前断点

规划时已核对的远端 main 为 `1d4dbf489549b4a3c0100674ea17b34d78bae74e`；本地工作树 HEAD 为 `87789896df99954712c80ad89cabb3d0e09f4345`。远端已比本地更新，执行人必须从开始执行时重新核对的最新 main 创建任务分支，不能把本地旧 HEAD 当目标基线。保持无关修改，不 reset、clean、stash 或覆盖共享工作树。

当前源码的关键事实：

- 租户和平台密码登录都固定 `ClientType_admin`，共用 `refresh_token` Cookie，旧 Path 为 `/api/v1/auth/refresh`。同一后端 host 下后登录覆盖前登录的刷新 Cookie。
- access token 由响应体返回，公网业务鉴权包含 JWT、Redis 会话、Casbin 与租户套餐检查。截图中的 localStorage 行为属于前端，尚未读取前端仓库独立确认。
- 刷新先原子消费旧 AT/RT，再查询用户、计算权限和创建新令牌；用户查询或签发失败时旧会话已经被消费。
- 退出依赖有效 Bearer，当前吊销该用户全部 admin 会话。MFA 完成也独立签发令牌并调用同一 Cookie 写入函数。
- 当前刷新链未发现独立 CSRF token 和精确 Origin/Referer 校验；Secure 随请求 TLS/转发协议变化。
- BFF Proto 声明统计为 252 个唯一方法与路径组合，其中 `/admin/v1` 191 个、`/api/v1` 61 个。这是声明统计，不是运行装配或部署验收结论。
- 用户、角色读取、个人资料和初始化是两端共用接口；ModelDev 的旧 `/admin/v1/modeldev` 是租户业务，不能因为旧前缀就认定为 BOSS 专用。
- API 目录同步按 `(method,path)` 匹配；改路径后直接 sync 会新增 API ID，旧授权不会自动迁到新地址。启动检查也包含旧地址常量。

远端相对于此前 `4e3e5b4` 的最新两条提交只涉及 ModelDev 客户端身份校验及接口登记，没有改变本计划引用的认证和 HTTP 路由实现。执行时仍需再次核对差异。

## 3 执行前必须补齐的输入

执行负责人记录下表的实际值；未提供时可推进不依赖该输入的源码工作，但不能假装已完成前端或部署验收。

| 输入 | 要求 |
| --- | --- |
| Gov 仓库、基线、分支 | 核对 remote main SHA、HEAD、dirty 状态；任务自有分支/工作树 |
| BOSS 与 Console 仓库 | 明确仓库路径、实际使用的版本、认证 Store、HTTP 拦截器、bootstrap、MFA 和 SSE 调用位置；本计划未发现或猜测这些路径 |
| 域名与入口 | 两端 Origin、API Origin、HTTPS 终止位置、同站/跨站关系、代理信任边界；包含测试与正式值 |
| 浏览器支持范围 | 确定最低浏览器版本，核对 Web Locks、BroadcastChannel 与 Cookie 行为 |
| 运行环境 | 明确生成/构建/测试主机。沿用已有本地源码与 Git、Fedora 重任务的环境约定；执行前核对实际可用主机，不套用历史 IP/context |
| 独占测试资源 | 明确授权的独占 PostgreSQL 库与 Redis 实例/任务自有隔离范围；不得默认共享业务 DSN 或 TRUNCATE 共享库 |
| 发布方式 | 默认协调切换并允许一次重新登录；如业务要求无停机兼容，先完成明确的双版本兼容方案 |
| 会话失效告知 | 本计划推荐拒绝旧版缺少应用/用途 claim 的浏览器令牌，发布时要求重新登录；执行负责人确认发布影响 |
| 部署和数据权限 | 明确谁批准、谁执行业务库迁移、前后端发布及代理切换；计划本身不扩大授权 |

## 4 目标 HTTP 路径合同

### 按功能分组

| 功能 | 目标前缀 | 说明 |
| --- | --- | --- |
| 后台治理管理与共用初始化 | `/api/v1/admin/...` | 用户、角色、租户、套餐、权限、菜单、个人资料、初始化、会话管理、审计、配置等；`admin` 不表示仅平台身份可访问 |
| 浏览器认证 | `/api/v1/auth/...` | 两端可共用 refresh、csrf、logout；原有两个密码登录入口保留区别 |
| 租户业务 | `/api/v1/<业务域>/...` | Network、Image、Inference、Accelerator 的既有 `/api/v1` 入口保持；ModelDev 从旧前缀迁至 `/api/v1/modeldev/...` |
| SSE 通知 | `/api/v1/events` | 当前 `/events` 是独立传输配置，需单独核对装配及前端调用，不包含在 Proto 声明统计中 |

静态邀请页面、Swagger 页面、健康检查、metrics、pprof 和内部 RPC 不作为业务 API 前缀替换对象。生产可访问性按各自现行部署约束核对，不借本批改路径扩大暴露面。

典型映射：

| 方法 | 旧地址 | 新地址 |
| --- | --- | --- |
| GET | `/admin/v1/users` | `/api/v1/admin/users` |
| GET | `/admin/v1/roles` | `/api/v1/admin/roles` |
| GET | `/admin/v1/tenants` | `/api/v1/admin/tenants` |
| GET | `/admin/v1/plans` | `/api/v1/admin/plans` |
| GET/PUT | `/admin/v1/me` | `/api/v1/admin/me` |
| GET | `/admin/v1/initial-context` | `/api/v1/admin/initial-context` |
| POST | `/admin/v1/mfa/verify` | `/api/v1/auth/mfa/verify` |
| GET/POST | `/admin/v1/modeldev/executions` | `/api/v1/modeldev/executions` |
| GET | `/events` | `/api/v1/events` |

同目录 [route-map.tsv](route-map.tsv) 是根据规划时源码提取的候选全量映射，执行人核对最新 main 后冻结它。保留 HTTP 方法、路径参数、RPC operation、Service tag、请求与响应格式、权限点和套餐模块；本批不顺带改 CRUD 风格、分页或字段命名。

### 共用浏览器认证入口

`application` 的 JSON 值严格为 `console` 或 `boss`，是选择应用会话的参数，不是可信身份。不得复用已有审计字段 `client_id` 或 `device_id` 作为应用授权依据。

| 接口 | 请求与身份来源 | 目标行为 |
| --- | --- | --- |
| POST `/api/v1/auth/password/login` | 现有租户账密请求 | 由入口确定 `application=console`，校验当前租户与账号规则；不能通过请求改为 boss |
| POST `/api/v1/auth/platform/password/login` | 现有平台账密请求 | 由入口确定 `application=boss`，保留平台角色准入 |
| POST `/api/v1/auth/mfa/verify` | 已有 operation_id 与验证答案 | 从原登录挑战取可信 application；响应报文保留现有兼容字段，补必要 CSRF 字段 |
| POST `/api/v1/auth/csrf` | JSON `{application}`，对应 RT Cookie，精确来源校验 | 非消费性校验已有会话，返回绑定当前逻辑会话的 CSRF token；不刷新、不更换 Cookie、不要求先有 AT |
| POST `/api/v1/auth/refresh` | JSON `{application, client_id?, device_id?}`，对应 RT Cookie、`X-CSRF-Token` | 无需 AT；校验应用、来源、证明、令牌用途及服务端状态；轮换令牌对 |
| POST `/api/v1/auth/logout` | JSON `{application}`，对应 RT Cookie、`X-CSRF-Token` | 无需有效 AT；退出当前应用的当前逻辑会话，覆盖其所有刷新后继；不退出另一应用或其他浏览器设备 |

新增字段只使用新 Proto field number，保留已有 field number 和语义。`csrf_token` 不含身份授权；AT 和 CSRF 只进入响应 JSON 与前端内存，RT 不进入响应 JSON。登录/MFA/刷新响应和所有认证错误使用 `Cache-Control: no-store`，不得通过 URL 返回任何令牌。

csrf、refresh、logout 以所选 RT 为会话凭证，前端不自动附加 Bearer 或 AK/SK。合同须明确拒绝意外混入的另一套会话凭证，禁止服务端在两套主体之间猜测。logout 不再根据用户上报的 jti 选择其他会话；如保留该旧字段，只用于审计。

错误合同在阶段 0 同步冻结。优先复用当前 Proto reason，确需新增时更新 Proto 与生成产物；前端按明确 reason 处理，不把所有 401/403 都解释成需要刷新。

| 情形 | HTTP 与处理要求 |
| --- | --- |
| application 缺失、非法，或混入另一套凭证 | 400；请求合同错误，不自动刷新 |
| RT 缺失、过期、重放，或签名/用途/会话不一致 | 401，沿用明确的无效刷新会话 reason；不能消费其他合法会话 |
| Origin/Referer 或 CSRF 失败 | 403，使用可区分的来源/证明 reason；不自动恢复，不消费 RT |
| 合法主体权限、租户或套餐拒绝 | 保留现有 403 reason；不会因换路径或 app 而放宽 |
| Redis/必要仓储不可用 | 503 或现有明确的服务错误；保留服务故障类别，不能变成无效密码/令牌 |
| 已确认退出 | 200，明确 revoked；已无活动会话的幂等结果单独表达，不宣称本次完成远端吊销 |

## 5 应用会话与 Cookie 合同

### 应用和用途必须有服务端证明

- Console/BOSS 登录入口决定 application，并写入 AT、RT 和 Redis 会话。使用明确的 audience/application 与 `token_use=access|refresh`；检查签名、有效期、预期 issuer、用途和已登记应用值。
- 新增稳定逻辑会话 ID `sid`。同一会话刷新后 jti 改变，sid 不变；uid/application/sid 必须与 Redis 状态一致。未知、缺失或冲突的应用与用途均拒绝，不给缺失 claim 默认归为 boss 或 console。
- 普通共用管理接口可接受合法的两种用户应用令牌，随后仍按原角色、租户和套餐检查。audience 只说明应用来源，不授予平台权限；不能看到 `boss` 就把租户改为 0。
- Cookie 名、请求 application、签名声明和 Redis 记录四者相互一致。不能从请求的 tenant/operator/owner 字段确定主体。
- MFA 挑战持久化可信 application；MFA 成功从挑战签发相应令牌并写正确 Cookie。检查所有签发路径，禁止留下旧 Login、测试辅助或后台方法可以签发缺少应用字段的浏览器令牌。
- 保留 `ClientType_admin` 的现有技术语义；应用维度另行表示。若决定改变 ClientType，必须说明全部缓存和在线会话调用的兼容影响。

### Cookie 属性

| 属性 | Console | BOSS |
| --- | --- | --- |
| Name | `console_refresh_token` | `boss_refresh_token` |
| Path | `/api/v1/auth` | `/api/v1/auth` |
| Domain | 不设置，host-only | 不设置，host-only |
| HttpOnly | true | true |
| Secure | 正式环境固定 true | 正式环境固定 true |
| SameSite | Lax | Lax |
| Max-Age | 与服务端该 RT 的实际期限一致 | 与服务端该 RT 的实际期限一致 |

Path 覆盖 csrf、refresh 和 logout，避免退出时浏览器根本不发送 RT。Path 和名称用于路由携带及避免覆盖，不作为安全授权边界。

正式环境采用同站 HTTPS 或同源反向代理。不同 Origin 可以同站；真正跨站部署会影响 Lax Cookie 的 fetch 携带，必须在阶段 0 解决，不能直接降为 SameSite=None 或取消 CSRF。只信任明确配置的 HTTPS 入口/代理，不让任意入站 X-Forwarded-Proto 决定正式 Cookie 是否降为非 Secure。开发 HTTP 如必须支持，使用明确隔离的开发配置，不能通过请求触发正式配置降级。

旧 `refresh_token` 和 `refresh_exp` 必须按实际 Name/Path/Domain 删除；旧 RT Path 至少包含 `/api/v1/auth/refresh`，旧过期提示 Path 为 `/`。核查实际历史 Domain，不笼统删除所有 Cookie。登录、MFA 成功或退出只设置/清理所属应用新 Cookie，不清另一端。

新前端不依赖 `refresh_exp` 判断已登录或是否可以恢复。默认不再新增可读的过期提示 Cookie；刷新调度使用响应 expires_in 与内存状态。若执行时发现另有受支持消费者，需要明确兼容窗口并为提示 Cookie 分应用命名，仍不能用于鉴权。

### 刷新与退出的最小一致性要求

- 用现有 Redis 设施承载 application、sid、uid、当前 jti、CSRF 证明、有效期及必要会话元数据，复用正常生产构造和依赖装配，不新增通用 session 服务。
- 新令牌和最新用户/租户/角色检查先准备，提交时再原子验证旧 RT、当前 sid/jti 与活动状态，并同时写入新令牌对、更新会话和删除旧对。准备失败不应提前消费旧会话。
- 保留单次使用轮换：旧 RT 重放被拒，旧 AT 按既有语义失效。旧登录时间和设备元数据须在删除前取得并随原子更新保留，不能先删后读。
- 当前 sid 的 logout 与 refresh 共享原子状态边界：logout 成功后，即便另一请求已准备好新令牌，也不能提交或复活该 sid。实现按实际 Redis 拓扑保证原子操作可用，不用进程内锁假装覆盖多实例。
- CSRF token 至少使用 32 字节密码学随机值，绑定 application 和稳定 sid，生命周期不超过会话。存储与比较走服务端可信状态；比较避免时序泄露，任何日志、监控和证据不记录其原值。bootstrap 不轮换证明，避免标签页互相破坏。
- Redis/权限仓储故障返回可区分的服务错误，不能一律转换成“用户凭证错”。失败必须保留原始内部上下文，同时脱敏公开响应。
- 本批不承诺网络响应丢失后仍可用旧 RT 无感重试；该情形保留安全失效和重新登录路径，不引入无限重放窗口或宽松 token-family 框架。
- 重复退出、RT 缺失或过期的本地清理，与服务端已确认 revoked 分开表示。只有吊销成功才能显示“服务端已退出”；Redis 故障不能返回伪 revoked 或误清另一应用。
- 现有用户禁用/删除和管理侧强制下线须继续覆盖目标用户或指定会话的新格式记录，不能因增加 application/sid 造成旧管理吊销失效。在线会话查询保持现有权限，只补充必要应用维度；这不是新增角色降权即时吊销功能。

## 6 浏览器安全和前端实现

### 来源和 CSRF

配置必须是 `application → 允许的精确 Origin 集合`，同时由 CORS、csrf bootstrap、refresh、logout 和对应登录/MFA入口使用。不能只维护包含 BOSS 与 Console 所有域名的全局集合，随后允许任一来源选择另一 application。

- 先校验 Origin；无 Origin 时只接受精确校验后的 Referer origin；两者均缺失或 Origin=null 时拒绝浏览器认证请求。校验 scheme/host/port，不能使用后缀、通配符或字符串 contains。
- CORS 凭证允许明确来源，不使用 `*`；允许实际需要的 Content-Type、Authorization、X-CSRF-Token 等头。OPTIONS 不消费会话或令牌；拒绝来源时不暴露凭据响应。
- 登录和 MFA 完成无既有已登录 CSRF token，执行来源校验、JSON Content-Type 限制、原有密码/MFA/限流约束。MFA 完成前不能签发有效 AT/RT。
- csrf bootstrap 同样要求合法应用来源、JSON 请求和有效 RT，但不要求预先有 CSRF token；它非消费性读取已有会话，返回证明。CORS 和来源校验必须阻止不可信站点读取该证明。
- refresh/logout 同时要求来源、合法应用 RT 和 X-CSRF-Token。来源错误或证明错误在消费 RT、改会话之前拒绝，HTTP 403，无副作用。
- 如果两端同 Origin，Cookie 名称和 audience 能避免正常会话覆盖，但不能隔离该 Origin 内已执行的恶意脚本。内存 AT 也不消除 XSS；保持输入输出处理和 CSP 的已有安全要求，不宣称同源应用达到互不信任隔离。

### 前端状态和恢复

前端负责人分别在两个仓库落实以下行为，并使用实际项目设施，避免再造通用认证框架：

1. 删除认证 Store/插件持久化中的 access_token、refresh_token；检查 localStorage、sessionStorage、IndexedDB、Service Worker/cache、URL、日志和错误上报。旧 `ani-platform-console-auth` 等实际持久化键只清除认证字段或本任务确认的认证键，不清其他业务设置。
2. access token、CSRF token 和当前会话状态仅存内存。收到登录/MFA成功响应后确认 application，与本前端固定用途一致才接收。
3. 页面启动进入 RESTORING 状态：向 csrf bootstrap 发送固定 application 和 credentials/include；成功取得证明后执行一次 refresh，将 AT 放进内存，再加载 me/initial-context。缺失或过期 RT 返回登录态；503 展示可重试的服务故障，不能伪装成密码错误。
4. 普通业务请求从内存注入 Bearer，使用统一的新 API 地址。csrf/refresh/logout 不走自动 Bearer 注入；刷新接口本身不能递归触发刷新。
5. 同一标签页只有一个 refresh promise。多个标签页使用以 API Origin 和 application 命名的 Web Lock，配合按应用命名的 BroadcastChannel 协调刷新结果/失效事件。AT 如跨标签页广播也只存在内存消息，不写持久存储；不跨应用广播。
6. 锁与 BroadcastChannel 只覆盖同 Origin。阶段 0 如果同一 application 会同时运行于多个不同 Origin，必须处理其共享 Cookie 的刷新竞态或限制受支持部署，不能把 Web Locks 当跨 Origin 锁。
7. 原业务请求在成功刷新后最多重发一次；只对明确的会话过期/轮换失效触发恢复。权限或套餐 403 不刷新；写请求保留原幂等键，不能自动重放未知结果的非幂等副作用。
8. logout 停止当前应用定时器与排队刷新，协调同应用标签页；服务端确认后清内存、路由上下文并广播失效。正在运行的异步成功响应使用会话代次检查，不能在 logout 后重新写入旧 AT。
9. 另一应用的 Cookie、内存状态、频道和接口调用继续有效。登录一个新账号应更新本应用会话代次，防止此前请求响应覆盖新账号。
10. 核对两端的 MFA、SSE、下载和其他凭据调用。使用 SSE 时通过受支持的 fetch/带 Header 客户端携带内存 Bearer，不将 token 放 URL；Gov 的当前 SSE 提取器支持 query token，且业务错误分支可记录原 token，需在本安全范围内禁用凭据 URL 入口并脱敏。保留已验证用户与 stream 归属检查。

## 7 路径与授权数据迁移

默认使用受控协调切换；不要求长期维护两套 path，也不依赖临时放宽权限。

### 冻结映射和代码

- 对候选 [route-map.tsv](route-map.tsv) 核对最新源码、实际装配、前端消费者、现有目录条目和权限。最终映射至少包含 method、old_path、new_path、operation、Service tag、业务模块和消费者类型。
- 只编辑手写 Proto 与配置输入；HTTP/OpenAPI 等经正式 `make api` 或本仓 `gow api` 完整生成。保留生成快照与工作树保护，不能手改生成实现或裸 Buf 生成后回退产物。
- 同步首次种子 `sql/bootstrap/001_initial.sql`、`sql/bootstrap/bootstrap.go` 的必要路径检查、受影响 `scripts/ops/sql`、部署代理、前端 URL 和接口登记。历史归档不全文替换。
- ModelDev 迁至业务域但保持 MODEL 模块；Accelerator 平台入口迁至 admin，租户入口保持业务域。共用用户/个人资料/初始化继续共用，身份和租户规则不变。

### 存量目录必须显式 rename

`sync-apis` 不识别旧地址与新地址的逻辑同一性。存量路径迁移使用可审查的显式数据步骤，放在 `sql/data` 或本仓已有受支持运维入口；不要通过重复 admin init、清表或新增一批默认授权来完成。

迁移前快照：受影响 sys_apis 的 ID/path/method/operation/tag/module/status/scope，以及 sys_permission_apis 关系。保存脱敏摘要和可恢复备份，不把完整业务 dump 提交进仓库。

事务前/事务内检查：

1. 旧 `(method,path)` 是否唯一且存在；新路径是否已被占用。
2. 映射是否一对一，有无多对一、重复、漏项、operation 或模块不一致。
3. 当前 API schema 的组合唯一约束不能替代全局 `(method,path)` 冲突检查。
4. 已在完整新状态则幂等跳过；完整旧状态才执行；混杂或未知状态拒绝。缺失记录不能自动补 ON 并假装 rename 完成。

应用时按已快照 ID、旧 method/path 与预期元数据条件 UPDATE path，每条核对影响行数。在同一事务中完成所选批次，保持原 ID、status、scope、模块和权限关系。不要覆盖同期用户/角色/套餐数据。

新增 csrf 等真正新接口单独登记：明确其认证白名单仅免 AT，由有效 RT 和来源校验保护。logout 从 Bearer/Casbin 主体变为独立 Cookie 会话退出时，登记新的安全合同，不能笼统将全部 `/api/v1/auth/*` 免鉴权。

dry-run 顺序：新版本 OpenAPI 在 rename 前应显示预期 ADD/retained 差异；完成 rename 后旧迁移项不再需要 ADD。真正新接口的新增与已授权旧接口 rename 分开处理，禁用的旧 API 不得因地址迁移变为启用。

所有 Gov 实例在应用 CLI/SQL 数据变更后按现有机制重启/刷新 Casbin。只验证一个实例不能证明多副本都已加载新策略。

### 确需兼容旧客户端时

先冻结单独的短期兼容策略：旧、新路由都必须实际注册并各自具有正确目录与原权限绑定；复用业务处理而不绕过 auth/租户/套餐。明确别名 API 的独立 ID、结束时间、访问观测和删除条件。

不要同时声称一个 API ID 对应两条 path；不要认为反向代理 rewrite 自然保证生成模板、OpenAPI、鉴权和审计一致。未设计兼容时，不进行旧新后端混跑的滚动发布。

## 8 分工和实施顺序

| 工作项 | 负责人 | 文件所有权/交付物 | 前置条件 | 完成信号 |
| --- | --- | --- | --- | --- |
| 0 合同与输入冻结 | 执行负责人 | 更新本计划、最终路由表、部署输入；明确版本与范围 | 最新 main、两前端仓库与域名 | 无影响实现的模糊 app/Origin/path/退出定义 |
| 1 最小后端会话链 | Gov 后端负责人 | auth Proto、JWT、AuthenticationService、MFA、UserTokenCache、REST安全配置/装配与相邻测试 | 0 | 真实 HTTP 登录→csrf→refresh→logout；两应用不覆盖，MFA正确继承app |
| 2 最小前端链 | 前端负责人，可分两端 | 认证 Store、拦截器、bootstrap、MFA和失效协调 | 1 的合同冻结；可与后端实现并行 | 两端AT只在内存，真实重载恢复与单次刷新 |
| 3 路由与数据迁移 | Gov 路由/数据负责人 | BFF Proto路径、种子/check、sql/data、脚本、路由合同及迁移测试 | 0；认证Proto文件与1明确分工 | 新地址、稳定API ID、原授权矩阵保持 |
| 4 生成与联调 | 集成负责人 | 正式生成输出、Gov/admin构建、独占PG/Redis+真实浏览器用例 | 1/2/3 | 完整主链和下表安全负向验收通过 |
| 5 协调发布与验收 | 发布负责人 | 审查后的版本/数据/代理切换和回滚步骤 | 4；明确发布/业务库授权 | 实际目标域名、多实例和发布版本验收 |

认证 Proto、JWT和缓存由会话负责人集中修改；路由负责人对同一 Proto 只提交已冻结的路径改动，避免两人并行覆盖。装配文件同样指定唯一合并负责人。前端两端可各自实现共用合同，不各自发明不同刷新协议。

先实现最小主链，在真实隔离环境验证之后再覆盖所有候选地址。不能先耗费多个阶段整理全部接口文档而没有任何可运行行为。

## 9 验证矩阵

默认只运行改动对应的具名行为测试与仓库明确门禁。认证装配、JWT/Redis和191条路径的变化具有实际跨模块影响，需覆盖下面受影响面；这不自动等于全仓 go test ./...。完整 API 生成是本仓明确门禁，不能用切片生成替代。

| 编号 | 必测行为 | 期望与证据 |
| --- | --- | --- |
| A01 | Console租户登录、BOSS平台登录 | 各自正确Cookie/claims，真实权限和租户检查；错误角色/租户仍拒绝 |
| A02 | 同一浏览器同一API host同时登录两端 | 两Cookie并存，两端分别调用me/initial-context；无身份跳变 |
| A03 | 登录到MFA完成 | 中间态无有效令牌；完成时application取原挑战，正确Cookie/CSRF |
| A04 | 普通页面重载 | 持久存储无AT/RT；csrf非消费读取→refresh→初始化成功 |
| A05 | 请求app、Cookie、aud、用途、sid不一致 | 拒绝，不能转换身份或消费另一应用会话 |
| A06 | AT作为RT、RT作为AT、旧缺claim令牌 | 均拒绝；其他仍有效会话保持 |
| A07 | RT轮换与重放 | 旧AT/RT失效，新对有效；CSRF/sid及登录时间保持 |
| A08 | 准备阶段用户/权限/Redis失败 | 具名错误，准备失败不提前消费；无伪成功 |
| A09 | 两个同应用标签页并发恢复/刷新 | 有界协调，不互踢；无递归刷新或无限重放 |
| A10 | refresh/logout竞争 | logout成功后sid不能留下可用后继；另一应用正常 |
| A11 | AT过期时退出、重复退出、Redis退出失败 | 可凭有效RT+CSRF退出；expired/no_active与revoked分清；失败不声称完成 |
| S01 | Cookie实际响应头与浏览器落地 | 正式Secure/HttpOnly/host-only/Lax/Path/TTL正确，旧Cookie删除且另一应用不被删除 |
| S02 | Origin/Referer/CSRF正反例 | 正确来源与证明成功；错误/缺失/null来源和错误/缺失证明403，拒绝无副作用 |
| S03 | Console来源选择boss，反向亦然 | 来源按app绑定，跨应用bootstrap/refresh/logout拒绝；同Origin残余边界记录 |
| S04 | CORS预检与凭据 | 实际测试域名成功；未知来源不暴露认证响应；OPTIONS不消费RT |
| S05 | 缓存和凭据泄露 | login/MFA/refresh/csrf及错误no-store；URL/浏览器存储/日志/证据无原始凭据 |
| S06 | SSE与下载等受影响调用 | 内存Bearer真实调用，stream归属仍拒绝；无token URL与原值日志 |
| R01 | 路由表、HTTP装配、OpenAPI | 每条目标方法模板存在、无冲突；符合最终映射；旧路径按发布策略404或限期兼容 |
| R02 | 存量API路径rename | ID/status/scope/模块/permission-api关联前后相同；禁用API仍拒绝 |
| R03 | 缺旧项、目标冲突、多对一、混合状态 | 迁移明确失败并事务回滚；不合并或自动补授权 |
| R04 | 重复执行与反向迁移 | 完整新状态幂等；在受控前提下反向恢复精确旧路径，其他业务数据不变 |
| R05 | 原有平台/租户授权矩阵 | 合法共用接口成功；原权限/套餐/跨租户拒绝保持；admin前缀不扩大平台权 |
| R06 | ModelDev/Accelerator受影响路径 | 业务模块、当前主体、RPC合同和已有拒绝语义保持 |
| R07 | 新库初始化与存量库启动 | 新种子、admin check和新路径一致；所有实例加载新Casbin策略 |
| E01 | 正式部署入口真实浏览器主链 | 实际HTTPS域名两端登录→重载→新路径调用→刷新→单端退出，记录对应版本与脱敏证据 |

单元测试可以覆盖解析和纯函数；SQLite/miniredis不能代替HTTP Set-Cookie、真实PG目录/事务、真实Redis并发和浏览器行为。已有认证SQLite测试使用用户仓储桩并明确未覆盖真实写Cookie段，不可把其结果提升为真实主链验收。

执行人先把矩阵映射到实际测试函数与命令，再开始测试；缺少现有用例则在正确业务层增加。示例命令必须从所执行版本的仓库根目录运行，且下列占位项不是已有测试名：

```bash
# 在已确认的构建/测试主机执行；保留既有GOFLAGS，不写全局配置。
GOWORK=off make api
GOWORK=off make build_only
GOWORK=off make build_admin

# 用实现后真实命名的筛选替换占位项；保存真实退出码和具名结果。
go test -mod=readonly -count=1 ./pkg/jwt -run '<本批实际JWT测试>'
go test -mod=readonly -count=1 ./pkg/middleware/auth -run '<本批实际来源和会话测试>'
go test -mod=readonly -count=1 ./app/admin/service/internal/data -run '<本批实际缓存和迁移测试>'
go test -mod=readonly -count=1 ./app/admin/service/internal/service -run '<本批实际认证与MFA测试>'
go test -mod=readonly -count=1 ./app/admin/service/internal/server -run '<本批实际HTTP路由与Cookie测试>'
```

真实Redis竞争测试按实际影响使用race/并发门禁；前端使用已有测试与浏览器设施。按分支实际CI规则完成必需门禁；不为文档修改或无新风险反复重跑已通过全量检查。skip/缓存/未执行与pass分别记录。

## 10 发布和回滚

### 默认协调切换

1. 完成源码、生成、隔离数据库/Redis和浏览器验收，得到明确的Gov、admin运维镜像、BOSS、Console、代理配置版本。
2. 用实际目标库只读检查冻结映射和冲突，备份本批目录与权限关联；准备反向映射，确认任务资源owner。
3. 在批准窗口暂停或排空旧请求，阻止旧代码在新目录上继续承接管理请求；不直接让旧、新后端混跑。
4. 执行本批显式路径rename，登记真正新增认证入口，执行新版本sync dry-run和admin check。旧缺应用claim的浏览器会话按已确认策略失效，不FLUSH Redis，不批量删除无关key。
5. 发布新Gov与两端前端、切换代理/SSE地址，刷新所有实例Casbin。两端执行一次重新登录，清旧认证存储和旧Cookie。
6. 执行E01及关键负向/目录保持检查，确认HTTP实例和前端bundle确为计划版本。任何主链失败不得宣布上线验收通过。

### 回滚条件和边界

- 路由缺失、正常身份403、API ID/授权变化、Cookie未落地、跨应用恢复身份错误、CSRF来源绕过或退出后会话复活，都触发停止流量并按配对版本回滚。
- 先停止不匹配版本的请求，按已快照ID与预期新路径反向迁移本批目录，再回退Gov/admin/前端/代理到同一套旧合同，刷新所有实例策略。不能恢复整库dump覆盖发布窗口内新增的其他业务修改。
- 如果发布期间目录、权限或路由已被其他任务修改，先核对差异；禁止盲目逆向覆盖。
- 新JWT/Redis会话格式、Cookie和旧版本解析能力必须一起考虑；回退路径不等于恢复已吊销会话。本计划默认允许重新登录，不尝试恢复原始令牌或缓存凭据。
- 回滚只处理本批自有文件、数据库行和资源。保留失败证据，分别说明数据回滚、代码回滚与业务恢复状态。

## 11 交付物和验收结论

只维护一份最终合同与一份执行结果，不创建多套状态台账。

- 更新既有接口登记 AUTH/SESSION 与相关业务条目，保持已有编号；浏览器认证合同在 `docs/contracts` 设一个权威入口并由登记链接，避免复制内容。
- 提供最终route-map、可审查数据迁移与反向映射、前置冲突检查、前后端实现与生成输出、具名测试及必要发布说明。
- 最终结果先说明真实主链跑到了哪一步，再报告Gov/两前端SHA、执行主机与实际入口、每个必需验收的pass/fail/not_verified、替身边界、旧路径兼容状态、API ID和权限保持、剩余风险。
- 后端完成但未接前端、独占环境通过但正式域名未验收、部署Ready但E01未执行，均不能称本计划整体完成。
- 继续明确 `/me` 自助角色写入风险未修复，完整权限即时收回不在本批范围。不要用本次认证加固替这些风险结项。

## 12 源码定位和外部依据

执行时按最新checkout核对行号与实现；以下是职责定位而非要求复制当前实现。

| 位置 | 本批作用 |
| --- | --- |
| `api/protos/admin/service/v1/i_authentication.proto`、`i_mfa.proto`与其他BFF Proto | 登录/认证HTTP合同、MFA功能分组、路由迁移 |
| `api/protos/authentication/service/v1/ani_auth.proto`、`user_token.proto` | application/CSRF等报文与可信payload，保留旧字段编号 |
| `app/admin/service/internal/service/authentication_service.go`、`mfa_service.go` | 登录/刷新/退出、Cookie与MFA签发 |
| `app/admin/service/internal/data/authenticator.go`、`user_token_cache.go`、`mfa_challenge_cache.go` | 令牌签发校验、逻辑会话与原子性、挑战application |
| `pkg/jwt`、`pkg/middleware/auth` | claim解析、可信Principal、JWT/AK边界和来源/CSRF处理 |
| `app/admin/service/internal/server/rest_server.go`、`sse_server.go`及`cmd/server/wiring_ent.go` | 真实装配、安全白名单边界、SSE入口；装配仅构造传参 |
| `app/admin/service/configs/server.yaml`与配置Proto/实际部署配置 | CORS、应用来源、HTTPS/代理和SSE路径 |
| `sql/bootstrap/catalog.go`、`bootstrap.go`、`001_initial.sql` | method/path目录同步、启动检查、新库种子 |
| `app/admin/service/internal/data/authorizer_provider.go`、`tenant_access_checker.go` | 路径Casbin与套餐fail-closed验收 |
| `scripts/ops/sql`、`sql/data` | 既有授权脚本、新受控路径迁移 |
| `app/admin/service/internal/service/online_session_service.go` | 现有会话查询/下线行为受新session维度影响，保持既有权限 |
| `docs/deployment.md`、`docs/interface-integration-register.md` | 发布、目录授权和合同登记 |

浏览器安全设计依据：JavaScript可读取Web Storage，故本计划将AT移出持久存储；HttpOnly/Secure/host-only与SameSite按实际域名部署一起验证。[OWASP HTML5 Security](https://cheatsheetseries.owasp.org/cheatsheets/HTML5_Security_Cheat_Sheet.html#local-storage)、[MDN Set-Cookie](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Set-Cookie)。

SameSite属于纵深防御，不能代替本计划的来源和CSRF证明；自定义头与精确来源共同用于浏览器认证写请求。[OWASP CSRF Prevention](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html)。

多标签页方案限定同Origin；Web Locks用于互斥，BroadcastChannel用于同Origin上下文通知，实际浏览器支持在阶段0核对。[MDN Web Locks](https://developer.mozilla.org/en-US/docs/Web/API/Web_Locks_API)、[MDN BroadcastChannel](https://developer.mozilla.org/en-US/docs/Web/API/Broadcast_Channel_API)。
