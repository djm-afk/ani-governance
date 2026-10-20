package data

import (
	"context"
	"go-wind-admin/app/admin/service/internal/data/ent"
	entCrud "go-wind-admin/pkg/localdeps/go-crud/entgo"
	"go-wind-admin/pkg/permcode"
)

const ComputeCatalogPath = permcode.ComputeCatalogPath

type ComputeCatalogAuthorizationRepo struct{ current *ActionAuthorizationRepo }

func NewComputeCatalogAuthorizationRepo(c *entCrud.EntClient[*ent.Client]) *ComputeCatalogAuthorizationRepo {
	return &ComputeCatalogAuthorizationRepo{NewActionAuthorizationRepo(c)}
}
func (r *ComputeCatalogAuthorizationRepo) AuthorizeComputeCatalog(ctx context.Context, tenant, user uint32, usage string) error {
	module, code, ok := permcode.ComputeCatalogGrant(usage)
	if !ok || r == nil || r.current == nil {
		return ErrActionAuthorizationDenied
	}
	return r.current.authorizeRouteGrant(ctx, tenant, user, ComputeCatalogPath, module, "SYSTEM", "GET", code)
}
func (r *ComputeCatalogAuthorizationRepo) AuthorizeCatalog(ctx context.Context, tenant, user uint32) error {
	if r == nil || r.current == nil {
		return ErrActionAuthorizationDenied
	}
	return r.current.authorizeRoute(ctx, tenant, user, "/api/v1/accelerator/profiles", "ACCELERATOR", "GET")
}
