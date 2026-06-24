package workflows

// Signal and query names are part of the workflow's public contract: external
// clients reference them by string, so they live here as constants shared by
// the workflow and the starter CLI.
const (
	// ApprovePromoteSignal carries a human's promote/reject decision into a
	// baked canary that is waiting at the approval gate.
	ApprovePromoteSignal = "approve-promote"

	// CanaryStatusQuery returns the workflow's current phase without mutating
	// state — used by the starter `status` command and during demos.
	CanaryStatusQuery = "canary-status"
)

// ApprovalSignal is the payload of ApprovePromoteSignal. Actor is recorded in
// the result (and, from Stage 6, the audit log) so every promotion is
// attributable to a person — the core of the compliance story.
type ApprovalSignal struct {
	Approve bool
	Actor   string
}
