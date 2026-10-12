# Governance ↔ 业务服务本机联调历史记录（2026-09-20）

本文件全文保留 2026-09-20 Network 联调的环境、命令与故障背景，**所有步骤均为历史证据**。其中“现在”“本仓库要求”等措辞只指当时时点。当前工具、依赖和生成使用 [开发指南](development.md)，新部署、首管理员、套餐、升级和登录验收使用 [部署与初始化流程](deployment.md)。2026-09-21 起已移除启动自动迁移和播种，API 同步改为增量模式；旧工具安装、校验和绕行、裸生成和默认密码补种不是当前操作入口。

联调验证的业务链路（以 Network 的 GetVPC 为例）：

```text
HTTP 客户端
  → governance :7788  登录(JWT) → authn → authz(casbin) → 租户闸门(套餐白名单) → API 登记
  → mTLS gRPC → network-service :19090  GetVPC
  ← 映射回 catalog.v1.VPC 响应
```

## 0. 机器环境事实（先核对，不要假设）

| 项 | 实测值 |
|---|---|
| Go | 系统是 1.26.4，**本仓库要求 1.26.7**；用 `golang.org/dl` 包装器装 1.26.7：`go install golang.org/dl/go1.26.7@latest && go1.26.7 download`，之后所有 go 命令用 `go1.26.7` 调用，且 `GOTOOLCHAIN=local` 防止它回退到 1.26.4 |
| 私有模块 | `zhangzhe-ctrl/*` 在 GitHub 公开可取；`goproxy.cn` 对其返回 `not found`，GOPROXY 需 `proxy.golang.org` 在前或走 `direct`，见 §1.1 |
| Postgres | 机器上无常驻 PG，联调用 docker 起（见 §2）；机器自带的 `lb02-*` 容器是别的业务，别动 |
| Redis | 同上，docker 起 |
| kind 集群 | kc062（control-plane + 2 worker），kubeconfig 在 `/home/ubuntu/.kube/config`，可给需要 K8s CR watch 的服务用 |
| 工具链 | `gow`、`ent`、`buf`、protoc 全家桶都要现装，见 §1 |

## 1. 一次性环境准备

```sh
export PATH=/home/ubuntu/go/bin:/usr/local/go/bin:$PATH

# 工具
# gow 不再外部安装：本仓在用命令已接管到 tools/localdeps/gow，构建入口是 make gow（输出 tools/bin/gow）
make gow && export PATH=$PWD/tools/bin:$PATH
go1.26.7 install entgo.io/ent/cmd/ent@v0.14.6        # 与 go.mod 的 entgo 版本一致
go1.26.7 install github.com/bufbuild/buf/cmd/buf@v1.60.0
# 注意 buf 版本必须为 1.60.0：scripts/generate-network-slice.sh 会断言
# `buf --version` 等于 1.60.0，不匹配直接退出。
# 该约束是切片脚本的钉版纪律，不是 buf 的能力要求 —— 全量生成路径上旧版产物
# 实测一致，详见下方"旧版 buf 会怎样"。
# （third_party/tx7do/buf/ 里记录的 v1.57.2 是那批模块备份的下载证据，
#  不是本仓生成链的要求，两者不要混用。）
go1.26.7 install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go1.26.7 install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
go1.26.7 install github.com/go-kratos/kratos/cmd/protoc-gen-go-http/v2@v2.0.0-20260404020628-f149714c1d54
go1.26.7 install github.com/go-kratos/kratos/cmd/protoc-gen-go-errors/v2@v2.0.0-20251205160234-b9fab9a5a5ab
go1.26.7 install github.com/google/gnostic/cmd/protoc-gen-openapi@v0.7.1   # openapi 生成会缺它
# 注意 protoc-gen-openapi 版本必须为 v0.7.1：scripts/generate-network-slice.sh
# 钉的就是 v0.7.1，且 go.mod 亦为 v0.7.1。
# 装 @latest 会让脚本与文档用不同版本产出内嵌 OpenAPI。
go1.26.7 install github.com/envoyproxy/protoc-gen-validate@v1.3.3
go1.26.7 install github.com/tx7do/go-wind-toolkit/protoc-gen-go-redact@v0.0.0-20260831125122-5bb4931991b2
```

