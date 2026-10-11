# 加密与签名工具

本包提供 AES-256-GCM 字符串加密、显式 payload 包装、HMAC 签名和独立的 Access Key 密钥加密器。实际使用范围以调用方源码为准。

## 可选的全局加密器

服务入口 [main.go](../../app/admin/service/cmd/server/main.go) 从 `GOWIND_CRYPTO_KEY` 读取非空密钥并调用 `InitGlobalEncryptor`。未配置时，`EncryptIfNeeded` 返回明文，`DecryptIfNeeded` 原样返回输入；这时 SMTP 密码和 TOTP secret 没有获得本包的静态加密保护。

当前真实业务调用位于 [NotificationChannelRepo](../../app/admin/service/internal/data/notification_channel_repo.go) 的 SMTP 密码读写和 [UserMfaFactorRepo](../../app/admin/service/internal/data/user_mfa_factor_repo.go) 的 TOTP secret 读写。Task schema、Repository 和 Service 没有自动加解密的 Ent hook；`EncryptPayload` / `DecryptPayload` 是需显式调用的工具，不能由它们的存在推断任务已经加密。

全局初始化使用 `sync.Once`，没有运行时换钥接口。密钥变更后的历史密文处理需单独设计；本包没有自动迁移或双密钥轮换流程。

## 格式与错误

[NewEncryptor](aes_gcm.go) 拒绝空字符串，对非空输入做 SHA-256 得到 AES-256 密钥；当前实现不检查输入是否达到 32 字节。密文为 `enc:` 加标准 Base64，编码内容依次为随机 12 字节 nonce、密文和 16 字节 GCM tag。

启用加密器后，`Decrypt` 对无 `enc:` 前缀的历史明文原样返回；Base64 损坏、密文截断或认证失败返回错误。SMTP/TOTP 调用方收到解密错误会终止对应操作。`IsEncrypted` 只检查前缀，不能证明密文有效。

[payload.go](payload.go) 仅在实际加密后设置 `_is_encrypted=true`。[SignData / VerifyData](hmac.go) 使用全局密钥计算 HMAC-SHA256；未初始化密钥时，签名返回错误，验证返回 false。签名有效期由调用方负责。

## 独立的 Access Key 加密器

[AccessKeyCipher](access_key.go) 不使用可选全局加密器。主服务装配 [wiring_ent.go](../../app/admin/service/cmd/server/wiring_ent.go) 必须加载 `ANI_ACCESS_KEY_ENCRYPTION_KEY_FILE`；文件内容为恰好 64 个十六进制字符，允许末尾一个换行。配置缺失、文件不可读或格式错误会使装配失败。

所有服务副本须加载同一密钥。解密要求 `enc:` 密文且明文符合 `sk-` 前缀；`DeriveSubkey` 按 label 派生用途隔离的子密钥，当前分页游标签名也使用它。更换主密钥会影响已保存的 SK 密文和派生用途，不能只重启服务就宣称轮换完成。

## 验证与来源

现有用例位于 [aes_gcm_test.go](aes_gcm_test.go)、[encryptor_test.go](encryptor_test.go)、[payload_test.go](payload_test.go)、[payload_must_test.go](payload_must_test.go)、[hmac_test.go](hmac_test.go) 和 [access_key_test.go](access_key_test.go)。包测试入口为仓库根目录的 `go test ./pkg/crypto`；包用例不代替业务存储或部署验收。

本包属于 ANI Governance，保留技术模块名 `go-wind-admin`。项目来源、原许可和接管边界见 [THIRD_PARTY_NOTICES.md](../../THIRD_PARTY_NOTICES.md)。
