// Package enricher implements the Context Enricher — Feature 2 of synapses-intelligence.
//
// During a get_context call, the enricher:
//  1. Loads pre-computed summaries from brain.sqlite (fast, no LLM)
//  2. Optionally generates a 2-sentence insight about the root entity's architectural role
//
// The summaries replace raw code in get_context responses, dramatically reducing
// token usage for the main LLM (Claude, Gemini, GPT, etc.).
package enricher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Divish1032/synapses-intelligence/internal/llm"
	"github.com/Divish1032/synapses-intelligence/internal/store"
)

const (
	// maxNamesInPrompt limits how many callee/caller names are sent to the LLM
	// to keep prompts short for 1-2B models.
	maxNamesInPrompt = 5

	promptTemplate = `Describe this code entity's architectural role in exactly TWO sentences.
Output ONLY valid JSON with no other text: {"insight": "...", "concerns": ["...", "..."]}

Entity: %s (%s)
Calls: %s
Called by: %s%s`
)

// Request carries the carved subgraph data for enrichment.
type Request struct {
	RootID       string
	RootName     string
	RootType     string
	CalleeNames  []string
	CallerNames  []string
	RelatedNames []string
	TaskContext  string
}

// Response is added to the get_context output.
type Response struct {
	Insight  string
	Concerns []string
	LLMUsed  bool // true when the LLM was called; false on cache hit (future)
}

type insightJSON struct {
	Insight  string   `json:"insight"`
	Concerns []string `json:"concerns"`
}

// Enricher adds semantic context to get_context responses.
type Enricher struct {
	llm     llm.LLMClient
	store   *store.Store
	timeout time.Duration
}

// New creates an Enricher.
func New(client llm.LLMClient, st *store.Store, timeout time.Duration) *Enricher {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &Enricher{llm: client, store: st, timeout: timeout}
}

// Enrich generates a 2-sentence insight for the root entity.
// This calls the LLM; callers should handle errors gracefully (fail-silent).
func (e *Enricher) Enrich(ctx context.Context, req Request) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	prompt := e.buildPrompt(req)
	raw, err := e.llm.Generate(ctx, prompt)
	if err != nil {
		return Response{}, fmt.Errorf("llm generate: %w", err)
	}

	result, err := parseInsight(raw)
	if err != nil {
		return Response{}, fmt.Errorf("parse insight: %w (raw: %q)", err, llm.Truncate(raw, 100))
	}

	return result, nil
}

func (e *Enricher) buildPrompt(req Request) string {
	callees := joinNames(req.CalleeNames, maxNamesInPrompt)
	callers := joinNames(req.CallerNames, maxNamesInPrompt)
	nodeType := req.RootType
	if nodeType == "" {
		nodeType = "entity"
	}
	if callees == "" {
		callees = "none"
	}
	if callers == "" {
		callers = "none"
	}

	taskSection := ""
	if req.TaskContext != "" {
		taskSection = "\nTask context: " + req.TaskContext
	}

	return fmt.Sprintf(promptTemplate,
		req.RootName, nodeType,
		callees, callers,
		taskSection,
	)
}

func parseInsight(raw string) (Response, error) {
	raw = llm.ExtractJSON(raw)
	var result insightJSON
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return Response{}, fmt.Errorf("unmarshal: %w", err)
	}
	insight := strings.TrimSpace(result.Insight)
	if insight == "" {
		return Response{}, fmt.Errorf("empty insight in response")
	}
	return Response{Insight: insight, Concerns: result.Concerns, LLMUsed: true}, nil
}

// joinNames joins up to n names into a comma-separated string.
func joinNames(names []string, n int) string {
	if len(names) > n {
		names = names[:n]
	}
	return strings.Join(names, ", ")
}
