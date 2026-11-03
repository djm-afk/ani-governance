package data

import (
	"context"
	"go-wind-admin/app/admin/service/internal/data/ent"
	entCrud "go-wind-admin/pkg/localdeps/go-crud/entgo"
)

type NetworkVNicCandidateAuthorizer struct{ current *ActionAuthorizationRepo }

func NewNetworkVNicCandidateAuthorizer(c *entCrud.EntClient[*ent.Client]) *NetworkVNicCandidateAuthorizer {
	return &NetworkVNicCandidateAuthorizer{NewActionAuthorizationRepo(c)}
}
func (r *NetworkVNicCandidateAuthorizer) Authorize(ctx context.Context, tenant, user uint32, path, method string) error {
	if r == nil || r.current == nil || method != "GET" {
		return ErrActionAuthorizationDenied
	}
	module := ""
	switch path {
	case VMGetPath:
		module = "VIRTUAL_MACHINE"
	case InstanceGetPath:
		module = "INSTANCE"
	default:
		return ErrActionAuthorizationDenied
	}
	return r.current.authorizeRoute(ctx, tenant, user, path, module, method)
}
