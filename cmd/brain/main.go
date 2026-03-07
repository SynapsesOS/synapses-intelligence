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

	"github.com/SynapsesOS/synapses-intelligence/config"
	"github.com/SynapsesOS/synapses-intelligence/internal/llm"
	"github.com/SynapsesOS/synapses-intelligence/internal/store"
	"github.com/SynapsesOS/synapses-intelligence/pkg/brain"
	"github.com/SynapsesOS/synapses-intelligence/server"
)

const version = "0.6.1"

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
	case "benchmark":
		cmdBenchmark(cfg)
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
	model := fs.String("model", cfg.Model, "Ollama model to use (ignored when backend=local)")
	fs.Parse(os.Args[2:])

	cfg.Port = *port
	cfg.Model = *model

	ctx := context.Background()

	if cfg.Backend == "local" {
		// Local backend: ensure GGUF exists, auto-download from HuggingFace if missing.
		if cfg.GGUFPath == "" {
			fmt.Fprintln(os.Stderr, "brain: backend=local but gguf_path is not set.")
			fmt.Fprintln(os.Stderr, "       run: brain config hf-repo <username/repo>  to configure auto-download")
			os.Exit(1)
		}
		if !llm.GGUFExists(cfg.GGUFPath) {
			if cfg.HFRepo == "" {
				fmt.Fprintf(os.Stderr, "brain: GGUF not found at %s\n", cfg.GGUFPath)
				fmt.Fprintln(os.Stderr, "       run: brain config hf-repo <username/repo>  to enable auto-download")
				os.Exit(1)
			}
			fmt.Printf("brain: GGUF not found — downloading from HuggingFace (%s/%s)...\n", cfg.HFRepo, cfg.HFFilename)
			path, err := llm.DownloadGGUF(ctx, llm.DownloadConfig{
				Repo:     cfg.HFRepo,
				Filename: cfg.HFFilename,
				DestDir:  cfg.ModelDir,
				Progress: os.Stderr,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "brain: download failed: %v\n", err)
				os.Exit(1)
			}
			cfg.GGUFPath = path
		} else {
			fmt.Printf("brain: local model: %s\n", cfg.GGUFPath)
		}
	} else {
		// Ollama backend: check reachability and pull model if needed.
		bCheck := brain.New(cfg)
		if !bCheck.Available() {
			fmt.Fprintf(os.Stderr,
				"brain: warning: Ollama not reachable at %s — LLM features disabled until Ollama starts\n",
				cfg.OllamaURL,
			)
		} else {
			fmt.Printf("brain: Ollama reachable, checking model %q...\n", bCheck.ModelName())
			if err := bCheck.EnsureModel(ctx, os.Stderr); err != nil {
				fmt.Fprintf(os.Stderr, "brain: warning: could not pull model: %v\n", err)
				fmt.Fprintf(os.Stderr, "brain: run manually:  ollama pull %s\n", bCheck.ModelName())
			} else {
				fmt.Printf("brain: model %q ready\n", bCheck.ModelName())
			}
		}
	}

	b := brain.New(cfg)
	srv := server.New(b, cfg.Port, cfg.TimeoutMS)

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
	fmt.Printf("Backend: %s\n", backendLabel(cfg))

	if cfg.Backend == "local" {
		exists := llm.GGUFExists(cfg.GGUFPath)
		ggufStatus := "missing"
		if exists {
			info, _ := os.Stat(cfg.GGUFPath)
			ggufStatus = fmt.Sprintf("ready (%.1f GB)", float64(info.Size())/(1024*1024*1024))
		}
		fmt.Printf("GGUF:    %s  [%s]\n", cfg.GGUFPath, ggufStatus)
		if cfg.HFRepo != "" {
			fmt.Printf("HF repo: %s/%s\n", cfg.HFRepo, cfg.HFFilename)
		}
		if !exists {
			fmt.Println("         run: brain config download  to fetch the model")
		}
	} else {
		// Ollama ping.
		b := brain.New(cfg)
		available := b.Available()
		availStr := "unreachable"
		if available {
			availStr = "connected"
		}
		fmt.Printf("Ollama:  %s (%s)\n", availStr, cfg.OllamaURL)
		fmt.Printf("Model:   %s\n", cfg.Model)
	}

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

	// SDLC config — read directly from store (no LLM needed).
	b := brain.New(cfg)
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

	case "backend":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: brain config backend <ollama|local>")
			os.Exit(1)
		}
		b := args[1]
		if b != "ollama" && b != "local" {
			fmt.Fprintf(os.Stderr, "brain config: backend must be \"ollama\" or \"local\", got %q\n", b)
			os.Exit(1)
		}
		cfg.Backend = b
		if err := config.SaveFile(cfgPath, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "brain config: save failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("brain config: backend set to %q (saved to %s)\n", b, cfgPath)
		if b == "local" {
			fmt.Println("tip: run  brain config hf-repo <username/repo>  to configure auto-download")
			fmt.Println("     run  brain config download               to fetch the GGUF now")
		}

	case "hf-repo":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: brain config hf-repo <username/repo>")
			os.Exit(1)
		}
		cfg.HFRepo = args[1]
		if err := config.SaveFile(cfgPath, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "brain config: save failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("brain config: hf_repo set to %q (saved to %s)\n", cfg.HFRepo, cfgPath)
		fmt.Printf("tip: run  brain config download  to fetch %s/%s now\n", cfg.HFRepo, cfg.HFFilename)

	case "hf-filename":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: brain config hf-filename <filename.gguf>")
			os.Exit(1)
		}
		cfg.HFFilename = args[1]
		if err := config.SaveFile(cfgPath, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "brain config: save failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("brain config: hf_filename set to %q (saved to %s)\n", cfg.HFFilename, cfgPath)

	case "download":
		// Download the GGUF from HuggingFace to ModelDir.
		if cfg.HFRepo == "" {
			fmt.Fprintln(os.Stderr, "brain config: hf_repo is not set.")
			fmt.Fprintln(os.Stderr, "       run: brain config hf-repo <username/repo>  first")
			os.Exit(1)
		}
		fmt.Printf("Downloading %s/%s → %s\n", cfg.HFRepo, cfg.HFFilename, cfg.ModelDir)
		path, err := llm.DownloadGGUF(context.Background(), llm.DownloadConfig{
			Repo:     cfg.HFRepo,
			Filename: cfg.HFFilename,
			DestDir:  cfg.ModelDir,
			Progress: os.Stdout,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "brain config: download failed: %v\n", err)
			os.Exit(1)
		}
		// Auto-save gguf_path if not already set.
		if cfg.GGUFPath == "" || cfg.GGUFPath != path {
			cfg.GGUFPath = path
			_ = config.SaveFile(cfgPath, cfg)
		}
		fmt.Printf("brain config: model ready at %s\n", path)
		fmt.Println("tip: run  brain config backend local  then  brain serve  to use it")

	default:
		fmt.Fprintf(os.Stderr, "brain config: unknown subcommand %q\n", subCmd)
		fmt.Fprintln(os.Stderr, "usage: brain config model <tag> [--pull]")
		fmt.Fprintln(os.Stderr, "       brain config ollama <url>")
		fmt.Fprintln(os.Stderr, "       brain config backend <ollama|local>")
		fmt.Fprintln(os.Stderr, "       brain config hf-repo <username/repo>")
		fmt.Fprintln(os.Stderr, "       brain config hf-filename <file.gguf>")
		fmt.Fprintln(os.Stderr, "       brain config download")
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

// AcceleratorType identifies the GPU backend available on this machine.
type AcceleratorType string

const (
	AccelCPU   AcceleratorType = "cpu"
	AccelCUDA  AcceleratorType = "cuda"  // NVIDIA GPU
	AccelMetal AcceleratorType = "metal" // Apple Silicon (M-series) unified GPU memory
	AccelROCm  AcceleratorType = "rocm"  // AMD GPU
)

// detectAccelerator returns the best GPU accelerator type available.
// Uses pure Go — no CGo. Detection is best-effort and fail-silent.
//
//   - macOS → Metal (Apple Silicon always has unified GPU memory)
//   - Linux/Windows + nvidia-smi in PATH → CUDA
//   - Linux/Windows + rocm-smi in PATH → ROCm
//   - Otherwise → CPU-only
func detectAccelerator() AcceleratorType {
	if runtime.GOOS == "darwin" {
		return AccelMetal
	}
	if _, err := exec.LookPath("nvidia-smi"); err == nil {
		return AccelCUDA
	}
	if _, err := exec.LookPath("rocm-smi"); err == nil {
		return AccelROCm
	}
	return AccelCPU
}

// tierModelsForAccelerator returns recommended tiered model names based on the
// detected accelerator. On GPU machines the full Qwen3.5 family is used
// (thinking mode works). On CPU-only machines qwen2.5-coder is used instead
// (proven fast: 10-20s on CPU; Qwen3.5 times out at >60s on CPU-only).
func tierModelsForAccelerator(accel AcceleratorType) (ingest, guardian, enrich, orchestrate string) {
	switch accel {
	case AccelMetal, AccelCUDA, AccelROCm:
		// GPU: Qwen3.5 family with thinking mode (auto-detected in brain.go).
		return "qwen3.5:0.8b", "qwen3.5:2b", "qwen3.5:4b", "qwen3.5:9b"
	default:
		// CPU-only: qwen2.5-coder proven fast (10-20s). Qwen3.5 times out on CPU.
		// Tier 0+1 use 1.5b (fast ingest+guardian). Tier 2+3 use 7b (richer analysis).
		return "qwen2.5-coder:1.5b", "qwen2.5-coder:1.5b", "qwen2.5-coder:7b", "qwen2.5-coder:7b"
	}
}

// probeMaxDuration is the per-model timeout used during setup and benchmark.
// Models that can't respond within this time are considered too slow for use.
const probeMaxDuration = 90 * time.Second

// cmdSetup runs an interactive-free setup wizard: detects RAM, probes installed
// Ollama models for actual inference latency, picks the fastest, and writes brain.json.
func cmdSetup(cfg config.BrainConfig, cfgPath string) {
	fmt.Println("synapses-intelligence setup")
	fmt.Println("────────────────────────────")

	// Step 1: RAM detection.
	ramGB := systemRAMGB()
	if ramGB > 0 {
		fmt.Printf("  System RAM:  %d GB\n", ramGB)
	} else {
		fmt.Println("  System RAM:  unknown")
		ramGB = 4
	}

	// Step 2: Ollama check.
	if !ollamaInstalled() {
		fmt.Println("  ✗ Ollama not found on PATH.")
		fmt.Println()
		fmt.Println("  Install Ollama first:")
		switch runtime.GOOS {
		case "darwin":
			fmt.Println("    brew install ollama")
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

	ctx := context.Background()

	// Step 3: Detect GPU accelerator (no CGo — pure PATH/OS detection).
	accel := detectAccelerator()
	accelLabel := map[AcceleratorType]string{
		AccelMetal: "Apple Silicon / Metal (unified GPU memory)",
		AccelCUDA:  "NVIDIA CUDA",
		AccelROCm:  "AMD ROCm",
		AccelCPU:   "CPU-only (no GPU detected)",
	}[accel]
	fmt.Printf("  Accelerator: %s\n", accelLabel)

	// Step 4: Discover installed models and probe actual latency.
	// This is more reliable than RAM-based heuristics — actual measurement
	// catches CPU architecture differences that theory cannot predict.
	installed, err := llm.ListInstalledModels(ctx, cfg.OllamaURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ Cannot reach Ollama at %s: %v\n", cfg.OllamaURL, err)
		fmt.Fprintf(os.Stderr, "    Start it with:  ollama serve\n")
		os.Exit(1)
	}

	var chosenModel string
	var chosenLatency time.Duration

	if len(installed) > 0 {
		fmt.Printf("\n  Probing %d installed model(s) (max %s each)...\n",
			len(installed), probeMaxDuration)
		chosenModel, chosenLatency = pickFastestModel(ctx, cfg.OllamaURL, installed, probeMaxDuration)
		if chosenModel != "" {
			fmt.Printf("\n  ✓ Fastest model: %s  (%s)\n", chosenModel, chosenLatency.Round(time.Millisecond))
		} else {
			fmt.Println("\n  ⚠ No installed model responded within", probeMaxDuration)
		}
	}

	// Step 5: Fall back to RAM-based recommendation if probe found nothing usable.
	if chosenModel == "" {
		chosenModel, _ = recommendedModel(ramGB)
		fmt.Printf("  Falling back to RAM-based recommendation: %s\n", chosenModel)
		fmt.Println("  (Run  brain benchmark  after pulling models to confirm actual speed)")
	}

	// Step 6: Assign tiered models based on accelerator type.
	// GPU machines use the full Qwen3.5 family (fast enough for thinking mode).
	// CPU-only machines use qwen2.5-coder (proven <20s per call; Qwen3.5 times out on CPU).
	cfg.Enabled = true
	ingestM, guardianM, enrichM, orchestrateM := tierModelsForAccelerator(accel)
	cfg.ModelIngest = ingestM
	cfg.ModelGuardian = guardianM
	cfg.ModelEnrich = enrichM
	cfg.ModelOrchestrate = orchestrateM
	cfg.Model = enrichM // primary model = Tier 2 (enrich)

	if accel == AccelCPU {
		fmt.Printf("  → CPU-only: tiers set to %s (fast) / %s (deep)\n", ingestM, enrichM)
		fmt.Println("    Tip: thinking mode is auto-disabled for non-Qwen3 models.")
	} else {
		fmt.Printf("  → GPU (%s): tiers set to Qwen3.5 family (0.8b/2b/4b/9b)\n", accel)
		fmt.Println("    Tip: thinking mode is auto-enabled for Qwen3.5 models.")
	}

	// Set timeout to 3× measured latency (or 60s default when latency unknown).
	if chosenLatency > 0 {
		cfg.TimeoutMS = int(chosenLatency.Milliseconds() * 3)
		if cfg.TimeoutMS < 30000 {
			cfg.TimeoutMS = 30000 // minimum 30s
		}
	} else {
		cfg.TimeoutMS = 60000
	}
	fmt.Printf("  timeout_ms set to %dms (3× measured latency)\n", cfg.TimeoutMS)

	// Step 6: Save config.
	if err := config.SaveFile(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "setup: could not write config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  ✓ Config saved to %s\n", cfgPath)

	// Step 7: Pull the model if not already installed.
	b := brain.New(cfg)
	if !b.Available() {
		fmt.Fprintf(os.Stderr, "\n  ✗ Ollama is not running.\n")
		fmt.Fprintf(os.Stderr, "    Start it with:  ollama serve\n")
		os.Exit(1)
	}
	if err := b.EnsureModel(ctx, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "\n  ✗ Pull failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "    Try manually:  ollama pull %s\n", cfg.Model)
		os.Exit(1)
	}

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
	fmt.Println("  Run  brain benchmark  at any time to re-measure model latency.")
	fmt.Println("  Run  brain config model <tag> --pull  to switch models later.")
}

// pickFastestModel probes each model in order and returns the name and latency
// of the fastest one that responds within maxDuration. Returns ("", 0) if none do.
func pickFastestModel(ctx context.Context, ollamaURL string, models []string, maxDuration time.Duration) (string, time.Duration) {
	type result struct {
		model   string
		latency time.Duration
	}

	var best result
	for _, model := range models {
		client := llm.NewOllamaClient(ollamaURL, model, int(maxDuration.Milliseconds()))
		fmt.Printf("    %-35s ", model)
		lat, err := client.ProbeLatency(ctx, maxDuration)
		if err != nil {
			fmt.Printf("❌  (%v)\n", shortErr(err))
			continue
		}
		fmt.Printf("✅  %s\n", lat.Round(time.Millisecond))
		if best.model == "" || lat < best.latency {
			best = result{model, lat}
		}
	}
	return best.model, best.latency
}

// cmdBenchmark probes all installed Ollama models and prints a latency table.
// Use this to decide which model to assign to each brain tier.
func cmdBenchmark(cfg config.BrainConfig) {
	ctx := context.Background()

	fmt.Println("brain benchmark — measuring actual inference latency")
	fmt.Println("────────────────────────────────────────────────────")
	fmt.Printf("  Ollama: %s\n", cfg.OllamaURL)
	fmt.Printf("  Max probe time per model: %s\n\n", probeMaxDuration)

	installed, err := llm.ListInstalledModels(ctx, cfg.OllamaURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchmark: cannot reach Ollama: %v\n", err)
		os.Exit(1)
	}
	if len(installed) == 0 {
		fmt.Println("  No models installed. Run  ollama pull <model>  first.")
		os.Exit(0)
	}

	type row struct {
		model   string
		latency time.Duration
		ok      bool
	}
	rows := make([]row, 0, len(installed))

	fmt.Printf("  %-35s  %s\n", "MODEL", "LATENCY")
	fmt.Printf("  %s\n", repeat("-", 55))
	for _, model := range installed {
		client := llm.NewOllamaClient(cfg.OllamaURL, model, int(probeMaxDuration.Milliseconds()))
		fmt.Printf("  %-35s  ", model)
		lat, err := client.ProbeLatency(ctx, probeMaxDuration)
		if err != nil {
			fmt.Printf("timeout / error  (%v)\n", shortErr(err))
			rows = append(rows, row{model, 0, false})
		} else {
			fmt.Printf("%s\n", lat.Round(time.Millisecond))
			rows = append(rows, row{model, lat, true})
		}
	}

	// Find fastest.
	var fastest row
	for _, r := range rows {
		if r.ok && (fastest.model == "" || r.latency < fastest.latency) {
			fastest = r
		}
	}

	fmt.Println()
	if fastest.model != "" {
		recommendedMS := int(fastest.latency.Milliseconds() * 3)
		if recommendedMS < 30000 {
			recommendedMS = 30000
		}
		fmt.Printf("  Fastest: %s (%s)\n", fastest.model, fastest.latency.Round(time.Millisecond))
		fmt.Printf("  Recommended timeout_ms: %d (3× latency)\n", recommendedMS)
		fmt.Println()
		fmt.Printf("  To apply:  brain config model %s\n", fastest.model)
		fmt.Printf("             brain setup   (re-runs probe and writes brain.json)\n")
	} else {
		fmt.Println("  No model responded within the probe timeout.")
		fmt.Println("  Consider pulling a smaller model:  ollama pull qwen2.5-coder:1.5b")
	}
}

// shortErr truncates long error messages for display.
// backendLabel returns a human-readable backend description for cmdStatus.
func backendLabel(cfg config.BrainConfig) string {
	if cfg.Backend == "local" {
		return fmt.Sprintf("local (embedded GGUF, no Ollama required)")
	}
	return fmt.Sprintf("ollama (%s)", cfg.OllamaURL)
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 50 {
		return s[:47] + "..."
	}
	return s
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
