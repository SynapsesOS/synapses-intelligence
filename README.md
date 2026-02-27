# synapses-intelligence

**The Thinking Brain for Synapses** — a local LLM sidecar that adds semantic reasoning,
SDLC phase awareness, and co-occurrence learning to the
[Synapses](https://github.com/synapses/synapses) code-graph MCP server.

---

## What it does

Raw code-graph nodes give an LLM too much noise and not enough signal. synapses-intelligence
replaces raw nodes with **Context Packets** — structured semantic documents assembled from
brain.sqlite and (optionally) a local Ollama model. A fully-enriched packet delivers the
same information in ~800 tokens that raw nodes would need 4,000+ tokens to express.

### Capabilities at a glance

| Capability | Method | LLM? | Latency |
|---|---|---|---|
| Summarise a code entity (1 sentence) | `Ingest` | yes | 1–3 s |
| Context Packet (summaries + constraints + guidance) | `BuildContextPacket` | optional | <5 ms fast path |
| Architectural insight for a neighbourhood | `Enrich` | yes | 1–3 s |
| Explain a rule violation in plain English | `ExplainViolation` | yes (cached) | <1 ms cached |
| Agent conflict work distribution | `Coordinate` | yes | 1–3 s |
| SDLC phase + quality mode management | `SetSDLCPhase` / `SetQualityMode` | no | <1 ms |
| Co-occurrence learning ("also check Y when editing X") | `LogDecision` | no | <1 ms |
| Get learned patterns | `GetPatterns` | no | <1 ms |
| Get summary for a node | `Summary` | no | <1 ms |

**Fail-silent guarantee**: if Ollama is unreachable, all methods return zero-value safe
responses. The caller always gets something usable.

---

## Prerequisites

| Requirement | Version |
|---|---|
| Go | 1.22+ |
| [Ollama](https://ollama.com) | any recent |
| Ollama model | see [Model tiers](#model-tiers) |

No CGO, no external databases, no network dependencies beyond Ollama.

---

## Quick start

```sh
# 1. Install Ollama and pull the default model
ollama pull qwen2.5-coder:1.5b

# 2. Build the binary
make build

# 3. Start the brain sidecar
./bin/brain serve

# 4. Verify it's running
curl http://localhost:11435/v1/health
# {"status":"ok","model":"qwen2.5-coder:1.5b","available":true}
```

---

## Installation

### Build from source

```sh
git clone https://github.com/synapses/synapses-intelligence
cd synapses-intelligence
make build          # produces ./bin/brain
make test           # runs all tests
```

### Go module (library mode)

```go
import "github.com/synapses/synapses-intelligence/pkg/brain"

cfg := config.DefaultConfig()
cfg.Enabled = true
b := brain.New(cfg)
```

---

## Configuration

Configuration is a JSON file loaded via the `BRAIN_CONFIG` environment variable.
All fields are optional — sensible defaults apply.

```json
{
  "enabled": true,
  "ollama_url": "http://localhost:11434",
  "model": "qwen2.5-coder:1.5b",
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

```sh
BRAIN_CONFIG=/path/to/brain.json brain serve
```

### Configuration reference

| Field | Default | Description |
|---|---|---|
| `enabled` | `false` | Master switch. Set `true` to activate all features. |
| `ollama_url` | `http://localhost:11434` | Ollama server base URL. |
| `model` | `qwen2.5-coder:1.5b` | Ollama model tag. See [Model tiers](#model-tiers). |
| `timeout_ms` | `3000` | Per-LLM-request timeout in milliseconds. |
| `db_path` | `~/.synapses/brain.sqlite` | SQLite database path (created if missing). |
| `port` | `11435` | HTTP sidecar port. |
| `ingest` | `true` | Enable `POST /v1/ingest` (semantic summaries). |
| `enrich` | `true` | Enable `POST /v1/enrich` (neighbourhood insight). |
| `guardian` | `true` | Enable `POST /v1/explain-violation` (rule explanations). |
| `orchestrate` | `true` | Enable `POST /v1/coordinate` (agent conflict resolution). |
| `context_builder` | `true` | Enable `POST /v1/context-packet` (Context Packets). |
| `learning_enabled` | `true` | Enable `POST /v1/decision` (co-occurrence learning). |
| `default_phase` | `development` | Initial SDLC phase when brain.sqlite is first created. |
| `default_mode` | `standard` | Initial quality mode when brain.sqlite is first created. |

---

## Model tiers

| System RAM | Model | Size | Notes |
|---|---|---|---|
| 4 GB | `qwen2.5-coder:1.5b` | ~900 MB | Default. Works on any dev machine. |
| 4 GB+ | `qwen3:1.7b` | ~1.1 GB | Recommended upgrade. Better reasoning. |
| 8 GB+ | `qwen3:4b` | ~2.5 GB | Power user. Noticeably better summaries. |
| 16 GB+ | `qwen3:8b` | ~5 GB | Enterprise. Best quality, higher latency. |

```sh
# Pull a specific model
ollama pull qwen3:1.7b

# Start brain with a different model
brain serve -model qwen3:1.7b

# Or set in config
{ "model": "qwen3:1.7b" }
```

---

## CLI reference

```
brain <command> [flags]
```

### `serve`

Start the HTTP sidecar server.

```sh
brain serve                     # default port 11435, default model
brain serve -port 11436         # custom port
brain serve -model qwen3:1.7b   # custom model
```

The server binds to `127.0.0.1` only (not exposed to the network).

### `status`

Show Ollama connectivity, model, SQLite stats, feature flags, and SDLC config.

```sh
brain status
# Ollama:  connected (http://localhost:11434)
# Model:   qwen2.5-coder:1.5b
# Store:   /home/user/.synapses/brain.sqlite
# Summaries: 42 stored
#
# Features:
#   ingest       enabled
#   enrich       enabled
#   ...
#
# SDLC:
#   phase        development
#   mode         standard
```

### `ingest`

Manually trigger ingestion of a code snippet. The JSON body accepts the same fields
as `POST /v1/ingest`.

```sh
# Inline JSON
brain ingest '{"node_id":"auth:AuthService","node_name":"AuthService","node_type":"struct","package":"auth","code":"type AuthService struct { ... }"}'

# From stdin
echo '{"node_id":"...","code":"..."}' | brain ingest -
```

### `summaries`

List all stored semantic summaries in a formatted table.

```sh
brain summaries
```

### `sdlc`

Show or set the project's SDLC phase and quality mode.

```sh
brain sdlc                          # show current phase and mode
brain sdlc phase testing            # set phase
brain sdlc phase development        # back to development
brain sdlc mode enterprise          # set quality mode
brain sdlc mode standard            # reset to standard
```

**Valid phases**: `planning`, `development`, `testing`, `review`, `deployment`

**Valid modes**: `quick`, `standard`, `enterprise`

### `decisions`

List recent agent decision log entries. Used to audit what LLM agents did and
to verify that the learning loop is receiving data.

```sh
brain decisions                     # last 20 decisions
brain decisions AuthService         # decisions involving AuthService
```

### `patterns`

List learned co-occurrence patterns sorted by confidence descending.
A pattern means: "when an agent edits TRIGGER, also check CO-CHANGE."

```sh
brain patterns
# TRIGGER                   CO-CHANGE                 CONF    COUNT  REASON
# AuthService               TokenValidator            0.87       13  co-edited in same session
# UserRepository            SessionStore              0.72        9  ...
```

### `reset`

Clear all brain data from brain.sqlite (prompts for confirmation).

```sh
brain reset
```

### `version`

```sh
brain version
# synapses-intelligence v0.3.0
```

---

## HTTP API reference

All endpoints accept and return `application/json`. The server binds to
`127.0.0.1:11435` by default.

---

### `GET /v1/health`

Health check and LLM availability probe.

**Response**
```json
{
  "status": "ok",
  "model": "qwen2.5-coder:1.5b",
  "available": true
}
```

---

### `POST /v1/ingest`

Generate and store a 1-sentence semantic summary (+ topic tags) for a code entity.
Call this whenever a function, struct, or method is saved.

**Request**
```json
{
  "node_id":   "auth:AuthService",
  "node_name": "AuthService",
  "node_type": "struct",
  "package":   "auth",
  "code":      "type AuthService struct { jwtKey []byte; store *UserStore }"
}
```

**Response**
```json
{
  "node_id": "auth:AuthService",
  "summary": "Central authentication coordinator that validates JWTs and manages user sessions.",
  "tags":    ["auth", "session", "jwt"]
}
```

**Notes**
- `node_id` and `code` are required. `node_type` and `package` are optional but improve summary quality.
- Re-ingesting the same `node_id` overwrites the old summary and invalidates the insight cache.
- Returns immediately (with no summary) if Ollama is unavailable.

---

### `GET /v1/summary/{nodeId}`

Fetch the stored summary for a single node. Fast (SQLite, no LLM).

```sh
curl http://localhost:11435/v1/summary/auth:AuthService
# {"summary": "Central authentication coordinator..."}
```

Returns `404` if the node has not been ingested.

---

### `POST /v1/enrich`

Generate a 2-sentence architectural insight + concerns for an entity and its
neighbourhood. Results cached by `(node_id, phase)` for 6 hours.

**Request**
```json
{
  "root_id":      "auth:AuthService",
  "root_name":    "AuthService",
  "root_type":    "struct",
  "callee_names": ["TokenValidator", "RateLimiter"],
  "caller_names": ["LoginHandler"],
  "task_context": "adding refresh token support"
}
```

**Response**
```json
{
  "insight":   "AuthService orchestrates JWT validation and session lifecycle; it is the sole entry point for all authentication state changes.",
  "concerns":  ["Handles sensitive JWT signing keys — verify no plaintext leaks in logs"],
  "summaries": {
    "auth:AuthService": "Central auth coordinator...",
    "auth:TokenValidator": "Parses and verifies JWT signatures..."
  },
  "llm_used": true
}
```

---

### `POST /v1/explain-violation`

Get a plain-English explanation and fix suggestion for an architectural rule violation.
Results cached by `(rule_id, source_file)` indefinitely (pruned after 7 days).

**Request**
```json
{
  "rule_id":       "no-db-in-handler",
  "rule_severity": "error",
  "description":   "Handler files must not import db packages directly",
  "source_file":   "internal/handlers/user.go",
  "target_name":   "db.QueryRow"
}
```

**Response**
```json
{
  "explanation": "Handler 'user.go' imports a database function directly, bypassing the repository layer. This couples HTTP routing to storage details and makes the code hard to test.",
  "fix":         "Move db.QueryRow into a UserRepository method and inject it into the handler via the constructor."
}
```

---

### `POST /v1/coordinate`

Suggest work distribution when two agents conflict on the same scope.

**Request**
```json
{
  "new_agent_id": "claude-backend-2",
  "new_scope":    "internal/auth/",
  "conflicting_claims": [
    {"agent_id": "claude-backend-1", "scope": "internal/auth/", "scope_type": "directory"}
  ]
}
```

**Response**
```json
{
  "suggestion":        "Agent 'claude-backend-1' already owns internal/auth/. Consider working on the tests (internal/auth/*_test.go) or a related package like internal/session/.",
  "alternative_scope": "internal/session/"
}
```

---

### `POST /v1/context-packet`

Assemble a phase-aware Context Packet for an agent. This is the primary endpoint —
it replaces raw graph nodes with a compact, structured semantic document.

Returns `204 No Content` when `context_builder` is disabled or Brain is unavailable
(the caller should fall back to raw context).

**Request**
```json
{
  "agent_id":    "claude-backend-1",
  "phase":       "development",
  "quality_mode": "standard",
  "enable_llm":  false,
  "snapshot": {
    "root_node_id":  "auth:AuthService",
    "root_name":     "AuthService",
    "root_type":     "struct",
    "root_file":     "internal/auth/service.go",
    "callee_names":  ["TokenValidator", "RateLimiter", "UserRepository"],
    "caller_names":  ["LoginHandler", "RefreshHandler"],
    "related_names": ["SessionStore"],
    "applicable_rules": [
      {"rule_id": "no-db-in-handler", "severity": "error", "description": "..."}
    ],
    "active_claims": [
      {"agent_id": "claude-backend-2", "scope": "internal/session/", "scope_type": "directory", "expires_at": "2026-02-27T15:00:00Z"}
    ],
    "task_context": "adding refresh token support",
    "task_id":     "task-42"
  }
}
```

**Response**
```json
{
  "agent_id":     "claude-backend-1",
  "entity_name":  "AuthService",
  "entity_type":  "struct",
  "generated_at": "2026-02-27T12:00:00Z",
  "phase":        "development",
  "quality_mode": "standard",
  "root_summary": "Central auth coordinator that validates JWTs and manages sessions.",
  "dependency_summaries": {
    "TokenValidator":  "Parses and verifies JWT signatures against the signing key.",
    "RateLimiter":     "Enforces per-user sliding window request limits.",
    "UserRepository":  "Fetches user records from PostgreSQL by ID or email."
  },
  "insight":   "",
  "concerns":  [],
  "llm_used":  false,
  "packet_quality": 0.5,
  "active_constraints": [
    {
      "rule_id":     "no-db-in-handler",
      "severity":    "error",
      "description": "Handler files must not import db packages directly",
      "hint":        "Move db.QueryRow into a UserRepository method"
    }
  ],
  "team_status": [
    {"agent_id": "claude-backend-2", "scope": "internal/session/", "scope_type": "directory", "expires_in": 3600}
  ],
  "quality_gate": {
    "require_tests":    true,
    "require_docs":     false,
    "require_pr_check": false,
    "checklist": [
      "Write unit tests for new/modified functions",
      "Run validate_plan — no rule violations",
      "Exported symbols should have doc comments"
    ]
  },
  "pattern_hints": [
    {"trigger": "AuthService", "co_change": "TokenValidator", "reason": "co-edited in same session", "confidence": 0.87}
  ],
  "phase_guidance": "You are in development phase. Claim scope via claim_work before editing. Respect all active constraints. Run validate_plan before major changes.",
  "packet_quality": 0.5
}
```

**`packet_quality` interpretation**

| Value | Meaning |
|---|---|
| `0.0` | No summaries ingested yet. Packet carries only static data (constraints, guidance). |
| `0.4` | Root summary present, no dependency summaries. |
| `0.5` | Root + at least one dependency summary. No LLM insight. |
| `1.0` | Full packet: root summary + dependencies + LLM insight (cached or live). |

**Phase → sections matrix**

| Section | planning | development | testing | review | deployment |
|---|---|---|---|---|---|
| Root summary | ✓ | ✓ | ✓ | ✓ | ✓ |
| Dep summaries | ✓ | ✓ | — | ✓ | — |
| LLM insight | ✓ | ✓ | — | ✓ | — |
| Constraints | — | ✓ | ✓ | ✓ | — |
| Team status | ✓ | ✓ | ✓ | ✓ | ✓ |
| Quality gate | — | ✓ | ✓ | ✓ | — |
| Pattern hints | — | ✓ | — | ✓ | — |
| Phase guidance | ✓ | ✓ | ✓ | ✓ | ✓ |

---

### `GET /v1/sdlc`

Get the current SDLC config.

**Response**
```json
{
  "phase":       "development",
  "quality_mode": "standard",
  "updated_at":  "2026-02-27T12:00:00Z",
  "updated_by":  "claude-backend-1"
}
```

---

### `PUT /v1/sdlc/phase`

Set the project SDLC phase. Returns the updated config.

**Request**
```json
{"phase": "testing", "agent_id": "claude-pm"}
```

**Valid values**: `planning`, `development`, `testing`, `review`, `deployment`

---

### `PUT /v1/sdlc/mode`

Set the project quality mode. Returns the updated config.

**Request**
```json
{"mode": "enterprise", "agent_id": "claude-pm"}
```

**Valid values**: `quick`, `standard`, `enterprise`

---

### `POST /v1/decision`

Log a completed agent action. Feeds the co-occurrence learning loop.
Call this after every significant change (file edit, test run, fix).

**Request**
```json
{
  "agent_id":        "claude-backend-1",
  "phase":           "development",
  "entity_name":     "AuthService",
  "action":          "edit",
  "related_entities": ["TokenValidator", "UserRepository"],
  "outcome":         "success",
  "notes":           "Added refresh token support"
}
```

**Valid actions**: `edit`, `test`, `review`, `fix_violation`

**Valid outcomes**: `success`, `violation`, `reverted`

**Response**
```json
{"status": "recorded"}
```

Learning effect: After this call, `context_patterns` gains (or strengthens) two
bidirectional pairs: `AuthService ↔ TokenValidator` and `AuthService ↔ UserRepository`.
Future Context Packets for any of these entities will include the others in `pattern_hints`.

---

### `GET /v1/patterns`

Get learned co-occurrence patterns. Useful for debugging and auditing.

```sh
curl "http://localhost:11435/v1/patterns?trigger=AuthService&limit=5"
```

**Query parameters**

| Param | Default | Description |
|---|---|---|
| `trigger` | (all) | Filter to patterns for a specific entity name. |
| `limit` | `20` | Maximum number of patterns to return. |

**Response**
```json
{
  "count": 2,
  "patterns": [
    {"trigger": "AuthService", "co_change": "TokenValidator", "reason": "co-edited in same session", "confidence": 0.87},
    {"trigger": "AuthService", "co_change": "UserRepository",  "reason": "", "confidence": 0.72}
  ]
}
```

---

## Go API (library mode)

Import `pkg/brain` to embed the Brain directly without running an HTTP server.

```go
import (
    "context"
    "github.com/synapses/synapses-intelligence/config"
    "github.com/synapses/synapses-intelligence/pkg/brain"
)

// Build the Brain from config.
cfg := config.DefaultConfig()
cfg.Enabled = true
cfg.Model = "qwen3:1.7b"
b := brain.New(cfg)

// Ingest a code entity.
resp, err := b.Ingest(ctx, brain.IngestRequest{
    NodeID:   "auth:AuthService",
    NodeName: "AuthService",
    NodeType: "struct",
    Package:  "auth",
    Code:     `type AuthService struct { jwtKey []byte }`,
})
// resp.Summary = "Central auth coordinator..."
// resp.Tags    = ["auth", "jwt"]

// Build a Context Packet (fast path — no LLM call).
pkt, err := b.BuildContextPacket(ctx, brain.ContextPacketRequest{
    AgentID: "my-agent",
    Snapshot: brain.SynapsesSnapshotInput{
        RootNodeID:  "auth:AuthService",
        RootName:    "AuthService",
        RootType:    "struct",
        CalleeNames: []string{"TokenValidator"},
    },
    EnableLLM: false,
})
// pkt is nil when Brain is unavailable — fall back to raw context.
if pkt != nil {
    fmt.Println(pkt.RootSummary)
    fmt.Printf("Quality: %.1f\n", pkt.PacketQuality)
}

// Log a decision to feed learning.
_ = b.LogDecision(ctx, brain.DecisionRequest{
    AgentID:         "my-agent",
    Phase:           "development",
    EntityName:      "AuthService",
    Action:          "edit",
    RelatedEntities: []string{"TokenValidator"},
    Outcome:         "success",
})
```

### `Brain` interface

```go
type Brain interface {
    // Semantic summaries
    Ingest(ctx, IngestRequest) (IngestResponse, error)
    Enrich(ctx, EnrichRequest) (EnrichResponse, error)
    Summary(nodeID string) string

    // Architectural analysis
    ExplainViolation(ctx, ViolationRequest) (ViolationResponse, error)
    Coordinate(ctx, CoordinateRequest) (CoordinateResponse, error)

    // Context Packet
    BuildContextPacket(ctx, ContextPacketRequest) (*ContextPacket, error)

    // Learning loop
    LogDecision(ctx, DecisionRequest) error
    GetPatterns(trigger string, limit int) []PatternHint

    // SDLC
    SetSDLCPhase(phase SDLCPhase, agentID string) error
    SetQualityMode(mode QualityMode, agentID string) error
    GetSDLCConfig() SDLCConfig

    // Diagnostics
    Available() bool
    ModelName() string
}
```

Use `brain.New(cfg)` for production and `&brain.NullBrain{}` for tests or when the
Brain is disabled. `NullBrain` satisfies the interface with all zero-value returns —
no panics, no errors.

---

## Integration with Synapses

synapses-intelligence is designed to run as a sidecar next to a Synapses MCP server.
See [INTELLIGENCE.md](INTELLIGENCE.md) for the complete integration guide including:

- The three Synapses injection points (file indexer, get_context, get_violations)
- The `BrainClient` interface Synapses must implement
- Proposed `synapses.json` config additions
- Known integration challenges and how to solve them
- v0.4.0 improvement roadmap

**Three-second summary of integration**:
1. Synapses starts the brain sidecar (or connects to it at `localhost:11435`).
2. On every file index event, Synapses calls `POST /v1/ingest` for each changed entity
   (fire-and-forget goroutine, non-blocking).
3. On `get_context`, Synapses calls `POST /v1/context-packet` with the graph snapshot.
   If the packet is non-nil, it is prepended to the response. If nil (Brain unavailable),
   the raw context is returned unchanged.
4. On `get_violations`, Synapses calls `POST /v1/explain-violation` for each violation
   and attaches the explanation + fix hint.

---

## SDLC workflow

```
planning → development → testing → review → deployment → planning
```

Set the phase at the start of each stage:

```sh
brain sdlc phase development    # start writing code
brain sdlc phase testing        # switch to test mode — constraints enabled, no LLM insight
brain sdlc phase review         # full review mode — all sections, enterprise checklist
brain sdlc phase deployment     # freeze code — only team status and guidance shown
```

The phase is shared across all agents working on the project (stored in brain.sqlite).
Agents receive `phase_guidance` in every Context Packet telling them what to do.

---

## Quality modes

| Mode | Tests required | Docs required | PR checklist | Use case |
|---|---|---|---|---|
| `quick` | No | No | No | Prototypes, hotfixes |
| `standard` (default) | Unit | No | No | Normal development |
| `enterprise` | Unit + integration | Full GoDoc | Yes + CHANGELOG | Production, open source |

```sh
brain sdlc mode enterprise    # enable full quality gate
```

---

## Data stored in brain.sqlite

| Table | Contents | TTL / Pruning |
|---|---|---|
| `semantic_summaries` | 1-sentence summary + tags per node | Never pruned (re-ingest overwrites) |
| `violation_cache` | Rule violation explanations per (rule_id, file) | 7 days |
| `insight_cache` | LLM-generated insights per (node_id, phase) | 6 hours; also invalidated on re-ingest |
| `sdlc_config` | Current project phase + quality mode (single row) | Never pruned |
| `context_patterns` | Co-occurrence pairs (trigger ↔ co_change) | 14 days if co_count < 2 |
| `decision_log` | Agent decision history | 30 days |

Reset everything with `brain reset` or `DELETE` all rows via the `/v1/reset` endpoint.

---

## Development

```sh
make build          # build ./bin/brain
make test           # run all tests with -v
make test-short     # run tests in short mode (skips slow paths)
make lint           # go vet ./...
make tidy           # go mod tidy
make bench          # run benchmarks in internal packages
```

### Adding a new feature

1. New internal logic goes in `internal/<package>/`.
2. Public types go in `pkg/brain/types.go`.
3. Wire the new method in `pkg/brain/brain.go` and add a no-op to `pkg/brain/null.go`.
4. Add the HTTP handler in `server/server.go`.
5. Add a CLI command in `cmd/brain/main.go`.
6. **Import rule**: `internal/*` packages must NEVER import `pkg/brain`. Use local types
   and let `pkg/brain/brain.go` do the conversion.

---

## License

See [LICENSE](LICENSE) in the repository root.
