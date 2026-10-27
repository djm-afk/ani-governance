package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	contractv1 "github.com/liangzai006/ani-inference-service/api/inference/contract/v1"
	modelv1 "github.com/liangzai006/ani-model-service/api/model/v1"
	"github.com/stretchr/testify/require"
	"go-wind-admin/app/admin/service/internal/commandcontract"
	"go-wind-admin/pkg/middleware/auth"
)

func TestInferenceCatalogSelectionFrozenCommand(t *testing.T) {
	for _, scope := range []string{"tenant", "platform"} {
		t.Run(scope, func(t *testing.T) {
			intent := inferencePublicIntent()
			intent.EngineImage = ""
			intent.ImageId = "img_" + strings.Repeat("a", 32)
			intent.ImageScope = scope
			binding := NewInferenceComputeBinding(nil, nil, &inferenceModelProbe{response: &modelv1.GetModelVersionResponse{Version: &modelv1.ModelVersion{Id: intent.ModelVersionId, Status: "ready"}}}, &inferenceComputeProbe{selection: inferenceCPUSelection(t)})
			tenant := "11111111-1111-4111-8111-111111111111"
			createID := "20000000-0000-4000-8000-000000000001"
			resourceID := "20000000-0000-4000-8000-000000000002"
			prepared, err := binding.PrepareManagedBusiness(context.Background(), &auth.Principal{Type: auth.SubjectUser, ID: 7, TenantID: 42}, tenant, intent)
			require.NoError(t, err)
			raw, err := prepared.freeze(tenant, "user", "7", createID, resourceID, nil)
			require.NoError(t, err, "Resource catalog img_ IDs must cross the actual frozen command boundary")
			stored := commandcontract.StoredCommand{Owner: commandcontract.InferenceOwner, Action: InferenceCreateAction, TenantID: tenant, ResourceID: resourceID, OperationID: createID, ActorType: "user", ActorID: "7", Canonical: raw, RequireChargeIDs: true}
			facts, err := commandcontract.ValidateStoredCommand(stored)
			require.NoError(t, err)
			business, err := contractv1.DecodeBusinessPayload(facts.BusinessPayload)
			require.NoError(t, err)
			require.EqualValues(t, 2, business.ContractVersion)
			require.Equal(t, intent.ImageId, business.Engine.ImageId)
			require.Equal(t, scope, business.Engine.ImageScope)
			require.Empty(t, business.Engine.Image)
			require.Equal(t, intent.Command, business.Engine.Command)
			canonical, err := commandcontract.DecodeInferenceCanonical(raw)
			require.NoError(t, err)
			business.Engine.ImageId = "img_" + strings.Repeat("b", 32)
			canonical.BusinessPayload, err = contractv1.CanonicalBusinessPayload(business)
			require.NoError(t, err)
			digest := sha256.Sum256(canonical.BusinessPayload)
			canonical.BusinessPayloadDigest = hex.EncodeToString(digest[:])
			changed, err := commandcontract.CanonicalJSON(canonical)
			require.NoError(t, err)
			stored.Canonical = changed
			_, err = commandcontract.ValidateStoredCommand(stored)
			require.Error(t, err, "changing selection cannot keep the original command hash")
			stored.Canonical = raw
			stored.TenantID = "11111111-1111-4111-8111-111111111112"
			_, err = commandcontract.ValidateStoredCommand(stored)
			require.Error(t, err, "catalog selection retains the original tenant binding")
		})
	}
}