> **上面各插件版本的来源与可信度不同，不要一概当作"权威钉版"**
>
> - `buf@v1.60.0`、`protoc-gen-openapi@v0.7.1`：**有权威出处** ——
>   `scripts/generate-network-slice.sh` 自己就这么钉，`go.mod` 亦为 `v0.7.1`。
> - `protoc-gen-go@v1.36.11`、`protoc-gen-go-grpc@v1.6.2`：**有据可查** ——
>   既有生成产物的版本头直接写明（如 `api/gen/go/**/*.pb.go` 顶部的
>   `protoc-gen-go v1.36.11` 与 `protoc-gen-go-grpc v1.6.2`）。
> - `protoc-gen-go-http/v2@v2.0.0-20260404020628-f149714c1d54`、
>   `protoc-gen-go-errors/v2@v2.0.0-20251205160234-b9fab9a5a5ab`：**实测反推，无版本头**。
>   go-http 的产物版本头写的是 `protoc-gen-go-http v2.9.2`，那是**所依赖的
>   kratos 库版本**而非插件模块版本；go-errors 的产物完全没有版本头。
>   这两项是通过"逐一试装并比对生成产物差异"反推出来的。
> - `protoc-gen-validate@v1.3.3`、`protoc-gen-go-redact@v0.0.0-20260831125122…`：
>   与 `go.mod` 中的依赖版本一致。
>
> **旧版 buf 会怎样（2026-09-21 实测，非推测）**
>
> 结论：**旧版 buf 的问题不是技术不兼容，而是切片脚本自设的版本门槛。**
>
> - **全量生成路径**（`buf.gen.yaml`，即 `make api` / `make openapi`）：
>   实测 `1.50.0`、`1.57.2`、`1.60.0` 三者的产物**逐字节完全一致**
>   （`api/gen/go/**/*.go` 全部文件的 sha256 相同）。
>   旧版在这条路径上**不会报错、也不会产生额外漂移**，`buf build` 与
>   `buf lint` 亦均可正常运行。
> - **切片路径**（`scripts/generate-network-slice.sh`）：
>   脚本内有 `test "$("$BUF" --version)" = 1.60.0`，配合 `set -euo pipefail`，
>   版本不符会**立即退出**且不生成任何文件。这是唯一真正会"断开"的地方。
> - 因此 `1.60.0` 这个约束的性质是**钉版纪律**（保证生成链可复现、可追责），
>   而非 buf 本身的能力要求。不要因为"旧版也能跑"就绕过脚本的断言。

> **生成链已记录并可逐字节复现（2026-09-26 T15 实测更新）**：早期本文认为
> `api/gen/go/` 继承自上游 fork、原始工具链未记录，因而"不保证逐字节复现"，并要求在
> 全量生成后执行 `scripts/post-generate-clean.sh` 抹平噪声。该判断的前提是当时存在两套
> 互相覆盖的入口（主模板用 PATH 插件与 managed `go_package`，切片模板钉旧插件版本并带显式
> `go_package`）。T15 已把它收敛为单一已验收链：`make api` 先构建本仓 `tools/bin/gow` 与
> `tools/bin/protoc-gen-go-redact`，再委托 `gow api`（校验插件/输入 → 隔离暂存副本生成 →
> 按受管清单写回），并在暂存副本内先执行 `scripts/finalize-aksk-openapi.py` 这一既有后处理（与 `make openapi` 同一脚本、同一规则，因此无需再补命令）；插件与版本记录在
> `tools/config/tool-lock.json`，PGV 范围记录在 `tools/config/pgv-scope.json`。
> 实测在干净检出上执行两遍，受管产物与已提交内容逐字节一致、`go.mod`/`go.sum` 不变。
> 因此 `post-generate-clean.sh` 已退役（现拒绝执行且不改任何文件）：若再次生成出现漂移，
> 按缺陷排查，不要在生成之后还原或删除产物。
>
> **写回与身份核对的实际含义（2026-09-26 独立复核 F2–F5 修正）**：
> - 工具区分**三棵树**：生成开始时的快照、暂存副本输出、写回前的工作树。比较只针对快照；
>   写回前再逐一核对目标当前内容与快照一致，不一致即为冲突——**保留用户内容并报错**，整批不写。
>   生成期间被改动的**手写**文件（如脱敏接管包内的 `interface.go`）不属于写回集合，任何情况下都不会被覆盖。
> - 写回集合只含本链能产出的文件类别（`*.pb.go`、`*.pb.validate.go`、`*.pb.redact.go`
>   与 `app/admin/service/cmd/server/assets/openapi.yaml`）；输出目录内的其他文件一律视为手写，
>   既不写回，也不因其未被重产而报"丢失"。
> - 写入前检查全部目标的路径与类型冲突（目标存在但为目录或符号链接会被拒绝，且**尚未写任何文件**）；
>   中途失败则恢复本次替换的文件、删除本次新增的文件。若回滚本身失败，会**单独报告**并保留备份目录，
>   不宣称工作树未变；回滚不覆盖其后出现的用户修改。这只是一次写回的有限撤销，**不承诺**断电或
>   kill 下的整目录原子性，也不是通用事务机制。
> - 全链路运行会先把暂存副本里生成类别的文件全部清掉，因此"复现"必须真正重建：已批准清单中的产物
>   缺失即失败；清单外 validator 无论新增还是已存在被改写都拒绝；`tools/config/pgv-scope.json`
>   缺失、损坏或为空是硬失败（不再是"没有规则"）。部分切片只核对自己写回的部分，不要求产出 103 项。
> - 插件仅"存在"已不足以通过：PATH 插件与 `buf` 都用 `go version -m` 读构建信息，对照
>   `tools/config/tool-lock.json` 的模块与版本；读不到就明确失败，`--version`
>   输出不作为身份依据。本仓自有的 redact 插件每次生成都经 `scripts/build-redact-plugin.sh`
>   从当前源码构建一次（Go 构建缓存承担重复成本；按插件计数，不按模板），并校验其 buildinfo 的
>   模块与包路径属本仓且无 tx7do 依赖。各模板已批准的不同钉版保持原样：不统一、不安装 `@latest`、
>   不改动用户全局工具。

