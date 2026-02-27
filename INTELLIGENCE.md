# synapses-intelligence v0.3.0
## The Thinking Brain for AI Agent Systems

---

## What This Is

`synapses-intelligence` is a **local LLM sidecar** that adds semantic reasoning to code agent systems. It runs as a lightweight HTTP server on `localhost:11435`, backed by [Ollama](https://ollama.com) (local LLM, no API keys, no cloud, no data leaves the machine) and its own `brain.sqlite` for persistent storage.

It is designed as **Layer 3** in a multi-layer AI operating system stack:

```
[ Agent (Claude / GPT / Gemini) ]   ← consumes Context Packets
         ↑
[ synapses-intelligence (Brain) ]   ← this module — semantic enrichment
         ↑
[ Synapses (Code Graph MCP) ]       ← structural graph, BFS traversal
         ↑
[ Codebase ]
```

**Primary job:** Transform raw structural graph data (node IDs, edge types, code snippets) into compact, phase-aware **Context Packets** — structured semantic documents that tell an LLM agent exactly what it needs to know, in ~600-800 tokens, instead of 4,000+ tokens of raw code.

**Secondary jobs:** Learn project patterns over time, enforce SDLC phase discipline, coordinate multi-agent work, explain architectural violations in plain English.

---

## Architecture Overview

```
brain.sqlite                 Ollama (local LLM)
    │                             │
    ▼                             ▼
┌─────────────────────────────────────────────┐
│              pkg/brain (Brain interface)     │
│                                             │
│  ┌──────────────┐  ┌────────────────────┐  │
│  │   Ingestor   │  │  Context Builder   │  │
│  │  (on save)   │  │  (on get_context)  │  │
│  └──────────────┘  └────────────────────┘  │
│  ┌──────────────┐  ┌────────────────────┐  │
│  │   Enricher   │  │  SDLC Manager      │  │
│  │  (LLM call)  │  │  (phase/mode)      │  │
│  └──────────────┘  └────────────────────┘  │
│  ┌──────────────┐  ┌────────────────────┐  │
│  │   Guardian   │  │  Co-occurrence     │  │
│  │  (violations)│  │  Learner           │  │
│  └──────────────┘  └────────────────────┘  │
│  ┌──────────────┐                          │
│  │ Orchestrator │                          │
│  │  (conflicts) │                          │
│  └──────────────┘                          │
└─────────────────────────────────────────────┘
                    ↓
         HTTP server :11435
```

**brain.sqlite tables:**

| Table | Purpose | TTL |
|-------|---------|-----|
| `semantic_summaries` | 1-sentence intent summaries per node | Permanent (updated on re-ingest) |
| `violation_cache` | Cached violation explanations per (rule_id, file) | 7 days |
| `insight_cache` | Cached LLM insight per (node_id, phase) | 6 hours; invalidated on re-ingest |
| `sdlc_config` | Current project phase + quality mode | Permanent (1 row) |
| `context_patterns` | Learned co-occurrence patterns | Pruned if stale after 14 days |
| `decision_log` | Agent action history for learning | Pruned after 30 days |

---

## Capabilities

### 1. Semantic Ingestor — `POST /v1/ingest`

Generates a 1-sentence intent summary and domain tags for any code entity. Called when a file changes.

**Request:**
```json
{
  "node_id": "pkg:func:AuthService.Validate",
  "node_name": "Validate",
  "node_type": "method",
  "package": "auth",
  "code": "func (a *AuthService) Validate(token string) (Claims, error) { ... }"
}
```

**Response:**
```json
{
  "node_id": "pkg:func:AuthService.Validate",
  "summary": "Validates JWT tokens by verifying signature and checking expiry.",
  "tags": ["auth", "jwt", "validation"]
}
```

Summaries are stored in `semantic_summaries`. When a node is re-ingested, its entry in `insight_cache` is automatically invalidated (code changed → old insight stale).

---

### 2. Context Enricher — `POST /v1/enrich`

Returns stored summaries for a set of node IDs plus an optional 2-sentence LLM insight about the root entity's architectural role.

**Request:**
```json
{
  "root_id": "pkg:struct:AuthService",
  "root_name": "AuthService",
  "root_type": "struct",
  "all_node_ids": ["pkg:struct:AuthService", "pkg:func:TokenValidator.Verify"],
  "callee_names": ["TokenValidator", "RateLimiter"],
  "caller_names": ["HTTPHandler"],
  "task_context": "Adding OAuth2 support"
}
```

**Response:**
```json
{
  "insight": "AuthService is the central authentication boundary, coordinating JWT verification and session management for all protected routes.",
  "concerns": ["token expiry handling", "rate limit bypass risk"],
  "summaries": {
    "pkg:struct:AuthService": "Central coordinator for user authentication and session lifecycle.",
    "pkg:func:TokenValidator.Verify": "Cryptographically verifies JWT signatures against the configured signing key."
  },
  "llm_used": true
}
```

---

### 3. Context Packet Builder — `POST /v1/context-packet` *(v0.2.0+)*

**The primary integration endpoint.** Builds a complete, phase-aware Context Packet — a structured semantic document replacing raw graph nodes. This is the main value the Brain provides to Synapses.

**Request:**
```json
{
  "agent_id": "claude-session-42",
  "snapshot": {
    "root_node_id": "pkg:struct:AuthService",
    "root_name": "AuthService",
    "root_type": "struct",
    "root_file": "internal/auth/service.go",
    "callee_names": ["TokenValidator", "RateLimiter", "UserRepository"],
    "caller_names": ["HTTPHandler", "GRPCHandler"],
    "applicable_rules": [
      {"rule_id": "no-db-in-handler", "severity": "error", "description": "handlers must not call db directly"}
    ],
    "active_claims": [
      {"agent_id": "claude-backend", "scope": "internal/session/", "scope_type": "directory", "expires_at": "2026-02-27T15:00:00Z"}
    ],
    "task_context": "Adding OAuth2 support",
    "task_id": "task-123"
  },
  "phase": "development",
  "quality_mode": "standard",
  "enable_llm": true
}
```

**Response — a full Context Packet (~600-800 tokens):**
```json
{
  "agent_id": "claude-session-42",
  "entity_name": "AuthService",
  "entity_type": "struct",
  "generated_at": "2026-02-27T14:30:00Z",
  "phase": "development",
  "quality_mode": "standard",
  "root_summary": "Central coordinator for user authentication and session lifecycle.",
  "dependency_summaries": {
    "TokenValidator": "Cryptographically verifies JWT signatures against the configured signing key.",
    "RateLimiter": "Enforces per-user sliding window request limits using Redis counters.",
    "UserRepository": "Fetches and caches user records from PostgreSQL by ID or email."
  },
  "insight": "AuthService is the central authentication boundary that coordinates JWT verification and session management; changes here affect all protected API routes.",
  "concerns": ["token expiry edge cases", "concurrent session invalidation"],
  "active_constraints": [
    {
      "rule_id": "no-db-in-handler",
      "severity": "error",
      "description": "handlers must not call db directly",
      "hint": "inject a repository interface instead of calling db.Query() directly"
    }
  ],
  "team_status": [
    {"agent_id": "claude-backend", "scope": "internal/session/", "scope_type": "directory", "expires_in_seconds": 1800}
  ],
  "quality_gate": {
    "require_tests": true,
    "require_docs": false,
    "require_pr_check": false,
    "checklist": [
      "Write unit tests for new/modified functions",
      "Run validate_plan — no rule violations",
      "Exported symbols should have doc comments"
    ]
  },
  "pattern_hints": [
    {"trigger": "AuthService", "co_change": "TokenValidator", "reason": "edit during development", "confidence": 0.87}
  ],
  "phase_guidance": "You are in development phase. Implement per the plan. Claim work before editing (claim_work). Respect all active constraints. Run validate_plan before major changes.",
  "llm_used": true,
  "packet_quality": 1.0
}
```

**Context Packet section matrix — what's included per SDLC phase:**

| Section | planning | development | testing | review | deployment |
|---------|----------|-------------|---------|--------|------------|
| Root summary | ✓ | ✓ | ✓ | ✓ | ✓ |
| Dep summaries | ✓ | ✓ | — | ✓ | — |
| LLM insight | ✓ | ✓ | — | ✓ | — |
| Constraints | — | ✓ | ✓ | ✓ | — |
| Team status | ✓ | ✓ | ✓ | ✓ | ✓ |
| Quality gate | — | ✓ | ✓ | ✓ | — |
| Pattern hints | — | ✓ | — | ✓ | — |
| Phase guidance | ✓ | ✓ | ✓ | ✓ | ✓ |

**`packet_quality` field:** 0.0–1.0 heuristic. `0.0` = no summaries ingested. `0.4` = root summary present. `0.5` = root + dep summaries. `1.0` = all sections including insight. Callers can use this to decide whether to request a follow-up LLM pass.

**Insight caching:** On the LLM path, insight is cached in `insight_cache` for 6 hours per `(node_id, phase)` pair. Subsequent calls for the same entity/phase within 6 hours are served from cache with zero LLM cost. `llm_used` is `false` on cache hits.

**Nil response (HTTP 204):** If the Brain is unavailable or `context_builder` is disabled, the server returns 204. Callers must treat this as "use raw context" and fall back without error.

---

### 4. Rule Guardian — `POST /v1/explain-violation`

Explains an architectural rule violation in plain English with a concrete fix suggestion. Results are cached per `(rule_id, source_file)` for 7 days.

**Request:**
```json
{
  "rule_id": "no-db-in-handler",
  "rule_severity": "error",
  "description": "view/handler files must not import db packages",
  "source_file": "internal/handlers/auth.go",
  "target_name": "db.QueryRow"
}
```

**Response:**
```json
{
  "explanation": "The handler auth.go is directly calling database functions, bypassing the repository layer. This couples HTTP handling to database implementation details, making the handler untestable and violating the clean architecture boundary.",
  "fix": "Create an AuthRepository interface in internal/auth/ and inject it into the handler constructor. Move all db.QueryRow calls into a concrete PostgresAuthRepository implementation."
}
```

---

### 5. Task Orchestrator — `POST /v1/coordinate`

Suggests work distribution when multiple agents claim overlapping scopes.

**Request:**
```json
{
  "new_agent_id": "claude-frontend",
  "new_scope": "internal/auth/",
  "conflicting_claims": [
    {"agent_id": "claude-backend", "scope": "internal/auth/service.go", "scope_type": "file"}
  ]
}
```

**Response:**
```json
{
  "suggestion": "Agent claude-backend is actively modifying auth/service.go. You can safely work on the auth handler layer (internal/handlers/auth.go) or the auth types (internal/auth/types.go) without conflict.",
  "alternative_scope": "internal/handlers/auth.go"
}
```

---

### 6. SDLC Phase Management

**GET /v1/sdlc** — Returns current phase and quality mode:
```json
{"phase": "development", "quality_mode": "standard", "updated_at": "...", "updated_by": "agent-1"}
```

**PUT /v1/sdlc/phase** — Set phase:
```json
{"phase": "testing", "agent_id": "agent-1"}
```

Valid phases: `planning` → `development` → `testing` → `review` → `deployment`

**PUT /v1/sdlc/mode** — Set quality mode:
```json
{"mode": "enterprise", "agent_id": "agent-1"}
```

Valid modes: `quick` | `standard` | `enterprise`

Phase and mode are stored in `brain.sqlite`. They persist across server restarts and affect every subsequent context packet.

---

### 7. Decision Log & Learning — `POST /v1/decision`

Agents log completed work. The Brain extracts co-occurrence patterns from the `related_entities` field, building a project-specific knowledge base of "when you edit X, you usually also edit Y."

**Request:**
```json
{
  "agent_id": "claude-session-42",
  "phase": "development",
  "entity_name": "AuthService",
  "action": "edit",
  "related_entities": ["TokenValidator", "SessionStore"],
  "outcome": "success",
  "notes": "Added OAuth2 token validation path"
}
```

Internally: For each `(AuthService, TokenValidator)` and `(AuthService, SessionStore)` pair, `co_count` is incremented in `context_patterns`. `confidence = co_count / total_count`. These patterns surface in future context packets as `pattern_hints`.

---

### 8. Pattern Query — `GET /v1/patterns?trigger=AuthService&limit=5`

Returns the top learned co-occurrence patterns for an entity. Used for debugging the learning loop.

**Response:**
```json
{
  "patterns": [
    {"trigger": "AuthService", "co_change": "TokenValidator", "reason": "edit during development", "confidence": 0.87},
    {"trigger": "AuthService", "co_change": "SessionStore", "reason": "edit during development", "confidence": 0.65}
  ],
  "count": 2
}
```

---

### 9. Health — `GET /v1/health`

```json
{"status": "ok", "model": "qwen2.5-coder:1.5b", "available": true}
```

`available: false` means Ollama is not reachable. All fast-path (SQLite-only) operations still work. Only LLM calls fail.

---

## CLI Reference

```
brain serve           Start the HTTP sidecar (default port 11435)
  -port <int>         Override port
  -model <string>     Override Ollama model

brain status          Show Ollama connectivity, model, stats, SDLC config
brain ingest <json>   Manually ingest a code snippet
brain summaries       List all stored summaries
brain sdlc            Show current phase and mode
brain sdlc phase <p>  Set phase (planning|development|testing|review|deployment)
brain sdlc mode <m>   Set mode (quick|standard|enterprise)
brain decisions       List recent agent decision log
brain patterns        List all learned co-occurrence patterns
brain reset           Clear all brain data (prompts for confirmation)
brain version         Print version
```

---

## Configuration

Set via `BRAIN_CONFIG` environment variable pointing to a JSON file:

```json
{
  "enabled": true,
  "model": "qwen2.5-coder:1.5b",
  "ollama_url": "http://localhost:11434",
  "timeout_ms": 3000,
  "db_path": "~/.synapses/brain.sqlite",
  "port": 11435,
  "ingest": true,
  "enrich": true,
  "guardian": true,
  "orchestrate": true,
  "context_builder": true,
  "learning_enabled": true,
  "default_phase": "development",
  "default_mode": "standard"
}
```

**Model tiers by RAM:**

| RAM | Model | Size | Notes |
|-----|-------|------|-------|
| 4 GB | `qwen2.5-coder:1.5b` | ~900 MB | Default, good enough |
| 4 GB+ | `qwen3:1.7b` | ~1.1 GB | Recommended upgrade |
| 8 GB+ | `qwen3:4b` | ~2.5 GB | Noticeably better quality |
| 16 GB+ | `qwen3:8b` | ~5 GB | Enterprise quality |

---

## Integration with Synapses Core

### Overview

Synapses is the structural layer — it parses codebases into a graph and serves relevance-ranked context to agents. The Brain is the semantic layer — it adds meaning, phase awareness, and learned patterns on top of that structure.

Integration replaces Synapses' raw `get_context` graph dump with a Brain-assembled Context Packet. The fallback (raw graph) is always preserved.

### 3 Integration Points

#### Point 1: `get_context` → Context Packet (highest priority)

**Location:** `internal/mcp/tools.go` in the `handleGetContext` function, after `CarveEgoGraph()` returns the subgraph.

**What Synapses must send:**

```go
// After carving the ego graph:
snapshot := brain.SynapsesSnapshotInput{
    RootNodeID:  rootNode.ID,                        // stable graph node ID
    RootName:    rootNode.Name,                      // "AuthService"
    RootType:    rootNode.Type,                      // "struct"
    RootFile:    rootNode.Metadata["file"],          // "internal/auth/service.go"
    CalleeNames: extractNames(subgraph, CALLS_OUT),  // direct callees
    CallerNames: extractNames(subgraph, CALLS_IN),   // direct callers
    RelatedNames: extractNames(subgraph, ALL),       // transitive neighbours
    ApplicableRules: matchRulesForFile(rootNode.Metadata["file"]), // see below
    ActiveClaims:    getAllActiveClaims(),            // from claims table
    TaskContext:     req.TaskContext,                 // from tool call params
    TaskID:          req.TaskID,
}
pkt, err := brainClient.BuildContextPacket(ctx, brain.ContextPacketRequest{
    AgentID:     req.AgentID,        // from MCP session or generated
    Snapshot:    snapshot,
    Phase:       "",                  // "" = use stored project phase
    QualityMode: "",                  // "" = use stored project mode
    EnableLLM:   cfg.BrainEnableLLM, // configurable, default true
})
if err != nil || pkt == nil {
    // Brain unavailable or returned nil — fall back to raw graph output, no error
    return formatRawGraph(subgraph)
}
return formatContextPacket(pkt)
```

**ApplicableRules extraction** — Synapses has architectural rules in its SQLite (from synapses.json). For each rule, check if `from_file_pattern` glob-matches `rootNode.Metadata["file"]`. Pass only matching rules:

```go
func matchRulesForFile(file string) []brain.RuleInput {
    rules := store.GetAllRules()
    var out []brain.RuleInput
    for _, r := range rules {
        if r.FromFilePattern == "" || glob.Match(r.FromFilePattern, file) {
            out = append(out, brain.RuleInput{
                RuleID:      r.ID,
                Severity:    r.Severity,
                Description: r.Description,
            })
        }
    }
    return out
}
```

#### Point 2: File indexing → Auto-Ingest (background, non-blocking)

**Location:** Synapses' file watcher / indexer pipeline, after a node is indexed or re-indexed.

**What Synapses must do:**

```go
// Fire-and-forget: do not block the indexing pipeline
go func() {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    brainClient.Ingest(ctx, brain.IngestRequest{
        NodeID:   node.ID,
        NodeName: node.Name,
        NodeType: node.Type,
        Package:  node.Package,
        Code:     node.Code, // truncated to 500 chars by the Brain
    })
}()
```

This is the cold-start solution: every time Synapses indexes a file, the Brain learns its meaning. After a full project index, all nodes have summaries. The Brain doesn't need any bootstrap step — it learns continuously as Synapses works.

#### Point 3: `get_violations` → Explanation Enrichment (optional, cached)

**Location:** `internal/mcp/tools.go` in the `handleGetViolations` function, after collecting violations.

```go
for i, v := range violations {
    resp, err := brainClient.ExplainViolation(ctx, brain.ViolationRequest{
        RuleID:       v.RuleID,
        RuleSeverity: v.Severity,
        Description:  v.Description,
        SourceFile:   v.SourceFile,
        TargetName:   v.TargetName,
    })
    if err == nil && resp.Explanation != "" {
        violations[i].Explanation = resp.Explanation
        violations[i].Fix = resp.Fix
    }
}
```

Results are cached by the Brain for 7 days — the first call for each `(rule_id, file)` pair is slow (~2s); all subsequent calls for the same pair are instant.

### BrainClient Synapses Needs

A minimal HTTP client with fail-silent semantics:

```go
type BrainClient struct {
    baseURL    string        // "http://localhost:11435"
    httpClient *http.Client  // with Timeout: 5s
}

func (c *BrainClient) BuildContextPacket(ctx context.Context, req brain.ContextPacketRequest) (*brain.ContextPacket, error) {
    // POST /v1/context-packet
    // On HTTP 204: return nil, nil
    // On any error: return nil, nil  (fail-silent)
}

func (c *BrainClient) Ingest(ctx context.Context, req brain.IngestRequest) {
    // POST /v1/ingest — fire-and-forget, ignore all errors
}

func (c *BrainClient) ExplainViolation(ctx context.Context, req brain.ViolationRequest) (brain.ViolationResponse, error) {
    // POST /v1/explain-violation
    // On any error: return empty response, nil  (fail-silent)
}

func (c *BrainClient) Available(ctx context.Context) bool {
    // GET /v1/health — used at startup only
}
```

### `synapses.json` Configuration (proposed additions)

```json
{
  "brain": {
    "enabled": true,
    "url": "http://localhost:11435",
    "timeout_ms": 5000,
    "enable_llm": true
  }
}
```

---

## Integration Challenges

### Challenge 1: Node ID Stability Contract

**Problem:** The Brain stores summaries keyed by Synapses node IDs. If a function is moved to a different file or package, its node ID changes — but the old summary stays in `semantic_summaries` under the old ID, and the new node ID has no summary.

**Current state:** Unresolved. Silent miss — `get_context` falls back to raw output for that node.

**Planned fix (v0.4.0):** Node lookup fallback by `(node_name, package)` when `node_id` lookup misses. Two-step: try exact ID, then try name-based lookup. Add `InvalidateNode(oldID)` to the BrainClient so Synapses can clean up on rename.

---

### Challenge 2: Agent ID Propagation

**Problem:** The context packet is personalized per agent (`agent_id` affects TeamStatus filtering). MCP clients don't advertise a stable agent ID. Claude Code doesn't pass `agent_id` as a tool parameter.

**Current state:** `agent_id` is empty in most real calls. TeamStatus shows all other agents including "self" if self-filtering fails.

**Planned fix:** Synapses generates a stable session ID per MCP connection (UUID on connect, stored in session state). Pass this as `agent_id` on every context packet request. Document the convention in the MCP tool schema.

---

### Challenge 3: Cold Start — No Summaries on Fresh Install

**Problem:** A developer installs the Brain, starts it, and immediately calls `get_context`. The Brain knows nothing — zero summaries in `semantic_summaries`. Context packets have `packet_quality: 0.0` and provide no value until nodes are ingested.

**Current state:** Auto-ingest on file-save (Integration Point 2) solves this progressively, but only after the developer edits files.

**Planned fix (v0.4.0):** On Brain startup (or on first `brain serve`), call Synapses' `GET /synapses/nodes` (new endpoint) to get all currently-indexed nodes with their code, and bulk-ingest them in a background goroutine. Progress reported via `brain status`.

Alternatively: Synapses calls `POST /v1/ingest` for every node at startup if the brain is available. This is the cleaner approach — Synapses initiates the bulk ingest, Brain processes it.

---

### Challenge 4: Context Packet Latency on LLM Path

**Problem:** `get_context` is expected to respond in <100ms. The LLM insight path adds 2-3 seconds. Even with the insight cache, a cold miss blocks the entire `get_context` call.

**Current state:** The 6-hour insight cache handles warm requests. Cold requests (new entity or cache expired) are slow.

**Planned fix (v0.4.0):** Two-phase response:
1. Return the packet immediately with `llm_used: false` and whatever is in SQLite (fast path, <5ms).
2. Asynchronously generate the insight and store it in `insight_cache`.
3. On the next call for the same entity, the cache hit serves the full packet instantly.

This changes the contract: `enable_llm: true` means "generate insight if not cached; do not block."

---

### Challenge 5: Rules Data Is in Synapses, Not in Brain

**Problem:** To populate `ApplicableRules` in the context packet request, Synapses must do a glob-match of every architectural rule against the root file. Synapses stores rules in its own SQLite. The Brain has no direct access to Synapses' data.

**Current state:** The Brain accepts `ApplicableRules` in the request — Synapses must compute and pass them. This is the correct architecture (Synapses knows its rules; Brain enriches with explanations).

**Implementation detail:** Synapses needs a `matchRulesForFile(file string) []RuleInput` helper that reads from its `rules` table and pattern-matches. This is ~20 lines of Go and straightforward.

---

### Challenge 6: Claims Data Lives in Synapses' SQLite

**Problem:** `TeamStatus` in the context packet requires `ActiveClaims` — which agent is working on what. But work claims are stored in Synapses' `claims` table (managed by the `claim_work` MCP tool). The Brain has no access to that table.

**Current state:** Synapses must read its own claims table and pass all active (non-expired) claims in the context packet request. This is correct architecture.

**Implementation detail:** `getAllActiveClaims()` — a SQL query against Synapses' `claims` table:
```sql
SELECT agent_id, scope, scope_type, expires_at FROM claims WHERE expires_at > datetime('now')
```
Pass results as `ClaimInput` array in the request.

---

## Improvements for Next Version (v0.4.0)

### Improvement 1: Async Insight Generation (Critical for UX)

Change `BuildContextPacket` to never block on LLM. Return fast-path packet immediately. Generate insight asynchronously. The client gets `packet_quality: 0.5` first time, `1.0` on next call (cache hit). This eliminates the 2-3s cold-miss latency entirely from the user's perspective.

### Improvement 2: Bulk Bootstrap Ingest

`brain ingest-all --synapses-url http://localhost:11434` — reads all indexed nodes from Synapses via a new REST endpoint, ingest them all in parallel goroutines (pool of 4 workers). Prints progress. Solves cold start.

### Improvement 3: Node ID Change Notification

Add `DELETE /v1/summary/{nodeId}` endpoint. When Synapses detects a node rename or deletion during re-indexing, it calls this to clean up stale summaries. Prevents ghost summaries accumulating for deleted code.

### Improvement 4: PacketQuality Scoring Refinement

Current weights (root +0.4, deps +0.1, insight +0.5) over-weight insight. A packet with 8 dep summaries but no insight scores 0.5, while a packet with insight but no dep summaries scores 0.9. Better formula:

```
quality = 0.0
if root_summary:       +0.35
if dep_summaries > 0:  +0.25 (scaled: min(len/8, 1.0) * 0.25)
if insight:            +0.40
```

This makes dep summaries worth 25% and insight worth 40% — reflecting that a full semantic map of the neighborhood is more useful than a single insight sentence.

### Improvement 5: Language-Aware Prompts

Current prompts are language-agnostic ("describe this code entity"). Add a `Language` field to `IngestRequest` (detectable from file extension by Synapses). Use language-specific prompt variants: Go prompts emphasize interfaces and goroutines; TypeScript prompts emphasize React components and async patterns; Python prompts emphasize class hierarchies and generators.

### Improvement 6: Insight Cache Warming on Phase Change

When `PUT /v1/sdlc/phase` is called, the new phase has different section flags. For the testing phase, LLM insight is disabled entirely — the cache for `development` phase is irrelevant. On phase transitions, proactively warm the cache for entities that were recently active (top-N from `decision_log`). This eliminates cold-start latency at the beginning of each phase.

### Improvement 7: Confidence Decay in Pattern Learning

Current co-occurrence learning has no temporal decay. A pattern from 6 months ago with 50 co-occurrences has the same confidence as a recent pattern. Add `last_seen_at` tracking. Decay confidence of old patterns: `effective_confidence = confidence * exp(-days_since_last / 30)`. Recent patterns rank higher than stale ones.

---

## Fail-Silent Guarantee

Every caller integration must treat the Brain as optional. The fail-silent contract:

| Situation | Brain Response | Synapses Behavior |
|-----------|---------------|-------------------|
| Brain process not running | Connection refused | Use raw graph output |
| Brain running, Ollama down | HTTP 200, llm_used=false | Use fast-path packet (summaries only) |
| Brain returns HTTP 204 | No content | Use raw graph output |
| Brain times out (>5s) | Timeout error | Use raw graph output |
| Brain returns malformed JSON | Parse error | Use raw graph output |

The NullBrain (`pkg/brain.NullBrain`) implements the full Brain interface with zero-value returns and no errors — safe to use when the Brain module is not compiled in.

---

## Quick Start

```bash
# 1. Install Ollama
curl -fsSL https://ollama.com/install.sh | sh
ollama pull qwen2.5-coder:1.5b

# 2. Start the Brain
brain serve

# 3. Verify
curl http://localhost:11435/v1/health
# {"status":"ok","model":"qwen2.5-coder:1.5b","available":true}

# 4. Ingest a code snippet
curl -X POST http://localhost:11435/v1/ingest \
  -H 'Content-Type: application/json' \
  -d '{"node_id":"pkg:auth:Validate","node_name":"Validate","node_type":"func","package":"auth","code":"func Validate(token string) (Claims, error) { ... }"}'

# 5. Build a context packet
curl -X POST http://localhost:11435/v1/context-packet \
  -H 'Content-Type: application/json' \
  -d '{"snapshot":{"root_node_id":"pkg:auth:Validate","root_name":"Validate"},"enable_llm":false}'
```
