package data

import (
	"context"
	"net"
	"testing"

	vm "github.com/zhangzhe-ctrl/ani-resource-service/api/vm/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type vmGetServer struct {
	vm.UnimplementedVMServiceServer
	requests []*vm.GetVMRequest
}

func (s *vmGetServer) GetVM(ctx context.Context, r *vm.GetVMRequest) (*vm.VirtualMachine, error) {
	s.requests = append(s.requests, r)
	return &vm.VirtualMachine{Id: r.GetVmId()}, nil
}

func vmGetTestConnection(t *testing.T) (*grpc.ClientConn, *vmGetServer) {
	t.Helper()
	peer := new(vmGetServer)
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	vm.RegisterVMServiceServer(server, peer)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///vm-get", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, peer
}

// 详情投影不得触发 runtime 采集：公开详情端点不能阻塞在 QGA 读取上，
// 且 false 必须显式设置——owner 侧把 nil 当作包含。
func TestVMClientGetDisablesRuntimeCollection(t *testing.T) {
	conn, peer := vmGetTestConnection(t)
	if _, err := (&VMClient{client: vm.NewVMServiceClient(conn)}).Get(context.Background(), vmLogsTestTenant, vmLogsTestActor, vmLogsTestVM); err != nil {
		t.Fatal(err)
	}
	if len(peer.requests) != 1 {
		t.Fatalf("get calls: %d", len(peer.requests))
	}
	request := peer.requests[0]
	if request.IncludeRuntime == nil {
		t.Fatal("include_runtime must be explicit, not omitted")
	}
	if request.GetIncludeRuntime() {
		t.Fatal("detail must not collect guest runtime")
	}
	if request.GetTenantId() != vmLogsTestTenant || request.GetVmId() != vmLogsTestVM {
		t.Fatalf("request: %+v", request)
	}
}

// 独立 runtime 路由必须显式要求采集，不依赖 owner 侧缺省。
func TestVMClientRuntimeRequiresCollection(t *testing.T) {
	conn, peer := vmGetTestConnection(t)
	if _, err := (&VMClient{client: vm.NewVMServiceClient(conn)}).Runtime(context.Background(), vmLogsTestTenant, vmLogsTestActor, vmLogsTestVM); err != nil {
		t.Fatal(err)
	}
	if len(peer.requests) != 1 {
		t.Fatalf("runtime calls: %d", len(peer.requests))
	}
	request := peer.requests[0]
	if request.IncludeRuntime == nil || !request.GetIncludeRuntime() {
		t.Fatalf("runtime route must set include_runtime=true, got %+v", request.IncludeRuntime)
	}
}
