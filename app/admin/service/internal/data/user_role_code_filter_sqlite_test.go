package data

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	paginationV1 "go-wind-admin/pkg/localdeps/go-crud/api/gen/go/pagination/v1"
	"go-wind-admin/pkg/localdeps/go-crud/viewer"
	"go-wind-admin/pkg/localdeps/go-utils/trans"

	identityV1 "go-wind-admin/api/gen/go/identity/service/v1"
	permissionV1 "go-wind-admin/api/gen/go/permission/service/v1"
	entRole "go-wind-admin/app/admin/service/internal/data/ent/role"
	"go-wind-admin/app/admin/service/tests/testutil"
	appViewer "go-wind-admin/pkg/entgo/viewer"
)

// roleCodeFilterTenantID 是本组用例使用的具名租户，用于验证“编码解析必须限定租户”。
const roleCodeFilterTenantID = uint32(7)

// newRoleCodeFilterFixture 造租户内的两个角色（tenant:manager / tenant:member）、
// 三个用户，并把角色分别分配给前两个用户；返回 repo、租户上下文与各实体 ID。
func newRoleCodeFilterFixture(t *testing.T) (repo *userRepo, tenantCtx context.Context, adminRoleID, memberRoleID, adminUserID, memberUserID, noRoleUserID uint32) {
	t.Helper()
	repo = newUserRepoSqlite(t)
	sysCtx := testutil.NewSystemViewerCtx(context.Background())
	ec := repo.entClient.Client()

	adminRole := ec.Role.Create().
		SetTenantID(roleCodeFilterTenantID).SetCode("tenant:manager").SetName("租户管理员").
		SetType(entRole.TypeTenant).SetStatus(entRole.StatusOn).SaveX(sysCtx)
	memberRole := ec.Role.Create().
		SetTenantID(roleCodeFilterTenantID).SetCode("tenant:member").SetName("普通成员").
		SetType(entRole.TypeTenant).SetStatus(entRole.StatusOn).SaveX(sysCtx)

	createUser := func(name string, status identityV1.User_Status) uint32 {
		dto, err := repo.Create(sysCtx, &identityV1.CreateUserRequest{
			Data: &identityV1.User{
				TenantId: trans.Ptr(roleCodeFilterTenantID),
				Username: trans.Ptr(name),
				Status:   status.Enum(),
			},
		})
		require.NoError(t, err)
		return dto.GetId()
	}
	adminUserID = createUser("rolecode_admin_user", identityV1.User_NORMAL)
	memberUserID = createUser("rolecode_member_user", identityV1.User_NORMAL)
	noRoleUserID = createUser("rolecode_norole_user", identityV1.User_LOCKED)

	tenantCtx = viewer.WithContext(context.Background(), appViewer.NewUserViewer(
		100, uint64(roleCodeFilterTenantID), 0, "", []viewer.DataScope{{ScopeType: viewer.ScopeTypeAll}}))

	require.NoError(t, repo.AssignUserRoles(tenantCtx, adminUserID, []*permissionV1.UserRole{
		{UserId: trans.Ptr(adminUserID), RoleId: trans.Ptr(adminRole.ID), Status: trans.Ptr(permissionV1.UserRole_ACTIVE)},
	}))
	require.NoError(t, repo.AssignUserRoles(tenantCtx, memberUserID, []*permissionV1.UserRole{
		{UserId: trans.Ptr(memberUserID), RoleId: trans.Ptr(memberRole.ID), Status: trans.Ptr(permissionV1.UserRole_ACTIVE)},
	}))

	return repo, tenantCtx, adminRole.ID, memberRole.ID, adminUserID, memberUserID, noRoleUserID
}

// pageQueryReq 构造走旧窗口（page/pageSize）路径、带 query 的列表请求。
func pageQueryReq(query string) *paginationV1.PagingRequest {
	return &paginationV1.PagingRequest{
		Page:          trans.Ptr(uint32(1)),
		PageSize:      trans.Ptr(uint32(20)),
		FilteringType: &paginationV1.PagingRequest_Query{Query: query},
	}
}

// idsOf 提取列表结果中的用户 ID 集合。
func idsOf(resp *identityV1.ListUserResponse) []uint32 {
	ids := make([]uint32, 0, len(resp.GetItems()))
	for _, item := range resp.GetItems() {
		ids = append(ids, item.GetId())
	}
	return ids
}

