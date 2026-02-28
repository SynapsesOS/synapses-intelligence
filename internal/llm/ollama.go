package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OllamaClient calls the Ollama REST API at POST /api/generate.
// It keeps a reusable http.Client for connection pooling.
type OllamaClient struct {
	baseURL    string
	model      string
	httpClient *http.Client
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
func (c *OllamaClient) Generate(ctx context.Context, prompt string) (string, error) {
	reqBody := ollamaRequest{
		Model:  c.model,
		Prompt: prompt,
		Stream: false,
		Options: ollamaOptions{
			Temperature: 0.1, // near-deterministic for structured JSON
			NumPredict:  150, // sufficient for all brain outputs (~100 tokens max)
			Stop:        []string{"\n\n", "```"},
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

	return strings.TrimSpace(result.Response), nil
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
