package server

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	errors "github.com/go-kratos/kratos/v2/errors"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	admin "go-wind-admin/api/gen/go/admin/service/v1"
	view "go-wind-admin/api/gen/go/usage/service/v1"
	"go-wind-admin/app/admin/service/internal/service"
	"google.golang.org/protobuf/proto"
)

const resourceUsagePrefix = "/api/v1/admin/resource-usage/"

var resourceUsageHTTPPath = regexp.MustCompile(`^/api/v1/admin/resource-usage/(overview|tenants(/[1-9][0-9]*)?)$`)

func validateResourceUsageHTTP(r *http.Request, list bool) error {
	invalid := errors.BadRequest("RESOURCE_USAGE_INVALID_QUERY", "invalid resource usage query")
	if r.Method != http.MethodGet || r.ContentLength != 0 || len(r.TransferEncoding) != 0 || len(r.URL.RawQuery) > 4096 {
		return invalid
	}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return invalid
	}
	for k, vs := range q {
		if len(vs) != 1 || vs[0] == "" {
			return invalid
		}
		v := vs[0]
		switch k {
		case "start", "end":
			t, e := time.Parse(time.RFC3339, v)
			if e != nil || t.Nanosecond() != 0 {
				return invalid
			}
		case "metric":
			if v != "gpu" && v != "cpu" && v != "memory" {
				return invalid
			}
		case "page", "pageSize":
			n, e := strconv.ParseUint(v, 10, 32)
			if !list || e != nil || n == 0 || strconv.FormatUint(n, 10) != v || k == "pageSize" && n > 100 {
				return invalid
			}
		case "keyword":
			if !list || len(v) > 256 || strings.TrimSpace(v) == "" {
				return invalid
			}
		case "order":
			if !list || (v != "asc" && v != "desc") {
				return invalid
			}
		default:
			return invalid
		}
	}
	if q.Get("start") == "" || q.Get("end") == "" {
		return invalid
	}
	return nil
}

func RegisterResourceUsageHTTPServer(s *khttp.Server, svc *service.ResourceUsageAdminService) {
	type route struct {
		path, operation string
		list            bool
		request         func() proto.Message
		invoke          func(context.Context, proto.Message) (proto.Message, error)
	}
	routes := []route{
		{resourceUsagePrefix + "overview", admin.OperationResourceUsageAdminServiceGetResourceUsageOverview, false, func() proto.Message { return new(view.OverviewRequest) }, func(c context.Context, r proto.Message) (proto.Message, error) {
			return svc.GetResourceUsageOverview(c, r.(*view.OverviewRequest))
		}},
		{resourceUsagePrefix + "tenants", admin.OperationResourceUsageAdminServiceListResourceUsageTenants, true, func() proto.Message { return new(view.TenantsRequest) }, func(c context.Context, r proto.Message) (proto.Message, error) {
			return svc.ListResourceUsageTenants(c, r.(*view.TenantsRequest))
		}},
		{resourceUsagePrefix + "tenants/{tenant_id}", admin.OperationResourceUsageAdminServiceGetResourceUsageTenant, false, func() proto.Message { return new(view.TenantRequest) }, func(c context.Context, r proto.Message) (proto.Message, error) {
			return svc.GetResourceUsageTenant(c, r.(*view.TenantRequest))
		}},
	}
	for _, r := range routes {
		r := r
		s.Route("/").GET(r.path, func(ctx khttp.Context) error {
			khttp.SetOperation(ctx, r.operation)
			if err := validateResourceUsageHTTP(ctx.Request(), r.list); err != nil {
				return err
			}
			req := r.request()
			if err := ctx.BindQuery(req); err != nil {
				return errors.BadRequest("RESOURCE_USAGE_INVALID_QUERY", "invalid resource usage query")
			}
			if err := ctx.BindVars(req); err != nil {
				return errors.BadRequest("RESOURCE_USAGE_INVALID_QUERY", "invalid resource usage target")
			}
			if detail, ok := req.(*view.TenantRequest); ok {
				id := strings.TrimPrefix(ctx.Request().URL.Path, resourceUsagePrefix+"tenants/")
				n, e := strconv.ParseUint(id, 10, 32)
				if e != nil || n == 0 || strconv.FormatUint(n, 10) != id || detail.TenantId != uint32(n) {
					return errors.BadRequest("RESOURCE_USAGE_INVALID_QUERY", "invalid tenant ID")
				}
			}
			out, err := ctx.Middleware(func(c context.Context, in any) (any, error) { return r.invoke(c, in.(proto.Message)) })(ctx, req)
			if err != nil {
				return err
			}
			return ctx.Result(http.StatusOK, out)
		})
	}
}