// TestUserRepoSqlite_RoleCodeFilterIntersection 验证 role_code / role_codes__in
// 与其它条件取交集，total 与 items 一致。
func TestUserRepoSqlite_RoleCodeFilterIntersection(t *testing.T) {
	repo, tenantCtx, _, _, adminUserID, memberUserID, _ := newRoleCodeFilterFixture(t)

	// 单值 role_code：只命中租户管理员
	single, err := repo.List(tenantCtx, pageQueryReq(`{"role_code":"tenant:manager"}`))
	require.NoError(t, err)
	require.Equal(t, uint64(1), single.Total, "role_code 单值应只命中 1 个用户")
	require.Equal(t, []uint32{adminUserID}, idsOf(single))

	// 集合 role_codes__in：命中管理员与成员
	set, err := repo.List(tenantCtx, pageQueryReq(`{"role_codes__in":["tenant:manager","tenant:member"]}`))
	require.NoError(t, err)
	require.Equal(t, uint64(2), set.Total, "role_codes__in 应命中 2 个用户")
	require.ElementsMatch(t, []uint32{adminUserID, memberUserID}, idsOf(set))

	// 交集：role_code + status 组合后 total/items 都基于交集
	inter, err := repo.List(tenantCtx, pageQueryReq(`{"role_code":"tenant:manager","status":"NORMAL"}`))
	require.NoError(t, err)
	require.Equal(t, uint64(1), inter.Total)
	require.Equal(t, []uint32{adminUserID}, idsOf(inter))

	// 交集：role_code 命中但 status 不匹配 → 空（不退化）
	emptyInter, err := repo.List(tenantCtx, pageQueryReq(`{"role_code":"tenant:manager","status":"LOCKED"}`))
	require.NoError(t, err)
	require.Zero(t, emptyInter.Total, "角色命中但状态不匹配应为空，不得返回全量")
}

// TestUserRepoSqlite_RoleCodeFilterNoDegrade 覆盖 §6 的空结果退化路径：
// 编码不存在、显式空数组、非数组标量都必须返回空，不得落回全量。
func TestUserRepoSqlite_RoleCodeFilterNoDegrade(t *testing.T) {
	repo, tenantCtx, _, _, _, _, noRoleUserID := newRoleCodeFilterFixture(t)

	// 基线：无该条件时返回全部 3 个用户，用于对照“不退化”
	all, err := repo.List(tenantCtx, pageQueryReq(`{}`))
	require.NoError(t, err)
	require.Equal(t, uint64(3), all.Total)

	// 编码在目标租户不存在 → 空结果（关键：不得退化为全量）
	missing, err := repo.List(tenantCtx, pageQueryReq(`{"role_code":"tenant:does_not_exist"}`))
	require.NoError(t, err)
	require.Zero(t, missing.Total, "编码不存在必须返回空，不得退化为全部用户")
	require.Empty(t, idsOf(missing))

	// 显式空数组 → 空结果
	emptyArr, err := repo.List(tenantCtx, pageQueryReq(`{"role_codes__in":[]}`))
	require.NoError(t, err)
	require.Zero(t, emptyArr.Total, "空数组必须返回空，不得视为无条件")

	// 非数组标量 → 空结果（契约要求数组）
	scalar, err := repo.List(tenantCtx, pageQueryReq(`{"role_codes__in":"tenant:manager"}`))
	require.NoError(t, err)
	require.Zero(t, scalar.Total, "非数组标量必须返回空，不得当作无条件")

	// 该用户确实存在，只是没有角色；确认上面的空不是把用户删没了
	_ = noRoleUserID
	again, err := repo.List(tenantCtx, pageQueryReq(`{}`))
	require.NoError(t, err)
	require.Equal(t, uint64(3), again.Total)
}

// TestUserRepoSqlite_RoleCodeFilterSubtreeFailClosed 验证 role_code 被放进
// $and/$or 子树时 fail-closed（400），不得因白名单静默丢弃而退化为全量。
func TestUserRepoSqlite_RoleCodeFilterSubtreeFailClosed(t *testing.T) {
	repo, tenantCtx, _, _, _, _, _ := newRoleCodeFilterFixture(t)

	_, err := repo.List(tenantCtx, pageQueryReq(`{"$and":[{"role_code":"tenant:manager"}]}`))
	require.True(t, identityV1.IsBadRequest(err), "role_code 位于 $and 子树应返回 400，实际 %v", err)

	_, err = repo.List(tenantCtx, pageQueryReq(`{"$or":[{"role_code":"tenant:manager"}]}`))
	require.True(t, identityV1.IsBadRequest(err), "role_code 位于 $or 子树应返回 400，实际 %v", err)
}

