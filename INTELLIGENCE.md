# synapses-intelligence v0.6.1
## The AI Brain for Synapses OS

---

## What This Is

`synapses-intelligence` is a **local LLM sidecar** that adds semantic reasoning to code agent systems. It runs as a lightweight HTTP server on `localhost:11435`, backed by [Ollama](https://ollama.com) (local inference, no API keys, no cloud, no data leaves your machine) and its own `brain.sqlite` for persistent storage.

It is **Layer 2** in the Synapses OS stack:

```
[ Agent (Claude / GPT / Gemini) ]   ← consumes Context Packets
         ↑
[ synapses-intelligence (Brain) ]   ← this module — semantic enrichment
         ↑
[ synapses (Code Graph MCP) ]       ← structural graph, BFS traversal
         ↑
[ Codebase ]
```

**Primary job:** Transform raw structural graph data (node IDs, edge types, code snippets) into compact, phase-aware **Context Packets** — structured semantic documents that tell an LLM agent exactly what it needs to know, in ~600-800 tokens, instead of 4,000+ raw tokens.

**Secondary jobs:** Learn project patterns, enforce SDLC phase discipline, coordinate multi-agent work, explain architectural violations in plain English, and store Architectural Decision Records (ADRs) as persistent cold memory.

---

## System Requirements

### Minimum (runs Tier 0 / Tier 1 models only)
- **OS:** macOS 10.15+, Linux x86-64/arm64, Windows 10+
- **RAM:** 4 GB (leaves ~3 GB for OS + Go runtime + Ollama)
- **Disk:** 2 GB for Ollama + a 1.5B model (~900 MB)
- **Dependencies:** [Ollama](https://ollama.com) running locally

### Recommended (all 4 tiers)
- **RAM:** 8 GB+ (allows the 7B model for Tier 2/3)
- **GPU (optional):** Apple Silicon (Metal), NVIDIA (CUDA), or AMD (ROCm)
  - GPU machines use the Qwen3.5 family with full thinking mode
  - CPU-only machines use `qwen2.5-coder` (proven fast: 10-20s/call)

### Hardware → Model Selection (auto-detected by `brain setup`)

| Hardware | Tier 0 (ingest/prune) | Tier 1 (guardian) | Tier 2 (enrich) | Tier 3 (orchestrate) |
|---|---|---|---|---|
| GPU (NVIDIA/AMD/Apple M-series) | `qwen3.5:0.8b` | `qwen3.5:2b` | `qwen3.5:4b` | `qwen3.5:9b` |
| CPU-only | `qwen2.5-coder:1.5b` | `qwen2.5-coder:1.5b` | `qwen2.5-coder:7b` | `qwen2.5-coder:7b` |

> `brain setup` detects your hardware automatically (Metal/CUDA/ROCm via PATH tools, no CGo), benchmarks installed models for actual inference latency, and writes optimal settings to `~/.synapses/brain.json`. Run it once after installing Ollama and pulling models.

---

## 4-Tier Nervous System

synapses-intelligence routes tasks to models by cognitive complexity:

| Tier | Name | Task | Why this tier |
|------|------|------|--------------|
| **0** | Reflex | `POST /v1/ingest`, `POST /v1/prune` | Fast, high-volume: every file change triggers an ingest. 0.8B handles 100+ nodes in background. |
| **1** | Sensory | `POST /v1/explain-violation` | Slightly richer: needs to reason about rule context and suggest concrete fixes. |
| **2** | Specialist | `POST /v1/enrich` | Domain-aware architectural insight. Needs enough capacity to reason about system-level patterns. |
| **3** | Architect | `POST /v1/coordinate` | Multi-agent conflict resolution. Requires global reasoning across all active claims. |

Each tier uses a separate Ollama model, configured in `brain.json`:
```json
{
  "model_ingest":     "qwen2.5-coder:1.5b",
  "model_guardian":   "qwen2.5-coder:1.5b",
  "model_enrich":     "qwen2.5-coder:7b",
  "model_orchestrate": "qwen2.5-coder:7b"
}
```

**Thinking mode** (Qwen3.x only): `brain.go` auto-detects whether a model supports thinking mode from its name (`qwen3.x → enabled`, others → disabled). The `/think` prefix is only sent to Qwen3.x models; non-Qwen3 models receive a clean prompt with no prefix.

---

## API Reference

### POST /v1/ingest — Semantic Ingestor (Tier 0)

Generates a 2-3 sentence prose briefing and domain tags for any code entity. Called automatically by synapses on file change.

**Request:**
```json
{
  "node_id":   "synapses::internal/auth/service.go::AuthService",
  "node_name": "AuthService",
  "node_type": "struct",
  "package":   "auth",
  "code":      "type AuthService struct { ... }"
}
```

**Response:**
```json
{
  "node_id": "synapses::internal/auth/service.go::AuthService",
  "summary": "Central coordinator for user authentication and session lifecycle. Delegates JWT verification to TokenValidator and enforces rate limits via RateLimiter. Changes here affect all protected API routes.",
  "tags": ["auth", "jwt", "session"]
}
```

Summaries are stored in `brain.sqlite`. Re-ingesting a node invalidates its `insight_cache` entry.

---

### POST /v1/prune — Boilerplate Stripper (Tier 0)

Strips navigation, ads, and boilerplate from web page content. Used by synapses-scout before distillation.

**Request:**
```json
{ "content": "<raw web page text up to 3000 chars>" }
```

**Response:**
```json
{ "pruned": "<clean technical content>" }
```

---

### POST /v1/enrich — Context Enricher (Tier 2)

Returns stored summaries for a set of node IDs plus a domain-aware 2-sentence LLM insight about the root entity. Domain is auto-detected from file path:

| File pattern | Domain | Enricher focus |
|---|---|---|
| `*/parser/*` | parser | Language-specific quirks, AST handling, missing language support |
| `*/mcp/*` | mcp | Tool contract, fail-silent semantics, latency |
| `*/graph/*` | graph | BFS correctness, edge cases, complexity |
| `*/store/*` | store | SQL correctness, migration safety |
| `*/brain/*`, `*/scout/*` | integration | Timeout handling, HTTP contracts |
| (other) | general | Architectural patterns, dependencies |

**Request:**
```json
{
  "root_id":    "synapses::AuthService",
  "root_name":  "AuthService",
  "root_type":  "struct",
  "root_file":  "internal/auth/service.go",
  "all_node_ids": ["synapses::AuthService", "synapses::TokenValidator"],
  "callee_names": ["TokenValidator", "RateLimiter"],
  "caller_names": ["HTTPHandler"],
  "task_context": "Adding OAuth2 support"
}
```

**Response:**
```json
{
  "insight": "AuthService is the central authentication boundary; changes here affect all protected routes.",
  "concerns": ["token expiry edge cases", "concurrent session invalidation"],
  "summaries": {
    "synapses::AuthService": "Central coordinator for user authentication...",
    "synapses::TokenValidator": "Cryptographically verifies JWT signatures..."
  },
  "llm_used": true
}
```

---

### POST /v1/context-packet — Phase-Aware Context Packet

**The primary integration endpoint.** Builds a complete Context Packet: a structured semantic document replacing raw graph nodes. ~600-800 tokens vs 4,000+ raw tokens.

**`packet_quality` field (0.0–1.0):**
- `0.0` — no summaries in brain.sqlite (cold start)
- `0.4` — root summary present (from brain or graph doc fallback)
- `0.5` — root + dep summaries
- `1.0` — root + deps + LLM insight (fully enriched)

**Phase matrix — what's included per SDLC phase:**

| Section | planning | development | testing | review | deployment |
|---------|:--------:|:-----------:|:-------:|:------:|:----------:|
| Root summary | ✓ | ✓ | ✓ | ✓ | ✓ |
| Dep summaries | ✓ | ✓ | — | ✓ | — |
| LLM insight | ✓ | ✓ | — | ✓ | — |
| Constraints | — | ✓ | ✓ | ✓ | — |
| Team status | ✓ | ✓ | ✓ | ✓ | ✓ |
| Quality gate | — | ✓ | ✓ | ✓ | — |
| Pattern hints | — | ✓ | — | ✓ | — |
| Phase guidance | ✓ | ✓ | ✓ | ✓ | ✓ |

**Insight caching:** LLM insight is cached per `(node_id, phase)` for 6 hours. Cache hits return instantly with `llm_used: false`.

**Doc fallback:** When brain.sqlite has no summary for a node, the graph's own AST doc comment is used as `root_summary`. This gives `packet_quality ≥ 0.4` immediately on a fresh install, without any LLM call.

---

### POST /v1/explain-violation — Rule Guardian (Tier 1)

Explains an architectural rule violation in plain English with a concrete fix. Results cached per `(rule_id, source_file)` for 7 days.

**Response:**
```json
{
  "explanation": "The handler auth.go is directly calling database functions, bypassing the repository layer...",
  "fix": "Create an AuthRepository interface in internal/auth/ and inject it via constructor."
}
```

---

### POST /v1/coordinate — Task Orchestrator (Tier 3)

Suggests work distribution when agents claim overlapping scopes.

---

### POST /v1/decision — Decision Log

Agents log completed work. The Brain extracts co-occurrence patterns from `related_entities`, building a project-specific "when you edit X, you usually also edit Y" knowledge base. Patterns surface in future context packets as `pattern_hints`.

---

### POST/GET /v1/adr — Architectural Decision Records (v0.6.0+)

Stores permanent "why" records for architectural decisions. ADRs appear in `get_context(format="compact")` output via synapses when the entity's file matches the ADR's `linked_files`.

**Store an ADR:**
```json
{
  "id": "adr-001-no-cgo",
  "title": "No CGo — use modernc/sqlite",
  "status": "accepted",
  "context": "Deployment targets include ARM and MUSL Linux; CGo breaks cross-compilation.",
  "decision": "Use modernc/sqlite (pure Go SQLite driver).",
  "consequences": "No libsqlite3 system dependency; binary is self-contained.",
  "linked_files": ["internal/store/"]
}
```

**List ADRs:** `GET /v1/adr` → `{"adrs": [...]}`

---

### GET /v1/health

```json
{"status": "ok", "model": "qwen2.5-coder:7b", "available": true}
```

`available: false` means Ollama is not reachable. All SQLite-fast-path operations still work.

---

## Configuration (`~/.synapses/brain.json`)

```json
{
  "enabled":           true,
  "port":              11435,
  "ollama_url":        "http://localhost:11434",
  "model":             "qwen2.5-coder:7b",
  "model_ingest":      "qwen2.5-coder:1.5b",
  "model_guardian":    "qwen2.5-coder:1.5b",
  "model_enrich":      "qwen2.5-coder:7b",
  "model_orchestrate": "qwen2.5-coder:7b",
  "timeout_ms":        120000,
  "db_path":           "~/.synapses/brain.sqlite"
}
```

> `brain setup` writes this file automatically based on detected hardware + model latency probing. Manual edits are preserved on the next `brain setup`.

**Tilde expansion:** `~/` in `db_path` is expanded at startup — you can safely write `"~/.synapses/brain.sqlite"` in the JSON.

---

## CLI Reference

```
brain serve           Start the HTTP sidecar on :11435
brain status          Show Ollama connectivity, model, SDLC config, summary stats
brain setup           Detect hardware, benchmark models, write brain.json
brain benchmark       Run latency benchmark on all installed Ollama models
brain ingest <json>   Manually ingest a code snippet (for testing)
brain summaries       List all stored semantic summaries
brain sdlc            Show current SDLC phase and quality mode
brain sdlc phase <p>  Set phase (planning|development|testing|review|deployment)
brain sdlc mode <m>   Set quality mode (quick|standard|enterprise)
brain decisions       List recent agent decision log
brain patterns        List all learned co-occurrence patterns
brain reset           Clear all brain data (prompts for confirmation)
brain version         Print version
```

---

## Integration with synapses

synapses calls synapses-intelligence automatically via `internal/brain/client.go`:

1. **On file index**: auto-ingest every node (fire-and-forget, background goroutines)
2. **On `get_context`**: call `/v1/context-packet` to enrich the BFS subgraph
3. **On `get_violations`**: call `/v1/explain-violation` for each violation (cached 7 days)
4. **On `web_fetch` / `web_annotate`**: call `/v1/ingest` with web article content

Configure in `synapses.json`:
```json
{
  "brain": {
    "url": "http://localhost:11435",
    "timeout_ms": 30000
  }
}
```

**Fail-silent guarantee:** All brain calls are optional. If brain is unavailable (not running, Ollama down, timeout), synapses falls back to raw graph output with a `brain_unavailable` hint. The `NullBrain` stub implements the full interface with zero-value returns — safe to use when brain is not configured.

---

## brain.sqlite Schema

| Table | Purpose | TTL |
|-------|---------|-----|
| `semantic_summaries` | Prose briefings per node | Permanent (updated on re-ingest) |
| `insight_cache` | LLM insight per `(node_id, phase)` | 6 hours; invalidated on re-ingest |
| `violation_cache` | Violation explanations per `(rule_id, file)` | 7 days |
| `context_patterns` | Learned co-occurrence patterns | Pruned if stale after 14 days |
| `decision_log` | Agent action history | Pruned after 30 days |
| `sdlc_config` | Current project phase + quality mode | Permanent (1 row) |
| `adrs` | Architectural Decision Records | Permanent (v0.6.0+) |

---

## Quick Start

```bash
# 1. Install Ollama
curl -fsSL https://ollama.com/install.sh | sh

# 2. Install brain
go install github.com/SynapsesOS/synapses-intelligence/cmd/brain@latest

# 3. Run setup (detects GPU/CPU, pulls appropriate models, writes brain.json)
brain setup

# 4. Start the sidecar
brain serve &

# 5. Verify
curl http://localhost:11435/v1/health
# {"status":"ok","model":"qwen2.5-coder:7b","available":true}
```

After setup, wire synapses to the brain by adding to `synapses.json`:
```json
{ "brain": { "url": "http://localhost:11435" } }
```
