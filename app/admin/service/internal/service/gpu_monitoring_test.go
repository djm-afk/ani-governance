package service

import (
	"context"
	"fmt"
	"testing"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	acc "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	obs "github.com/zhangzhe-ctrl/ani-observability-service/api/observability/gpu/v1"
	view "go-wind-admin/api/gen/go/gpu/service/v1"
	"go-wind-admin/app/admin/service/internal/data"
	"go-wind-admin/pkg/middleware/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type gpuPhysicalInventoryStub struct {
	acc.AcceleratorAdminServiceClient
	devices []*acc.PhysicalDevice
}

func (p *gpuPhysicalInventoryStub) ListDevices(context.Context, *acc.ListDevicesRequest, ...grpc.CallOption) (*acc.ListDevicesResponse, error) {
	return &acc.ListDevicesResponse{Items: p.devices}, nil
}

type gpuPhysicalReaderProbe struct {
	latest, ranges int
	target         *obs.GpuTarget
}

func (p *gpuPhysicalReaderProbe) result(target *obs.GpuTarget, metricCodes []string) (*obs.QueryGpuResponse, error) {
	p.target = target
	// The Obs PHYSICAL boundary rejects a query containing any partition.
	for _, scope := range target.Scopes {
		if scope.DeviceKind != "PHYSICAL_DEVICE" {
			return nil, status.Error(codes.InvalidArgument, "physical device required")
		}
	}
	r := &obs.QueryGpuResponse{QueryTime: timestamppb.Now()}
	for _, code := range metricCodes {
		r.Series = append(r.Series, &obs.GpuSeries{MetricCode: code})
	}
	return r, nil
}

func (p *gpuPhysicalReaderProbe) QueryGpuLatest(_ context.Context, _ bool, _, _ string, r *obs.QueryGpuLatestRequest) (*obs.QueryGpuResponse, error) {
	p.latest++
	return p.result(r.Target, r.MetricCodes)
}

func (p *gpuPhysicalReaderProbe) QueryGpuRange(_ context.Context, _ bool, _, _ string, r *obs.QueryGpuRangeRequest) (*obs.QueryGpuResponse, error) {
	p.ranges++
	return p.result(r.Target, r.MetricCodes)
}

func newGpuPhysicalFixture(partitions int) (*GpuMonitoringService, context.Context, *gpuPhysicalInventoryStub, *gpuPhysicalReaderProbe) {
	parent := &acc.PhysicalDevice{
		DeviceId: uuid.NewString(), ClusterId: uuid.NewString(),
		Node:      &acc.NodeRef{Name: "gpu-node", Uid: uuid.NewString()},
		DeviceUid: "parent-card", DeviceKind: acc.DeviceKind_PHYSICAL_DEVICE,
		AdapterId: "hygon-hcu-v1", ModelKey: "parent-model",
	}
	inventory := &gpuPhysicalInventoryStub{devices: []*acc.PhysicalDevice{parent}}
	for i := 0; i < partitions; i++ {
		inventory.devices = append(inventory.devices, &acc.PhysicalDevice{
			DeviceId: uuid.NewString(), ClusterId: parent.ClusterId, Node: parent.Node,
			DeviceUid: fmt.Sprintf("partition-%d", i), ParentDeviceUid: parent.DeviceUid,
			DeviceKind: acc.DeviceKind_FIXED_PARTITION_DEVICE,
			AdapterId:  parent.AdapterId, ModelKey: "partition-model",
		})
	}
	reader := &gpuPhysicalReaderProbe{}
	accelerator := NewAcceleratorService(&data.AcceleratorClient{Admin: inventory}, nil, nil, nil, nil)
	ctx := auth.NewPrincipalContext(context.Background(), &auth.Principal{Type: auth.SubjectUser, ID: 7})
	return NewGpuMonitoringService(accelerator, reader), ctx, inventory, reader
}

func queryGpuPhysical(s *GpuMonitoringService, ctx context.Context, cluster, id string, latest bool) (*view.GpuQueryResponse, error) {
	if latest {
		r := &view.GpuPhysicalLatestRequest{ClusterId: cluster, DeviceId: id}
		if id == "" {
			return s.GetPlatformGpuLatest(ctx, r)
		}
		return s.GetPhysicalGpuLatest(ctx, r)
	}
	r := &view.GpuPhysicalQueryRequest{DeviceId: id, Data: &view.GpuQueryData{ClusterId: cluster, Window: "1h"}}
	if id == "" {
		return s.QueryPlatformGpu(ctx, r)
	}
	return s.QueryPhysicalGpu(ctx, r)
}

