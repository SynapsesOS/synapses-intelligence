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
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/Divish1032/synapses-intelligence/config"
	"github.com/Divish1032/synapses-intelligence/internal/store"
	"github.com/Divish1032/synapses-intelligence/pkg/brain"
	"github.com/Divish1032/synapses-intelligence/server"
)

const version = "0.3.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Load config from the default path (BRAIN_CONFIG env or ~/.synapses/brain.json).
	// If the file doesn't exist yet that's fine — defaults apply.
	cfgPath := config.DefaultConfigPath()
	cfg, _ := config.LoadFile(cfgPath) // errors silently fall back to defaults

	// Override enabled for CLI commands — the binary is always "enabled".
	cfg.Enabled = true

	switch os.Args[1] {
	case "serve":
		cmdServe(cfg)
	case "status":
		cmdStatus(cfg)
	case "config":
		cmdConfig(cfg, cfgPath, os.Args[2:])
	case "setup":
		cmdSetup(cfg, cfgPath)
	case "ingest":
		cmdIngest(cfg, os.Args[2:])
	case "summaries":
		cmdSummaries(cfg)
	case "sdlc":
		cmdSDLC(cfg, os.Args[2:])
	case "decisions":
		cmdDecisions(cfg, os.Args[2:])
	case "patterns":
		cmdPatterns(cfg)
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

	ctx := context.Background()
	if !b.Available() {
		fmt.Fprintf(os.Stderr,
			"brain: warning: Ollama not reachable at %s — LLM features disabled until Ollama starts\n",
			cfg.OllamaURL,
		)
	} else {
		fmt.Printf("brain: Ollama reachable, checking model %q...\n", b.ModelName())
		if err := b.EnsureModel(ctx, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "brain: warning: could not pull model: %v\n", err)
			fmt.Fprintf(os.Stderr, "brain: run manually:  ollama pull %s\n", b.ModelName())
		} else {
			fmt.Printf("brain: model %q ready\n", b.ModelName())
		}
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
	printFeature("context_builder", cfg.ContextBuilder)
	printFeature("learning", cfg.LearningEnabled)

	// SDLC config (reuse b from above).
	sdlcCfg := b.GetSDLCConfig()
	fmt.Printf("\nSDLC:\n")
	fmt.Printf("  %-12s %s\n", "phase", sdlcCfg.Phase)
	fmt.Printf("  %-12s %s\n", "mode", sdlcCfg.QualityMode)
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

// cmdSDLC shows or sets the SDLC phase and quality mode.
//
//	brain sdlc              Show current phase and mode
//	brain sdlc phase <p>    Set phase (planning|development|testing|review|deployment)
//	brain sdlc mode <m>     Set mode (quick|standard|enterprise)
func cmdSDLC(cfg config.BrainConfig, args []string) {
	b := brain.New(cfg)

	if len(args) == 0 {
		// Show current SDLC config.
		cfg := b.GetSDLCConfig()
		fmt.Printf("Phase:        %s\n", cfg.Phase)
		fmt.Printf("Quality Mode: %s\n", cfg.QualityMode)
		if cfg.UpdatedAt != "" {
			fmt.Printf("Updated:      %s", cfg.UpdatedAt)
			if cfg.UpdatedBy != "" {
				fmt.Printf(" by %s", cfg.UpdatedBy)
			}
			fmt.Println()
		}
		return
	}

	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "brain sdlc: usage: brain sdlc phase <phase>  OR  brain sdlc mode <mode>")
		os.Exit(1)
	}

	switch args[0] {
	case "phase":
		if err := b.SetSDLCPhase(brain.SDLCPhase(args[1]), "cli"); err != nil {
			fmt.Fprintf(os.Stderr, "brain sdlc phase: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Phase set to: %s\n", args[1])
	case "mode":
		if err := b.SetQualityMode(brain.QualityMode(args[1]), "cli"); err != nil {
			fmt.Fprintf(os.Stderr, "brain sdlc mode: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Quality mode set to: %s\n", args[1])
	default:
		fmt.Fprintf(os.Stderr, "brain sdlc: unknown subcommand %q — use 'phase' or 'mode'\n", args[0])
		os.Exit(1)
	}
}

// cmdDecisions lists recent decision log entries.
//
//	brain decisions              Show last 20 decisions
//	brain decisions <entity>     Show decisions for a specific entity
func cmdDecisions(cfg config.BrainConfig, args []string) {
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "brain decisions: open store: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	entity := ""
	if len(args) > 0 {
		entity = args[0]
	}

	entries, err := st.GetRecentDecisions(entity, 20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "brain decisions: query: %v\n", err)
		os.Exit(1)
	}

	if len(entries) == 0 {
		if entity != "" {
			fmt.Printf("No decisions recorded for entity %q.\n", entity)
		} else {
			fmt.Println("No decisions recorded yet. Agents call POST /v1/decision to log decisions.")
		}
		return
	}

	fmt.Printf("%-20s  %-12s  %-20s  %-8s  %s\n", "ENTITY", "PHASE", "ACTION", "OUTCOME", "AGENT")
	fmt.Println(repeat("-", 90))
	for _, e := range entries {
		fmt.Printf("%-20s  %-12s  %-20s  %-8s  %s\n",
			truncate(e.EntityName, 18),
			truncate(e.Phase, 10),
			truncate(e.Action, 18),
			truncate(e.Outcome, 6),
			truncate(e.AgentID, 20),
		)
	}
	fmt.Printf("\nShowing %d entries\n", len(entries))
}

