// Command agent runs Agent 1: a single fault-diagnosis pass (or, with
// -watch, a repeating one) over the DroneFleet simulated fleet, using an
// MCP connection to dronefleet-mcp and a local Ollama model.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spillala/dronefleet-agent/internal/agent"
	"github.com/spillala/dronefleet-agent/internal/health"
	"github.com/spillala/dronefleet-agent/internal/llm"
	"github.com/spillala/dronefleet-agent/internal/mcpclient"
)

const diagnoseInstruction = `Check the fleet for any drones in a fault state. ` +
	`For each one, look up its details and summarize the likely cause and ` +
	`severity. If nothing is faulted, say so briefly.`

func main() {
	var (
		mcpServerPath = flag.String("mcp-server", envOr("DRONEFLEET_MCP_PATH", "./dronefleet-mcp"), "path to the dronefleet-mcp binary")
		dronefleetURL = flag.String("dronefleet-url", envOr("DRONEFLEET_URL", "http://localhost:8080"), "base URL of the dronefleet API, passed to dronefleet-mcp")
		ollamaURL     = flag.String("ollama-url", envOr("OLLAMA_URL", "http://localhost:11434"), "base URL of the Ollama server")
		model         = flag.String("model", envOr("OLLAMA_MODEL", "gemma4:e2b-it-q4_K_M"), "Ollama model tag to use")
		watch         = flag.Duration("watch", 0, "if set, re-run the diagnosis on this interval instead of exiting after one pass")
		healthAddr    = flag.String("health-addr", envOr("HEALTH_ADDR", ""), "if set, serve /healthz and /readyz on this address (e.g. :8081) — for running under Kubernetes")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var h *health.Server
	if *healthAddr != "" {
		h = health.New()
		go func() {
			if err := h.ListenAndServe(*healthAddr); err != nil {
				log.Fatalf("health server: %v", err)
			}
		}()
	}

	mcp, err := mcpclient.Connect(ctx, *mcpServerPath, []string{"DRONEFLEET_URL=" + *dronefleetURL})
	if err != nil {
		log.Fatalf("connect to dronefleet-mcp: %v", err)
	}
	defer mcp.Close()

	if h != nil {
		h.SetReady(true)
	}

	llmClient := llm.New(*ollamaURL)
	a := agent.New(mcp, llmClient, *model)

	runOnce := func() {
		findings, err := a.Diagnose(ctx, diagnoseInstruction)
		if err != nil {
			log.Printf("diagnosis failed: %v", err)
			return
		}
		fmt.Println("=== Agent 1 findings ===")
		fmt.Println(findings)
	}

	runOnce()
	if *watch <= 0 {
		return
	}

	ticker := time.NewTicker(*watch)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce()
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
