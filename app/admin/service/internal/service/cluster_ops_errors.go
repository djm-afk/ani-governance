package service

import (
	"context"
	stderrors "errors"

	"github.com/go-kratos/kratos/v2/errors"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type clusterOpsErrorPolicy struct {
	code    int
	message string
}

// Only stable domain reasons have a public meaning. Never use a raw gRPC
// message (or a log frame's message), which could contain credentials or bodies.
var clusterOpsErrorPolicies = map[string]clusterOpsErrorPolicy{
	"INVALID_CLUSTEROPS_REQUEST":            {400, "集群运维请求无效"},
	"INVALID_ARGUMENT":                      {400, "请求参数无效"},
	"SYSTEM_FIELD_READ_ONLY":                {400, "系统字段不能修改"},
	"KUBERNETES_INVALID":                    {400, "Kubernetes 校验未通过"},
	"POD_CONTAINER_REQUIRED":                {400, "请选择要查看日志的容器"},
	"CLUSTEROPS_REQUEST_TOO_LARGE":          {413, "请求正文超过传输上限"},
	"RESOURCE_READ_ONLY":                    {403, "本期仅支持查看该类型"},
	"TENANT_NAMESPACE_READ_ONLY":            {403, "租户命名空间资源，本入口只读"},
	"KUBERNETES_FORBIDDEN":                  {403, "Kubernetes 拒绝此操作"},
	"CLUSTER_NOT_CONFIGURED":                {404, "集群未配置"},
	"RESOURCE_NOT_FOUND":                    {404, "资源不存在"},
	"RESOURCE_ALREADY_EXISTS":               {409, "资源已存在"},
	"RESOURCE_UID_CHANGED":                  {409, "资源身份已变化，请刷新目标后确认"},
	"RESOURCE_VERSION_CONFLICT":             {409, "资源版本冲突，请保留草稿并刷新目标后确认"},
	"POD_CHANGED":                           {409, "Pod 已被替换，请重新选择"},
	"RESOURCE_TYPE_UNAVAILABLE":             {409, "资源类型当前不可用"},
	"ACTION_NOT_SUPPORTED":                  {409, "该资源不支持此操作"},
	"RESTART_STRATEGY_UNSUPPORTED":          {409, "当前工作负载策略不支持重启"},
	"CRD_HAS_PROTECTED_NAMESPACE_INSTANCES": {409, "该定义在受保护命名空间中仍有实例"},
	"CRD_INSPECTION_FAILED":                 {409, "无法完成 CRD 删除检查"},
	"LIST_EXPIRED":                          {409, "列表快照已过期，请刷新"},
	"RESOURCE_EXHAUSTED":                    {429, "请求已受限"},
	"KUBERNETES_UNAUTHORIZED":               {502, "下游 Kubernetes 凭据不可用"},
	"PLATFORM_SCOPE_REQUIRED":               {502, "下游平台委托配置不可用"},
	"NAMESPACE_POLICY_UNAVAILABLE":          {503, "租户命名空间登记暂不可用"},
	"KUBERNETES_UNAVAILABLE":                {503, "Kubernetes 暂不可用"},
	"LOG_READ_FAILED":                       {503, "Pod 日志读取失败"},
	"LOG_SEND_TIMEOUT":                      {504, "日志发送超时"},
	"DEADLINE_EXCEEDED":                     {504, "请求超时，结果尚未确认，请刷新资源查看"},
	"INTERNAL_ERROR":                        {500, "集群运维服务内部错误"},
	"CLUSTEROPS_UNAVAILABLE":                {503, "集群运维接入或部署版本尚未就绪"},
	"CLUSTEROPS_UPSTREAM_AUTH_FAILED":       {502, "下游服务认证失败"},
	"LOG_STREAM_INTERRUPTED":                {503, "日志流意外中断，请手动重新连接"},
	"LOG_STREAM_PROTOCOL_ERROR":             {502, "日志流协议异常"},
	"CLUSTEROPS_UPSTREAM_ERROR":             {502, "集群运维下游请求失败"},
}

var clusterOpsPublicMetadata = map[string]bool{
	"cluster_id": true, "group": true, "version": true, "resource": true,
	"namespace": true, "name": true, "kubernetes_reason": true,
	"kubernetes_http_status": true, "field_paths": true,
}

func clusterOpsPublicError(reason string, metadata map[string]string) error {
	policy, ok := clusterOpsErrorPolicies[reason]
	if !ok {
		reason = "CLUSTEROPS_UPSTREAM_ERROR"
		policy = clusterOpsErrorPolicies[reason]
	}
	public := make(map[string]string)
	for key, value := range metadata {
		if clusterOpsPublicMetadata[key] {
			public[key] = value
		}
	}
	return errors.New(policy.code, reason, policy.message).WithMetadata(public)
}

func mapClusterOpsError(err error) error {
	if err == nil {
		return nil
	}
	if stderrors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return context.Canceled
	}
	if stderrors.Is(err, context.DeadlineExceeded) {
		return clusterOpsPublicError("DEADLINE_EXCEEDED", nil)
	}
	rpc, ok := status.FromError(err)
	if !ok {
		return clusterOpsPublicError("CLUSTEROPS_UPSTREAM_ERROR", nil)
	}
	var domainInfo *errdetails.ErrorInfo
	for _, detail := range rpc.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if !ok || info.Domain != "clusterops.ani.io" {
			continue
		}
		if domainInfo != nil {
			return clusterOpsPublicError("CLUSTEROPS_UPSTREAM_ERROR", nil)
		}
		domainInfo = info
	}
	if domainInfo != nil {
		return clusterOpsPublicError(domainInfo.Reason, domainInfo.Metadata)
	}
	switch rpc.Code() {
	case codes.Unimplemented:
		return clusterOpsPublicError("CLUSTEROPS_UNAVAILABLE", nil)
	case codes.Unauthenticated, codes.PermissionDenied:
		return clusterOpsPublicError("CLUSTEROPS_UPSTREAM_AUTH_FAILED", nil)
	case codes.DeadlineExceeded:
		return clusterOpsPublicError("DEADLINE_EXCEEDED", nil)
	default:
		// Without trusted domain information, ResourceExhausted may be a
		// transport receive limit rather than Kubernetes/business throttling.
		return clusterOpsPublicError("CLUSTEROPS_UPSTREAM_ERROR", nil)
	}
}

// MapClusterOpsError is shared with the single HTTP log writer after READY.
func MapClusterOpsError(err error) error {
	return mapClusterOpsError(err)
}

// ClusterOpsLogFrameError maps a Res ERROR frame without exposing its message.
func ClusterOpsLogFrameError(reason string) error {
	return clusterOpsPublicError(reason, nil)
}
