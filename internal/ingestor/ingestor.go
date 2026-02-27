// Package ingestor implements the Semantic Ingestor — Feature 1 of synapses-intelligence.
//
// On a file-save event, the ingestor receives a code snippet, sends a short prompt
// to the local LLM, and persists a 1-sentence "intent summary" in brain.sqlite.
// These summaries are later served by the Enricher during get_context calls,
// replacing raw source code with compact semantic descriptions for the main LLM.
package ingestor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/synapses/synapses-intelligence/internal/llm"
	"github.com/synapses/synapses-intelligence/internal/store"
)

const (
	// maxCodeChars is the maximum code snippet size sent to the LLM.
	// Keeps prompts small for fast inference on 1-2B models.
	maxCodeChars = 500

	// promptTemplate is tuned for small models:
	//   - Imperative instruction first
	//   - Strict JSON-only output format
	//   - No markdown, no preamble
	// tags: 1-3 short domain labels e.g. ["auth","http","database"]
	promptTemplate = `Describe what this code entity does in ONE sentence. Add 1-3 short domain tags.
Output ONLY valid JSON with no other text: {"summary": "...", "tags": ["tag1"]}

Name: %s (%s, package %s)
Code:
%s`
)

// Request carries a code snippet for summarization.
type Request struct {
	NodeID   string
	NodeName string
	NodeType string
	Package  string
	Code     string
}

// Response holds the generated summary and domain tags.
type Response struct {
	NodeID  string
	Summary string
	Tags    []string // 1-3 short domain labels (may be empty for legacy LLM responses)
}

// summaryJSON is used to parse the LLM's JSON output.
type summaryJSON struct {
	Summary string   `json:"summary"`
	Tags    []string `json:"tags,omitempty"`
}

// Ingestor summarizes code snippets via the LLM and persists them.
type Ingestor struct {
	llm     llm.LLMClient
	store   *store.Store
	timeout time.Duration
}

// New creates an Ingestor backed by the given LLM client and store.
func New(client llm.LLMClient, st *store.Store, timeout time.Duration) *Ingestor {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &Ingestor{llm: client, store: st, timeout: timeout}
}

// Summarize generates and stores a 1-sentence summary for the given code entity.
// If the LLM is unavailable or returns an unparseable response, an error is returned
// but the call is non-fatal — callers should log and continue.
func (ing *Ingestor) Summarize(ctx context.Context, req Request) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, ing.timeout)
	defer cancel()

	prompt := ing.buildPrompt(req)

	raw, err := ing.llm.Generate(ctx, prompt)
	if err != nil {
		return Response{NodeID: req.NodeID}, fmt.Errorf("llm generate: %w", err)
	}

	summary, tags, err := parseSummary(raw)
	if err != nil {
		return Response{NodeID: req.NodeID}, fmt.Errorf("parse summary: %w (raw: %q)", err, truncate(raw, 100))
	}

	if err := ing.store.UpsertSummary(req.NodeID, req.NodeName, summary, tags); err != nil {
		return Response{NodeID: req.NodeID, Summary: summary, Tags: tags},
			fmt.Errorf("store summary: %w", err)
	}

	return Response{NodeID: req.NodeID, Summary: summary, Tags: tags}, nil
}

// buildPrompt constructs the LLM prompt for a code entity.
func (ing *Ingestor) buildPrompt(req Request) string {
	code := truncateCode(req.Code)
	nodeType := req.NodeType
	if nodeType == "" {
		nodeType = "entity"
	}
	pkg := req.Package
	if pkg == "" {
		pkg = "unknown"
	}
	return fmt.Sprintf(promptTemplate, req.NodeName, nodeType, pkg, code)
}

// parseSummary extracts the summary and tags from the LLM JSON response.
// Handles cases where the model wraps the JSON in markdown code fences.
// Tags are optional — returns nil if the model did not include them.
func parseSummary(raw string) (summary string, tags []string, err error) {
	raw = extractJSON(raw)
	var result summaryJSON
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		return "", nil, fmt.Errorf("unmarshal: %w", err)
	}
	summary = strings.TrimSpace(result.Summary)
	if summary == "" {
		return "", nil, fmt.Errorf("empty summary in response")
	}
	return summary, result.Tags, nil
}

// extractJSON strips markdown code fences if the LLM wrapped its output.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	// Strip ```json ... ``` or ``` ... ```
	if idx := strings.Index(s, "```"); idx >= 0 {
		s = s[idx:]
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		if end := strings.Index(s, "```"); end >= 0 {
			s = s[:end]
		}
	}
	// Find the first { to skip any leading text
	if start := strings.Index(s, "{"); start >= 0 {
		s = s[start:]
	}
	// Find the last } to skip any trailing text
	if end := strings.LastIndex(s, "}"); end >= 0 {
		s = s[:end+1]
	}
	return strings.TrimSpace(s)
}

// truncateCode caps the code snippet at maxCodeChars runes.
func truncateCode(code string) string {
	code = strings.TrimSpace(code)
	if utf8.RuneCountInString(code) <= maxCodeChars {
		return code
	}
	runes := []rune(code)
	return string(runes[:maxCodeChars]) + "..."
}

// truncate shortens a string for error messages.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
