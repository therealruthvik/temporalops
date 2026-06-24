// Package audit provides the append-only compliance log: every activity
// start/end and every approval decision is written to a queryable SQLite table
// tagged with workflow ID, run ID, timestamp, and actor.
package audit

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no cgo)
)

// Entry is one audit row.
type Entry struct {
	WorkflowID   string
	RunID        string
	ActivityID   string
	Attempt      int
	ActivityType string
	Phase        string // "start" | "end" | "approval"
	Status       string // started | completed | failed | approved | rejected | timeout | auto
	Actor        string
	Detail       string
	Timestamp    time.Time
}

// Store is a thin append-only writer over SQLite. SQLite allows a single writer
// at a time, so writes are serialised with a mutex; activities can run
// concurrently, and the busy_timeout pragma covers any remaining contention.
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

const schema = `
CREATE TABLE IF NOT EXISTS audit_log (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workflow_id   TEXT    NOT NULL,
    run_id        TEXT    NOT NULL,
    activity_id   TEXT    NOT NULL,
    attempt       INTEGER NOT NULL,
    activity_type TEXT    NOT NULL,
    phase         TEXT    NOT NULL,
    status        TEXT    NOT NULL,
    actor         TEXT    NOT NULL DEFAULT '',
    detail        TEXT    NOT NULL DEFAULT '',
    ts            TEXT    NOT NULL
);
-- Idempotency: a given (workflow, run, activity, attempt, phase) is recorded at
-- most once, so a retried workflow task or a re-applied write does not duplicate
-- rows. This is what makes the log trustworthy as a compliance artifact.
CREATE UNIQUE INDEX IF NOT EXISTS audit_idem
    ON audit_log(workflow_id, run_id, activity_id, attempt, phase);
`

// Open opens (creating if needed) the SQLite audit database at path.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open audit db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init audit schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Record appends one entry. INSERT OR IGNORE makes it idempotent against the
// uniqueness index. A zero Timestamp is filled with the current time.
func (s *Store) Record(e Entry) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO audit_log
            (workflow_id, run_id, activity_id, attempt, activity_type, phase, status, actor, detail, ts)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.WorkflowID, e.RunID, e.ActivityID, e.Attempt, e.ActivityType,
		e.Phase, e.Status, e.Actor, e.Detail, e.Timestamp.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("record audit entry: %w", err)
	}
	return nil
}

// QueryByWorkflow returns all entries for a workflow ID in insertion order.
func (s *Store) QueryByWorkflow(workflowID string) ([]Entry, error) {
	rows, err := s.db.Query(
		`SELECT workflow_id, run_id, activity_id, attempt, activity_type, phase, status, actor, detail, ts
           FROM audit_log WHERE workflow_id = ? ORDER BY id`,
		workflowID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		var e Entry
		var ts string
		if err := rows.Scan(&e.WorkflowID, &e.RunID, &e.ActivityID, &e.Attempt,
			&e.ActivityType, &e.Phase, &e.Status, &e.Actor, &e.Detail, &ts); err != nil {
			return nil, err
		}
		e.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- package default, shared by the worker interceptor and audit activities ---

var defaultStore *Store

// SetDefault registers the process-wide store (called once from the worker).
func SetDefault(s *Store) { defaultStore = s }

// Record writes to the default store if one is set; a no-op otherwise so code
// paths without an initialised store (e.g. unit tests) do not fail.
func Record(e Entry) error {
	if defaultStore == nil {
		return nil
	}
	return defaultStore.Record(e)
}
