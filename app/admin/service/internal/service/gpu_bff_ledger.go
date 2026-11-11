package service

import (
	"context"
	gpu "github.com/zhangzhe-ctrl/ani-observability-service/api/observability/gpu/v1"
	"go-wind-admin/app/admin/service/internal/data"
)

// GpuBffLedgerBridge reads immutable original business identity from Gov.
type GpuBffLedgerBridge struct{ ledger *data.QuotaLedgerRepo }

func NewGpuBffLedgerBridge(ledger *data.QuotaLedgerRepo) *GpuBffLedgerBridge {
	return &GpuBffLedgerBridge{ledger: ledger}
}
func (s *GpuBffLedgerBridge) ResolveGpuUsageRef(ctx context.Context, tenant uint32, owner, resource string) (*GpuUsageRef, error) {
	return resolveGpuUsageRef(ctx, s.ledger, tenant, owner, resource)
}

// UsageRef belongs to the original business identity, independent of ACC inventory.
type GpuUsageRef = gpu.UsageRef