### 1.1 私有模块校验和不匹配（已定位根因，2026-09-21）

`go mod download` 报 `SECURITY ERROR ... checksum mismatch`（如
`zhangzhe-ctrl/ani-network-service`）。

**根因（已实测定案）**：不是上游代码差异，而是历史上用 file-GOPROXY 交付的
模块 zip **丢掉了点文件**（`.gitignore`、`.gitattributes` 等，network 还丢了
`.github/workflows/ci.yml` 与 `.gitleaks.toml`）；共有文件内容逐字节一致。
Go 的 `h1:` 是整棵目录树的 dirhash，少一个文件即变。上游仓库在 GitHub 上是
好的，直接从公共源取即可。

**现在的正确做法**：直接 `go get` 上游固定版本，`GOPROXY` 把
`proxy.golang.org` 放在前面或走 `direct`（`goproxy.cn` 对这些模块返回
`not found`，不要把它排在最前）。**不要再用自建 file-GOPROXY 打包交付**。

**遗留环境的临时绕行**（仅当仍持有旧交付且无法联网核对时）：把 go.sum 里
`zhangzhe-ctrl/*` 对应行删掉，重跑下载，让它按 proxy 返回值重建：

```sh
export GOFLAGS=-mod=mod GOPROXY=https://proxy.golang.org,https://goproxy.cn,direct \
       GOTOOLCHAIN=local
go1.26.7 mod tidy && go1.26.7 mod download all
```

### 1.2 代码生成

```sh
cd app/admin/service
ent generate --feature privacy --feature entql --feature sql/modifier \
             --feature sql/upsert --feature sql/lock ./internal/data/ent/schema
cd ../../api && buf generate                              # Go API
buf generate --template buf.admin.openapi.gen.yaml        # OpenAPI（会嵌进 assets）
```

**注意**：ent 生成前 `internal/data/ent/entql.go` 的 `Nodes` 切片长度必须
与实际 schema 数一致（手工改过 schema 后生成器会以现有 entql.go 为输入做
一致性检查，长度不对直接 panic）。正常流程"改 schema → 生成"不会踩到；
只有"手工删过生成代码"时才会，重跑一遍生成即可自愈。

## 2. 基础设施容器

```sh
# PG（镜像与 ani-network-service 集成测试锁定同一 digest）
docker run -d --name gov-int-pg --memory=512m --cpus=1 \
  -e POSTGRES_PASSWORD=govint -p 127.0.0.1:25432:5432 \
  docker.io/library/postgres@sha256:4ef4dbc939d61acea57712655ddb4b4ab27419c913f94cca0cd57cb3ea3c2280

docker run -d --name gov-int-redis --memory=256m -p 127.0.0.1:26379:6379 redis:7-alpine

# 库
docker exec gov-int-pg psql -U postgres -c "CREATE DATABASE gwa;"
docker exec gov-int-pg psql -U postgres -c "CREATE DATABASE network;"
docker exec gov-int-pg psql -U postgres -c "CREATE ROLE owner LOGIN PASSWORD 'owner' NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;"
docker exec gov-int-pg psql -U postgres -d network -c "GRANT USAGE ON SCHEMA public TO owner; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO owner;"
```

