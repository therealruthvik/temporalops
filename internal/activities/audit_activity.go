package activities

import (
	"context"

	"go.temporal.io/sdk/activity"

	"github.com/therealruthvik/temporalops/internal/audit"
)

// ApprovalAudit captures a promote/reject/timeout decision for the compliance
// log. Actor is the signal sender — the person accountable for the promotion.
type ApprovalAudit struct {
	Actor    string
	Decision string // approved | rejected | timeout | auto
	Detail   string
}

// RecordApproval writes the human approval decision to the audit log. The
// activity-level interceptor records start/end for every activity; this one
// additionally records the *actor*, which only the workflow knows (from the
// signal), satisfying the "tagged with actor (signal sender for approvals)"
// requirement.
func RecordApproval(ctx context.Context, in ApprovalAudit) error {
	info := activity.GetInfo(ctx)
	return audit.Record(audit.Entry{
		WorkflowID:   info.WorkflowExecution.ID,
		RunID:        info.WorkflowExecution.RunID,
		ActivityID:   info.ActivityID,
		Attempt:      int(info.Attempt),
		ActivityType: "RecordApproval",
		Phase:        "approval",
		Status:       in.Decision,
		Actor:        in.Actor,
		Detail:       in.Detail,
	})
}
