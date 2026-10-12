package data

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	paginationV1 "go-wind-admin/pkg/localdeps/go-crud/api/gen/go/pagination/v1"
	"go-wind-admin/pkg/localdeps/go-utils/trans"

	permissionV1 "go-wind-admin/api/gen/go/permission/service/v1"
	"go-wind-admin/app/admin/service/internal/data/ent/permissiongroup"
	"go-wind-admin/app/admin/service/tests/testutil"
)

// itoa 是 strconv.FormatUint 的简写，用于拼 query 里的分组 ID。
func itoa(id uint32) string {
	return strconv.FormatUint(uint64(id), 10)
}

// fieldMaskParent 是“只更新 parent_id”的字段掩码。
var fieldMaskParent = &fieldmaskpb.FieldMask{Paths: []string{"parent_id"}}

// buildGroupTree 造一棵分组树：root → child → grandchild，另有一个独立 root2。
// 返回各节点 ID。
func buildGroupTree(t *testing.T, repo *PermissionGroupRepo, ctx context.Context) (root, child, grandchild, root2 uint32) {
	t.Helper()
	mk := func(name string, parent *uint32) uint32 {
		dto, err := repo.Create(ctx, &permissionV1.CreatePermissionGroupRequest{
			Data: &permissionV1.PermissionGroup{Name: trans.Ptr(name), ParentId: parent},
		})
		require.NoError(t, err)
		return dto.GetId()
	}
	root = mk("subtree-root", nil)
	child = mk("subtree-child", trans.Ptr(root))
	grandchild = mk("subtree-grandchild", trans.Ptr(child))
	root2 = mk("subtree-root2", nil)
	return root, child, grandchild, root2
}

// TestPermissionGroupRepoSqlite_ExcludeSubtree 验证 exclude_subtree 排除自身及
// 全部后代，且 total 与 items 一致（Count 同步）。
func TestPermissionGroupRepoSqlite_ExcludeSubtree(t *testing.T) {
	entClient := testutil.NewEntClientForTest(t)
	repo := newPermissionGroupRepoSqlite(t, entClient)
	ctx := testutil.NewSystemViewerCtx(context.Background())

	root, child, grandchild, root2 := buildGroupTree(t, repo, ctx)

	filtered, err := repo.List(ctx, &paginationV1.PagingRequest{
		FilteringType: &paginationV1.PagingRequest_Query{Query: `{"exclude_subtree":"` + itoa(root) + `"}`},
	}, false)
	require.NoError(t, err)
	require.Equal(t, uint64(1), filtered.Total, "排除 root 子树后应只剩 root2，total 应同步")
	require.Len(t, filtered.Items, 1)
	require.Equal(t, root2, filtered.Items[0].GetId(), "结果不应包含被排除子树中的任何节点")

	// 排除 child 子树：保留 root、root2，排除 child 与 grandchild
	mid, err := repo.List(ctx, &paginationV1.PagingRequest{
		FilteringType: &paginationV1.PagingRequest_Query{Query: `{"exclude_subtree":"` + itoa(child) + `"}`},
	}, false)
	require.NoError(t, err)
	require.Equal(t, uint64(2), mid.Total)
	got := map[uint32]bool{}
	for _, it := range mid.Items {
		got[it.GetId()] = true
	}
	require.True(t, got[root] && got[root2], "应保留 root 与 root2")
	require.False(t, got[child] || got[grandchild], "应排除 child 与 grandchild")
}

// TestPermissionGroupRepoSqlite_ExcludeSubtreeNoop 验证空值/不存在 id 按无排除处理，
// 不报错、不改变结果集。
func TestPermissionGroupRepoSqlite_ExcludeSubtreeNoop(t *testing.T) {
	entClient := testutil.NewEntClientForTest(t)
	repo := newPermissionGroupRepoSqlite(t, entClient)
	ctx := testutil.NewSystemViewerCtx(context.Background())

	buildGroupTree(t, repo, ctx)

	// 不存在的 id：无排除
	miss, err := repo.List(ctx, &paginationV1.PagingRequest{
		FilteringType: &paginationV1.PagingRequest_Query{Query: `{"exclude_subtree":"987654"}`},
	}, false)
	require.NoError(t, err)
	require.Equal(t, uint64(4), miss.Total, "不存在的 id 应按无排除处理")

	// 空值：无排除
	empty, err := repo.List(ctx, &paginationV1.PagingRequest{
		FilteringType: &paginationV1.PagingRequest_Query{Query: `{"exclude_subtree":""}`},
	}, false)
	require.NoError(t, err)
	require.Equal(t, uint64(4), empty.Total, "空值应按无排除处理")
}

