package workflows

import (
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"github.com/therealruthvik/temporalops/internal/activities"
)

// baseInput is a happy-path-shaped request; individual tests inject failures
// by changing the activity mocks rather than the input, because in unit tests
// the activities are mocked and never read the simulation flags.
func baseInput() CanaryInput {
	return CanaryInput{
		Service:         "web",
		ImageTag:        "v2",
		TargetReplicas:  3,
		CanaryReplicas:  1,
		BakeSeconds:     1,
		ApprovalTimeout: time.Minute,
	}
}

// TestCanary_HappyPath: all steps pass and an approval arrives before the
// timeout, so the canary is promoted.
func TestCanary_HappyPath(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()

	env.OnActivity(activities.PolicyCheck, mock.Anything, mock.Anything).Return(activities.PolicyResult{Allowed: true}, nil)
	env.OnActivity(activities.ScaleCanary, mock.Anything, mock.Anything).Return(activities.ScaleResult{ReadyReplicas: 1}, nil)
	env.OnActivity(activities.HealthCheck, mock.Anything, mock.Anything).Return(activities.HealthResult{Healthy: true}, nil)
	env.OnActivity(activities.ShiftTraffic, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(activities.Promote, mock.Anything, mock.Anything).Return(nil)

	// Send the approval shortly after the workflow reaches the gate.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ApprovePromoteSignal, ApprovalSignal{Approve: true, Actor: "alice"})
	}, time.Second)

	env.ExecuteWorkflow(CanaryDeployWorkflow, baseInput())

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res CanaryResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, StatusPromoted, res.Status)
	require.Equal(t, "alice", res.Actor)
	env.AssertExpectations(t)
}

// TestCanary_PolicyRejected: an unsigned image is rejected at the gate before
// anything changes, so there is no compensation.
func TestCanary_PolicyRejected(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()

	env.OnActivity(activities.PolicyCheck, mock.Anything, mock.Anything).
		Return(activities.PolicyResult{Allowed: false, Reason: "unsigned image"}, nil)
	env.OnActivity(activities.Alert, mock.Anything, mock.Anything).Return(nil)
	// ScaleCanary et al. are intentionally not mocked: if the workflow called
	// them, the test env would fail on an unexpected activity.

	env.ExecuteWorkflow(CanaryDeployWorkflow, baseInput())

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res CanaryResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, StatusPolicyRejected, res.Status)
	env.AssertExpectations(t)
}

// TestCanary_HealthFailRollsBack: the canary bakes unhealthy, so it scales back
// down. Traffic was never shifted, so ShiftTrafficBack must NOT run — verified
// by not mocking it (an unexpected call fails the test).
func TestCanary_HealthFailRollsBack(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()

	env.OnActivity(activities.PolicyCheck, mock.Anything, mock.Anything).Return(activities.PolicyResult{Allowed: true}, nil)
	env.OnActivity(activities.ScaleCanary, mock.Anything, mock.Anything).Return(activities.ScaleResult{ReadyReplicas: 1}, nil)
	env.OnActivity(activities.HealthCheck, mock.Anything, mock.Anything).
		Return(activities.HealthResult{Healthy: false, Reason: "5xx over threshold"}, nil)
	env.OnActivity(activities.ScaleDownCanary, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(activities.Alert, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(CanaryDeployWorkflow, baseInput())

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res CanaryResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, StatusRolledBack, res.Status)
	env.AssertExpectations(t)
}

// TestCanary_ApprovalTimeoutRollsBack: traffic is shifted, then no approval
// arrives. The workflow must auto-rollback in reverse order (traffic back, then
// scale down) and finish as TimedOut, not hang. The test env advances the
// virtual clock past ApprovalTimeout automatically.
func TestCanary_ApprovalTimeoutRollsBack(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()

	env.OnActivity(activities.PolicyCheck, mock.Anything, mock.Anything).Return(activities.PolicyResult{Allowed: true}, nil)
	env.OnActivity(activities.ScaleCanary, mock.Anything, mock.Anything).Return(activities.ScaleResult{ReadyReplicas: 1}, nil)
	env.OnActivity(activities.HealthCheck, mock.Anything, mock.Anything).Return(activities.HealthResult{Healthy: true}, nil)
	env.OnActivity(activities.ShiftTraffic, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(activities.ShiftTrafficBack, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(activities.ScaleDownCanary, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(activities.Alert, mock.Anything, mock.Anything).Return(nil)
	// Promote intentionally not mocked: it must never run on a timeout.

	env.ExecuteWorkflow(CanaryDeployWorkflow, baseInput())

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res CanaryResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, StatusTimedOut, res.Status)
	env.AssertExpectations(t)
}

// TestCanary_RejectionRollsBack: a human rejects the promotion, so the canary
// rolls back in full reverse order.
func TestCanary_RejectionRollsBack(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()

	env.OnActivity(activities.PolicyCheck, mock.Anything, mock.Anything).Return(activities.PolicyResult{Allowed: true}, nil)
	env.OnActivity(activities.ScaleCanary, mock.Anything, mock.Anything).Return(activities.ScaleResult{ReadyReplicas: 1}, nil)
	env.OnActivity(activities.HealthCheck, mock.Anything, mock.Anything).Return(activities.HealthResult{Healthy: true}, nil)
	env.OnActivity(activities.ShiftTraffic, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(activities.ShiftTrafficBack, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(activities.ScaleDownCanary, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(activities.Alert, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ApprovePromoteSignal, ApprovalSignal{Approve: false, Actor: "bob"})
	}, time.Second)

	env.ExecuteWorkflow(CanaryDeployWorkflow, baseInput())

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res CanaryResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, StatusRolledBack, res.Status)
	require.Equal(t, "bob", res.Actor)
	env.AssertExpectations(t)
}
