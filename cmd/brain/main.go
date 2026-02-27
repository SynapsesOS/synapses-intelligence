// Command brain is the synapses-intelligence CLI.
//
// Usage:
//
//	brain serve              Start the HTTP sidecar server
//	brain status             Show brain status (Ollama ping, SQLite stats)
//	brain ingest <file>      Manually ingest a source file
//	brain summaries          List all stored semantic summaries
//	brain reset              Clear all brain data (summaries, violation cache)
//	brain version            Print version
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/synapses/synapses-intelligence/config"
	"github.com/synapses/synapses-intelligence/internal/store"
	"github.com/synapses/synapses-intelligence/pkg/brain"
	"github.com/synapses/synapses-intelligence/server"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Load config (optional, defaults apply if not found).
	cfgPath := os.Getenv("BRAIN_CONFIG")
	var cfg config.BrainConfig
	if cfgPath != "" {
		var err error
		cfg, err = config.LoadFile(cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "brain: warning: could not load config %q: %v\n", cfgPath, err)
			cfg = config.DefaultConfig()
		}
	} else {
		cfg = config.DefaultConfig()
	}

	// Override enabled for CLI commands — the binary is always "enabled".
	cfg.Enabled = true

	switch os.Args[1] {
	case "serve":
		cmdServe(cfg)
	case "status":
		cmdStatus(cfg)
	case "ingest":
		cmdIngest(cfg, os.Args[2:])
	case "summaries":
		cmdSummaries(cfg)
	case "reset":
		cmdReset(cfg)
	case "version", "--version", "-v":
		fmt.Printf("synapses-intelligence v%s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "brain: unknown command %q\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// cmdServe starts the HTTP sidecar server.
func cmdServe(cfg config.BrainConfig) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.Int("port", cfg.Port, "HTTP port to listen on")
	model := fs.String("model", cfg.Model, "Ollama model to use")
	fs.Parse(os.Args[2:])

	cfg.Port = *port
	cfg.Model = *model

	b := brain.New(cfg)

	// Warn if Ollama is not available at startup.
	ctx := context.Background()
	if !b.Available() {
		fmt.Fprintf(os.Stderr,
			"brain: warning: Ollama not reachable at %s — LLM features disabled until Ollama starts\n",
			cfg.OllamaURL,
		)
	} else {
		fmt.Printf("brain: connected to Ollama (%s)\n", b.ModelName())
	}

	srv := server.New(b, cfg.Port)

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := srv.ListenAndServe(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "brain: server error: %v\n", err)
		os.Exit(1)
	}
}

// cmdStatus shows the current brain status.
func cmdStatus(cfg config.BrainConfig) {
	// Ollama ping.
	b := brain.New(cfg)
	available := b.Available()

	availStr := "unreachable"
	if available {
		availStr = "connected"
	}
	fmt.Printf("Ollama:  %s (%s)\n", availStr, cfg.OllamaURL)
	fmt.Printf("Model:   %s\n", cfg.Model)

	// SQLite stats.
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Printf("Store:   error opening %s: %v\n", cfg.DBPath, err)
		return
	}
	defer st.Close()

	count := st.SummaryCount()
	fmt.Printf("Store:   %s\n", cfg.DBPath)
	fmt.Printf("Summaries: %d stored\n", count)

	// Features.
	fmt.Println("\nFeatures:")
	printFeature("ingest", cfg.Ingest)
	printFeature("enrich", cfg.Enrich)
	printFeature("guardian", cfg.Guardian)
	printFeature("orchestrate", cfg.Orchestrate)
}

func printFeature(name string, enabled bool) {
	status := "disabled"
	if enabled {
		status = "enabled"
	}
	fmt.Printf("  %-12s %s\n", name, status)
}

