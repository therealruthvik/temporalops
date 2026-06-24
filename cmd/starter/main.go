// Command starter drives workflows from the CLI: start a canary deploy, send
// the approve-promote signal, query live status, or run the Stage-1 hello
// smoke test.
//
//	starter canary  --service web --tag v2 --replicas 3 --bake 15 --approval-timeout 15m
//	starter approve --id <workflow-id> --actor alice [--reject]
//	starter status  --id <workflow-id>
//	starter hello   --name world
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"go.temporal.io/sdk/client"

	"github.com/therealruthvik/temporalops/internal/audit"
	"github.com/therealruthvik/temporalops/internal/config"
	"github.com/therealruthvik/temporalops/internal/workflows"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	hostPort := os.Getenv("TEMPORAL_ADDRESS")
	if hostPort == "" {
		hostPort = client.DefaultHostPort
	}
	c, err := client.Dial(client.Options{HostPort: hostPort})
	if err != nil {
		log.Fatalf("dial temporal: %v", err)
	}
	defer c.Close()

	switch cmd {
	case "hello":
		runHello(c, args)
	case "canary":
		runCanary(c, args)
	case "release":
		runRelease(c, args)
	case "approve":
		runApprove(c, args)
	case "status":
		runStatus(c, args)
	case "audit":
		runAudit(args)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: starter <hello|canary|release|approve|status|audit> [flags]")
	os.Exit(2)
}

func runAudit(args []string) {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	id := fs.String("id", "", "workflow id to show the audit trail for")
	_ = fs.Parse(args)
	if *id == "" {
		log.Fatal("--id is required")
	}

	store, err := audit.Open(config.AuditDBPath())
	if err != nil {
		log.Fatalf("open audit db: %v", err)
	}
	defer store.Close()

	entries, err := store.QueryByWorkflow(*id)
	if err != nil {
		log.Fatalf("query audit log: %v", err)
	}
	if len(entries) == 0 {
		fmt.Printf("no audit entries for %s\n", *id)
		return
	}
	fmt.Printf("audit trail for %s (%d entries)\n", *id, len(entries))
	for _, e := range entries {
		actor := e.Actor
		if actor == "" {
			actor = "-"
		}
		fmt.Printf("  %s  %-16s %-9s %-10s actor=%-12s %s\n",
			e.Timestamp.Format("15:04:05"), e.ActivityType, e.Phase, e.Status, actor, e.Detail)
	}
}

func runHello(c client.Client, args []string) {
	fs := flag.NewFlagSet("hello", flag.ExitOnError)
	name := fs.String("name", "world", "name to greet")
	_ = fs.Parse(args)

	opts := client.StartWorkflowOptions{
		ID:        fmt.Sprintf("hello-%d", time.Now().Unix()),
		TaskQueue: workflows.TaskQueue,
	}
	we, err := c.ExecuteWorkflow(context.Background(), opts, workflows.HelloWorkflow, *name)
	if err != nil {
		log.Fatalf("start workflow: %v", err)
	}
	var result string
	if err := we.Get(context.Background(), &result); err != nil {
		log.Fatalf("workflow result: %v", err)
	}
	log.Printf("result: %s", result)
}

func runCanary(c client.Client, args []string) {
	fs := flag.NewFlagSet("canary", flag.ExitOnError)
	wfID := fs.String("id", "", "explicit workflow id (default: canary-<service>-<unix>)")
	service := fs.String("service", "web", "service name")
	tag := fs.String("tag", "nginx:1.27-alpine", "full image reference to deploy as the canary")
	replicas := fs.Int("replicas", 3, "target replica count")
	canaryReplicas := fs.Int("canary-replicas", 1, "canary replica count")
	bake := fs.Int("bake", 15, "bake duration in seconds")
	approvalTimeout := fs.Duration("approval-timeout", 15*time.Minute, "auto-rollback if unapproved within this window")
	wait := fs.Bool("wait", false, "block and print the final result")
	// Stage-2 failure injection (removed once real K8s/Kyverno land).
	failPolicy := fs.Bool("fail-policy", false, "simulate Kyverno rejection")
	failHealth := fs.Bool("fail-health", false, "simulate unhealthy canary")
	failTraffic := fs.Bool("fail-traffic", false, "simulate traffic-shift failure")
	_ = fs.Parse(args)

	in := workflows.CanaryInput{
		Service:              *service,
		ImageTag:             *tag,
		TargetReplicas:       *replicas,
		CanaryReplicas:       *canaryReplicas,
		BakeSeconds:          *bake,
		ApprovalTimeout:      *approvalTimeout,
		SimulatePolicyReject: *failPolicy,
		SimulateHealthFail:   *failHealth,
		SimulateTrafficFail:  *failTraffic,
	}

	id := *wfID
	if id == "" {
		id = fmt.Sprintf("canary-%s-%d", *service, time.Now().Unix())
	}
	opts := client.StartWorkflowOptions{ID: id, TaskQueue: workflows.TaskQueue}

	we, err := c.ExecuteWorkflow(context.Background(), opts, workflows.CanaryDeployWorkflow, in)
	if err != nil {
		log.Fatalf("start canary: %v", err)
	}
	log.Printf("started canary workflow id=%s run=%s", we.GetID(), we.GetRunID())
	log.Printf("approve with: starter approve --id %s --actor <you>", we.GetID())

	if *wait {
		var res workflows.CanaryResult
		if err := we.Get(context.Background(), &res); err != nil {
			log.Fatalf("workflow result: %v", err)
		}
		log.Printf("result: status=%s actor=%s reason=%q", res.Status, res.Actor, res.Reason)
	}
}