// TestPermissionGroupRepoSqlite_ExcludeSubtreeDirtyData 验证脏数据（环）下
// exclude_subtree 不死循环、不 500。
//
// 注：SQLite 夹具开启了外键（见 testutil），无法注入“悬空 parent_id”；该形态只可能
// 来自历史/同步的无外键写入。此处覆盖可由夹具构造的“环”，其 visited 防环与悬空同源。
func TestPermissionGroupRepoSqlite_ExcludeSubtreeDirtyData(t *testing.T) {
	entClient := testutil.NewEntClientForTest(t)
	repo := newPermissionGroupRepoSqlite(t, entClient)
	ctx := testutil.NewSystemViewerCtx(context.Background())
	ec := entClient.Client()

	a, b, _, _ := buildGroupTree(t, repo, ctx)

	// 制造环：a.parent_id = b（b 已是 a 的后代），形成 a→b→...→a
	require.NoError(t, ec.PermissionGroup.UpdateOneID(a).SetParentID(b).Exec(ctx))

	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := repo.List(ctx, &paginationV1.PagingRequest{
			FilteringType: &paginationV1.PagingRequest_Query{Query: `{"exclude_subtree":"` + itoa(a) + `"}`},
		}, false)
		require.NoError(t, err)
		require.NotNil(t, resp)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("exclude_subtree 遇环死循环未返回")
	}
}

// TestPermissionGroupRepoSqlite_UpdateCycleRejected 验证 Update 把 parent_id 设为
// 自身或后代 → 400 且数据库无环残留（落库前预检生效）。
func TestPermissionGroupRepoSqlite_UpdateCycleRejected(t *testing.T) {
	entClient := testutil.NewEntClientForTest(t)
	repo := newPermissionGroupRepoSqlite(t, entClient)
	ctx := testutil.NewSystemViewerCtx(context.Background())
	ec := entClient.Client()

	root, child, grandchild, _ := buildGroupTree(t, repo, ctx)

	// 设为自身
	err := repo.Update(ctx, &permissionV1.UpdatePermissionGroupRequest{
		Id:         root,
		UpdateMask: fieldMaskParent,
		Data:       &permissionV1.PermissionGroup{ParentId: trans.Ptr(root)},
	})
	require.True(t, permissionV1.IsBadRequest(err), "把父设为自身应 400，实际 %v", err)

	// 设为后代（grandchild）
	err = repo.Update(ctx, &permissionV1.UpdatePermissionGroupRequest{
		Id:         root,
		UpdateMask: fieldMaskParent,
		Data:       &permissionV1.PermissionGroup{ParentId: trans.Ptr(grandchild)},
	})
	require.True(t, permissionV1.IsBadRequest(err), "把父设为后代应 400，实际 %v", err)

	// 数据库无环残留：root.parent_id 仍为空，child.parent_id 仍为 root
	rootRow, err := ec.PermissionGroup.Query().Where(permissiongroup.IDEQ(root)).Only(ctx)
	require.NoError(t, err)
	require.True(t, rootRow.ParentID == nil || *rootRow.ParentID == 0, "被拒绝的更新不应改变 root.parent_id")
	childRow, err := ec.PermissionGroup.Query().Where(permissiongroup.IDEQ(child)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, root, *childRow.ParentID, "child.parent_id 应保持指向 root")
}

// TestPermissionGroupRepoSqlite_UpdateParentRepathsSubtree 验证合法的改父会原子重算
// 子树物化路径。
func TestPermissionGroupRepoSqlite_UpdateParentRepathsSubtree(t *testing.T) {
	entClient := testutil.NewEntClientForTest(t)
	repo := newPermissionGroupRepoSqlite(t, entClient)
	ctx := testutil.NewSystemViewerCtx(context.Background())
	ec := entClient.Client()

	_, child, grandchild, root2 := buildGroupTree(t, repo, ctx)

	// 把 child 移到 root2 下
	require.NoError(t, repo.Update(ctx, &permissionV1.UpdatePermissionGroupRequest{
		Id:         child,
		UpdateMask: fieldMaskParent,
		Data:       &permissionV1.PermissionGroup{ParentId: trans.Ptr(root2)},
	}))

	wantChild := "/" + itoa(root2) + "/" + itoa(child) + "/"
	wantGrand := wantChild + itoa(grandchild) + "/"

	childRow, err := ec.PermissionGroup.Query().Where(permissiongroup.IDEQ(child)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, wantChild, *childRow.Path, "child 路径应重算到新父下")
	grandRow, err := ec.PermissionGroup.Query().Where(permissiongroup.IDEQ(grandchild)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, wantGrand, *grandRow.Path, "grandchild 路径应随之重算")
}

// TestPermissionGroupRepoSqlite_CreateInvalidParent 验证建组时父不存在/自身成父被拒。
func TestPermissionGroupRepoSqlite_CreateInvalidParent(t *testing.T) {
	entClient := testutil.NewEntClientForTest(t)
	repo := newPermissionGroupRepoSqlite(t, entClient)
	ctx := testutil.NewSystemViewerCtx(context.Background())

	// 父不存在
	_, err := repo.Create(ctx, &permissionV1.CreatePermissionGroupRequest{
		Data: &permissionV1.PermissionGroup{Name: trans.Ptr("bad-parent"), ParentId: trans.Ptr(uint32(999999))},
	})
	require.True(t, permissionV1.IsBadRequest(err), "父不存在应 400，实际 %v", err)

	// 自身成父（显式指定主键 = parent_id）
	_, err = repo.Create(ctx, &permissionV1.CreatePermissionGroupRequest{
		Data: &permissionV1.PermissionGroup{Id: trans.Ptr(uint32(555)), Name: trans.Ptr("self-parent"), ParentId: trans.Ptr(uint32(555))},
	})
	require.True(t, permissionV1.IsBadRequest(err), "自身成父应 400，实际 %v", err)
}
