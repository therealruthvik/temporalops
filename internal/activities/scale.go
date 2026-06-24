package activities

import (
	"context"

	"go.temporal.io/sdk/activity"
)

// ScaleInput drives both the forward scale-up and the compensating scale-down.
type ScaleInput struct {
	Service  string
	ImageTag string
	Replicas int
}

// ScaleResult reports observed state so the workflow logs reflect reality
// rather than intent.
type ScaleResult struct {
	ReadyReplicas int
}

// ScaleCanary deploys canary replicas of the new image. Idempotent by design:
// it expresses a desired replica count, so a retry or a post-crash re-run
// converges to the same state instead of stacking extra pods. Mocked in
// Stage 2; backed by client-go in Stage 3.
func ScaleCanary(ctx context.Context, in ScaleInput) (ScaleResult, error) {
	activity.GetLogger(ctx).Info("scale canary", "service", in.Service, "image", in.ImageTag, "replicas", in.Replicas)
	return ScaleResult{ReadyReplicas: in.Replicas}, nil
}

// ScaleDownCanary is the compensation for ScaleCanary: drive the canary
// Deployment back to zero replicas. Setting a target (not decrementing) keeps
// it idempotent, so running it twice during a messy rollback is safe.
func ScaleDownCanary(ctx context.Context, in ScaleInput) error {
	activity.GetLogger(ctx).Info("scale down canary (compensation)", "service", in.Service, "replicas", in.Replicas)
	return nil
}
