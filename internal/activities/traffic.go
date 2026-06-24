package activities

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/activity"
)

// TrafficInput expresses the desired canary weight (0-100). Under the
// replica-ratio model the "weight" is realised by the stable/canary replica
// split behind a single Service; in Stage 3 this writes the replica counts.
type TrafficInput struct {
	Service      string
	CanaryWeight int

	// SimulateFail is a Stage-2 knob to force a traffic-shift failure.
	SimulateFail bool
}

// ShiftTraffic moves traffic weight toward the canary. Idempotent: it sets an
// absolute target weight, not a delta. An error here is an infra failure
// (could not update the Service), which is retryable and, if it persists,
// triggers rollback.
func ShiftTraffic(ctx context.Context, in TrafficInput) error {
	if in.SimulateFail {
		return fmt.Errorf("failed to update Service weight for %s", in.Service)
	}
	activity.GetLogger(ctx).Info("shift traffic", "service", in.Service, "canaryWeight", in.CanaryWeight)
	return nil
}

// ShiftTrafficBack is the compensation for ShiftTraffic: restore 100% of
// traffic to the stable version. Absolute target keeps it idempotent.
func ShiftTrafficBack(ctx context.Context, in TrafficInput) error {
	activity.GetLogger(ctx).Info("shift traffic back to stable (compensation)", "service", in.Service)
	return nil
}
