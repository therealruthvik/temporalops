package activities

import (
	"context"
	"fmt"
)

// Greet is the Stage 1 placeholder activity. Activities are where all I/O and
// non-determinism live in Temporal; workflow code must stay deterministic, so
// even a trivial side effect like building a string goes here to keep the
// pattern consistent with the real activities added later.
func Greet(ctx context.Context, name string) (string, error) {
	if name == "" {
		name = "world"
	}
	return fmt.Sprintf("hello, %s", name), nil
}
