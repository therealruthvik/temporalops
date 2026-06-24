package workflows

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"github.com/therealruthvik/temporalops/internal/activities"
)

// TestRelease_PartialFailureSurfaced fans out two services where one promotes
// and the other bakes unhealthy. The orchestrator must report both outcomes
// rather than failing the whole release or hiding the failure.
func TestRelease_PartialFailureSurfaced(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()

	// The child canary workflow runs inside the orchestrator.
	env.RegisterWorkflow(CanaryDeployWorkflow)

	isWeb := func(svc string) interface{} {
		return mock.MatchedBy(func(in activities.HealthInput) bool { return in.Service == svc })
	}

	env.OnActivity(activities.PolicyCheck, mock.Anything, mock.Anything).Return(activities.PolicyResult{Allowed: true}, nil)
	env.OnActivity(activities.ScaleCanary, mock.Anything, mock.Anything).Return(activities.ScaleResult{ReadyReplicas: 1}, nil)

	// web is healthy and promotes; api is unhealthy and rolls back.
	env.OnActivity(activities.HealthCheck, mock.Anything, isWeb("web")).Return(activities.HealthResult{Healthy: true}, nil)
	env.OnActivity(activities.HealthCheck, mock.Anything, isWeb("api")).Return(activities.HealthResult{Healthy: false, Reason: "5xx"}, nil)

	env.OnActivity(activities.ShiftTraffic, mock.Anything, mock.Anything).Return(nil)    // web only
	env.OnActivity(activities.Promote, mock.Anything, mock.Anything).Return(nil)         // web only
	env.OnActivity(activities.ScaleDownCanary, mock.Anything, mock.Anything).Return(nil) // api rollback
	env.OnActivity(activities.Alert, mock.Anything, mock.Anything).Return(nil)           // api rollback
	env.OnActivity(activities.RecordApproval, mock.Anything, mock.Anything).Return(nil)  // auto-promote audit (web)

	in := ReleaseInput{
		ReleaseID: "release-test",
		Services: []CanaryInput{
			{Service: "web", ImageTag: "nginx:1.27", TargetReplicas: 3, CanaryReplicas: 1, BakeSeconds: 1},
			{Service: "api", ImageTag: "nginx:1.27", TargetReplicas: 3, CanaryReplicas: 1, BakeSeconds: 1},
		},
	}

	env.ExecuteWorkflow(ReleaseOrchestratorWorkflow, in)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res ReleaseResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.False(t, res.AllPromoted)
	require.ElementsMatch(t, []string{"web"}, res.Promoted)
	require.ElementsMatch(t, []string{"api"}, res.NotPromoted)
	require.Len(t, res.Results, 2)
}
