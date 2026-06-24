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

// Promote completes the rollout: scale the new version to the target replica
// count and retire the old one. Idempotent — it sets the desired end state, so
// a retry after a partial promotion converges rather than over-scaling.
// Mocked in Stage 2; backed by client-go in Stage 3.
func Promote(ctx context.Context, in PromoteInput) error {
	activity.GetLogger(ctx).Info("promote", "service", in.Service, "image", in.ImageTag, "targetReplicas", in.TargetReplicas)
	return nil
}
