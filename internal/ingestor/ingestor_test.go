package ingestor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/synapses/synapses-intelligence/internal/llm"
	"github.com/synapses/synapses-intelligence/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "brain.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestSummarize_Success(t *testing.T) {
	mock := llm.NewMockClient(`{"summary": "Validates JWT tokens and checks expiry."}`)
	st := newTestStore(t)
	ing := New(mock, st, 3*time.Second)

	resp, err := ing.Summarize(context.Background(), Request{
		NodeID:   "node:auth:Validate",
		NodeName: "Validate",
		NodeType: "method",
		Package:  "auth",
		Code:     "func (s *AuthService) Validate(token string) (Claims, error) { ... }",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Summary != "Validates JWT tokens and checks expiry." {
		t.Errorf("unexpected summary: %q", resp.Summary)
	}
	if resp.NodeID != "node:auth:Validate" {
		t.Errorf("unexpected node ID: %q", resp.NodeID)
	}

	// Verify persisted to store.
	stored := st.GetSummary("node:auth:Validate")
	if stored != "Validates JWT tokens and checks expiry." {
		t.Errorf("store not updated, got: %q", stored)
	}
}

func TestSummarize_LLMUnavailable(t *testing.T) {
	mock := &llm.MockClient{Err: os.ErrDeadlineExceeded}
	st := newTestStore(t)
	ing := New(mock, st, 3*time.Second)

	_, err := ing.Summarize(context.Background(), Request{
		NodeID:   "node:x",
		NodeName: "X",
		Code:     "func X() {}",
	})
	if err == nil {
		t.Fatal("expected error when LLM unavailable, got nil")
	}

	// Nothing should be written to the store on failure.
	if stored := st.GetSummary("node:x"); stored != "" {
		t.Errorf("expected no stored summary on failure, got: %q", stored)
	}
}

func TestSummarize_BadJSON(t *testing.T) {
	mock := llm.NewMockClient(`not valid json at all`)
	st := newTestStore(t)
	ing := New(mock, st, 3*time.Second)

	_, err := ing.Summarize(context.Background(), Request{
		NodeID: "node:x",
		Code:   "func X() {}",
	})
	if err == nil {
		t.Fatal("expected parse error for bad JSON, got nil")
	}
}

func TestSummarize_MarkdownWrappedJSON(t *testing.T) {
	mock := llm.NewMockClient("```json\n{\"summary\": \"Does auth validation.\"}\n```")
	st := newTestStore(t)
	ing := New(mock, st, 3*time.Second)

	resp, err := ing.Summarize(context.Background(), Request{
		NodeID:   "node:y",
		NodeName: "Y",
		Code:     "func Y() {}",
	})
	if err != nil {
		t.Fatalf("unexpected error with markdown-wrapped JSON: %v", err)
	}
	if resp.Summary != "Does auth validation." {
		t.Errorf("unexpected summary: %q", resp.Summary)
	}
}

func TestBuildPrompt_TruncatesLongCode(t *testing.T) {
	ing := &Ingestor{}
	longCode := string(make([]byte, 1000))
	prompt := ing.buildPrompt(Request{
		NodeName: "Foo",
		NodeType: "function",
		Package:  "pkg",
		Code:     longCode,
	})
	if len(prompt) > 2000 {
		t.Errorf("prompt too long: %d chars", len(prompt))
	}
}

func TestExtractJSON(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{`{"summary": "hello"}`, `{"summary": "hello"}`},
		{"```json\n{\"summary\": \"hello\"}\n```", `{"summary": "hello"}`},
		{"Here is the answer: {\"summary\": \"hello\"} done.", `{"summary": "hello"}`},
		{" \n{\"summary\": \"hello\"}\n", `{"summary": "hello"}`},
	}
	for _, tc := range cases {
		got := extractJSON(tc.input)
		if got != tc.want {
			t.Errorf("extractJSON(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
