package workflows

import "go.temporal.io/sdk/workflow"

// saga is a hand-built compensation stack. It is deliberately not a library:
// the whole point is that the rollback logic is visible and explainable. Each
// forward step that produces a side effect pushes its inverse here; on failure
// the stack is unwound in LIFO order so effects are undone in the reverse of
// the order they were applied (traffic shifts back before the canary scales
// down), mirroring how a human would roll back.
type saga struct {
	compensations []compensation
}

type compensation struct {
	name string
	fn   func(ctx workflow.Context) error
}

func newSaga() *saga { return &saga{} }

// register pushes a compensation onto the stack after its forward action has
// succeeded.
func (s *saga) register(name string, fn func(ctx workflow.Context) error) {
	s.compensations = append(s.compensations, compensation{name: name, fn: fn})
}

// compensate unwinds the stack in reverse order. It runs on a disconnected
// context so that even if the workflow is being cancelled, every compensation
// still executes to completion — a half-finished rollback is worse than none.
// Compensations are best-effort: a failure is logged and the remaining ones
// still run, so one stuck step cannot strand the rest of the rollback.
func (s *saga) compensate(ctx workflow.Context) {
	logger := workflow.GetLogger(ctx)
	disconnected, cancel := workflow.NewDisconnectedContext(ctx)
	defer cancel()

	for i := len(s.compensations) - 1; i >= 0; i-- {
		c := s.compensations[i]
		logger.Info("compensating", "step", c.name)
		if err := c.fn(disconnected); err != nil {
			logger.Error("compensation failed", "step", c.name, "error", err)
		}
	}
}