// TestUserRepoSqlite_RoleCodeFilterUnionWithRoleID 验证 role_code 与 role_id
// 混用时取并集去重后再取用户交集。
func TestUserRepoSqlite_RoleCodeFilterUnionWithRoleID(t *testing.T) {
	repo, tenantCtx, _, memberRoleID, adminUserID, memberUserID, _ := newRoleCodeFilterFixture(t)

	// role_code=tenant:manager（→adminUser） 并 role_id=memberRole（→memberUser）
	resp, err := repo.List(tenantCtx, pageQueryReq(
		fmt.Sprintf(`{"role_code":"tenant:manager","role_id":"%d"}`, memberRoleID)))
	require.NoError(t, err)
	require.Equal(t, uint64(2), resp.Total, "编码与 ID 混用应取并集，命中 2 个用户")
	require.ElementsMatch(t, []uint32{adminUserID, memberUserID}, idsOf(resp))
}

// TestUserRepoSqlite_RoleCodeFilterTenantScope 验证租户边界：
// 平台/系统上下文必须显式给出顶层 EQ 数字 tenant_id，否则 400；给出后精确命中目标租户。
func TestUserRepoSqlite_RoleCodeFilterTenantScope(t *testing.T) {
	repo, _, _, _, adminUserID, _, _ := newRoleCodeFilterFixture(t)
	sysCtx := testutil.NewSystemViewerCtx(context.Background())

	// 平台上下文缺 tenant_id → 400
	_, err := repo.List(sysCtx, pageQueryReq(`{"role_code":"tenant:manager"}`))
	require.True(t, identityV1.IsBadRequest(err), "平台上下文缺 tenant_id 应 400，实际 %v", err)

	// tenant_id 非 EQ → 400
	_, err = repo.List(sysCtx, pageQueryReq(
		fmt.Sprintf(`{"tenant_id__ne":"%d","role_code":"tenant:manager"}`, roleCodeFilterTenantID)))
	require.True(t, identityV1.IsBadRequest(err), "tenant_id 非 EQ 应 400，实际 %v", err)

	// tenant_id 非数字 → 400
	_, err = repo.List(sysCtx, pageQueryReq(`{"tenant_id":"not-a-number","role_code":"tenant:manager"}`))
	require.True(t, identityV1.IsBadRequest(err), "tenant_id 非数字应 400，实际 %v", err)

	// tenant_id 顶层 EQ 数字 → 精确命中目标租户管理员
	ok, err := repo.List(sysCtx, pageQueryReq(
		fmt.Sprintf(`{"tenant_id":"%d","role_code":"tenant:manager"}`, roleCodeFilterTenantID)))
	require.NoError(t, err)
	require.Equal(t, uint64(1), ok.Total)
	require.Equal(t, []uint32{adminUserID}, idsOf(ok))
}

// TestUserRepoSqlite_RoleCodeFilterCursorPathConsistency 验证游标路径与旧页码
// 路径语义一致：role_code 筛选下两条路径 total 相同、游标翻页不退化。
func TestUserRepoSqlite_RoleCodeFilterCursorPathConsistency(t *testing.T) {
	repo, tenantCtx, _, _, adminUserID, memberUserID, _ := newRoleCodeFilterFixture(t)
	setUserCursorSecretForTest(t)

	query := `{"role_codes__in":["tenant:manager","tenant:member"]}`

	oldPath, err := repo.List(tenantCtx, pageQueryReq(query))
	require.NoError(t, err)
	require.Equal(t, uint64(2), oldPath.Total)

	cursorResp, err := repo.List(tenantCtx, &paginationV1.PagingRequest{
		Limit:         trans.Ptr(uint32(1)),
		FilteringType: &paginationV1.PagingRequest_Query{Query: query},
	})
	require.NoError(t, err)
	require.Equal(t, uint64(2), cursorResp.Total, "游标路径 total 应与页码路径一致")
	require.Len(t, cursorResp.GetItems(), 1)
	require.NotEmpty(t, cursorResp.GetNextCursor())

	second, err := repo.List(tenantCtx, &paginationV1.PagingRequest{
		Limit:         trans.Ptr(uint32(1)),
		FilteringType: &paginationV1.PagingRequest_Query{Query: query},
		Cursor:        trans.Ptr(cursorResp.GetNextCursor()),
	})
	require.NoError(t, err)
	require.Equal(t, uint64(2), second.Total, "翻页后 total 不变")
	secondID := idsOf(second)[0]
	require.ElementsMatch(t, []uint32{adminUserID, memberUserID},
		[]uint32{idsOf(cursorResp)[0], secondID}, "两页应覆盖两个命中用户，不重不漏")
}
