package activities

import (
	"context"

	"go.temporal.io/sdk/activity"
)

// PolicyInput is the request for a pre-deploy policy gate.
type PolicyInput struct {
	Service  string
	ImageTag string

	// SimulateReject forces a rejection without consulting the cluster, for the
	// chaos scripts.
	SimulateReject bool
}

// PolicyResult separates two distinct outcomes on purpose:
//   - Allowed=false is a deterministic policy decision (image not approved),
//     not an error — retrying would never change it, so the workflow aborts
//     cleanly with no compensation.
//   - A returned error means the policy backend / API server was unreachable;
//     that IS retryable and is handled by the activity RetryPolicy.
type PolicyResult struct {
	Allowed bool
	Reason  string
}

// PolicyCheck confirms the candidate image passes cluster policy before any
// deploy step runs. It does this by dry-run-applying the image to the canary
// Deployment: the API server runs the mutation through Kyverno's admission
// webhook without persisting anything. If Kyverno denies it, the image is not
// approved; if the call fails for infrastructure reasons, that is surfaced as a
// retryable error.
func PolicyCheck(ctx context.Context, in PolicyInput) (PolicyResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("policy check", "service", in.Service, "image", in.ImageTag)

	if in.SimulateReject {
		return PolicyResult{
			Allowed: false,
			Reason:  "injected policy rejection",
		}, nil
	}

	err := dryRunSetImage(ctx, canaryName(in.Service), in.ImageTag)
	if err == nil {
		return PolicyResult{Allowed: true, Reason: "image admitted by cluster policy"}, nil
	}
	if isInfraError(err) {
		// Retryable: let the activity RetryPolicy try again before giving up.
		return PolicyResult{}, err
	}
	// Admission denial -> deterministic policy rejection.
	logger.Info("policy rejected image", "service", in.Service, "image", in.ImageTag, "reason", err.Error())
	return PolicyResult{Allowed: false, Reason: err.Error()}, nil
}
