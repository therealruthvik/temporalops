package activities

import (
	"context"

	"go.temporal.io/sdk/activity"
)

// PromoteInput drives the final rollout to the target replica count.
type PromoteInput struct {
	Service        string
	ImageTag       string
	TargetReplicas int
}

// Promote completes the rollout: the stable Deployment adopts the new image at
// the full target replica count, and the canary is retired to zero. Idempotent
// — it sets the desired end state, so a retry after a partial promotion
// converges rather than over-scaling. Once stable runs the new image the canary
// is redundant, so scaling it down is safe.
func Promote(ctx context.Context, in PromoteInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("promote", "service", in.Service, "image", in.ImageTag, "targetReplicas", in.TargetReplicas)

	if err := setImage(ctx, stableName(in.Service), in.ImageTag); err != nil {
		return err
	}
	if err := scaleDeployment(ctx, stableName(in.Service), int32(in.TargetReplicas)); err != nil {
		return err
	}
	return scaleDeployment(ctx, canaryName(in.Service), 0)
}
