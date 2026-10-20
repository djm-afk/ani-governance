package service

import (
	"context"
	stderrors "errors"
	"github.com/go-kratos/kratos/v2/errors"
	res "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
)

type InstanceCatalogConfig struct {
	Monitoring InstanceMonitoringClient
	ClusterID  string
	Cluster    InstanceSchedulingCluster
	Settings   *InstanceSchedulingPolicy
}

// InstanceSchedulingCluster accesses the one deployment-managed cluster.
type InstanceSchedulingCluster interface {
	ReadSchedulerConfig(context.Context) (string, string, error)
	WriteSchedulerConfig(context.Context, string, string) (string, string, error)
	PriorityClassExists(context.Context, string) error
}

func instanceBusinessError(err error) error {
	reason := "INVALID_INSTANCE_REQUEST"
	if stderrors.Is(err, res.ErrReservedConfig) {
		reason = "INSTANCE_RESERVED_CONFIG"
	}
	if stderrors.Is(err, res.ErrInvalidScheduling) {
		reason = "INVALID_SCHEDULING_OPTIONS"
	}
	if stderrors.Is(err, res.ErrSchedulingConflict) {
		reason = "SCHEDULING_FIELD_CONFLICT"
	}
	return errors.BadRequest(reason, "invalid frozen instance business")
}
