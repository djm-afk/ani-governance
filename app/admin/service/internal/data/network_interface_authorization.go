package data

import (
	"context"
	"go-wind-admin/pkg/middleware/auth"
)

func authorizeNetworkPrincipal(ctx context.Context, r *ActionAuthorizationRepo, p *auth.Principal, path, module, method string) error {
	if p == nil || p.ID == 0 || p.TenantID == 0 || r == nil {
		return ErrActionAuthorizationDenied
	}
	switch p.Type {
	case auth.SubjectUser:
		return r.authorizeRoute(ctx, p.TenantID, p.ID, path, module, method)
	case auth.SubjectAPIKey:
		return r.authorizeKeyRoute(ctx, p.TenantID, p.ID, path, module, method)
	default:
		return ErrActionAuthorizationDenied
	}
}
func (r *VMAuthorizationRepo) AuthorizeNetworkPrincipal(ctx context.Context, p *auth.Principal, path, method string) error {
	if r == nil {
		return ErrActionAuthorizationDenied
	}
	module := "VIRTUAL_MACHINE"
	switch {
	case path == NetworkVNicReadPath && method == "GET":
		module = "NETWORK"
	case path == "/api/v1/virtual-machines/{vm_id}/network-interfaces" && method == "GET":
	case path == "/api/v1/virtual-machines/{vm_id}/network-interfaces/{slot}" && method == "PUT":
	default:
		return ErrActionAuthorizationDenied
	}
	return authorizeNetworkPrincipal(ctx, r.current, p, path, module, method)
}
func (r *InstanceAuthorizationRepo) AuthorizeNetworkPrincipal(ctx context.Context, p *auth.Principal, path, method string) error {
	if r == nil {
		return ErrActionAuthorizationDenied
	}
	module := "INSTANCE"
	switch {
	case path == NetworkVNicReadPath && method == "GET":
		module = "NETWORK"
	case path == InstanceNetworkInterfacesPath && method == "GET":
	case path == InstanceNetworkInterfacePath && method == "PUT":
	default:
		return ErrActionAuthorizationDenied
	}
	return authorizeNetworkPrincipal(ctx, r.current, p, path, module, method)
}
func (r *NetworkVNicCandidateAuthorizer) AuthorizeNetworkPrincipal(ctx context.Context, p *auth.Principal, path, method string) error {
	if r == nil || method != "GET" {
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
	return authorizeNetworkPrincipal(ctx, r.current, p, path, module, method)
}
