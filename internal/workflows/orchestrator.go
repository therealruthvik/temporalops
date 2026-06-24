package workflows

import (
	"fmt"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// ReleaseInput is a multi-service release: one canary spec per service, run
// concurrently under a single parent workflow.
type ReleaseInput struct {
	ReleaseID string
	Services  []CanaryInput
}

// ReleaseResult aggregates the children. Partial failure is never swallowed:
// the caller can see exactly which services promoted and which did not.
type ReleaseResult struct {
	ReleaseID  string
	Results    []CanaryResult
	Promoted   []string
	NotPromoted []string
	AllPromoted bool
}

// ReleaseOrchestratorWorkflow fans out one CanaryDeployWorkflow child per
// service, waits for all of them (fan-in), and aggregates the outcomes. Each
// child is a full canary deploy with its own saga and audit trail; running them
// as children (not activities) means each gets its own durable history and is
// independently visible in the Web UI.
func ReleaseOrchestratorWorkflow(ctx workflow.Context, in ReleaseInput) (ReleaseResult, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("release started", "releaseID", in.ReleaseID, "services", len(in.Services))

	result := ReleaseResult{ReleaseID: in.ReleaseID}

	// Fan-out: start every child first, collecting futures, so they run
	// concurrently rather than one-at-a-time.
	type pending struct {
		service string
		future  workflow.ChildWorkflowFuture
	}
	futures := make([]pending, 0, len(in.Services))

	for _, svc := range in.Services {
		// Orchestrated services promote at the release level, so the per-canary
		// approval gate is bypassed.
		svc.AutoPromote = true

		cwo := workflow.ChildWorkflowOptions{
			WorkflowID: fmt.Sprintf("%s-%s", in.ReleaseID, svc.Service),
			// PARENT_CLOSE_POLICY_TERMINATE: if the orchestrator is terminated,
			// the children stop too rather than continuing a release no one is
			// tracking.
			ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_TERMINATE,
			RetryPolicy: &temporal.RetryPolicy{
				// The child workflow already does its own saga rollback and
				// returns a result rather than failing, so a retry of the whole
				// child is rarely useful; allow one to ride out an unexpected
				// workflow task failure.
				MaximumAttempts: 1,
			},
		}
		childCtx := workflow.WithChildOptions(ctx, cwo)
		future := workflow.ExecuteChildWorkflow(childCtx, CanaryDeployWorkflow, svc)
		futures = append(futures, pending{service: svc.Service, future: future})
	}

	// Fan-in: wait for each child and record its outcome. A child that fails
	// outright (returns an error rather than a result) is recorded as a failure
	// for its service instead of aborting the whole release.
	for _, p := range futures {
		var res CanaryResult
		if err := p.future.Get(ctx, &res); err != nil {
			logger.Error("child workflow failed", "service", p.service, "error", err)
			res = CanaryResult{
				Service: p.service,
				Status:  StatusRolledBack,
				Reason:  "child workflow error: " + err.Error(),
			}
		}
		result.Results = append(result.Results, res)
		if res.Status == StatusPromoted {
			result.Promoted = append(result.Promoted, res.Service)
		} else {
			result.NotPromoted = append(result.NotPromoted, res.Service)
		}
	}

	result.AllPromoted = len(result.NotPromoted) == 0
	logger.Info("release complete",
		"promoted", result.Promoted, "notPromoted", result.NotPromoted)
	return result, nil
}
