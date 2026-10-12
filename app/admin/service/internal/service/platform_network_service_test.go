package service

import (
	"context"
	"testing"

	"github.com/go-kratos/kratos/v2/errors"
	authv1 "go-wind-admin/api/gen/go/authentication/service/v1"
	"go-wind-admin/pkg/middleware/auth"
)

func TestPlatformNetworkIdentity(t *testing.T) {
	// The real JWT decoder omits TenantId's pointer for platform value zero.
	// IsPlatformAdmin remains a display flag; explicit action permissions apply.
	claims := &authv1.UserTokenPayload{UserId: 9, Roles: []string{"platform:network-reader"}, Permissions: []string{"network:platform:public-pool:list"}}
	s := NewPlatformNetworkService(nil)
	if _, err := s.actor(auth.NewContext(context.Background(), claims), "ListPublicAddressPools"); errors.FromError(err).Code != 503 {
		t.Fatalf("valid platform identity should reach the missing-dependency check: %v", err)
	}
	for _, tc := range []struct {
		name      string
		principal *auth.Principal
		operation string
	}{
		{"key", &auth.Principal{Type: auth.SubjectAPIKey, ID: 9, Claims: claims, Roles: claims.Roles}, "ListPublicAddressPools"},
		{"tenant", &auth.Principal{Type: auth.SubjectUser, ID: 9, TenantID: 1, Claims: claims, Roles: claims.Roles}, "ListPublicAddressPools"},
		{"missing_role", &auth.Principal{Type: auth.SubjectUser, ID: 9, Claims: claims}, "ListPublicAddressPools"},
		{"missing_permission", &auth.Principal{Type: auth.SubjectUser, ID: 9, Claims: claims, Roles: claims.Roles}, "CreatePublicAddressPool"},
		{"wrong_actor", &auth.Principal{Type: auth.SubjectUser, ID: 10, Claims: claims, Roles: claims.Roles}, "ListPublicAddressPools"},
		{"unknown_operation", &auth.Principal{Type: auth.SubjectUser, ID: 9, Claims: claims, Roles: claims.Roles}, "Unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.actor(auth.NewPrincipalContext(context.Background(), tc.principal), tc.operation); errors.FromError(err).Code != 403 {
				t.Fatalf("untrusted platform identity admitted: %v", err)
			}
		})
	}
}
