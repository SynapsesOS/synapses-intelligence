// Package config provides BrainConfig loading and defaults for synapses-intelligence.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// BrainConfig holds all configuration for the thinking brain.
type BrainConfig struct {
	// Enabled controls whether the brain is active. Default: false.
	Enabled bool `json:"enabled"`

	// OllamaURL is the base URL of the Ollama server. Default: "http://localhost:11434".
	OllamaURL string `json:"ollama_url,omitempty"`

	// Model is the Ollama model tag to use.
	// Default: "qwen2.5-coder:1.5b" (~900MB, fits in 4GB system RAM).
	// Upgrade options:
	//   "qwen3:1.7b"  — recommended when available (~1.1GB)
	//   "qwen3:4b"    — power user, needs 8GB+ (~2.5GB)
	//   "qwen3:8b"    — enterprise, needs 16GB+ (~5GB)
	Model string `json:"model,omitempty"`

	// TimeoutMS is the per-request LLM timeout in milliseconds. Default: 3000.
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
		Enabled:         false,
		OllamaURL:       "http://localhost:11434",
		Model:           "qwen2.5-coder:1.5b",
		TimeoutMS:       30000,
		DBPath:          filepath.Join(home, ".synapses", "brain.sqlite"),
		Port:            11435,
		Ingest:          true,
		Enrich:          true,
		Guardian:        true,
		Orchestrate:     true,
		ContextBuilder:  true,
		LearningEnabled: true,
		DefaultPhase:    "development",
		DefaultMode:     "standard",
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
func (c *BrainConfig) applyDefaults() {
	if c.OllamaURL == "" {
		c.OllamaURL = "http://localhost:11434"
	}
	if c.Model == "" {
		c.Model = "qwen2.5-coder:1.5b"
	}
	if c.TimeoutMS <= 0 {
		c.TimeoutMS = 30000
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
}
