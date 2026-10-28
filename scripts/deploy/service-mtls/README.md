# 服务证书续期后的进程加载

本目录保存 `ani-system` 现有证书重载 Job 的配置和源码。cert-manager 更新
Secret 后，已经启动的进程需要重新加载证书；Job 每两分钟比较根信任与已登记
叶证书的摘要，只在变化时修改对应工作负载的 Pod 模板注解以触发滚动重启。
无变化不重启。它不签发证书，不轮换根 CA，也不修改业务配置或应用镜像。

`ani-inference-gateway-authz-mtls` 的客户端身份为精确 DNS
`ani-inference-gateway.ani.internal`，由 Inference Chart 的 cert-manager
Certificate 管理。该 Secret 同时登记于 Role 的 `resourceNames` 和
`ani-inference-service` 的 bindings；续期后，同一 Pod 的 Inference 与鉴权
companion 一起重新加载，继续按[服务间调用合同](../../../docs/contracts/service-to-service-calls.md)
建立 mTLS 连接。

清单保留现有其他工作负载的绑定与权限。跨环境应用前须核对 namespace、工作负载、
证书 Secret、`ConfigMap/ani-internal-service-ca` 和固定镜像引用；不要将现场快照
当作通用 Helm values。修改登记时同时检查 bindings 和 Role 的精确资源名，
保留其他工作负载的配置。

镜像使用 `Reload.Dockerfile`、`reload.sh` 和经校验的固定版本 kubectl 二进制
构建，部署清单固定镜像 digest。只有公开叶证书和根信任参与摘要，日志不输出证书
或私钥内容。Role 仅允许读取登记的 Secret、信任 ConfigMap，以及 get/patch
登记的 Deployment/DaemonSet。

使用明确指定的环境应用，经授权的真实续期可通过 cmctl 发起：

```sh
kubectl --context "$ANI_TARGET_CONTEXT" apply -f scripts/deploy/service-mtls/reload-cronjob.yaml
cmctl --context "$ANI_TARGET_CONTEXT" -n ani-system renew ani-inference-gateway-authz-mtls
```

验收须确认 Certificate revision 与叶证书序列变化、Job 成功、对应 Pod 更新完成，
并继续通过有效 Key 的真实推理及无效 Key 拒绝。无变化的后续 Job 不应重复重启。
Secret 更新和 Pod Ready 不能单独代替 mTLS 及业务验收。
