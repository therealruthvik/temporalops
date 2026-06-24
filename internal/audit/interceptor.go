package audit

import (
	"context"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/interceptor"
)

// NewWorkerInterceptor returns a worker interceptor that writes an audit row at
// the start and end of every activity execution. Using an interceptor (rather
// than sprinkling audit calls through each activity) means the compliance log
// captures every activity uniformly and automatically, including future ones,
// with no boilerplate in the activity code.
func NewWorkerInterceptor(store *Store) interceptor.WorkerInterceptor {
	return &workerInterceptor{store: store}
}

type workerInterceptor struct {
	interceptor.WorkerInterceptorBase
	store *Store
}

func (w *workerInterceptor) InterceptActivity(
	ctx context.Context, next interceptor.ActivityInboundInterceptor,
) interceptor.ActivityInboundInterceptor {
	in := &activityInbound{store: w.store}
	in.Next = next
	return in
}

type activityInbound struct {
	interceptor.ActivityInboundInterceptorBase
	store *Store
}

func (a *activityInbound) ExecuteActivity(
	ctx context.Context, in *interceptor.ExecuteActivityInput,
) (interface{}, error) {
	info := activity.GetInfo(ctx)
	base := Entry{
		WorkflowID:   info.WorkflowExecution.ID,
		RunID:        info.WorkflowExecution.RunID,
		ActivityID:   info.ActivityID,
		Attempt:      int(info.Attempt),
		ActivityType: info.ActivityType.Name,
	}

	start := base
	start.Phase, start.Status = "start", "started"
	_ = a.store.Record(start)

	res, err := a.Next.ExecuteActivity(ctx, in)

	end := base
	end.Phase = "end"
	if err != nil {
		end.Status, end.Detail = "failed", err.Error()
	} else {
		end.Status = "completed"
	}
	_ = a.store.Record(end)

	return res, err
}
