// Command starter kicks off workflows from the CLI and waits for the result.
// In Stage 1 it only drives HelloWorkflow; later stages extend it to start
// CanaryDeployWorkflow, send the approve-promote signal, and query state.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"go.temporal.io/sdk/client"

	"github.com/therealruthvik/temporalops/internal/workflows"
)

func main() {
	name := flag.String("name", "world", "name to greet")
	flag.Parse()

	hostPort := os.Getenv("TEMPORAL_ADDRESS")
	if hostPort == "" {
		hostPort = client.DefaultHostPort
	}

	c, err := client.Dial(client.Options{HostPort: hostPort})
	if err != nil {
		log.Fatalf("dial temporal: %v", err)
	}
	defer c.Close()

	opts := client.StartWorkflowOptions{
		ID:        fmt.Sprintf("hello-%d", time.Now().Unix()),
		TaskQueue: workflows.TaskQueue,
	}

	we, err := c.ExecuteWorkflow(context.Background(), opts, workflows.HelloWorkflow, *name)
	if err != nil {
		log.Fatalf("start workflow: %v", err)
	}
	log.Printf("started workflow id=%s run=%s", we.GetID(), we.GetRunID())

	var result string
	if err := we.Get(context.Background(), &result); err != nil {
		log.Fatalf("workflow result: %v", err)
	}
	log.Printf("result: %s", result)
}
