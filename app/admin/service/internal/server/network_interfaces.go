package server

import (
	"bytes"
	"context"
	"encoding/json"
	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/transport/http"
	view "go-wind-admin/api/gen/go/catalog/service/v1"
	"go-wind-admin/app/admin/service/internal/data"
	"go-wind-admin/app/admin/service/internal/service"
	"google.golang.org/protobuf/proto"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"
)

func registerVMNetworkInterfaces(server *http.Server, s *service.VMService) {
	registerNetworkInterfaceRoute(server, "GET", "/api/v1/virtual-machines/{vm_id}/network-interfaces", "/admin.service.v1.VMService/GetVMNetworkInterfaces", func() proto.Message { return new(view.GetVMNetworkInterfacesRequest) }, func(c context.Context, p proto.Message) (proto.Message, error) {
		return s.GetVMNetworkInterfaces(c, p.(*view.GetVMNetworkInterfacesRequest))
	})
	registerNetworkInterfaceRoute(server, "PUT", "/api/v1/virtual-machines/{vm_id}/network-interfaces/{slot}", "/admin.service.v1.VMService/SetVMNetworkInterface", func() proto.Message { return new(view.SetVMNetworkInterfaceRequest) }, func(c context.Context, p proto.Message) (proto.Message, error) {
		return s.SetVMNetworkInterface(c, p.(*view.SetVMNetworkInterfaceRequest))
	})
}
func registerInstanceNetworkInterfaces(server *http.Server, s *service.InstanceService) {
	registerNetworkInterfaceRoute(server, "GET", data.InstanceNetworkInterfacesPath, "/admin.service.v1.InstanceService/GetInstanceNetworkInterfaces", func() proto.Message { return new(view.GetInstanceNetworkInterfacesRequest) }, func(c context.Context, p proto.Message) (proto.Message, error) {
		return s.GetInstanceNetworkInterfaces(c, p.(*view.GetInstanceNetworkInterfacesRequest))
	})
	registerNetworkInterfaceRoute(server, "PUT", data.InstanceNetworkInterfacePath, "/admin.service.v1.InstanceService/SetInstanceNetworkInterface", func() proto.Message { return new(view.SetInstanceNetworkInterfaceRequest) }, func(c context.Context, p proto.Message) (proto.Message, error) {
		return s.SetInstanceNetworkInterface(c, p.(*view.SetInstanceNetworkInterfaceRequest))
	})
}
func registerNetworkInterfaceRoute(server *http.Server, method, path, operation string, newRequest func() proto.Message, invoke func(context.Context, proto.Message) (proto.Message, error)) {
	handler := func(ctx http.Context) error {
		invalid := kratoserrors.BadRequest("INVALID_NETWORK_INTERFACE_REQUEST", "invalid network interface request")
		r := ctx.Request()
		if r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
			return invalid
		}
		http.SetOperation(ctx, operation)
		request := newRequest()
		if method == "PUT" {
			if e := decodeNetworkInterfaceHTTP(r, request); e != nil {
				return invalid
			}
		} else if r.Body != nil {
			body, e := io.ReadAll(io.LimitReader(r.Body, 1))
			if e != nil || len(bytes.TrimSpace(body)) > 0 {
				return invalid
			}
		}
		bound := newRequest()
		if e := ctx.BindVars(bound); e != nil {
			return invalid
		}
		switch in := request.(type) {
		case *view.SetVMNetworkInterfaceRequest:
			p := bound.(*view.SetVMNetworkInterfaceRequest)
			if (in.VmId != "" && in.VmId != p.VmId) || (in.Slot != "" && in.Slot != p.Slot) {
				return invalid
			}
			in.VmId = p.VmId
			in.Slot = p.Slot
		case *view.SetInstanceNetworkInterfaceRequest:
			p := bound.(*view.SetInstanceNetworkInterfaceRequest)
			if (in.InstanceId != "" && in.InstanceId != p.InstanceId) || (in.Slot != "" && in.Slot != p.Slot) {
				return invalid
			}
			in.InstanceId = p.InstanceId
			in.Slot = p.Slot
		default:
			request = bound
		}
		out, e := ctx.Middleware(func(c context.Context, p any) (any, error) { return invoke(c, p.(proto.Message)) })(ctx, request)
		if e != nil {
			if strings.HasPrefix(operation, "/admin.service.v1.InstanceService/") {
				return instanceHTTPError(e)
			}
			return vmHTTPError(e)
		}
		code := 200
		if method == "PUT" {
			code = 202
		}
		return ctx.Result(code, out)
	}
	router := server.Route("/")
	if method == "GET" {
		router.GET(path, handler)
	} else {
		router.PUT(path, handler)
	}
}

func decodeNetworkInterfaceHTTP(r *nethttp.Request, message proto.Message) error {
	if r == nil || r.Body == nil {
		return errInvalidJSONRequest
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, 65537))
	if e != nil || len(raw) > 65536 {
		return errInvalidJSONRequest
	}
	envelope, e := instanceJSONObject(raw)
	if e != nil || len(envelope) != 1 {
		return errInvalidJSONRequest
	}
	fields, e := instanceJSONObject(envelope["data"])
	if e != nil {
		return errInvalidJSONRequest
	}
	for k, v := range fields {
		switch k {
		case "mode", "vnic_id", "expected_version", "idempotency_key":
		default:
			return errInvalidJSONRequest
		}
		var text string
		if json.Unmarshal(v, &text) != nil || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return errInvalidJSONRequest
		}
		if k == "expected_version" {
			n, e := strconv.ParseInt(text, 10, 64)
			if e != nil || n < 1 || strconv.FormatInt(n, 10) != text {
				return errInvalidJSONRequest
			}
		}
	}
	for _, k := range []string{"mode", "expected_version", "idempotency_key"} {
		if _, ok := fields[k]; !ok {
			return errInvalidJSONRequest
		}
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return decodeStrictJSONHTTP(r, message)
}
