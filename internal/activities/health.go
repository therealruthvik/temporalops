package activities

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/sdk/activity"
)

// HealthInput controls the bake: how long to observe the canary before judging.
type HealthInput struct {
	Service     string
	BakeSeconds int

	// SimulateFail forces an unhealthy verdict without inspecting the cluster.
	// Kept for the chaos scripts so a failure path can be exercised on demand.
	SimulateFail bool
}

// HealthResult distinguishes an unhealthy canary (Healthy=false, a verdict that
// triggers rollback) from an infra error reaching the API (returned error,
// which is retryable).
type HealthResult struct {
	Healthy   bool
	Reason    string
	ErrorRate float64
}

// HealthCheck bakes the canary: it polls the canary Deployment's readiness once
// per second for BakeSeconds, recording a heartbeat each tick so a long bake
// does not look stuck and Temporal can detect a wedged poll via the heartbeat
// timeout. The verdict is based on the readiness probe defined on the pods
// (the "synthetic health endpoint"): the canary is healthy only if every
// desired replica is Ready by the end of the bake. A bad image, a failing
// probe, or a crash-looping container therefore surfaces as unhealthy and
// triggers rollback.
func HealthCheck(ctx context.Context, in HealthInput) (HealthResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("health check / bake start", "service", in.Service, "bakeSeconds", in.BakeSeconds)

	if in.SimulateFail {
		// Still consume the bake window so the timeline looks realistic.
		for i := 0; i < in.BakeSeconds; i++ {
			select {
			case <-ctx.Done():
				return HealthResult{}, ctx.Err()
			case <-time.After(time.Second):
			}
			activity.RecordHeartbeat(ctx, i+1)
		}
		return HealthResult{Healthy: false, Reason: "injected health failure", ErrorRate: 0.42}, nil
	}

	name := canaryName(in.Service)
	var ready, desired int32
	for i := 0; i < in.BakeSeconds; i++ {
		select {
		case <-ctx.Done():
			return HealthResult{}, ctx.Err()
		case <-time.After(time.Second):
		}
		var err error
		ready, desired, err = deploymentStatus(ctx, name)
		if err != nil {
			return HealthResult{}, err
		}
		activity.RecordHeartbeat(ctx, fmt.Sprintf("%d/%d ready", ready, desired))
	}

	if desired == 0 || ready < desired {
		return HealthResult{
			Healthy: false,
			Reason:  fmt.Sprintf("canary %d/%d replicas ready after %ds bake", ready, desired, in.BakeSeconds),
		}, nil
	}
	return HealthResult{
		Healthy:   true,
		Reason:    fmt.Sprintf("canary %d/%d replicas ready", ready, desired),
		ErrorRate: 0.0,
	}, nil
}