// cmdPatterns lists learned co-occurrence patterns.
func cmdPatterns(cfg config.BrainConfig) {
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "brain patterns: open store: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	patterns, err := st.AllPatterns()
	if err != nil {
		fmt.Fprintf(os.Stderr, "brain patterns: query: %v\n", err)
		os.Exit(1)
	}

	if len(patterns) == 0 {
		fmt.Println("No patterns learned yet. Patterns are built from POST /v1/decision calls.")
		return
	}

	fmt.Printf("%-25s  %-25s  %6s  %6s  %s\n", "TRIGGER", "CO-CHANGE", "CONF", "COUNT", "REASON")
	fmt.Println(repeat("-", 100))
	for _, p := range patterns {
		fmt.Printf("%-25s  %-25s  %6.2f  %6d  %s\n",
			truncate(p.Trigger, 23),
			truncate(p.CoChange, 23),
			p.Confidence,
			p.CoCount,
			truncate(p.Reason, 40),
		)
	}
	fmt.Printf("\nTotal: %d patterns\n", len(patterns))
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
  serve           Start the HTTP sidecar server (default port: 11435)
                  Flags: -port <int>, -model <string>
  status          Show Ollama connectivity, model, SQLite stats, SDLC config
  ingest          Manually ingest a code snippet (JSON via argument or stdin)
  summaries       List all stored semantic summaries
  sdlc            Show or set SDLC phase and quality mode
                  Examples:
                    brain sdlc
                    brain sdlc phase testing
                    brain sdlc mode enterprise
  decisions       List recent agent decision log entries
                  brain decisions [entity_name]
  patterns        List learned co-occurrence patterns
  reset           Clear all brain data (prompts for confirmation)
  version         Print version
  setup           Interactive-free setup: detects RAM, picks model, pulls it, writes config
  config          Read or update brain.json settings
                  brain config                       — show current config
                  brain config model <tag> [--pull]  — set model (optionally pull now)
                  brain config ollama <url>          — set Ollama URL

Quick start (3 steps):
  1. brain setup          ← detects RAM, picks model, pulls it, writes config
  2. brain serve          ← start the sidecar
  3. Add to synapses.json: {"brain": {"url": "http://localhost:11435", "enable_llm": true}}

Config file: ~/.synapses/brain.json  (override with BRAIN_CONFIG env var)

Model tiers (by system RAM):
  any   →  qwen2.5-coder:1.5b  (default, ~900MB)
  4GB+  →  qwen3:1.7b           (recommended, ~1.1GB, thinking mode)
  8GB+  →  qwen3:4b             (~2.5GB)
  16GB+ →  qwen3:8b             (~5GB)

  Change model:  brain config model qwen3:4b --pull
`, version)
}

// cmdConfig reads or mutates brain.json settings.
//
//	brain config                   — show current config path + active settings
//	brain config model <tag>       — set model and save; use --pull to also pull it
//	brain config ollama <url>      — set Ollama URL and save
func cmdConfig(cfg config.BrainConfig, cfgPath string, args []string) {
	if len(args) == 0 {
		// Show current effective config.
		fmt.Printf("Config file: %s\n", cfgPath)
		data, _ := json.MarshalIndent(cfg, "", "  ")
		fmt.Println(string(data))
		return
	}

	subCmd := args[0]
	switch subCmd {
	case "model":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: brain config model <tag> [--pull]")
			os.Exit(1)
		}
		newModel := args[1]
		pull := len(args) > 2 && args[2] == "--pull"

		cfg.Model = newModel
		if err := config.SaveFile(cfgPath, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "brain config: save failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("brain config: model set to %q (saved to %s)\n", newModel, cfgPath)

		if pull {
			b := brain.New(cfg)
			if !b.Available() {
				fmt.Fprintf(os.Stderr, "brain config: Ollama not reachable at %s — skipping pull\n", cfg.OllamaURL)
				return
			}
			fmt.Printf("brain config: pulling %q...\n", newModel)
			if err := b.EnsureModel(context.Background(), os.Stderr); err != nil {
				fmt.Fprintf(os.Stderr, "brain config: pull failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("\nbrain config: model %q ready\n", newModel)
		} else {
			fmt.Printf("tip: run  brain config model %s --pull  to download now, or it will be pulled on next  brain serve\n", newModel)
		}

	case "ollama":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: brain config ollama <url>")
			os.Exit(1)
		}
		cfg.OllamaURL = args[1]
		if err := config.SaveFile(cfgPath, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "brain config: save failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("brain config: ollama_url set to %q (saved to %s)\n", cfg.OllamaURL, cfgPath)

	default:
		fmt.Fprintf(os.Stderr, "brain config: unknown subcommand %q\n", subCmd)
		fmt.Fprintln(os.Stderr, "usage: brain config model <tag> [--pull]")
		fmt.Fprintln(os.Stderr, "       brain config ollama <url>")
		os.Exit(1)
	}
}

// modelTiers lists the recommended models ordered from smallest to largest.
var modelTiers = []struct {
	minGB int
	model string
	size  string
	note  string
}{
	{0, "qwen2.5-coder:1.5b", "~900MB", "default, runs on any machine"},
	{4, "qwen3:1.7b", "~1.1GB", "recommended — thinking mode"},
	{8, "qwen3:4b", "~2.5GB", "power user"},
	{16, "qwen3:8b", "~5GB", "enterprise"},
}

// systemRAMGB returns total system RAM in gigabytes (best-effort, returns 0 on failure).
func systemRAMGB() int {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err != nil {
			return 0
		}
		var bytes int64
		if _, err := fmt.Sscanf(string(out), "%d", &bytes); err != nil {
			return 0
		}
		return int(bytes / (1024 * 1024 * 1024))
	case "linux":
		out, err := exec.Command("grep", "MemTotal", "/proc/meminfo").Output()
		if err != nil {
			return 0
		}
		var kb int64
		if _, err := fmt.Sscanf(string(out), "MemTotal: %d kB", &kb); err != nil {
			return 0
		}
		return int(kb / (1024 * 1024))
	}
	return 0
}

// recommendedModel returns the largest model that fits in the detected RAM.
func recommendedModel(ramGB int) (string, string) {
	best := modelTiers[0]
	for _, t := range modelTiers {
		if ramGB >= t.minGB {
			best = t
		}
	}
	return best.model, best.size
}

// ollamaInstalled returns true if the `ollama` binary is on PATH.
func ollamaInstalled() bool {
	_, err := exec.LookPath("ollama")
	return err == nil
}

// cmdSetup runs an interactive-free setup wizard: detects RAM, picks a model,
// checks/explains Ollama, pulls the model, and writes brain.json.
func cmdSetup(cfg config.BrainConfig, cfgPath string) {
	fmt.Println("synapses-intelligence setup")
	fmt.Println("────────────────────────────")

	// Step 1: RAM detection.
	ramGB := systemRAMGB()
	if ramGB > 0 {
		fmt.Printf("  System RAM:  %d GB\n", ramGB)
	} else {
		fmt.Println("  System RAM:  unknown")
		ramGB = 4 // safe default
	}

	// Step 2: Model recommendation.
	model, size := recommendedModel(ramGB)
	fmt.Printf("  Recommended: %s  (%s)\n", model, size)
	fmt.Println()
	fmt.Println("  All tiers:")
	for _, t := range modelTiers {
		marker := "  "
		if t.model == model {
			marker = "→ "
		}
		fmt.Printf("    %s%-26s %s   %s\n", marker, t.model, t.size, t.note)
	}
	fmt.Println()

	// Step 3: Ollama check.
	if !ollamaInstalled() {
		fmt.Println("  ✗ Ollama not found on PATH.")
		fmt.Println()
		fmt.Println("  Install Ollama first:")
		switch runtime.GOOS {
		case "darwin":
			fmt.Println("    brew install ollama")
			fmt.Println("    # or download from https://ollama.com/download")
		case "linux":
			fmt.Println("    curl -fsSL https://ollama.com/install.sh | sh")
		default:
			fmt.Println("    https://ollama.com/download")
		}
		fmt.Println()
		fmt.Println("  Then run  brain setup  again.")
		os.Exit(1)
	}
	fmt.Println("  ✓ Ollama installed")

	// Step 4: Update config with recommended model if user hasn't already customised.
	if cfg.Model == config.DefaultConfig().Model {
		cfg.Model = model
	}
	cfg.Enabled = true

	// Step 5: Save config.
	if err := config.SaveFile(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "setup: could not write config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  ✓ Config saved to %s\n", cfgPath)

	// Step 6: Pull model.
	fmt.Printf("  Pulling %s...\n", cfg.Model)
	b := brain.New(cfg)
	if !b.Available() {
		fmt.Fprintf(os.Stderr, "\n  ✗ Ollama is installed but not running.\n")
		fmt.Fprintf(os.Stderr, "    Start it with:  ollama serve\n")
		fmt.Fprintf(os.Stderr, "    Then run:       brain setup\n")
		os.Exit(1)
	}
	if err := b.EnsureModel(context.Background(), os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "\n  ✗ Pull failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "    Try manually:  ollama pull %s\n", cfg.Model)
		os.Exit(1)
	}

	// Step 7: Done.
	fmt.Println()
	fmt.Println("  ✓ Model ready")
	fmt.Println()
	fmt.Println("────────────────────────────")
	fmt.Println("Setup complete. Next steps:")
	fmt.Println()
	fmt.Println("  1. Start the brain sidecar:")
	fmt.Println("       brain serve")
	fmt.Println()
	fmt.Println("  2. Add brain URL to your project's synapses.json:")
	fmt.Println(`       { "brain": { "url": "http://localhost:11435", "enable_llm": true } }`)
	fmt.Println()
	fmt.Println("  3. (Re)start synapses:")
	fmt.Println("       synapses start --path .")
	fmt.Println()
	fmt.Printf("  To change model later:  brain config model <tag> --pull\n")
	fmt.Printf("  Available tags: qwen2.5-coder:1.5b  qwen3:1.7b  qwen3:4b  qwen3:8b\n")
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
