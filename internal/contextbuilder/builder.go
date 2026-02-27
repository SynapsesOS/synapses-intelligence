// Package contextbuilder assembles a structured Context Packet from a Synapses
// graph snapshot, SDLC phase/mode, and the Brain's learned data.
//
// The Context Packet replaces raw graph nodes with semantic summaries, active
// constraints, team coordination info, quality gate requirements, and learned
// co-occurrence hints — capped at ~800 tokens for the main LLM consumer.
//
// All types here are internal; pkg/brain converts them to public brain.* types.
package contextbuilder

import (
	"context"
	"time"

	"github.com/synapses/synapses-intelligence/internal/enricher"
	"github.com/synapses/synapses-intelligence/internal/sdlc"
	"github.com/synapses/synapses-intelligence/internal/store"
)

const (
	maxDependencySummaries = 8
	maxPatternHints        = 3
	maxConstraints         = 5
	maxTeamStatus          = 5
)

// ConstraintItem is a rule the agent must respect.
type ConstraintItem struct {
	RuleID      string
	Severity    string
	Description string
	Hint        string // cached fix suggestion, may be empty
}

// AgentItem represents another agent's work claim.
type AgentItem struct {
	AgentID   string
	Scope     string
	ScopeType string
	ExpiresIn int // seconds until expiry (0 = unknown)
}

// PatternItem is a learned co-occurrence: "when editing Trigger, also check CoChange".
type PatternItem struct {
	Trigger    string
	CoChange   string
	Reason     string
	Confidence float64
}

// Packet is the assembled context document returned by Builder.Build().
// pkg/brain converts this to brain.ContextPacket for the public API.
type Packet struct {
	AgentID     string
	EntityName  string
	EntityType  string
	GeneratedAt string
	Phase       string
	QualityMode string

	RootSummary         string
	DependencySummaries map[string]string

	Insight  string
	Concerns []string

	ActiveConstraints []ConstraintItem
	TeamStatus        []AgentItem
	Gate              sdlc.Gate
	PatternHints      []PatternItem
	PhaseGuidance     string
}

// RuleRef is a single architectural rule reference from the Synapses snapshot.
type RuleRef struct {
	RuleID      string
	Severity    string
	Description string
}

// ClaimRef is a work claim from another agent.
type ClaimRef struct {
	AgentID   string
	Scope     string
	ScopeType string
	ExpiresAt string // RFC3339; empty = unknown
}

// Request is the input to Builder.Build().
type Request struct {
	AgentID     string
	Phase       string // "" = use stored project phase
	QualityMode string // "" = use stored project mode
	EnableLLM   bool   // true = allow LLM insight generation (adds ~2-3s)

	// From Synapses snapshot:
	RootNodeID      string
	RootName        string
	RootType        string
	RootFile        string   // used for constraint hint lookups
	CalleeNames     []string // direct callees
	CallerNames     []string // direct callers
	RelatedNames    []string // transitive neighbours
	ApplicableRules []RuleRef
	ActiveClaims    []ClaimRef
	TaskContext     string
	TaskID          string
}

// Builder assembles a Context Packet from a Synapses snapshot and brain data.
type Builder struct {
	store   *store.Store
	manager *sdlc.Manager
	enr     *enricher.Enricher // nil = LLM insight disabled
}

// New creates a Builder. enr may be nil to disable LLM insight generation.
func New(st *store.Store, mgr *sdlc.Manager, enr *enricher.Enricher) *Builder {
	return &Builder{store: st, manager: mgr, enr: enr}
}