## 3. network-service 启动（门槛最多的一侧）

配置文件直接用仓库 `configs/config.yaml`。**环境变量必须带 `ANI_` 前缀**
——它用 `env.NewSource("ANI")` 覆盖配置，所以 `NETWORK_DATABASE_DSN` 是
错的，正确的是 `ANI_NETWORK_DATABASE_DSN`（kratos env source 的语义是
`<前缀>_<配置路径>`，路径里的层级用 `_` 连接）。

```sh
# 迁移（一次性）：runtime role 必须是受限的 owner，不能用 postgres 超管
export ANI_NETWORK_MIGRATION_DSN="postgres://postgres:govint@127.0.0.1:25432/network?sslmode=disable"
export ANI_NETWORK_RUNTIME_ROLE=owner
./ani-network-service -conf <配置目录> -migrate

# 运行
export ANI_NETWORK_DATABASE_DSN="postgres://owner:owner@127.0.0.1:25432/network?sslmode=disable"
export ANI_NETWORK_KUBECONFIG=/home/ubuntu/.kube/config     # 启动强制要 kubeconfig
export ANI_NETWORK_CLUSTER_ID=test-cluster
export ANI_NETWORK_NAMESPACE_PREFIX=tenant-
# 必须是 base64、解码后 32..128 字节；"随便一串明文"会启动失败
export ANI_NETWORK_CURSOR_SIGNING_KEY="dGVzdC1zaWduaW5nLWtleS0wMTIzNDU2Nzg5YWJjZGVmMDEyMzQ1Njc4OWFiY2RlZg=="
./ani-network-service -conf <配置目录>
```

启动失败速查（都实测踩过）：

| 报错 | 原因与解法 |
|---|---|
| `migration requires ANI_NETWORK_MIGRATION_DSN and ...` | `-migrate` 模式必须显式给迁移 DSN 和 runtime role |
| `role "owner" does not exist` | 先建角色再迁移 |
| `runtime database role has administrative privileges` | 运行时 DSN 用了超管，或 owner 还有 CREATEDB/CREATE 权限（启动时会查 `pg_roles` + schema/db 权限，逐项 REVOKE） |
| `Kubernetes credentials/configuration could not be loaded` | 没给 `ANI_NETWORK_KUBECONFIG`，指向 `/home/ubuntu/.kube/config` 即可 |
| `cursor_signing_key must encode 32..128 bytes as base64` | key 必须先 base64，不能传明文 |
| `Network database, placement and worker config are required` | 环境变量前缀用错了（`NETWORK_` → 应为 `ANI_NETWORK_`），没覆盖到 |

## 4. governance 启动

```sh
# 数据库/redis 指向容器：改 data.yaml（database.source、redis.addr），
# server.yaml 里 rest.addr 改 127.0.0.1:7788、asynq.uri 改 redis://127.0.0.1:26379/1
#   注意：-c 指定的目录下只允许 yaml，多余的文件（如 ca.pem）会导致
#   "failed to merge config source: unsupported key: ca.pem format: pem" 直接 panic
export ANI_NETWORK_ADDR=127.0.0.1:19090
export ANI_NETWORK_CA=... ANI_NETWORK_CERT=... ANI_NETWORK_KEY=...
./ani-governance -c <配置目录>
```

**已知环境限制**：`NetworkClient` 强制 mTLS（见
[network_client.go](../app/admin/service/internal/data/network_client.go)，
证书必须有 DNS SAN `ani-governance`，ServerName 固定 `ani-network-service`）。
本地裸跑的 network-service 是明文 gRPC，直连会报
`first record does not look like a TLS handshake`。生产无此问题：TLS 由
cert-manager + 集群内网关统一承载（见
[scripts/deploy/governance-mtls/](../scripts/deploy/governance-mtls/)）。
本机联调二选一：

- 接受业务链路在"明文直连下游"下验证（mTLS 层交给部署环境）；
- 或给 network-service 前面加一个 TLS 终止代理（stunnel/envoy），把
  `ANI_NETWORK_*` 指向代理端口。

## 5. 联调数据种子（最小集，全部 SQL 实测）

governance 的闸门是层层串联的，**每一层都要有数据**，缺一层就是一个
不同的 403/400。按请求实际穿越顺序：

