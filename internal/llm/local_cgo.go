//go:build llamacpp

package llm

// local_cgo.go — CGo implementation of LocalClient using godeps/gollama.
//
// Build requirements:
//   CGO_ENABLED=1
//   godeps/gollama in go.mod (add with: go get github.com/godeps/gollama)
//
// Platform notes:
//   macOS (Apple Silicon): Requires Xcode command-line tools for Metal.
//   Linux (NVIDIA):        Requires CUDA toolkit for CuBLAS acceleration.
//   Linux/Windows (CPU):   Uses AVX-512/AVX2 SIMD automatically via llama.cpp.
//
// godeps/gollama is the maintained fork of the abandoned go-skynet/go-llama.cpp.
// The llama.cpp library is embedded as a C/C++ submodule — no separate
// installation is needed beyond the Go dependency.
//
// API has three levels:
//   1. llama.LoadModel(path, ModelOptions...)   → *llama.Model
//   2. model.NewContext(ContextOptions...)       → *llama.Context
//   3. ctx.Generate(prompt, GenerateOptions...) → string, error

import (
	"context"
	"fmt"
	"strings"

	llama "github.com/godeps/gollama"
)

// loadModel loads the GGUF file and configures hardware acceleration.
// Called once by NewLocalClient.
func (c *LocalClient) loadModel() error {
	// --- Level 1: load model weights ---
	modelOpts := []llama.ModelOption{
		llama.WithMMap(true), // memory-map weights; reduces cold-start time
	}

	if c.hw.HasMetal || c.hw.HasCUDA {
		// Offload the configured number of transformer layers to the GPU.
		// Apple Silicon: GPULayers=99 (all layers, unified memory).
		// NVIDIA: auto-tuned by DetectHardware based on VRAM.
		modelOpts = append(modelOpts, llama.WithGPULayers(c.hw.GPULayers))
	}
	// CPU fallback: no GPU option; llama.cpp auto-detects AVX-512/AVX2.

	model, err := llama.LoadModel(c.modelPath, modelOpts...)
	if err != nil {
		c.available = false
		return err
	}
	c.model = model

	// --- Level 2: create inference context ---
	llamaCtx, err := model.NewContext(
		llama.WithContext(c.contextSize), // token context window size
	)
	if err != nil {
		c.available = false
		return fmt.Errorf("create inference context: %w", err)
	}
	c.llamaCtx = llamaCtx

	return nil
}

// generate runs a single inference call and returns the decoded text.
// Called under c.mu, so single-threaded access to the context is guaranteed.
func (c *LocalClient) generate(_ context.Context, prompt string) (string, error) {
	llamaCtx, ok := c.llamaCtx.(*llama.Context)
	if !ok || llamaCtx == nil {
		return "", fmt.Errorf("local LLM: inference context is nil")
	}

	// Prepend /no_think prefix for Qwen3 models when extended reasoning is off.
	// When think=true, the model produces <think>...</think> blocks; these are
	// consumed by the intelligence pipeline to populate ContextPacket.Insight.
	fullPrompt := prompt
	if isQwen3Model(c.modelName) && !c.think {
		fullPrompt = "/no_think\n" + prompt
	}

	// --- Level 3: generate ---
	result, err := llamaCtx.Generate(fullPrompt,
		llama.WithMaxTokens(512),     // match grpo_train max_completion_length
		llama.WithTemperature(0.1),   // low temp for deterministic code graph analysis
		llama.WithTopP(0.9),
		llama.WithRepeatPenalty(1.1),
	)
	if err != nil {
		return "", fmt.Errorf("local LLM generate: %w", err)
	}

	// Strip any lingering <think>...</think> blocks when thinking is disabled.
	if !c.think {
		result = stripThinkBlocks(result)
	}

	return strings.TrimSpace(result), nil
}

// isQwen3Model returns true if the model name contains "qwen3" (case-insensitive).
func isQwen3Model(name string) bool {
	return strings.Contains(strings.ToLower(name), "qwen3")
}
