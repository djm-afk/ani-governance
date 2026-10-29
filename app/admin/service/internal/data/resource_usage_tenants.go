package data

import (
	"context"
	"go-wind-admin/app/admin/service/internal/data/ent/tenant"
)

type ResourceUsageTenant struct {
	ID               uint32
	Name             string
	ResourceTenantID string
}

// ResourceUsageTenants snapshots persisted identities once for the whole
// statistics request. Inactive tenants remain visible for historical usage.
func (r *TenantRepo) ResourceUsageTenants(ctx context.Context, onlyID uint32) ([]ResourceUsageTenant, error) {
	query := r.entClient.Client().Tenant.Query().Order(tenant.ByID()).Limit(129)
	if onlyID != 0 {
		query.Where(tenant.IDEQ(onlyID))
	}
	rows, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ResourceUsageTenant, 0, len(rows))
	for _, row := range rows {
		if row.ID != 0 {
			name := ""
			if row.Name != nil {
				name = *row.Name
			}
			out = append(out, ResourceUsageTenant{ID: row.ID, Name: name, ResourceTenantID: row.ResourceTenantID})
		}
	}
	return out, nil
}
