package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// thinkTagRe strips Qwen3.5 extended thinking output (<think>...</think> blocks).
var thinkTagRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// OllamaClient calls the Ollama REST API at POST /api/generate.
// It keeps a reusable http.Client for connection pooling.
type OllamaClient struct {
	baseURL    string
	model      string
	httpClient *http.Client
	// think controls Qwen3.5 extended thinking mode.
	// When true, "/think\n\n" is prepended to the prompt (deeper reasoning).
	// When false, "/no_think\n\n" is prepended (faster, no chain-of-thought).
	// Models that don't support thinking mode silently ignore the prefix.
	think bool
}

// NewOllamaClient creates a client targeting the given Ollama base URL and model.
// timeoutMS is the per-request timeout in milliseconds.
func NewOllamaClient(baseURL, model string, timeoutMS int) *OllamaClient {
	if timeoutMS <= 0 {
		timeoutMS = 3000
	}
	return &OllamaClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		httpClient: &http.Client{
			Timeout: time.Duration(timeoutMS) * time.Millisecond,
		},
	}
}

// WithThinking configures extended thinking mode for Qwen3.5 models.
// Call on construction: llm.NewOllamaClient(...).WithThinking(true)
// Returns the client to allow chaining.
func (c *OllamaClient) WithThinking(enabled bool) *OllamaClient {
	c.think = enabled
	return c
}

// ollamaRequest is the payload for POST /api/generate.
type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
	// Options tuned for small models: low temperature for deterministic JSON output.
	Options ollamaOptions `json:"options"`
}

type ollamaOptions struct {
	Temperature float64  `json:"temperature"`
	NumPredict  int      `json:"num_predict"` // max output tokens
	Stop        []string `json:"stop,omitempty"`
}

// ollamaResponse is the non-streaming response from Ollama.
type ollamaResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
	Error    string `json:"error,omitempty"`
}

// Generate sends a prompt and returns the response text.
// Uses stream=false for simplicity and lowest latency on small outputs.
// If thinking mode is configured, prepends /think or /no_think to the prompt
// (Qwen3.5 extended reasoning control) and strips <think>...</think> from output.
func (c *OllamaClient) Generate(ctx context.Context, prompt string) (string, error) {
	// Apply Qwen3.5 thinking mode prefix. Models that don't support this ignore it.
	if c.think {
		prompt = "/think\n\n" + prompt
	} else {
		prompt = "/no_think\n\n" + prompt
	}

	reqBody := ollamaRequest{
		Model:  c.model,
		Prompt: prompt,
		Stream: false,
		Options: ollamaOptions{
			Temperature: 0.1, // near-deterministic for structured JSON
			NumPredict:  400, // enough for JSON insight with headroom for 7b verbosity
			// No stop tokens: models wrap JSON in markdown fences and stop tokens
			// fire immediately (e.g. "```" fires on the opening fence), producing
			// empty responses. ExtractJSON handles all formatting variants.
		},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/generate", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama generate: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("ollama returned %d: %s", resp.StatusCode, body)
	}

	var result ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("ollama error: %s", result.Error)
	}

	// Strip extended thinking blocks (<think>...</think>) that Qwen3.5 emits
	// when thinking mode is enabled. The actual answer follows after the block.
	response := thinkTagRe.ReplaceAllString(result.Response, "")
	return strings.TrimSpace(response), nil
}

// Available checks if Ollama is reachable by calling GET /api/tags.
// Returns true only if the HTTP call succeeds with a 200 status.
func (c *OllamaClient) Available(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return false
	}
	// Use a short ping timeout regardless of the configured timeout.
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req = req.WithContext(pingCtx)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ModelName returns the configured model tag.
func (c *OllamaClient) ModelName() string {
	return c.model
}

// ModelPulled returns true if the configured model is already present in
// Ollama's local model library (i.e. no pull is needed).
func (c *OllamaClient) ModelPulled(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := c.httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return false
	}
	defer resp.Body.Close()

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false
	}
	for _, m := range result.Models {
		// Ollama tags may have ":latest" appended; match with or without tag.
		if m.Name == c.model || strings.HasPrefix(m.Name, c.model+":") ||
			strings.TrimSuffix(m.Name, ":latest") == strings.TrimSuffix(c.model, ":latest") {
			return true
		}
	}
	return false
}

// ProbeLatency measures actual inference latency by generating a short fixed
// response. Uses a raw prompt (no thinking prefix) to measure baseline speed.
// Returns the wall-clock duration, or an error if the model doesn't respond
// within maxDuration. Callers should use this to pick the fastest installed model.
func (c *OllamaClient) ProbeLatency(ctx context.Context, maxDuration time.Duration) (time.Duration, error) {
	probeCtx, cancel := context.WithTimeout(ctx, maxDuration)
	defer cancel()

	reqBody := ollamaRequest{
		Model:  c.model,
		Prompt: "Reply with one word: ready",
		Stream: false,
		Options: ollamaOptions{
			Temperature: 0.0,
			NumPredict:  8, // minimal output — just enough to confirm the model runs
		},
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost,
		c.baseURL+"/api/generate", bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	probeCli := &http.Client{Timeout: maxDuration + time.Second}
	start := time.Now()
	resp, err := probeCli.Do(req)
	if err != nil {
		return 0, fmt.Errorf("probe: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("probe returned HTTP %d", resp.StatusCode)
	}
	var result ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("probe decode: %w", err)
	}
	if result.Error != "" {
		return 0, fmt.Errorf("probe error: %s", result.Error)
	}
	return time.Since(start), nil
}

// ListInstalledModels returns all model names present in Ollama's local library.
func ListInstalledModels(ctx context.Context, baseURL string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(baseURL, "/")+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	cli := &http.Client{Timeout: 5 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list models: HTTP %d", resp.StatusCode)
	}
	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("list models decode: %w", err)
	}
	names := make([]string, 0, len(result.Models))
	for _, m := range result.Models {
		names = append(names, m.Name)
	}
	return names, nil
}

// PullModel pulls the configured model from the Ollama registry, streaming
// progress lines to w. Pass os.Stderr for terminal feedback.
// Blocks until the pull completes or ctx is cancelled.
func (c *OllamaClient) PullModel(ctx context.Context, w io.Writer) error {
	body, err := json.Marshal(map[string]any{"name": c.model, "stream": true})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	// Pull can take minutes; use a long timeout client.
	pullCli := &http.Client{Timeout: 30 * time.Minute}
	resp, err := pullCli.Do(req)
	if err != nil {
		return fmt.Errorf("pull request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("ollama pull returned %d: %s", resp.StatusCode, b)
	}

	// Stream newline-delimited JSON progress events.
	dec := json.NewDecoder(resp.Body)
	var lastStatus string
	for {
		var evt struct {
			Status    string `json:"status"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
			Error     string `json:"error"`
		}
		if err := dec.Decode(&evt); err != nil {
			break // EOF = done
		}
		if evt.Error != "" {
			return fmt.Errorf("pull error: %s", evt.Error)
		}
		if evt.Status != lastStatus {
			if evt.Total > 0 {
				pct := int(float64(evt.Completed) / float64(evt.Total) * 100)
				fmt.Fprintf(w, "\r  %-40s %3d%%", evt.Status, pct)
			} else {
				fmt.Fprintf(w, "\r  %-40s     ", evt.Status)
			}
			lastStatus = evt.Status
		}
	}
	fmt.Fprintln(w) // newline after progress line
	return nil
}
