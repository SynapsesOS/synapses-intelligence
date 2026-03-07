// Package config provides BrainConfig loading and defaults for synapses-intelligence.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// BrainConfig holds all configuration for the thinking brain.
type BrainConfig struct {
	// Enabled controls whether the brain is active. Default: false.
	Enabled bool `json:"enabled"`

	// OllamaURL is the base URL of the Ollama server. Default: "http://localhost:11434".
	OllamaURL string `json:"ollama_url,omitempty"`

	// Model is the primary Ollama model tag (enrichment fallback when ModelEnrich is unset).
	// Default: "qwen3.5:4b" — fast on CPU (~12s), beats qwen2.5-coder:7b at 1/3 the size.
	// Legacy option: "qwen2.5-coder:7b" (~4.5GB, needs 6GB VRAM or 8GB RAM).
	Model string `json:"model,omitempty"`

	// FastModel is the Ollama model tag for bulk ingestion (fallback when ModelIngest is unset).
	// Default: "qwen3.5:0.8b" — runs in <3s on CPU, fits in 2GB RAM.
	// Legacy option: "qwen2.5-coder:1.5b" (~900MB).
	FastModel string `json:"fast_model,omitempty"`

	// --- Tiered Nervous System: per-task model assignment ---
	// Each tier defaults to the appropriate Qwen3.5 model.
	// Set to "" to fall back to FastModel/Model. All 4 can point to the same model.

	// ModelIngest is the model for bulk node summarization at index time.
	// Tier 0 (Reflex): simple extraction, no reasoning needed. Default: "qwen3.5:0.8b".
	ModelIngest string `json:"model_ingest,omitempty"`

	// ModelGuardian is the model for rule violation explanations.
	// Tier 1 (Sensory): structured plain-English output. Default: "qwen3.5:2b".
	ModelGuardian string `json:"model_guardian,omitempty"`

	// ModelEnrich is the model for architectural enrichment and insight generation.
	// Tier 2 (Specialist): complex analysis across multiple callers/callees. Default: "qwen3.5:4b".
	ModelEnrich string `json:"model_enrich,omitempty"`

	// ModelOrchestrate is the model for multi-agent conflict resolution.
	// Tier 3 (Architect): deep reasoning about competing scope claims. Default: "qwen3.5:9b".
	ModelOrchestrate string `json:"model_orchestrate,omitempty"`

	// Backend selects the LLM backend.
	// "ollama" (default): calls the Ollama HTTP sidecar.
	// "local": loads a GGUF file directly via gollama (no Ollama required).
	// Build with -tags llamacpp and CGO_ENABLED=1 for the local backend.
	Backend string `json:"backend,omitempty"`

	// GGUFPath is the path to the fine-tuned GGUF model file.
	// Only used when Backend == "local". If empty, auto-computed as ModelDir/HFFilename.
	// Example: "~/.synapses/models/sil-coder-Q5_K_M.gguf"
	GGUFPath string `json:"gguf_path,omitempty"`

	// ModelDir is the directory where GGUF models are stored.
	// Default: ~/.synapses/models/
	ModelDir string `json:"model_dir,omitempty"`

	// HFRepo is the HuggingFace repository to download the model from.
	// Example: "divish/sil-coder"
	// Used by `brain config download` and `brain serve` (auto-download on first run).
	HFRepo string `json:"hf_repo,omitempty"`

	// HFFilename is the GGUF filename within the HuggingFace repo.
	// Default: "sil-coder-Q5_K_M.gguf"
	HFFilename string `json:"hf_filename,omitempty"`

	// TimeoutMS is the per-request LLM timeout in milliseconds.
	// The HTTP server WriteTimeout is set to 2× this value. Default: 60000 (60s).
	// Must exceed the slowest LLM inference time on your hardware (~25s for 9b CPU).
	TimeoutMS int `json:"timeout_ms,omitempty"`

	// DBPath is the path to the brain's own SQLite database.
	// Default: ~/.synapses/brain.sqlite
	DBPath string `json:"db_path,omitempty"`

	// Port is the HTTP server port for sidecar mode. Default: 11435.
	Port int `json:"port,omitempty"`

	// v0.1.0 feature flags — all default to true when Enabled=true.
	Ingest      bool `json:"ingest"`
	Enrich      bool `json:"enrich"`
	Guardian    bool `json:"guardian"`
	Orchestrate bool `json:"orchestrate"`

	// v0.2.0: Context Packet and SDLC intelligence.
	// ContextBuilder enables BuildContextPacket (default: true when Enabled=true).
	ContextBuilder bool `json:"context_builder"`
	// LearningEnabled enables the decision log and co-occurrence learning (default: true).
	LearningEnabled bool `json:"learning_enabled"`
	// DefaultPhase is the initial SDLC phase stored in brain.sqlite if none is set.
	// Values: "planning" | "development" | "testing" | "review" | "deployment"
	// Default: "development"
	DefaultPhase string `json:"default_phase,omitempty"`
	// DefaultMode is the initial quality mode stored in brain.sqlite if none is set.
	// Values: "quick" | "standard" | "enterprise"
	// Default: "standard"
	DefaultMode string `json:"default_mode,omitempty"`
}