// Build assembles and returns a Context Packet for the given request.
// The fast path (SQLite lookups) always runs; the LLM path runs only if
// req.EnableLLM is true and the enricher is available.
func (b *Builder) Build(ctx context.Context, req Request) (*Packet, error) {
	// 1. Resolve effective phase and quality mode (request override or stored).
	phase := b.manager.ResolvePhase(req.Phase)
	mode := b.manager.ResolveMode(req.QualityMode)
	sections := sdlc.SectionsForPhase(phase)

	pkt := &Packet{
		AgentID:     req.AgentID,
		EntityName:  req.RootName,
		EntityType:  req.RootType,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Phase:       phase,
		QualityMode: mode,
	}

	// Section 1: Root summary (fast path — SQLite).
	if sections.RootSummary && req.RootNodeID != "" {
		pkt.RootSummary = b.store.GetSummary(req.RootNodeID)
	}

	// Section 1b: Dependency summaries (fast path — SQLite).
	if sections.DependencySummaries {
		names := collectDepNames(req, maxDependencySummaries)
		if len(names) > 0 {
			pkt.DependencySummaries = b.store.GetSummariesByName(names)
		}
	}

	// Section 2: LLM Insight (slow path — optional).
	if sections.LLMInsight && req.EnableLLM && b.enr != nil {
		r, err := b.enr.Enrich(ctx, enricher.Request{
			RootID:       req.RootNodeID,
			RootName:     req.RootName,
			RootType:     req.RootType,
			CalleeNames:  req.CalleeNames,
			CallerNames:  req.CallerNames,
			RelatedNames: req.RelatedNames,
			TaskContext:  req.TaskContext,
		})
		if err == nil {
			pkt.Insight = r.Insight
			pkt.Concerns = r.Concerns
		}
		// Error is non-fatal — insight section stays empty.
	}

	// Section 3: Active constraints.
	if sections.ActiveConstraints {
		pkt.ActiveConstraints = buildConstraints(b.store, req.ApplicableRules, req.RootFile, maxConstraints)
	}

	// Section 4: Team status.
	if sections.TeamStatus {
		pkt.TeamStatus = buildTeamStatus(req.AgentID, req.ActiveClaims, maxTeamStatus)
	}

	// Section 5: Quality gate.
	if sections.QualityGate {
		pkt.Gate = sdlc.GateForMode(mode, phase)
	}

	// Section 6: Learned pattern hints.
	if sections.PatternHints {
		triggers := patternTriggers(req)
		patterns := b.store.GetPatternsForTriggers(triggers, maxPatternHints)
		pkt.PatternHints = toPatternItems(patterns)
	}

	// Section 7: Phase guidance.
	if sections.PhaseGuidance {
		pkt.PhaseGuidance = sdlc.PhaseGuidance(phase, mode)
	}

	return pkt, nil
}

// --- helpers ---

// collectDepNames returns callee names first, then caller names, capped at limit.
// Priority: callees (what root CALLS) are most relevant during development.
func collectDepNames(req Request, limit int) []string {
	seen := make(map[string]bool)
	var out []string
	for _, name := range append(req.CalleeNames, req.CallerNames...) {
		if name == "" || name == req.RootName || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// buildConstraints converts rule refs to ConstraintItems and looks up cached hints.
func buildConstraints(st *store.Store, rules []RuleRef, rootFile string, limit int) []ConstraintItem {
	if len(rules) == 0 {
		return nil
	}
	if limit > len(rules) {
		limit = len(rules)
	}
	out := make([]ConstraintItem, 0, limit)
	for _, r := range rules[:limit] {
		item := ConstraintItem{
			RuleID:      r.RuleID,
			Severity:    r.Severity,
			Description: r.Description,
		}
		// Try to find a cached fix hint from a past violation.
		if rootFile != "" {
			_, fix, found := st.GetViolationExplanation(r.RuleID, rootFile)
			if found && fix != "" {
				item.Hint = fix
			}
		}
		out = append(out, item)
	}
	return out
}

// buildTeamStatus converts claim refs to AgentItems, excluding the requesting agent.
func buildTeamStatus(selfAgentID string, claims []ClaimRef, limit int) []AgentItem {
	out := make([]AgentItem, 0, limit)
	for _, c := range claims {
		if c.AgentID == selfAgentID {
			continue // skip own claim
		}
		item := AgentItem{
			AgentID:   c.AgentID,
			Scope:     c.Scope,
			ScopeType: c.ScopeType,
		}
		if c.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, c.ExpiresAt); err == nil {
				secs := int(time.Until(t).Seconds())
				if secs > 0 {
					item.ExpiresIn = secs
				}
			}
		}
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// patternTriggers returns trigger names for pattern lookups: root + top callees.
func patternTriggers(req Request) []string {
	triggers := []string{}
	if req.RootName != "" {
		triggers = append(triggers, req.RootName)
	}
	for i, name := range req.CalleeNames {
		if i >= 4 {
			break
		}
		if name != "" {
			triggers = append(triggers, name)
		}
	}
	return triggers
}

// toPatternItems converts store patterns to PatternItems.
func toPatternItems(patterns []store.ContextPattern) []PatternItem {
	out := make([]PatternItem, len(patterns))
	for i, p := range patterns {
		out[i] = PatternItem{
			Trigger:    p.Trigger,
			CoChange:   p.CoChange,
			Reason:     p.Reason,
			Confidence: p.Confidence,
		}
	}
	return out
}
