package workflows

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/therealruthvik/temporalops/internal/activities"
)

// TaskQueue is shared by the worker (which polls it) and the starter (which
// targets it). Keeping it in one place avoids the classic "worker and client
// disagree on queue name, workflow hangs forever" mistake.
const TaskQueue = "temporalops"

// HelloWorkflow is a smoke test for Stage 1: it proves the dev server, worker,
// and starter are wired together and that a workflow can drive an activity to
// completion. Replaced by CanaryDeployWorkflow in later stages.
func HelloWorkflow(ctx workflow.Context, name string) (string, error) {
	// ActivityOptions are mandatory: without a StartToCloseTimeout Temporal
	// refuses to schedule the activity. The RetryPolicy here is deliberately
	// modest — a greeting has no external dependency to wait on.
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	logger := workflow.GetLogger(ctx)
	logger.Info("HelloWorkflow started", "name", name)

	var greeting string
	if err := workflow.ExecuteActivity(ctx, activities.Greet, name).Get(ctx, &greeting); err != nil {
		return "", err
	}
	return greeting, nil
}
