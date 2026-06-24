package activities

import (
	"context"
	"time"

	"go.temporal.io/sdk/activity"
)

// HealthInput controls the bake: how long to observe the canary before judging.
type HealthInput struct {
	Service     string
	BakeSeconds int

	// SimulateFail is a Stage-2 knob to force an unhealthy verdict.
	SimulateFail bool
}

// HealthResult, like PolicyResult, distinguishes an unhealthy canary
// (Healthy=false, a business verdict that triggers rollback) from an infra
// error reaching the pods (returned error, which is retryable).
type HealthResult struct {
	Healthy   bool
	Reason    string
	ErrorRate float64
}

// HealthCheck bakes the canary: it polls once per second for BakeSeconds,
// recording a heartbeat each tick. The heartbeat matters for two reasons:
// it lets a long bake exceed a short StartToClose without looking stuck, and
// it lets Temporal detect a wedged activity via the heartbeat timeout.
func HealthCheck(ctx context.Context, in HealthInput) (HealthResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("health check / bake start", "service", in.Service, "bakeSeconds", in.BakeSeconds)

	for i := 0; i < in.BakeSeconds; i++ {
		select {
		case <-ctx.Done():
			return HealthResult{}, ctx.Err()
		case <-time.After(time.Second):
		}
		activity.RecordHeartbeat(ctx, i+1)
	}

	if in.SimulateFail {
		return HealthResult{
			Healthy:   false,
			Reason:    "synthetic health endpoint returned 5xx above threshold",
			ErrorRate: 0.42,
		}, nil
	}
	return HealthResult{
		Healthy:   true,
		Reason:    "canary pods ready, error rate nominal",
		ErrorRate: 0.001,
	}, nil
}
