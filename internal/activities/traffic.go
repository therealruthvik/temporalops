package activities

import (
	"context"
	"fmt"
	"math"

	"go.temporal.io/sdk/activity"
)

// TrafficInput expresses the desired canary weight (0-100). Under the
// replica-ratio model the weight is realised by the stable/canary replica split
// behind a single Service. TargetReplicas is the steady-state total used to
// turn a percentage into concrete replica counts.
type TrafficInput struct {
	Service        string
	CanaryWeight   int
	TargetReplicas int

	// SimulateFail forces a traffic-shift failure for the chaos scripts.
	SimulateFail bool
}

// ShiftTraffic moves traffic toward the canary by setting the canary/stable
// replica split to approximate CanaryWeight percent. Idempotent: it writes
// absolute targets, not deltas. A failure here is an infra error (could not
// update a Deployment), which is retryable and, if it persists, triggers
// rollback.
func ShiftTraffic(ctx context.Context, in TrafficInput) error {
	if in.SimulateFail {
		return fmt.Errorf("injected traffic-shift failure for %s", in.Service)
	}

	total := in.TargetReplicas
	if total < 1 {
		total = 1
	}
	canary := int32(math.Round(float64(total) * float64(in.CanaryWeight) / 100.0))
	if canary < 1 && in.CanaryWeight > 0 {
		canary = 1 // ensure the canary actually gets traffic at any non-zero weight
	}
	stable := int32(total) - canary
	if stable < 0 {
		stable = 0
	}

	activity.GetLogger(ctx).Info("shift traffic",
		"service", in.Service, "canaryWeight", in.CanaryWeight, "canary", canary, "stable", stable)

	if err := scaleDeployment(ctx, canaryName(in.Service), canary); err != nil {
		return err
	}
	return scaleDeployment(ctx, stableName(in.Service), stable)
}

// ShiftTrafficBack is the compensation for ShiftTraffic: restore all traffic to
// stable by returning stable to its full target and the canary to zero.
// Absolute targets keep it idempotent.
func ShiftTrafficBack(ctx context.Context, in TrafficInput) error {
	activity.GetLogger(ctx).Info("shift traffic back to stable (compensation)", "service", in.Service)

	if err := scaleDeployment(ctx, canaryName(in.Service), 0); err != nil {
		return err
	}
	target := in.TargetReplicas
	if target < 1 {
		target = 1
	}
	return scaleDeployment(ctx, stableName(in.Service), int32(target))
}