// DefaultConfig returns a BrainConfig with all defaults applied.
func DefaultConfig() BrainConfig {
	home, _ := os.UserHomeDir()
	return BrainConfig{
		Enabled:          false,
		OllamaURL:        "http://localhost:11434",
		Model:            "qwen3.5:4b",
		FastModel:        "qwen3.5:0.8b",
		ModelIngest:      "qwen3.5:0.8b",
		ModelGuardian:    "qwen3.5:2b",
		ModelEnrich:      "qwen3.5:4b",
		ModelOrchestrate: "qwen3.5:9b",
		TimeoutMS:        60000,
		DBPath:           filepath.Join(home, ".synapses", "brain.sqlite"),
		ModelDir:         filepath.Join(home, ".synapses", "models"),
		HFFilename:       "sil-coder-Q5_K_M.gguf",
		Port:             11435,
		Ingest:           true,
		Enrich:           true,
		Guardian:         true,
		Orchestrate:      true,
		ContextBuilder:   true,
		LearningEnabled:  true,
		DefaultPhase:     "development",
		DefaultMode:      "standard",
	}
}

// DefaultConfigPath returns the conventional path for brain.json:
// $BRAIN_CONFIG if set, otherwise ~/.synapses/brain.json.
func DefaultConfigPath() string {
	if p := os.Getenv("BRAIN_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".synapses", "brain.json")
}

// SaveFile writes cfg as indented JSON to path, creating parent directories
// as needed.
func SaveFile(path string, cfg BrainConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// LoadFile reads a JSON config file and merges it onto the defaults.
// Missing fields in the file retain their default values.
func LoadFile(path string) (BrainConfig, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	cfg.applyDefaults()
	return cfg, nil
}

// applyDefaults fills in zero values with defaults.
// Tier models fall back to the legacy fast_model/model fields if unset.
func (c *BrainConfig) applyDefaults() {
	if c.OllamaURL == "" {
		c.OllamaURL = "http://localhost:11434"
	}
	if c.Model == "" {
		c.Model = "qwen3.5:4b"
	}
	if c.FastModel == "" {
		c.FastModel = "qwen3.5:0.8b"
	}
	// Tier fallback chain: tier model → legacy field → hardcoded default
	if c.ModelIngest == "" {
		c.ModelIngest = c.FastModel
	}
	if c.ModelGuardian == "" {
		c.ModelGuardian = "qwen3.5:2b"
	}
	if c.ModelEnrich == "" {
		c.ModelEnrich = c.Model
	}
	if c.ModelOrchestrate == "" {
		c.ModelOrchestrate = c.Model
	}
	if c.TimeoutMS <= 0 {
		c.TimeoutMS = 60000
	}
	if c.Port <= 0 {
		c.Port = 11435
	}
	if c.DBPath == "" {
		home, _ := os.UserHomeDir()
		c.DBPath = filepath.Join(home, ".synapses", "brain.sqlite")
	}
	if c.DefaultPhase == "" {
		c.DefaultPhase = "development"
	}
	if c.DefaultMode == "" {
		c.DefaultMode = "standard"
	}
	if c.ModelDir == "" {
		home, _ := os.UserHomeDir()
		c.ModelDir = filepath.Join(home, ".synapses", "models")
	}
	if c.HFFilename == "" {
		c.HFFilename = "sil-coder-Q5_K_M.gguf"
	}
	// Expand leading ~/ in paths.
	for _, p := range []*string{&c.DBPath, &c.GGUFPath, &c.ModelDir} {
		if strings.HasPrefix(*p, "~/") {
			home, _ := os.UserHomeDir()
			*p = filepath.Join(home, (*p)[2:])
		}
	}
	// Auto-compute GGUFPath from ModelDir+HFFilename when backend=local and not set explicitly.
	if c.Backend == "local" && c.GGUFPath == "" {
		c.GGUFPath = filepath.Join(c.ModelDir, c.HFFilename)
	}
}
