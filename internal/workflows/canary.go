package workflows

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/therealruthvik/temporalops/internal/activities"
)

// CanaryInput is the request for a single-service canary deploy.
type CanaryInput struct {
	Service        string
	ImageTag       string
	TargetReplicas int
	CanaryReplicas int
	BakeSeconds    int
	ApprovalTimeout time.Duration

	// Stage-2 simulation knobs. They let the chaos/demo scripts force each
	// failure path without real infra. Removed once Stages 3-4 wire real K8s
	// and Kyverno.
	SimulatePolicyReject bool
	SimulateHealthFail   bool
	SimulateTrafficFail  bool
}

// CanaryStatus is the terminal outcome of the workflow.
type CanaryStatus string

const (
	StatusPromoted       CanaryStatus = "Promoted"
	StatusRolledBack     CanaryStatus = "RolledBack"
	StatusTimedOut       CanaryStatus = "TimedOut"
	StatusPolicyRejected CanaryStatus = "PolicyRejected"
)

// CanaryResult is returned to the caller (and aggregated by the orchestrator in
// Stage 5). A rollback or timeout is reported here as a *normal* result, not a
// workflow error: the workflow did its job (it deployed safely and rolled
// back). Only an unrecoverable infra error after retries fails the workflow.
type CanaryResult struct {
	Service string
	Status  CanaryStatus
	Reason  string
	Actor   string
}

// CanaryDeployWorkflow runs a progressive canary release with saga rollback and
// a human approval gate. See ARCHITECTURE.md for the end-to-end design.
func CanaryDeployWorkflow(ctx workflow.Context, in CanaryInput) (CanaryResult, error) {
	logger := workflow.GetLogger(ctx)
	res := CanaryResult{Service: in.Service}

	// Expose the current phase for live querying during demos.
	phase := "starting"
	if err := workflow.SetQueryHandler(ctx, CanaryStatusQuery, func() (string, error) {
		return phase, nil
	}); err != nil {
		return res, err
	}

	saga := newSaga()

	// 1. Policy gate. Nothing has changed yet, so a rejection aborts with no
	// compensation. An *error* here means Kyverno was unreachable after
	// retries (retryable infra failure) and fails the workflow.
	phase = "policy-check"
	var policy activities.PolicyResult
	err := workflow.ExecuteActivity(
		workflow.WithActivityOptions(ctx, policyOpts()),
		activities.PolicyCheck,
		activities.PolicyInput{Service: in.Service, ImageTag: in.ImageTag, SimulateReject: in.SimulatePolicyReject},
	).Get(ctx, &policy)
	if err != nil {
		return res, err
	}
	if !policy.Allowed {
		phase = "policy-rejected"
		res.Status = StatusPolicyRejected
		res.Reason = policy.Reason
		alert(ctx, res)
		return res, nil
	}

	// 2. Scale up the canary. On success, register the inverse.
	phase = "scale-canary"
	var scaled activities.ScaleResult
	err = workflow.ExecuteActivity(
		workflow.WithActivityOptions(ctx, scaleOpts()),
		activities.ScaleCanary,
		activities.ScaleInput{Service: in.Service, ImageTag: in.ImageTag, Replicas: in.CanaryReplicas},
	).Get(ctx, &scaled)
	if err != nil {
		return unwind(ctx, saga, &res, StatusRolledBack, "scale canary failed: "+err.Error())
	}
	saga.register("scale-down-canary", func(c workflow.Context) error {
		return workflow.ExecuteActivity(
			workflow.WithActivityOptions(c, scaleOpts()),
			activities.ScaleDownCanary,
			activities.ScaleInput{Service: in.Service, Replicas: 0},
		).Get(c, nil)
	})

	// 3. Bake: observe canary health for BakeSeconds. An unhealthy verdict
	// (not an error) triggers rollback before any traffic moves.
	phase = "health-bake"
	var health activities.HealthResult
	err = workflow.ExecuteActivity(
		workflow.WithActivityOptions(ctx, healthOpts(in.BakeSeconds)),
		activities.HealthCheck,
		activities.HealthInput{Service: in.Service, BakeSeconds: in.BakeSeconds, SimulateFail: in.SimulateHealthFail},
	).Get(ctx, &health)
	if err != nil {
		return unwind(ctx, saga, &res, StatusRolledBack, "health check error: "+err.Error())
	}
	if !health.Healthy {
		return unwind(ctx, saga, &res, StatusRolledBack, "canary unhealthy: "+health.Reason)
	}

	// 4. Shift traffic to the canary. On success, register the inverse.
	phase = "shift-traffic"
	err = workflow.ExecuteActivity(
		workflow.WithActivityOptions(ctx, trafficOpts()),
		activities.ShiftTraffic,
		activities.TrafficInput{Service: in.Service, CanaryWeight: 50, SimulateFail: in.SimulateTrafficFail},
	).Get(ctx, nil)
	if err != nil {
		return unwind(ctx, saga, &res, StatusRolledBack, "shift traffic failed: "+err.Error())
	}
	saga.register("shift-traffic-back", func(c workflow.Context) error {
		return workflow.ExecuteActivity(
			workflow.WithActivityOptions(c, trafficOpts()),
			activities.ShiftTrafficBack,
			activities.TrafficInput{Service: in.Service, CanaryWeight: 0},
		).Get(c, nil)
	})

	// 5. Human approval gate. Wait for the approve-promote signal, but never
	// hang: if no decision arrives within ApprovalTimeout, auto-rollback and
	// finish as TimedOut. This bounds operator inattention — a forgotten
	// canary cleans itself up instead of sitting half-deployed forever.
	phase = "awaiting-approval"
	signal, timedOut := waitForApproval(ctx, in.ApprovalTimeout)
	res.Actor = signal.Actor

	switch {
	case timedOut:
		logger.Info("approval timed out, rolling back")
		return unwind(ctx, saga, &res, StatusTimedOut, "no approval within "+in.ApprovalTimeout.String())
	case !signal.Approve:
		return unwind(ctx, saga, &res, StatusRolledBack, "promotion rejected by "+signal.Actor)
	}

	// 6. Promote: full rollout to the target replica count.
	phase = "promote"
	err = workflow.ExecuteActivity(
		workflow.WithActivityOptions(ctx, promoteOpts()),
		activities.Promote,
		activities.PromoteInput{Service: in.Service, ImageTag: in.ImageTag, TargetReplicas: in.TargetReplicas},
	).Get(ctx, nil)
	if err != nil {
		return unwind(ctx, saga, &res, StatusRolledBack, "promote failed: "+err.Error())
	}

	phase = "promoted"
	res.Status = StatusPromoted
	res.Reason = "promoted to target replicas, approved by " + signal.Actor
	logger.Info("canary promoted", "service", in.Service, "actor", signal.Actor)
	return res, nil
}

