package service

import (
	"context"
	"testing"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/stretchr/testify/require"
	modeldevv1 "go-wind-admin/api/gen/go/modeldev/service/v1"
	"go-wind-admin/app/admin/service/internal/data"
	"go-wind-admin/pkg/middleware/auth"
)

// Design §17.2 step 1 pauses every entry that starts new work: training
// admission, environment Ensure and Dataset create/import/retry. Queries and
// the commands that close in-flight work stay reachable, or the drain could not
// be observed and cancelled.
func TestModelDevDatasetMaintenanceBlocksNewWorkOnly(t *testing.T) {
	const datasetID = "11111111-2222-4333-8444-555555555555"
	s := NewModelDevService(nil, nil, nil, nil, nil)
	s.SetCreateMaintenance(NewModelDevCreateMaintenance(true))
	ctx := auth.NewPrincipalContext(context.Background(), &auth.Principal{Type: auth.SubjectUser, ID: 1, TenantID: 1})

	newWork := map[string]func() error{
		"create": func() error {
			_, err := s.CreateDataset(ctx, &modeldevv1.CreateDatasetRequest{IdempotencyKey: "k-1", Data: &modeldevv1.CreateDatasetData{Name: "set"}})
			return err
		},
		"import": func() error {
			_, err := s.ImportDatasetVersion(ctx, &modeldevv1.ImportDatasetVersionRequest{DatasetId: datasetID, IdempotencyKey: "k-2"})
			return err
		},
		"retry": func() error {
			_, err := s.RetryDatasetImport(ctx, &modeldevv1.RetryDatasetImportRequest{ImportTaskId: datasetID, IdempotencyKey: "k-3"})
			return err
		},
	}
	for name, call := range newWork {
		t.Run(name, func(t *testing.T) {
			err := call()
			require.NotNil(t, err, "paused deployment accepted new work")
			require.Equal(t, "MODELDEV_MAINTENANCE", kerrors.FromError(err).Reason)
		})
	}

	// Cancel stays outside the gate: a paused deployment still has to be able to
	// stop accepted imports. It proceeds past the gate and fails later for lack
	// of a database, which is exactly the difference being asserted.
	t.Run("cancel is not gated", func(t *testing.T) {
		_, err := s.CancelDatasetImport(ctx, &modeldevv1.CancelDatasetImportRequest{ImportTaskId: datasetID, IdempotencyKey: "k-4"})
		require.NotNil(t, err)
		require.NotEqual(t, "MODELDEV_MAINTENANCE", kerrors.FromError(err).Reason, "cancel must stay reachable during maintenance")
	})
	t.Run("query is not gated", func(t *testing.T) {
		_, err := s.GetDatasetImportTask(ctx, &modeldevv1.GetDatasetImportTaskRequest{ImportTaskId: datasetID})
		require.NotNil(t, err)
		require.NotEqual(t, "MODELDEV_MAINTENANCE", kerrors.FromError(err).Reason, "queries must stay reachable during maintenance")
	})
}

// An unauthenticated caller must be told nothing about the maintenance state:
// identity is resolved before the gate.
func TestModelDevDatasetMaintenanceDoesNotPrecedeAuthentication(t *testing.T) {
	s := NewModelDevService(nil, nil, nil, nil, nil)
	s.SetCreateMaintenance(NewModelDevCreateMaintenance(true))
	_, err := s.CreateDataset(context.Background(), &modeldevv1.CreateDatasetRequest{IdempotencyKey: "k-1"})
	require.Equal(t, 401, int(kerrors.FromError(err).Code))
}

// An open gate releases its slot even when the grant check fails, so a burst of
// refused requests cannot leak the drain counter and block maintenance forever.
func TestModelDevDatasetScopeReleasesTheGateOnEarlyFailure(t *testing.T) {
	gate := NewModelDevCreateMaintenance(false)
	s := NewModelDevService(nil, nil, nil, nil, nil)
	s.SetCreateMaintenance(gate)
	ctx := auth.NewPrincipalContext(context.Background(), &auth.Principal{Type: auth.SubjectUser, ID: 1, TenantID: 1})
	// No authorization repository is wired, so every call fails after entering
	// the gate.
	for i := 0; i < 5; i++ {
		if _, _, err := s.modelDevDatasetScope(ctx, data.ModelDevDatasetActionGet, true); err == nil {
			t.Fatal("an unwired service authorized a Dataset action")
		}
	}
	require.NoError(t, gate.pauseAndDrain(context.Background()), "a refused request leaked its gate slot")
}

func TestModelDevDatasetPausedIsServiceUnavailable(t *testing.T) {
	err := modelDevDatasetPaused()
	converted := kerrors.FromError(err)
	require.Equal(t, 503, int(converted.Code))
	require.Equal(t, "MODELDEV_MAINTENANCE", converted.Reason)
}
