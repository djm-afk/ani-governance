//go:build quota_pg

package data

import (
	"context"
	entsql "entgo.io/ent/dialect/sql"
	"errors"
	"go-wind-admin/app/admin/service/internal/data/ent/quotacharge"
	"go-wind-admin/app/admin/service/internal/data/ent/tenant"
	"testing"
	"time"

	entgo "entgo.io/ent"
	"github.com/stretchr/testify/require"
	"go-wind-admin/app/admin/service/internal/data/ent"
	"go-wind-admin/app/admin/service/internal/data/ent/quotaaccount"
	"go-wind-admin/app/admin/service/internal/data/ent/quotaoperation"
	appViewer "go-wind-admin/pkg/entgo/viewer"
	bLogger "go-wind-admin/pkg/localdeps/kratos-bootstrap/logger"
)

// A mutation hook must see the real admission writes. Rejecting the second
// charge must roll back the first charge, its account and the operation too.
func TestQuotaEntMutationHookRollback(t *testing.T) {
	c := newLedgerPGClient(t)
	cleanLedger(t, c)
	r := newTestRepo(c)
	r.log = bLogger.NewHelper(bLogger.NewStdLogger())
	tid, pid := seedTenantPlan(t, c, "ent-hook-rollback", 20)
	in := gpuLedgerInput(t, c.Client(), tid, pid)
	calls := 0
	c.Client().QuotaCharge.Use(func(next entgo.Mutator) entgo.Mutator {
		return entgo.MutateFunc(func(ctx context.Context, m entgo.Mutation) (entgo.Value, error) {
			calls++
			if calls == 2 {
				return nil, errors.New("test charge hook rejection")
			}
			return next.Mutate(ctx, m)
		})
	})
	_, err := r.Occupy(context.Background(), in)
	require.Error(t, err)
	require.Equal(t, 2, calls)
	ctx := appViewer.NewSystemViewerContext(context.Background())
	require.Zero(t, c.Client().QuotaOperation.Query().Where(quotaoperation.TenantIDEQ(tid)).CountX(ctx))
	require.Zero(t, c.Client().QuotaAccount.Query().Where(quotaaccount.TenantIDEQ(tid)).CountX(ctx))
	require.Zero(t, c.Client().QuotaCharge.Query().Where(quotacharge.TenantIDEQ(tid)).CountX(ctx))
}

func TestQuotaEntTransactionHooks(t *testing.T) {
	c := newLedgerPGClient(t)
	cleanLedger(t, c)
	ctx := appViewer.NewSystemViewerContext(context.Background())
	for _, abort := range []bool{false, true} {
		commits, rollbacks := 0, 0
		failure := errors.New("test abort")
		err := quotaTransaction(ctx, c.Client(), func(tx *ent.Tx) error {
			tx.OnCommit(func(next ent.Committer) ent.Committer {
				return ent.CommitFunc(func(ctx context.Context, tx *ent.Tx) error { commits++; return next.Commit(ctx, tx) })
			})
			tx.OnRollback(func(next ent.Rollbacker) ent.Rollbacker {
				return ent.RollbackFunc(func(ctx context.Context, tx *ent.Tx) error { rollbacks++; return next.Rollback(ctx, tx) })
			})
			if abort {
				return failure
			}
			return nil
		})
		if abort {
			require.ErrorIs(t, err, failure)
			require.Equal(t, 0, commits)
			require.Equal(t, 1, rollbacks)
		} else {
			require.NoError(t, err)
			require.Equal(t, 1, commits)
			require.Equal(t, 0, rollbacks)
		}
	}
}

