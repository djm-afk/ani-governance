package server

import (
	"context"
	"encoding/json"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/stretchr/testify/require"
	view "go-wind-admin/api/gen/go/catalog/service/v1"
	vmview "go-wind-admin/api/gen/go/vm/service/v1"
	"google.golang.org/protobuf/proto"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNetworkInterfaceCommandPreservesPathVersionAndPendingSemantics(t *testing.T) {
	const id = "33333333-3333-4333-8333-333333333333"
	called := 0
	srv := khttp.NewServer()
	registerNetworkInterfaceRoute(srv, "PUT", "/api/v1/virtual-machines/{vm_id}/network-interfaces/{slot}", "/admin.service.v1.VMService/SetVMNetworkInterface", func() proto.Message { return new(view.SetVMNetworkInterfaceRequest) }, func(_ context.Context, p proto.Message) (proto.Message, error) {
		called++
		r := p.(*view.SetVMNetworkInterfaceRequest)
		require.Equal(t, id, r.VmId)
		require.Equal(t, "primary", r.Slot)
		require.Equal(t, int64(9007199254740993), r.Data.ExpectedVersion)
		require.Equal(t, "manual", r.Data.Mode)
		return &vmview.VMOperation{Id: "operation", VmId: id, Action: "set-network-interface", State: "pending"}, nil
	})
	body := `{"data":{"mode":"manual","vnic_id":"vnic_0123456789abcdef0123456789abcdef","expected_version":"9007199254740993","idempotency_key":"33333333-3333-4333-8333-333333333333"}}`
	req := httptest.NewRequest("PUT", "/api/v1/virtual-machines/"+id+"/network-interfaces/primary", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	require.Equal(t, 202, w.Code, w.Body.String())
	require.Equal(t, 1, called)
	var result map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Equal(t, "pending", result["state"])
	for _, bad := range []string{
		strings.Replace(body, `"9007199254740993"`, `9007199254740993`, 1),
		strings.Replace(body, `"9007199254740993"`, `null`, 1),
		strings.Replace(body, `"expected_version":"9007199254740993"`, `"expected_version":"9","expected_version":"10"`, 1),
		strings.Replace(body, `"mode":"manual"`, `"mode":"manual","tenant_id":"foreign"`, 1),
		strings.Replace(body, `{"data":`, `{"vm_id":"different","data":`, 1),
	} {
		req := httptest.NewRequest("PUT", "/api/v1/virtual-machines/"+id+"/network-interfaces/primary", strings.NewReader(bad))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		require.Equal(t, 400, w.Code, bad, w.Body.String())
	}
	require.Equal(t, 1, called, "malformed commands must not reach an owner")
}