// unwind runs the saga compensations, alerts, and returns a terminal result.
// It centralizes the rollback path so every failure branch behaves identically.
func unwind(ctx workflow.Context, saga *saga, res *CanaryResult, status CanaryStatus, reason string) (CanaryResult, error) {
	res.Status = status
	res.Reason = reason
	saga.compensate(ctx)
	alert(ctx, *res)
	return *res, nil
}

// alert fires AlertActivity on a disconnected context so it still runs even
// during a cancellation-driven rollback.
func alert(ctx workflow.Context, res CanaryResult) {
	disconnected, cancel := workflow.NewDisconnectedContext(ctx)
	defer cancel()
	_ = workflow.ExecuteActivity(
		workflow.WithActivityOptions(disconnected, alertOpts()),
		activities.Alert,
		activities.AlertInput{Service: res.Service, Status: string(res.Status), Reason: res.Reason},
	).Get(disconnected, nil)
}

// waitForApproval blocks on the approve-promote signal or the timeout,
// whichever comes first, using a Selector so the workflow reacts to exactly
// one of them deterministically.
func waitForApproval(ctx workflow.Context, timeout time.Duration) (ApprovalSignal, bool) {
	var signal ApprovalSignal
	var timedOut bool

	ch := workflow.GetSignalChannel(ctx, ApprovePromoteSignal)
	timerCtx, cancelTimer := workflow.WithCancel(ctx)
	defer cancelTimer()
	timer := workflow.NewTimer(timerCtx, timeout)

	selector := workflow.NewSelector(ctx)
	selector.AddReceive(ch, func(c workflow.ReceiveChannel, _ bool) {
		c.Receive(ctx, &signal)
	})
	selector.AddFuture(timer, func(workflow.Future) {
		timedOut = true
	})
	selector.Select(ctx)

	return signal, timedOut
}

// --- Activity option / retry policy choices (interview talking points) ---

// policyOpts: the Kyverno query is a fast read. Retry a few times with quick
// backoff to ride out a transient API-server blip, but cap soon — a real
// policy-backend outage should surface fast, not stall the deploy.
func policyOpts() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    4,
		},
	}
}

// scaleOpts / trafficOpts / promoteOpts: K8s mutations are idempotent (they set
// desired state), so retries are safe. Backoff is bounded; persistent failure
// trips rollback rather than retrying forever.
func scaleOpts() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    20 * time.Second,
			MaximumAttempts:    5,
		},
	}
}

func trafficOpts() workflow.ActivityOptions { return scaleOpts() }
func promoteOpts() workflow.ActivityOptions { return scaleOpts() }

// healthOpts: StartToClose must outlast the bake, so it is derived from
// BakeSeconds plus a buffer. HeartbeatTimeout catches a wedged poll loop. Only
// 2 attempts — a flaky-then-passing health check should still be treated with
// suspicion, so we do not aggressively retry a bad bake.
func healthOpts(bakeSeconds int) workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(bakeSeconds)*time.Second + 30*time.Second,
		HeartbeatTimeout:    15 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumAttempts:    2,
		},
	}
}

// alertOpts: notifications are fire-and-forget best effort; retry a little but
// never block the rollback on a failing webhook.
func alertOpts() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 15 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
}