func runRelease(c client.Client, args []string) {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	services := fs.String("services", "web,api", "comma-separated service names")
	tag := fs.String("tag", "nginx:1.27-alpine", "full image reference to deploy to every service")
	replicas := fs.Int("replicas", 3, "target replica count per service")
	canaryReplicas := fs.Int("canary-replicas", 1, "canary replica count per service")
	bake := fs.Int("bake", 15, "bake duration in seconds")
	_ = fs.Parse(args)

	names := strings.Split(*services, ",")
	specs := make([]workflows.CanaryInput, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		specs = append(specs, workflows.CanaryInput{
			Service:        n,
			ImageTag:       *tag,
			TargetReplicas: *replicas,
			CanaryReplicas: *canaryReplicas,
			BakeSeconds:    *bake,
			// Children promote at the release level; no per-canary gate.
			AutoPromote: true,
		})
	}

	releaseID := fmt.Sprintf("release-%d", time.Now().Unix())
	in := workflows.ReleaseInput{ReleaseID: releaseID, Services: specs}
	opts := client.StartWorkflowOptions{ID: releaseID, TaskQueue: workflows.TaskQueue}

	we, err := c.ExecuteWorkflow(context.Background(), opts, workflows.ReleaseOrchestratorWorkflow, in)
	if err != nil {
		log.Fatalf("start release: %v", err)
	}
	log.Printf("started release id=%s services=%v", we.GetID(), names)

	var res workflows.ReleaseResult
	if err := we.Get(context.Background(), &res); err != nil {
		log.Fatalf("release result: %v", err)
	}
	log.Printf("release complete: allPromoted=%v promoted=%v notPromoted=%v",
		res.AllPromoted, res.Promoted, res.NotPromoted)
	for _, r := range res.Results {
		log.Printf("  %-8s %-14s %s", r.Service, r.Status, r.Reason)
	}
}

func runApprove(c client.Client, args []string) {
	fs := flag.NewFlagSet("approve", flag.ExitOnError)
	id := fs.String("id", "", "workflow id")
	actor := fs.String("actor", "", "who is approving (recorded for audit)")
	reject := fs.Bool("reject", false, "reject the promotion instead of approving")
	_ = fs.Parse(args)
	if *id == "" || *actor == "" {
		log.Fatal("--id and --actor are required")
	}

	sig := workflows.ApprovalSignal{Approve: !*reject, Actor: *actor}
	if err := c.SignalWorkflow(context.Background(), *id, "", workflows.ApprovePromoteSignal, sig); err != nil {
		log.Fatalf("signal workflow: %v", err)
	}
	verb := "approved"
	if *reject {
		verb = "rejected"
	}
	log.Printf("%s promotion for %s as %s", verb, *id, *actor)
}

func runStatus(c client.Client, args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	id := fs.String("id", "", "workflow id")
	_ = fs.Parse(args)
	if *id == "" {
		log.Fatal("--id is required")
	}

	resp, err := c.QueryWorkflow(context.Background(), *id, "", workflows.CanaryStatusQuery)
	if err != nil {
		log.Fatalf("query workflow: %v", err)
	}
	var phase string
	if err := resp.Get(&phase); err != nil {
		log.Fatalf("decode query result: %v", err)
	}
	// Print to stdout (it is data, not a log line) so callers can capture it.
	fmt.Printf("phase: %s\n", phase)
}
