package activities

import (
	"context"

	"go.temporal.io/sdk/activity"
)

// ScaleInput drives both the forward scale-up and the compensating scale-down.
// ImageTag is a full image reference (e.g. "nginx:1.27-alpine").
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

// ScaleCanary rolls the new image onto the canary Deployment and scales it up.
// Idempotent: setImage and scaleDeployment both express desired state, so a
// retry or post-crash re-run converges instead of stacking pods. Readiness is
// not awaited here — the HealthCheck activity owns the bake.
func ScaleCanary(ctx context.Context, in ScaleInput) (ScaleResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("scale canary", "service", in.Service, "image", in.ImageTag, "replicas", in.Replicas)

	name := canaryName(in.Service)
	if err := setImage(ctx, name, in.ImageTag); err != nil {
		return ScaleResult{}, err
	}
	if err := scaleDeployment(ctx, name, int32(in.Replicas)); err != nil {
		return ScaleResult{}, err
	}
	ready, _, err := deploymentStatus(ctx, name)
	if err != nil {
		return ScaleResult{}, err
	}
	return ScaleResult{ReadyReplicas: int(ready)}, nil
}

// ScaleDownCanary is the compensation for ScaleCanary: drive the canary back to
// zero replicas. Setting an absolute target (not decrementing) keeps it
// idempotent, so running it twice during a messy rollback is safe.
func ScaleDownCanary(ctx context.Context, in ScaleInput) error {
	activity.GetLogger(ctx).Info("scale down canary (compensation)", "service", in.Service)
	return scaleDeployment(ctx, canaryName(in.Service), 0)
}
