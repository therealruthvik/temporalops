package activities

import (
	"context"

	"go.temporal.io/sdk/activity"
)

// PolicyInput is the request for a pre-deploy policy gate.
type PolicyInput struct {
	Service  string
	ImageTag string

	// SimulateReject is a Stage-2 knob: when true the mock returns a rejection
	// as if Kyverno blocked the image. Removed once the real Kyverno query
	// lands in Stage 4.
	SimulateReject bool
}

// PolicyResult separates two distinct outcomes on purpose:
//   - Allowed=false is a *deterministic policy decision* (image not signed),
//     not an error — retrying would never change it, so the workflow aborts
//     cleanly with no compensation.
//   - A returned error means the policy backend was unreachable; that IS
//     retryable and is handled by the activity RetryPolicy.
type PolicyResult struct {
	Allowed bool
	Reason  string
}

// PolicyCheck confirms the image is signed/scanned before any deploy step runs.
// Mocked in Stage 2; backed by a Kyverno PolicyReport query in Stage 4.
func PolicyCheck(ctx context.Context, in PolicyInput) (PolicyResult, error) {
	activity.GetLogger(ctx).Info("policy check", "service", in.Service, "image", in.ImageTag)
	if in.SimulateReject {
		return PolicyResult{
			Allowed: false,
			Reason:  "image " + in.ImageTag + " is not signed/scanned (Kyverno require-signed-image)",
		}, nil
	}
	return PolicyResult{Allowed: true, Reason: "image signed and scanned"}, nil
}
