package audit

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStore_RecordQueryAndIdempotency(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "audit.db"))
	require.NoError(t, err)
	defer s.Close()

	start := Entry{
		WorkflowID: "wf1", RunID: "r1", ActivityID: "5", Attempt: 1,
		ActivityType: "ScaleCanary", Phase: "start", Status: "started",
	}
	require.NoError(t, s.Record(start))

	// Same key again must NOT create a duplicate row (INSERT OR IGNORE on the
	// uniqueness index) — this is what keeps the log trustworthy across replays.
	require.NoError(t, s.Record(start))

	end := start
	end.Phase, end.Status = "end", "completed"
	require.NoError(t, s.Record(end))

	rows, err := s.QueryByWorkflow("wf1")
	require.NoError(t, err)
	require.Len(t, rows, 2, "duplicate start must be ignored; start+end remain")
	require.Equal(t, "start", rows[0].Phase)
	require.Equal(t, "end", rows[1].Phase)
	require.False(t, rows[0].Timestamp.IsZero(), "timestamp auto-filled")
}

func TestStore_QueryIsolatesByWorkflow(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "audit.db"))
	require.NoError(t, err)
	defer s.Close()

	require.NoError(t, s.Record(Entry{WorkflowID: "a", RunID: "1", ActivityID: "1", Attempt: 1, ActivityType: "X", Phase: "start", Status: "started"}))
	require.NoError(t, s.Record(Entry{WorkflowID: "b", RunID: "1", ActivityID: "1", Attempt: 1, ActivityType: "X", Phase: "start", Status: "started"}))

	rows, err := s.QueryByWorkflow("a")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "a", rows[0].WorkflowID)
}

func TestStore_DefaultRecord(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "audit.db"))
	require.NoError(t, err)
	defer s.Close()

	SetDefault(s)
	defer SetDefault(nil)

	require.NoError(t, Record(Entry{
		WorkflowID: "wf", RunID: "r", ActivityID: "1", Attempt: 1,
		ActivityType: "RecordApproval", Phase: "approval", Status: "approved", Actor: "alice",
	}))

	rows, err := s.QueryByWorkflow("wf")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "alice", rows[0].Actor)
}

func TestRecord_NoopWhenDefaultUnset(t *testing.T) {
	SetDefault(nil)
	// Must not panic or error when no store is configured (e.g. in unit tests).
	require.NoError(t, Record(Entry{WorkflowID: "x"}))
}