func TestGpuMonitoringPhysicalFiltersPartitionsBeforeModelsAndBudget(t *testing.T) {
	for _, latest := range []bool{true, false} {
		for _, partitions := range []int{2, 129} {
			t.Run(fmt.Sprintf("latest=%t/partitions=%d", latest, partitions), func(t *testing.T) {
				s, ctx, inventory, reader := newGpuPhysicalFixture(partitions)
				parent := inventory.devices[0]
				_, err := queryGpuPhysical(s, ctx, parent.ClusterId, "", latest)
				require.NoError(t, err, "partitions must not enter model selection or the physical source budget")
				require.Equal(t, "PHYSICAL", reader.target.Kind)
				require.Len(t, reader.target.Scopes, 1)
				scope := reader.target.Scopes[0]
				require.Equal(t, parent.DeviceId, scope.DeviceId)
				require.Equal(t, parent.DeviceUid, scope.DeviceUid)
				require.Equal(t, parent.Node.Uid, scope.NodeUid)
				require.Equal(t, "PHYSICAL_DEVICE", scope.DeviceKind)
				if latest {
					require.Equal(t, 1, reader.latest)
					require.Zero(t, reader.ranges)
				} else {
					require.Equal(t, 1, reader.ranges)
					require.Zero(t, reader.latest)
				}
			})
		}
	}
}

func TestGpuMonitoringDeviceInventoryPreservesPartitions(t *testing.T) {
	s, ctx, inventory, reader := newGpuPhysicalFixture(2)
	r, err := s.ListGpuDevices(ctx, &view.ListGpuDevicesRequest{ClusterId: inventory.devices[0].ClusterId})
	require.NoError(t, err)
	require.EqualValues(t, 3, r.Total)
	require.Len(t, r.Items, 3)
	kinds := map[string]int{}
	for _, item := range r.Items {
		kinds[item.DeviceKind]++
	}
	require.Equal(t, map[string]int{"PHYSICAL_DEVICE": 1, "FIXED_PARTITION_DEVICE": 2}, kinds)
	require.Zero(t, reader.latest+reader.ranges)
}

func TestGpuMonitoringPhysicalPartitionIDRejected(t *testing.T) {
	for _, latest := range []bool{true, false} {
		t.Run(fmt.Sprintf("latest=%t", latest), func(t *testing.T) {
			s, ctx, inventory, reader := newGpuPhysicalFixture(2)
			parent, partition := inventory.devices[0], inventory.devices[1]
			_, err := queryGpuPhysical(s, ctx, parent.ClusterId, partition.DeviceId, latest)
			require.EqualValues(t, 400, kerrors.Code(err))
			require.Equal(t, "GPU_PHYSICAL_DEVICE_REQUIRED", kerrors.Reason(err))
			require.Zero(t, reader.latest+reader.ranges, "reject partitions before contacting Obs")
			_, err = queryGpuPhysical(s, ctx, parent.ClusterId, uuid.NewString(), latest)
			require.Equal(t, "GPU_NOT_FOUND", kerrors.Reason(err))
			_, err = queryGpuPhysical(s, ctx, parent.ClusterId, parent.DeviceId, latest)
			require.NoError(t, err)
			require.Len(t, reader.target.Scopes, 1)
			require.Equal(t, parent.DeviceId, reader.target.Scopes[0].DeviceId)
		})
	}
}

func TestGpuMonitoringPhysicalParentModelAndBudgetLimitsRemain(t *testing.T) {
	for _, count := range []int{2, 129} {
		t.Run(fmt.Sprintf("parents=%d", count), func(t *testing.T) {
			s, ctx, inventory, reader := newGpuPhysicalFixture(count - 1)
			for _, device := range inventory.devices {
				device.DeviceKind = acc.DeviceKind_PHYSICAL_DEVICE
				device.ParentDeviceUid = ""
			}
			_, err := queryGpuPhysical(s, ctx, inventory.devices[0].ClusterId, "", true)
			want := "GPU_MODEL_FILTER_REQUIRED"
			if count > 128 {
				want = "GPU_QUERY_BUDGET_EXCEEDED"
			}
			require.Equal(t, want, kerrors.Reason(err))
			require.Zero(t, reader.latest+reader.ranges)
		})
	}
}