// cmdIngest manually triggers ingestion of a source file.
// Reads a JSON request from stdin or from arguments.
func cmdIngest(cfg config.BrainConfig, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "brain ingest: provide a JSON IngestRequest via stdin or as an argument")
		fmt.Fprintln(os.Stderr, "  echo '{\"node_id\":\"...\",\"node_name\":\"...\",\"code\":\"...\"}' | brain ingest")
		os.Exit(1)
	}

	// Parse JSON from first argument or stdin.
	var reqJSON []byte
	if args[0] == "-" {
		var err error
		reqJSON, err = os.ReadFile("/dev/stdin")
		if err != nil {
			fmt.Fprintf(os.Stderr, "brain ingest: read stdin: %v\n", err)
			os.Exit(1)
		}
	} else {
		reqJSON = []byte(args[0])
	}

	var req brain.IngestRequest
	if err := json.Unmarshal(reqJSON, &req); err != nil {
		fmt.Fprintf(os.Stderr, "brain ingest: parse request: %v\n", err)
		os.Exit(1)
	}

	b := brain.New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := b.Ingest(ctx, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "brain ingest: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("node_id: %s\n", resp.NodeID)
	fmt.Printf("summary: %s\n", resp.Summary)
}

// cmdSummaries lists all stored semantic summaries.
func cmdSummaries(cfg config.BrainConfig) {
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "brain summaries: open store: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	summaries, err := st.AllSummaries()
	if err != nil {
		fmt.Fprintf(os.Stderr, "brain summaries: query: %v\n", err)
		os.Exit(1)
	}

	if len(summaries) == 0 {
		fmt.Println("No summaries stored yet. Run 'brain ingest' or use the /v1/ingest endpoint.")
		return
	}

	fmt.Printf("%-40s  %-20s  %s\n", "NODE ID", "NAME", "SUMMARY")
	fmt.Printf("%s\n", repeat("-", 100))
	for _, s := range summaries {
		nodeID := truncate(s.NodeID, 38)
		name := truncate(s.NodeName, 18)
		summary := truncate(s.Summary, 60)
		fmt.Printf("%-40s  %-20s  %s\n", nodeID, name, summary)
	}
	fmt.Printf("\nTotal: %d summaries\n", len(summaries))
}

// cmdReset clears all brain data.
func cmdReset(cfg config.BrainConfig) {
	fmt.Printf("This will delete all summaries and violation cache from %s.\n", cfg.DBPath)
	fmt.Print("Confirm? [y/N] ")

	var confirm string
	fmt.Scanln(&confirm)
	if confirm != "y" && confirm != "Y" {
		fmt.Println("Aborted.")
		return
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "brain reset: open store: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	if err := st.Reset(); err != nil {
		fmt.Fprintf(os.Stderr, "brain reset: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Brain data cleared.")
}

func printUsage() {
	fmt.Printf(`synapses-intelligence v%s — The Thinking Brain for Synapses

Usage:
  brain <command> [flags]

Commands:
  serve        Start the HTTP sidecar server (default port: 11435)
               Flags: -port <int>, -model <string>
  status       Show Ollama connectivity, model, and SQLite stats
  ingest       Manually ingest a code snippet (JSON via argument or stdin)
  summaries    List all stored semantic summaries
  reset        Clear all brain data (prompts for confirmation)
  version      Print version

Environment:
  BRAIN_CONFIG   Path to a JSON config file (optional)

Quick start:
  1. Install Ollama: https://ollama.com
  2. Pull the default model: ollama pull qwen2.5-coder:1.5b
  3. Start the brain: brain serve
  4. Check status: brain status

Config example (brain.json):
  {
    "enabled": true,
    "model": "qwen2.5-coder:1.5b",
    "ollama_url": "http://localhost:11434",
    "timeout_ms": 3000
  }

Model tiers (by system RAM):
  4GB   →  qwen2.5-coder:1.5b  (default, ~900MB)
  4GB+  →  qwen3:1.7b           (recommended, ~1.1GB)
  8GB+  →  qwen3:4b             (~2.5GB)
  16GB+ →  qwen3:8b             (~5GB)
`, version)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func repeat(s string, n int) string {
	result := make([]byte, n*len(s))
	for i := range n {
		copy(result[i*len(s):], s)
	}
	return string(result)
}
