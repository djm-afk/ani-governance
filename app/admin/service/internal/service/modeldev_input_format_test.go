package service

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	modeldevcontractv1 "github.com/zhangzhe-ctrl/ani-modeldev-service/api/ani/modeldev/v1"
	trainingcontractv1 "github.com/zhangzhe-ctrl/ani-modeldev-service/api/ani/training/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// The base model identity must be published verbatim so a client can name what a
// finetuning run started from. Absent for general training.
func TestModelDevPublicExecutionExposesBaseModel(t *testing.T) {
	in := &modeldevcontractv1.ExecutionView{
		Identity:   &trainingcontractv1.ExecutionIdentity{OperationId: "operation", ExecutionId: "execution"},
		States:     &modeldevcontractv1.ExecutionStates{},
		AcceptedAt: timestamppb.New(time.Now()), DeadlineAt: timestamppb.New(time.Now()), ObservedAt: timestamppb.New(time.Now()),
		BaseModel: &modeldevcontractv1.BaseModelIdentity{
			ModelId: "66666666-6666-4666-8666-666666666666", VersionId: "77777777-7777-4777-8777-777777777777",
			Family: "Qwen2.5-0.5B-Instruct", Format: "safetensors",
		},
	}
	out := modelDevPublicExecution(in)
	require.NotNil(t, out.BaseModel)
	require.Equal(t, "66666666-6666-4666-8666-666666666666", out.BaseModel.ModelId)
	require.Equal(t, "77777777-7777-4777-8777-777777777777", out.BaseModel.VersionId)
	require.Equal(t, "Qwen2.5-0.5B-Instruct", out.BaseModel.Family)
	require.Equal(t, "safetensors", out.BaseModel.Format)

	// General training carries no base model, and the detail must not invent one.
	general := &modeldevcontractv1.ExecutionView{
		Identity:   &trainingcontractv1.ExecutionIdentity{OperationId: "operation", ExecutionId: "execution"},
		States:     &modeldevcontractv1.ExecutionStates{},
		AcceptedAt: timestamppb.New(time.Now()), DeadlineAt: timestamppb.New(time.Now()), ObservedAt: timestamppb.New(time.Now()),
	}
	require.Nil(t, modelDevPublicExecution(general).BaseModel)
}

// A general training execution publishes the catalogue image it runs and leaves
// the retired image_version_id empty. Finetuning keeps its own frozen identity
// and publishes no training image facts.
func TestModelDevPublicExecutionSplitsTrainingAndFinetuningImageIdentity(t *testing.T) {
	base := func(kind trainingcontractv1.ExecutionKind) *modeldevcontractv1.ExecutionView {
		return &modeldevcontractv1.ExecutionView{
			Identity:   &trainingcontractv1.ExecutionIdentity{OperationId: "operation", ExecutionId: "execution"},
			Kind:       kind,
			States:     &modeldevcontractv1.ExecutionStates{},
			AcceptedAt: timestamppb.New(time.Now()), DeadlineAt: timestamppb.New(time.Now()), ObservedAt: timestamppb.New(time.Now()),
		}
	}

	training := base(trainingcontractv1.ExecutionKind_EXECUTION_KIND_GENERAL_TRAINING)
	training.ImageVersionId = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	training.TrainingImageId = "img_" + strings.Repeat("a", 32)
	training.TrainingImageScope = "platform"
	training.TrainingImageDigest = "sha256:" + strings.Repeat("e", 64)
	training.TrainingImageRegistryAuthority = "registry.example.test"
	out := modelDevPublicExecution(training)
	require.Empty(t, out.ImageVersionId, "training must not publish the retired image identity")
	require.Equal(t, training.TrainingImageId, out.TrainingImageId)
	require.Equal(t, "platform", out.TrainingImageScope)
	require.Equal(t, training.TrainingImageDigest, out.TrainingImageDigest)
	require.Equal(t, "registry.example.test", out.TrainingImageRegistryAuthority)

	finetuning := base(trainingcontractv1.ExecutionKind_EXECUTION_KIND_FINETUNING)
	finetuning.ImageVersionId = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	fine := modelDevPublicExecution(finetuning)
	require.Equal(t, "ffffffff-ffff-4fff-8fff-ffffffffffff", fine.ImageVersionId)
	require.Empty(t, fine.TrainingImageId)
	require.Empty(t, fine.TrainingImageScope)
	require.Empty(t, fine.TrainingImageDigest)
	require.Empty(t, fine.TrainingImageRegistryAuthority)
}

// Only the two verified formats may be forwarded upstream; anything else has to
// be rejected here rather than becoming an empty page.
func TestModelDevInputFormatSelection(t *testing.T) {
	format, ok := modelDevInputFormat(nil)
	require.True(t, ok)
	require.Nil(t, format)

	for _, value := range []string{"CSV", "JSON"} {
		value := value
		selected, ok := modelDevInputFormat(&value)
		require.True(t, ok, value)
		require.NotNil(t, selected)
		require.Equal(t, value, *selected)
	}

	for _, value := range []string{"", "csv", "json", "PARQUET", "CSV,JSON", " CSV"} {
		value := value
		_, ok := modelDevInputFormat(&value)
		require.False(t, ok, value)
	}
}
