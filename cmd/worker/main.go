// Command worker runs the Temporal worker process: it connects to the dev
// server, registers every workflow and activity, and polls the shared task
// queue. Killing and restarting this process is the core of the durability
// demo (Stage 7) — Temporal replays history so the workflow resumes from its
// last completed step.
package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/uber-go/tally/v4"
	promreporter "github.com/uber-go/tally/v4/prometheus"
	"go.temporal.io/sdk/client"
	sdktally "go.temporal.io/sdk/contrib/tally"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/worker"

	"github.com/therealruthvik/temporalops/internal/activities"
	"github.com/therealruthvik/temporalops/internal/audit"
	"github.com/therealruthvik/temporalops/internal/config"
	"github.com/therealruthvik/temporalops/internal/workflows"
)

func main() {
	hostPort := config.TemporalAddress()

	// Open the append-only audit log and register it as the process default so
	// the interceptor and the RecordApproval activity write to it.
	auditPath := config.AuditDBPath()
	if dir := filepath.Dir(auditPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("create audit dir: %v", err)
		}
	}
	store, err := audit.Open(auditPath)
	if err != nil {
		log.Fatalf("open audit store: %v", err)
	}
	defer store.Close()
	audit.SetDefault(store)

	// Expose Temporal SDK metrics to Prometheus. The SDK reports through a
	// tally scope backed by a Prometheus reporter; the reporter's HTTP handler
	// is served on /metrics for Prometheus to scrape.
	reporter := promreporter.NewReporter(promreporter.Options{})
	scope, scopeCloser := tally.NewRootScope(tally.ScopeOptions{
		CachedReporter:  reporter,
		Separator:       promreporter.DefaultSeparator,
		SanitizeOptions: &sdktally.PrometheusSanitizeOptions,
	}, time.Second)
	defer scopeCloser.Close()

	metricsAddr := config.MetricsAddr()
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", reporter.HTTPHandler())
		log.Printf("serving Prometheus metrics on %s/metrics", metricsAddr)
		if err := http.ListenAndServe(metricsAddr, mux); err != nil {
			log.Printf("metrics server stopped: %v", err)
		}
	}()

	c, err := client.Dial(client.Options{
		HostPort:       hostPort,
		MetricsHandler: sdktally.NewMetricsHandler(scope),
	})
	if err != nil {
		log.Fatalf("dial temporal: %v", err)
	}
	defer c.Close()

	w := worker.New(c, workflows.TaskQueue, worker.Options{
		// The audit interceptor records every activity start/end automatically.
		Interceptors: []interceptor.WorkerInterceptor{audit.NewWorkerInterceptor(store)},
	})

	w.RegisterWorkflow(workflows.HelloWorkflow)
	w.RegisterWorkflow(workflows.CanaryDeployWorkflow)
	w.RegisterWorkflow(workflows.ReleaseOrchestratorWorkflow)

	w.RegisterActivity(activities.Greet)
	w.RegisterActivity(activities.PolicyCheck)
	w.RegisterActivity(activities.ScaleCanary)
	w.RegisterActivity(activities.ScaleDownCanary)
	w.RegisterActivity(activities.HealthCheck)
	w.RegisterActivity(activities.ShiftTraffic)
	w.RegisterActivity(activities.ShiftTrafficBack)
	w.RegisterActivity(activities.Promote)
	w.RegisterActivity(activities.Alert)
	w.RegisterActivity(activities.RecordApproval)

	log.Printf("worker polling task queue %q at %s (audit: %s)", workflows.TaskQueue, hostPort, auditPath)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("worker stopped: %v", err)
	}
}
