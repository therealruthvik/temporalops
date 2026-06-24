// Command worker runs the Temporal worker process: it connects to the dev
// server, registers every workflow and activity, and polls the shared task
// queue. Killing and restarting this process is the core of the durability
// demo in later stages — Temporal replays history so the workflow resumes
// from its last completed step.
package main

import (
	"log"
	"os"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/therealruthvik/temporalops/internal/activities"
	"github.com/therealruthvik/temporalops/internal/workflows"
)

func main() {
	hostPort := os.Getenv("TEMPORAL_ADDRESS")
	if hostPort == "" {
		hostPort = client.DefaultHostPort // 127.0.0.1:7233
	}

	c, err := client.Dial(client.Options{HostPort: hostPort})
	if err != nil {
		log.Fatalf("dial temporal: %v", err)
	}
	defer c.Close()

	w := worker.New(c, workflows.TaskQueue, worker.Options{})

	w.RegisterWorkflow(workflows.HelloWorkflow)
	w.RegisterWorkflow(workflows.CanaryDeployWorkflow)

	w.RegisterActivity(activities.Greet)
	w.RegisterActivity(activities.PolicyCheck)
	w.RegisterActivity(activities.ScaleCanary)
	w.RegisterActivity(activities.ScaleDownCanary)
	w.RegisterActivity(activities.HealthCheck)
	w.RegisterActivity(activities.ShiftTraffic)
	w.RegisterActivity(activities.ShiftTrafficBack)
	w.RegisterActivity(activities.Promote)
	w.RegisterActivity(activities.Alert)

	log.Printf("worker polling task queue %q at %s", workflows.TaskQueue, hostPort)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("worker stopped: %v", err)
	}
}