// Database time is the contract for persistence timestamps, not the process
// clock. The hook reads CURRENT_TIMESTAMP through the very same Ent transaction.
func TestQuotaEntDatabaseTimestamps(t *testing.T) {
	c := newLedgerPGClient(t)
	cleanLedger(t, c)
	r := newTestRepo(c)
	tid, pid := seedTenantPlan(t, c, "ent-database-clock", 20)
	in := gpuLedgerInput(t, c.Client(), tid, pid)
	checked := map[string]bool{}
	check := func(next entgo.Mutator) entgo.Mutator {
		return entgo.MutateFunc(func(ctx context.Context, m entgo.Mutation) (entgo.Value, error) {
			client := m.(interface{ Client() *ent.Client }).Client()
			var rows []struct {
				Now time.Time `json:"database_now"`
			}
			err := client.Tenant.Query().Where(tenant.IDEQ(tid)).Modify(func(s *entsql.Selector) {
				s.SelectExpr(entsql.Expr("CURRENT_TIMESTAMP AS database_now"))
			}).Scan(ctx, &rows)
			if err != nil {
				return nil, err
			}
			require.Len(t, rows, 1)
			for _, field := range []string{"created_at", "updated_at"} {
				if value, ok := m.Field(field); ok {
					require.True(t, rows[0].Now.Equal(value.(time.Time)), "%s.%s must use transaction time", m.Type(), field)
					checked[m.Type()+"."+field] = true
				}
			}
			return next.Mutate(ctx, m)
		})
	}
	c.Client().QuotaAccount.Use(check)
	c.Client().QuotaOperation.Use(check)
	c.Client().QuotaCharge.Use(check)
	c.Client().GpuDeleteAcceptance.Use(check)
	c.Client().QuotaReleaseReceipt.Use(check)
	c.Client().PlanQuota.Use(check)
	ctx := context.Background()
	created, err := r.Occupy(ctx, in)
	require.NoError(t, err)

	require.NoError(t, r.CancelUnsent(ctx, tid, created.OperationID))
	for _, field := range []string{"QuotaAccount.created_at", "QuotaAccount.updated_at", "QuotaOperation.created_at", "QuotaOperation.updated_at", "QuotaCharge.created_at", "QuotaCharge.updated_at"} {
		require.True(t, checked[field], "real ledger admission and cancellation must exercise %s", field)
	}
}

func TestQuotaEntTransactionFailures(t *testing.T) {
	for _, mode := range []string{"panic", "cancel", "commit-error"} {
		t.Run(mode, func(t *testing.T) {
			c := newLedgerPGClient(t)
			cleanLedger(t, c)
			ctx, cancel := context.WithCancel(appViewer.NewSystemViewerContext(context.Background()))
			defer cancel()
			failure := errors.New("injected commit failure")
			rollbacks := 0
			var id uint32
			run := func() error {
				return quotaTransaction(ctx, c.Client(), func(tx *ent.Tx) error {
					tx.OnRollback(func(next ent.Rollbacker) ent.Rollbacker {
						return ent.RollbackFunc(func(ctx context.Context, tx *ent.Tx) error { rollbacks++; return next.Rollback(ctx, tx) })
					})
					p, err := tx.Plan.Create().SetName("must-rollback-" + mode).Save(ctx)
					if err != nil {
						return err
					}
					id = p.ID
					switch mode {
					case "panic":
						panic("quota transaction panic")
					case "cancel":
						cancel()
						return ctx.Err()
					case "commit-error":
						tx.OnCommit(func(ent.Committer) ent.Committer {
							return ent.CommitFunc(func(context.Context, *ent.Tx) error { return failure })
						})
					}
					return nil
				})
			}
			if mode == "panic" {
				require.PanicsWithValue(t, "quota transaction panic", func() { _ = run() })
			} else {
				err := run()
				if mode == "cancel" {
					require.ErrorIs(t, err, context.Canceled)
				} else {
					require.ErrorIs(t, err, failure)
				}
			}
			require.Equal(t, 1, rollbacks)
			require.NotZero(t, id)
			_, err := c.Client().Plan.Get(appViewer.NewSystemViewerContext(context.Background()), id)
			require.True(t, ent.IsNotFound(err), "failed transaction must not persist its writes: %v", err)
		})
	}
}
