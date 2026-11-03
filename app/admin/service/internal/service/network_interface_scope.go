package service

import (
	"context"
	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	"go-wind-admin/app/admin/service/internal/data"
	"go-wind-admin/app/admin/service/internal/data/ent"
	"go-wind-admin/pkg/middleware/auth"
)

type networkPrincipalAuthorizer interface {
	AuthorizeNetworkPrincipal(context.Context, *auth.Principal, string, string) error
}

func networkPrincipal(ctx context.Context) (*auth.Principal, error) {
	p, e := auth.PrincipalFromContext(ctx)
	if e != nil || p == nil || p.ID == 0 || p.TenantID == 0 || (p.Type != auth.SubjectUser && p.Type != auth.SubjectAPIKey) {
		return nil, kratoserrors.Unauthorized("INVALID_LOGIN", "tenant identity required")
	}
	return p, nil
}
func authorizeNetworkInterface(ctx context.Context, authorizer InstanceActionAuthorizer, path, method string) error {
	p, e := networkPrincipal(ctx)
	if e != nil {
		return e
	}
	current, ok := authorizer.(networkPrincipalAuthorizer)
	if !ok {
		if p.Type != auth.SubjectUser || authorizer == nil {
			return kratoserrors.Forbidden("NETWORK_INTERFACE_SCOPE_DENIED", "network interface action denied")
		}
		return instanceAuthorizationError(authorizer.Authorize(ctx, p.TenantID, p.ID, path, method))
	}
	return instanceAuthorizationError(current.AuthorizeNetworkPrincipal(ctx, p, path, method))
}
func (s *VMService) networkInterfaceScope(ctx context.Context, action string) (string, string, error) {
	route, ok := data.VMActionRoute(action)
	if !ok || s.binding == nil {
		return "", "", vmInvalid()
	}
	if e := authorizeNetworkInterface(ctx, s.binding.authorization, route.Path, route.Method); e != nil {
		return "", "", e
	}
	if s.binding.client == nil || s.resolver == nil || s.ledger == nil {
		return "", "", kratoserrors.ServiceUnavailable("VM_ADAPTER_UNAVAILABLE", "VM owner unavailable")
	}
	p, e := networkPrincipal(ctx)
	if e != nil {
		return "", "", e
	}
	a, e := p.Actor()
	if e != nil {
		return "", "", e
	}
	t, e := s.resolver.ResourceTenantID(ctx, p.TenantID)
	return t, a, e
}
func (s *InstanceService) networkInterfaceScope(ctx context.Context, path, method string) (string, string, error) {
	if s.binding == nil {
		return "", "", kratoserrors.Forbidden("INSTANCE_SCOPE_DENIED", "instance action denied")
	}
	if e := authorizeNetworkInterface(ctx, s.binding.authorization, path, method); e != nil {
		return "", "", e
	}
	if s.binding.client == nil || s.resolver == nil || s.ledger == nil {
		return "", "", kratoserrors.ServiceUnavailable("INSTANCE_ADAPTER_UNAVAILABLE", "instance owner unavailable")
	}
	p, e := networkPrincipal(ctx)
	if e != nil {
		return "", "", e
	}
	a, e := p.Actor()
	if e != nil {
		return "", "", e
	}
	t, e := s.resolver.ResourceTenantID(ctx, p.TenantID)
	return t, a, e
}
func (s *VMService) canConfigureNetwork(ctx context.Context, id string) error {
	p, e := networkPrincipal(ctx)
	if e != nil {
		return e
	}
	deleting, e := s.ledger.HasAcceptedVMDelete(ctx, p.TenantID, id)
	if ent.IsNotFound(e) {
		return kratoserrors.NotFound("VM_NOT_FOUND", "VM not found")
	}
	if e != nil {
		return e
	}
	if deleting {
		return kratoserrors.Conflict("VM_OPERATION_CONFLICT", "VM final deletion already accepted")
	}
	return nil
}
func (s *InstanceService) canConfigureNetwork(ctx context.Context, id string) error {
	p, e := networkPrincipal(ctx)
	if e != nil {
		return e
	}
	deleting, e := s.ledger.HasAcceptedInstanceDelete(ctx, p.TenantID, id)
	if ent.IsNotFound(e) {
		return kratoserrors.NotFound("INSTANCE_NOT_FOUND", "instance not found")
	}
	if e != nil {
		return e
	}
	if deleting {
		return kratoserrors.Conflict("INSTANCE_DELETING", "instance final deletion already accepted")
	}
	return nil
}
