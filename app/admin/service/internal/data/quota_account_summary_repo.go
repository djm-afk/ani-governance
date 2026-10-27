package data

import (
	"context"
	"fmt"
	"strconv"

	quotapb "go-wind-admin/api/gen/go/quota/service/v1"
	"go-wind-admin/app/admin/service/internal/data/ent"
	"go-wind-admin/app/admin/service/internal/data/ent/plan"
	"go-wind-admin/app/admin/service/internal/data/ent/planquota"
	"go-wind-admin/app/admin/service/internal/data/ent/quotaaccount"
	"go-wind-admin/app/admin/service/internal/data/ent/quotadefinition"
	"go-wind-admin/app/admin/service/internal/data/ent/tenant"
	appViewer "go-wind-admin/pkg/entgo/viewer"
)

type TenantQuotaAccountSummaryFilter struct {
	Page, PageSize uint32
	TenantID       *uint32
	QuotaCode      string
}

// ListTenantAccountSummaries is a platform-only read projection. Authorization
// precedes this cross-tenant repository. Query count is bounded independently of
// page size: definitions, count, tenant page, plans, policies and accounts.
func (r *QuotaAdminRepo) ListTenantAccountSummaries(ctx context.Context, filter TenantQuotaAccountSummaryFilter) (items []*quotapb.TenantQuotaAccountSummary, total uint64, err error) {
	if filter.Page == 0 || filter.PageSize == 0 || filter.PageSize > 100 || (filter.TenantID != nil && *filter.TenantID == 0) {
		return nil, 0, quotapb.ErrorInvalidQuotaRequest("%s", "invalid quota account page or tenant")
	}
	items = []*quotapb.TenantQuotaAccountSummary{}
	err = quotaReadSnapshot(ctx, r.entClient.Client(), func(tx *ent.Tx) error {
		ctx := appViewer.NewSystemViewerContext(ctx)
		defs, e := quotaSummaryDefinitions(ctx, tx, filter.QuotaCode)
		if e != nil {
			return e
		}
		query := tx.Tenant.Query().Where(tenant.IDGT(0), tenant.DeletedAtIsNil())
		if filter.TenantID != nil {
			query.Where(tenant.IDEQ(*filter.TenantID))
		}
		count, e := query.Clone().Count(ctx)
		if e != nil {
			return e
		}
		total = uint64(count)
		offset := (uint64(filter.Page) - 1) * uint64(filter.PageSize)
		if offset >= total {
			return nil
		}
		tenants, e := query.Order(ent.Asc(tenant.FieldID)).Offset(int(offset)).Limit(int(filter.PageSize)).All(ctx)
		if e != nil {
			return e
		}
		items, e = r.quotaAccountSummaries(ctx, tx, tenants, defs)
		return e
	})
	return
}

func quotaSummaryDefinitions(ctx context.Context, tx *ent.Tx, code string) ([]*ent.QuotaDefinition, error) {
	query := tx.QuotaDefinition.Query().Order(ent.Asc(quotadefinition.FieldCode))
	if code != "" {
		query.Where(quotadefinition.CodeEQ(code))
	}
	defs, err := query.All(ctx)
	if err == nil && code != "" && len(defs) == 0 {
		return nil, quotapb.ErrorInvalidQuotaRequest("%s", "unknown quota code")
	}
	return defs, err
}

// quotaAccountSummaries is also used by the existing single-tenant routes so
// configured, balance and enforcement cannot drift between public projections.
func (r *QuotaAdminRepo) quotaAccountSummaries(ctx context.Context, tx *ent.Tx, tenants []*ent.Tenant, defs []*ent.QuotaDefinition) ([]*quotapb.TenantQuotaAccountSummary, error) {
	items := make([]*quotapb.TenantQuotaAccountSummary, 0, len(tenants))
	if len(tenants) == 0 {
		return items, nil
	}
	tenantIDs := make([]uint32, 0, len(tenants))
	planIDs := make([]uint32, 0, len(tenants))
	codes := make([]string, 0, len(defs))
	for _, d := range defs {
		codes = append(codes, d.Code)
	}
	seenPlan := map[uint32]bool{}
	for _, t := range tenants {
		tenantIDs = append(tenantIDs, t.ID)
		if t.PlanID != nil && !seenPlan[*t.PlanID] {
			planIDs = append(planIDs, *t.PlanID)
			seenPlan[*t.PlanID] = true
		}
	}
	plans := map[uint32]*ent.Plan{}
	if len(planIDs) != 0 {
		rows, err := tx.Plan.Query().Where(plan.IDIn(planIDs...)).WithQuotas(func(q *ent.PlanQuotaQuery) {
			q.Where(planquota.QuotaCodeIn(codes...))
		}).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range rows {
			plans[p.ID] = p
		}
	}
	rows, err := tx.QuotaAccount.Query().Where(quotaaccount.TenantIDIn(tenantIDs...), quotaaccount.QuotaCodeIn(codes...)).All(ctx)
	if err != nil {
		return nil, err
	}
	occupied := map[uint32]map[string]uint64{}
	for _, a := range rows {
		if a.TenantID == nil || a.OccupiedUnits < 0 {
			return nil, fmt.Errorf("invalid quota account numeric projection")
		}
		if occupied[*a.TenantID] == nil {
			occupied[*a.TenantID] = map[string]uint64{}
		}
		occupied[*a.TenantID][a.QuotaCode] = uint64(a.OccupiedUnits)
	}
	for _, t := range tenants {
		summary := &quotapb.TenantQuotaAccountSummary{TenantId: t.ID, PlanId: t.PlanID, Accounts: []*quotapb.QuotaAccountView{}}
		if t.Name != nil {
			summary.TenantName = *t.Name
		}
		if t.Code != nil {
			summary.TenantCode = *t.Code
		}
		limits := map[string]uint64{}
		if t.PlanID != nil && plans[*t.PlanID] != nil {
			p := plans[*t.PlanID]
			if p.Name != nil {
				summary.PlanName = *p.Name
			}
			for _, policy := range p.Edges.Quotas {
				if policy.QuotaValue == nil {
					return nil, fmt.Errorf("invalid quota policy numeric projection")
				}
				limits[policy.QuotaCode] = *policy.QuotaValue
			}
		}
		for _, d := range defs {
			limit, configured := limits[d.Code]
			occ := occupied[t.ID][d.Code]
			available := uint64(0)
			if limit > occ {
				available = limit - occ
			}
			summary.Accounts = append(summary.Accounts, &quotapb.QuotaAccountView{
				QuotaCode: d.Code, Unit: d.Unit, Limit: strconv.FormatUint(limit, 10),
				Occupied: strconv.FormatUint(occ, 10), Available: strconv.FormatUint(available, 10),
				OverLimit: occ > limit, Enforcement: r.enforcement(d.Code), Configured: configured,
			})
		}
		items = append(items, summary)
	}
	return items, nil
}
