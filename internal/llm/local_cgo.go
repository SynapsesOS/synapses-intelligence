//go:build llamacpp

package llm

// local_cgo.go — CGo implementation of LocalClient using go-skynet/go-llama.cpp.
//
// Build requirements:
//   CGO_ENABLED=1
//   go-skynet/go-llama.cpp in go.mod (add with: go get github.com/go-skynet/go-llama.cpp)
//
// Platform notes:
//   macOS (Apple Silicon): Requires Xcode command-line tools for Metal.
//   Linux (NVIDIA):        Requires CUDA toolkit for CuBLAS acceleration.
//   Linux/Windows (CPU):   Uses AVX-512/AVX2 SIMD automatically via llama.cpp.
//
// The llama.cpp library is embedded as a C/C++ submodule inside go-llama.cpp —
// no separate installation is needed beyond the Go dependency.

import (
	"context"
	"fmt"
	"strings"

	llama "github.com/go-skynet/go-llama.cpp"
)

// loadModel loads the GGUF file and configures hardware acceleration.
// Called once by NewLocalClient.
func (c *LocalClient) loadModel() error {
	opts := []llama.ModelOption{
		llama.SetContext(c.contextSize),
		llama.SetSeed(-1), // random seed for non-deterministic generation
	}

	// Hardware-specific acceleration
	if c.hw.HasMetal {
		// Apple Silicon: offload all layers to the Metal GPU.
		// go-llama.cpp uses llama.cpp's Metal backend automatically on darwin/arm64.
		opts = append(opts, llama.SetGPULayers(c.hw.GPULayers))
	} else if c.hw.HasCUDA {
		// NVIDIA: offload computed number of layers to CUDA.
		opts = append(opts, llama.SetGPULayers(c.hw.GPULayers))
	}
	// CPU fallback: no GPU options; llama.cpp auto-detects AVX-512/AVX2.

	model, err := llama.New(c.modelPath, opts...)
	if err != nil {
		return err
	}
	c.model = model
	return nil
}

// generate runs a single inference call and returns the decoded text.
// Called under c.mu, so single-threaded access to the model is guaranteed.
func (c *LocalClient) generate(ctx context.Context, prompt string) (string, error) {
	model, ok := c.model.(*llama.LLamaModel)
	if !ok || model == nil {
		return "", fmt.Errorf("local LLM: model handle is nil")
	}

	// Prepend /no_think prefix for Qwen3 models when extended reasoning is off.
	// When think=true, the model produces <think>...</think> blocks; these are
	// consumed by the intelligence pipeline to populate ContextPacket.Insight.
	fullPrompt := prompt
	if isQwen3Model(c.modelName) && !c.think {
		fullPrompt = "/no_think\n" + prompt
	}

	// go-llama.cpp prediction options
	predictOpts := []llama.PredictOption{
		llama.SetTokens(512),     // max output tokens — match grpo_train max_completion_length
		llama.SetTemperature(0.1), // low temperature for deterministic code graph analysis
		llama.SetTopP(0.9),
		llama.SetRepeatPenalty(1.1),
	}

	result, err := model.Predict(fullPrompt, predictOpts...)
	if err != nil {
		return "", fmt.Errorf("local LLM predict: %w", err)
	}

	// Strip any lingering <think>...</think> blocks when thinking is disabled.
	if !c.think {
		result = stripThinkBlocks(result)
	}

	// Check for context cancellation after the (potentially long) CGo call.
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	return strings.TrimSpace(result), nil
}

// isQwen3Model returns true if the model name contains "qwen3" (case-insensitive).
func isQwen3Model(name string) bool {
	return strings.Contains(strings.ToLower(name), "qwen3")
}
