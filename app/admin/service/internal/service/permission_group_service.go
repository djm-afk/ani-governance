package service

import (
	"context"

	paginationV1 "go-wind-admin/pkg/localdeps/go-crud/api/gen/go/pagination/v1"
	"go-wind-admin/pkg/localdeps/go-utils/trans"
	"go-wind-admin/pkg/localdeps/kratos-bootstrap/bootstrap"
	bLogger "go-wind-admin/pkg/localdeps/kratos-bootstrap/logger"
	"google.golang.org/protobuf/types/known/emptypb"

	"go-wind-admin/app/admin/service/internal/data"

	adminV1 "go-wind-admin/api/gen/go/admin/service/v1"
	permissionV1 "go-wind-admin/api/gen/go/permission/service/v1"

	"go-wind-admin/pkg/middleware/auth"
)

type PermissionGroupService struct {
	adminV1.PermissionGroupServiceHTTPServer

	log *bLogger.Helper

	permissionGroupRepo *data.PermissionGroupRepo
	permissionRepo      *data.PermissionRepo
}

func NewPermissionGroupService(
	ctx *bootstrap.Context,
	permissionGroupRepo *data.PermissionGroupRepo,
	permissionRepo *data.PermissionRepo,
) *PermissionGroupService {
	svc := &PermissionGroupService{
		log:                 ctx.NewLoggerHelper("permission-group/service/admin-service"),
		permissionGroupRepo: permissionGroupRepo,
		permissionRepo:      permissionRepo,
	}

	// Database initialization is an explicit deployment step; constructors never seed data.

	return svc
}

func (s *PermissionGroupService) List(ctx context.Context, req *paginationV1.PagingRequest) (*permissionV1.ListPermissionGroupResponse, error) {
	// A page can contain children whose parent is on another page. Building a
	// tree from that partial window silently drops them. Explicit page requests
	// return flat rows; complete-tree queries retain the existing representation.
	treeTravel := req == nil || req.GetNoPaging() || (req.Page == nil && req.PageSize == nil)
	return s.permissionGroupRepo.List(ctx, req, treeTravel)
}

func (s *PermissionGroupService) Get(ctx context.Context, req *permissionV1.GetPermissionGroupRequest) (*permissionV1.PermissionGroup, error) {
	return s.permissionGroupRepo.Get(ctx, req)
}

func (s *PermissionGroupService) Create(ctx context.Context, req *permissionV1.CreatePermissionGroupRequest) (*emptypb.Empty, error) {
	if req.Data == nil {
		return nil, adminV1.ErrorBadRequest("invalid parameter")
	}

	// 获取操作人信息
	operator, err := auth.FromContext(ctx)
	if err != nil {
		return nil, err
	}

	req.Data.CreatedBy = trans.Ptr(operator.UserId)

	if _, err = s.permissionGroupRepo.Create(ctx, req); err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, nil
}

func (s *PermissionGroupService) Update(ctx context.Context, req *permissionV1.UpdatePermissionGroupRequest) (*emptypb.Empty, error) {
	if req.Data == nil {
		return nil, adminV1.ErrorBadRequest("invalid parameter")
	}

	// 获取操作人信息
	operator, err := auth.FromContext(ctx)
	if err != nil {
		return nil, err
	}

	req.Data.Id = trans.Ptr(req.GetId())

	req.Data.UpdatedBy = trans.Ptr(operator.UserId)
	if req.UpdateMask != nil {
		req.UpdateMask.Paths = append(req.UpdateMask.Paths, "updated_by")
	}

	if err = s.permissionGroupRepo.Update(ctx, req); err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, nil
}

func (s *PermissionGroupService) Delete(ctx context.Context, req *permissionV1.DeletePermissionGroupRequest) (*emptypb.Empty, error) {
	var err error

	// 先清理组内权限点，再删分组本身（两 repo 无共享事务，顺序保证失败可重试：
	// 权限点清理成功而分组删除失败时，组仍保留，重试删除不会产生孤儿数据）。
	if err = s.permissionRepo.Delete(ctx, &permissionV1.DeletePermissionRequest{
		QueryBy: &permissionV1.DeletePermissionRequest_GroupId{GroupId: req.GetId()},
	}); err != nil {
		return nil, err
	}

	if err = s.permissionGroupRepo.Delete(ctx, req); err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, nil
}
