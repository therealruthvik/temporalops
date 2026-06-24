package activities

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// isInfraError is the heart of the policy gate: it decides whether a failed
// dry-run is a retryable infrastructure problem or a deterministic policy
// denial. Getting this wrong would either mislabel a Kyverno rejection as a
// crash, or retry forever on a real outage.
func TestIsInfraError(t *testing.T) {
	gr := schema.GroupResource{Group: "apps", Resource: "deployments"}

	cases := []struct {
		name  string
		err   error
		infra bool
	}{
		{"nil", nil, false},
		{"network/transport error (not an API status)", errors.New("dial tcp 127.0.0.1:6443: connect: connection refused"), true},
		{"service unavailable", apierrors.NewServiceUnavailable("apiserver down"), true},
		{"server timeout", apierrors.NewServerTimeout(gr, "patch", 1), true},
		{"too many requests", apierrors.NewTooManyRequestsError("slow down"), true},
		{"not found (target missing, surfaced not mislabeled)", apierrors.NewNotFound(gr, "web-canary"), true},
		{"forbidden / admission denial", apierrors.NewForbidden(gr, "web-canary", errors.New("denied by policy")), false},
		{"bad request (4xx admission rejection)", apierrors.NewBadRequest("validation error"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.infra, isInfraError(tc.err))
		})
	}
}
