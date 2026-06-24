// Package config centralises the handful of environment-tunable settings so the
// worker and starter agree on defaults (task queue address, audit DB path,
// metrics endpoint).
package config

import "os"

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// TemporalAddress is the frontend gRPC host:port (default the dev server).
func TemporalAddress() string { return env("TEMPORAL_ADDRESS", "127.0.0.1:7233") }

// AuditDBPath is the SQLite file backing the compliance log.
func AuditDBPath() string { return env("AUDIT_DB", "audit/audit.db") }

// MetricsAddr is the address the worker serves Prometheus metrics on.
func MetricsAddr() string { return env("METRICS_ADDR", ":9090") }