1. **登录免不了验证码**：`CaptchaEnabled=true` 是硬编码（H5 闸门），
   验证码答案存在 Redis `gowind:captcha:<id>`，联调脚本里直接
   `GET /api/v1/auth/captcha` 拿 id 后从 Redis 读答案再带
   `X-Captcha-Id`/`X-Captcha-Value` 头登录。登录密码是
   `base64(AES-CBC(明文, DefaultAESKey, iv=key))`，`DefaultAESKey` 在
   tx7do/go-utils/crypto 里，明文 `Abcd@1234` 加密后是
   `5iATn4qtWi3ej5hNxxBcEA==`（ DefaultAESKey 实测值见
   `pkg/constants` 的 DefaultUserPassword 流程）。
2. **租户**：`sys_tenants` 里造一行（`resource_tenant_id` 是 Network 侧
   的租户 UUID，governance 直接透传给下游），`status='ON'`。
3. **租户用户**：`sys_users`（status 用 `'NORMAL'`，不是 'ON'！这是
   proto 枚举名）+ `sys_user_credentials`（PASSWORD_HASH 的值是 bcrypt，
   直接复用 admin 行的哈希最省事）+ `sys_memberships`（ACTIVE）。
4. **角色**：造一个**非 template:** 前缀的租户角色（template 角色会被
   策略装载器跳过），绑 `sys:access_backend` 权限（登录授权要求）+
   目标 API 的权限；`sys_user_roles`/`sys_membership_roles` 绑到用户。
   **改完角色必须重启 governance**——策略只在启动时装载
   （日志 `reloaded policy rules [N]`，N 应包含你新增的行）。
5. **套餐**：`sys_plans` 一行；`sys_tenants.plan_id`（外键列）指过去；
   `sys_plan_modules` 插 `(plan_id, 'NETWORK')`。只写
   `subscription_plan` 字符串没用，闸门读的是 plan 外键 + 模块白名单。
6. **API 登记**：`sys_apis` 插目标路由（path/method/module/
   business_module='NETWORK'）。参照
   [scripts/ops/sql/bootstrap-network-access.sql](../scripts/ops/sql/bootstrap-network-access.sql)。
   这一步漏了就是"access denied"，一步对齐一层 403 就会消失。
7. **租户登录必须带 `tenant_name`**（body 里，取值 `sys_tenants.code`），
   端点为 `POST /api/v1/auth/password/login`；平台管理员走
   `POST /api/v1/auth/platform/password/login`（报文不含 `tenant_name`）。
   两者已拆为独立端点：租户端点漏传 `tenant_name` 直接 400，
   **不会**静默降级为平台登录。验证码仍经 `X-Captcha-Id`/`X-Captcha-Value` 请求头传递。

## 6. 验证命令（逐层收敛）

```sh
# ① 未登录 → 401 UNAUTHORIZED（闸门存在性验证）
curl -s -o /dev/null -w "%{http_code}\n" \
  http://127.0.0.1:7788/api/v1/networks/vpcs/vpc_0123456789abcdef0123456789abcdef
# 期望：401

# ② 登录（§5 流程）拿 access_token

# ③ 带非法参数 → 400（业务校验生效）
curl -s "http://127.0.0.1:7788/api/v1/networks/vpcs/bad_id?x=1" -H "Authorization: Bearer $TOKEN"
# 期望：400 INVALID_VPC_ID / INVALID_QUERY

# ④ 全链路 → 下游真实响应
curl -s "http://127.0.0.1:7788/api/v1/networks/vpcs/vpc_0123456789abcdef0123456789abcdef" \
  -H "Authorization: Bearer $TOKEN"
# 期望：404 VPC_NOT_FOUND（库中无此 VPC，说明请求已穿过 governance 全部
# 闸门、经 mTLS 客户端真实到达 network-service 并带回了业务结果）

# ⑤ 对照：绕过 governance 直连下游（明文），排除下游本身故障
./getvpc vpc_0123456789abcdef0123456789abcdef
# 期望：NotFound（与 ④ 语义一致，证明 403/503 差异来自 governance 层）

# ⑥ 下游单测兜底（有真实 PG 的完整集成套件，110s 左右）
cd ~/Workspace/ani-network-service && ./scripts/integration ./...
```

⑥ 通过 + ④ 返回 404 + ① ② ③ 语义正确，即可认定链路健康。
mTLS 段在生产部署由集群内证书体系保证（§4 的环境限制）。

## 7. 收尾

```sh
docker rm -f gov-int-pg gov-int-redis
pkill -f gov-int/ani-governance; pkill -f gov-int/ani-network-service
```

临时库、测试租户数据都只存在于上述容器/数据库里，不污染任何真实环境。
