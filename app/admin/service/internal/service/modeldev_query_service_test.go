package service

import (
	"testing"
	"time"

	modeldevcontractv1 "github.com/zhangzhe-ctrl/ani-modeldev-service/api/ani/modeldev/v1"
	trainingcontractv1 "github.com/zhangzhe-ctrl/ani-modeldev-service/api/ani/training/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// The public detail must expose the derived timeline and curve unchanged; any
// missing timestamp stays empty instead of becoming an epoch value.
func TestModelDevPublicExecutionPassesThroughStepsAndMetrics(t *testing.T) {
	started := time.Date(2026, 10, 22, 1, 2, 3, 0, time.UTC)
	step := int64(7)
	in := &modeldevcontractv1.ExecutionView{
		Identity:   &trainingcontractv1.ExecutionIdentity{OperationId: "operation", ExecutionId: "execution"},
		States:     &modeldevcontractv1.ExecutionStates{},
		AcceptedAt: timestamppb.New(started), DeadlineAt: timestamppb.New(started), ObservedAt: timestamppb.New(started),
		Steps: []*modeldevcontractv1.StepProgress{
			{Role: "prepare", State: "SUCCEEDED", StartedAt: timestamppb.New(started), FinishedAt: timestamppb.New(started.Add(time.Minute)), ExitCode: 0},
			{Role: "train-wait", State: "RUNNING", StartedAt: timestamppb.New(started.Add(time.Minute)), DeadlineAt: timestamppb.New(started.Add(time.Hour))},
			{Role: "export", State: "PENDING"},
		},
		Metrics: []*trainingcontractv1.Metric{{Name: "train.loss", DecimalValue: "0.87", Step: &step}},
	}
	out := modelDevPublicExecution(in)
	if len(out.Steps) != 3 || len(out.Metrics) != 1 {
		t.Fatalf("steps=%d metrics=%d, want 3 and 1", len(out.Steps), len(out.Metrics))
	}
	prepare := out.Steps[0]
	if prepare.Role != "prepare" || prepare.State != "SUCCEEDED" || prepare.StartedAt != started.Format(time.RFC3339Nano) || prepare.FinishedAt != started.Add(time.Minute).Format(time.RFC3339Nano) {
		t.Fatalf("prepare=%+v", prepare)
	}
	trainWait := out.Steps[1]
	if trainWait.FinishedAt != "" || trainWait.DeadlineAt != started.Add(time.Hour).Format(time.RFC3339Nano) {
		t.Fatalf("train-wait=%+v", trainWait)
	}
	pending := out.Steps[2]
	if pending.StartedAt != "" || pending.FinishedAt != "" || pending.DeadlineAt != "" {
		t.Fatalf("pending=%+v, want no times", pending)
	}
	metric := out.Metrics[0]
	if metric.Name != "train.loss" || metric.DecimalValue != "0.87" || metric.Step == nil || *metric.Step != 7 {
		t.Fatalf("metric=%+v", metric)
	}
}
