package server

import (
	"context"
	nethttp "net/http"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/transport/http"
	admin "go-wind-admin/api/gen/go/admin/service/v1"
	view "go-wind-admin/api/gen/go/inference/service/v1"
	"go-wind-admin/app/admin/service/internal/service"
)

func registerInferencePolicyHTTP(server *http.Server, inference *service.InferenceService) {
	path := "/api/v1/inference/services/{resource_id}/policy"
	server.Route("/").GET(path, func(ctx http.Context) error {
		http.SetOperation(ctx, admin.OperationInferenceServiceGetServicePolicy)
		if ctx.Request().URL.RawQuery != "" || ctx.Request().URL.ForceQuery || (ctx.Request().URL.RawPath != "" && ctx.Request().URL.RawPath != ctx.Request().URL.Path) {
			return errors.BadRequest("INVALID_INFERENCE_POLICY", "query parameters are not accepted")
		}
		for _, header := range []string{"X-Ani-Tenant-Id", "X-Tenant-Id", "X-Ani-Actor", "X-Ani-Operator", "X-Resource-Tenant-Id"} {
			if len(ctx.Request().Header.Values(header)) != 0 {
				return errors.BadRequest("INVALID_INFERENCE_POLICY", "public identity headers are not accepted")
			}
		}
		request := new(view.GetServicePolicyRequest)
		if err := ctx.BindVars(request); err != nil {
			return err
		}
		out, err := ctx.Middleware(func(call context.Context, in interface{}) (interface{}, error) {
			return inference.GetServicePolicy(call, in.(*view.GetServicePolicyRequest))
		})(ctx, request)
		if err != nil {
			return err
		}
		return ctx.Result(nethttp.StatusOK, out)
	})
	server.Route("/").PUT(path, func(ctx http.Context) error {
		http.SetOperation(ctx, admin.OperationInferenceServiceUpdateServicePolicy)
		request := new(view.UpdateServicePolicyRequest)
		if err := decodeInferenceHTTP(ctx.Request(), request); err != nil {
			return err
		}
		id := ctx.Vars().Get("resource_id")
		if id == "" || (request.ResourceId != "" && request.ResourceId != id) {
			return errors.BadRequest("INVALID_INFERENCE_POLICY", "resource ID must match path")
		}
		request.ResourceId = id
		out, err := ctx.Middleware(func(call context.Context, in interface{}) (interface{}, error) {
			return inference.UpdateServicePolicy(call, in.(*view.UpdateServicePolicyRequest))
		})(ctx, request)
		if err != nil {
			return err
		}
		return ctx.Result(nethttp.StatusOK, out)
	})
}
