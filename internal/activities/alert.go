package activities

import (
	"context"

	"go.temporal.io/sdk/activity"
)

// AlertInput carries enough context for an on-call human to understand a
// rollback without opening the Temporal UI.
type AlertInput struct {
	Service string
	Status  string
	Reason  string
}

// Alert notifies an operator that a deploy was rolled back, timed out, or
// rejected. Mocked as a structured log in Stage 2; a webhook in later stages.
func Alert(ctx context.Context, in AlertInput) error {
	activity.GetLogger(ctx).Warn("deploy alert", "service", in.Service, "status", in.Status, "reason", in.Reason)
	return nil
}
