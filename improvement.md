# synapses-intelligence improvement log

## v0.5.1 — Tiered Nervous System + Scout Prune Pipeline (2026-03-03)

### Changes

#### P0 — Fix WriteTimeout (BUG-I01 closed)
`server/server.go`: `WriteTimeout` now set to `2 × cfg.TimeoutMS` instead of hardcoded 30s.
`config/config.go`: Default `TimeoutMS` raised from 30000 → 60000ms.
All 3 broken endpoints (`/v1/enrich`, `/v1/explain-violation`, `/v1/coordinate`) are now
reachable on CPU. Closes BUG-I01.

#### P1 — Four-Tier Model Config
`config/config.go`: Added 4 new tier model fields:
- `ModelIngest`      (Tier 0 Reflex, default `qwen3.5:0.8b`)
- `ModelGuardian`    (Tier 1 Sensory, default `qwen3.5:2b`)
- `ModelEnrich`      (Tier 2 Specialist, default `qwen3.5:4b`)
- `ModelOrchestrate` (Tier 3 Architect, default `qwen3.5:9b`)
Backward-compatible fallback chain in `applyDefaults()`: missing tier fields fall back to
`fast_model` (ingest) or `model` (others).

#### P2 — Route Handlers to Correct Tier
`pkg/brain/brain.go`: `New()` creates 4 separate `OllamaClient` instances, one per tier.
- Ingestor → Tier 0 (0.8B): bulk summarization, fast and cheap
- Guardian → Tier 1 (2B): violation explanations, was broken with 7b, now <5s
- Enricher → Tier 2 (4B): architectural insight, ~12s on CPU
- Orchestrator → Tier 3 (9B): multi-agent conflict resolution, ~25s on CPU

#### P3 — ThinkingBudget per Tier (Qwen3.5 /think mode)
`internal/llm/ollama.go`:
- Added `think bool` field + `WithThinking(enabled bool)` builder method
- `Generate()` prepends `/think\n\n` or `/no_think\n\n` to the prompt
- `<think>...</think>` blocks in responses are stripped via `thinkTagRe` regex
Tier 0 (ingest) + Tier 1 (guardian): `thinking=false` — fast, deterministic
Tier 2 (enrich) + Tier 3 (orchestrate): `thinking=true` — deeper reasoning

#### P7 — /v1/prune Scout Preprocessing Endpoint
New package `internal/pruner/pruner.go`: strips web page boilerplate (navigation, ads,
footers, cookie banners) using Tier 0 model. Returns clean technical paragraphs as plain text.
`server/server.go`: `POST /v1/prune` handler added.
`pkg/brain/brain.go`: `Prune(ctx, content) (string, error)` added to Brain interface and impl.
`pkg/brain/null.go`: NullBrain.Prune() returns original content unchanged.

**Effect:** Scout raw web content (3000 chars) → pruned clean signal (~1200 chars) → 4B
distillation sees only valuable content → better summaries, faster inference.

---

## v0.4.0 — E2E Test Run (2026-03-03)

---

### CRITICAL BUGS

#### BUG-I01 — HTTP WriteTimeout kills all 7b-model endpoints silently
**Severity:** Critical
**Root cause:** `server/server.go` line 52:
```go
WriteTimeout: 30 * time.Second, // LLM calls can take up to ~3s
```
The comment is **stale**. The enricher/guardian/orchestrator use `qwen2.5-coder:7b`
which takes **30-40 seconds** on CPU. The HTTP write timeout fires at exactly the
same time as the LLM context deadline → the handler never writes a response →
the client receives an empty body.
**Affected endpoints:** `/v1/enrich`, `/v1/explain-violation`, `/v1/coordinate`
**Evidence:** All three return empty body after exactly 30.1s.
**Fix options (in priority order):**
  1. Make `WriteTimeout` configurable: `server.WriteTimeout = time.Duration(cfg.TimeoutMS*2) * time.Millisecond`
  2. Remove `WriteTimeout` entirely for LLM endpoints and rely on the enricher's
     context deadline instead (set per-handler timeouts via `http.TimeoutHandler`).
  3. Short-term workaround: increase default TimeoutMS in config to 60000 and
     set WriteTimeout to 90s.

#### BUG-I02 — Ingest summary truncated mid-sentence
**Severity:** High
**Observed:** `/v1/ingest` response contains `"summary": "...The tags associated…"`
— summary cut at `NumPredict` token limit. The stored summary in brain.sqlite
is incomplete and propagates into context-packet `root_summary` with a trailing `…`.
**Root cause:** `NumPredict` in `internal/llm/ollama.go` is set to 250 tokens.
Summaries for complex code entities exceed this limit.
**Fix:** Increase `NumPredict` to 400-500. Or use a two-phase approach: request
a JSON object with `{"summary": "..."}` and set NumPredict to stop cleanly at
the closing brace.

#### BUG-I03 — Intelligence ingestor prompt uses "code entity" for all node types
**Severity:** Medium
**Observed:** When scout sends a web article for distillation with
`node_type: "web article"`, the returned summary says "This **code entity**
provides information about the Model Context Protocol...". The ingestor prompt
template hardcodes "code entity" regardless of `node_type`.
**Root cause:** `internal/ingestor/ingestor.go` `buildPrompt()` likely uses a
static phrase.
**Fix:** Use the `node_type` field in the prompt:
```go
fmt.Sprintf("Summarize this %s in 1-2 sentences: %s\n\n%s", nodeType, name, code)
```
This makes summaries contextually correct for web articles, YouTube videos, and
search result sets.

#### BUG-I04 — Tags always empty in ingest response
**Severity:** Medium
**Observed:** Scout receives `"tags": []` from every ingest call. The
`IngestResponse` has a `Tags []string` field but it's never populated.
**Root cause:** The ingestor prompt doesn't ask for tags, and `parseInsight`/
`parseSummary` don't extract them.
**Fix:** Add to the ingest prompt: "Also list 2-3 relevant tags as a
comma-separated list on the last line, prefixed with 'tags:'". Parse and return.
Tags improve search relevance and categorisation in brain.sqlite.

---

### ARCHITECTURE ISSUES

#### ARCH-I01 — packet_quality ceiling of 0.4 on CPU-only deployments
**Observed:** `packet_quality: 0.4` means only `root_summary` is present.
`insight` (requires `/v1/enrich`) and `dep_summaries` (requires bulk ingest of
callee nodes) are missing because enrich times out on CPU with 7b model.
**Impact:** On a GPU-less machine, context packets are never fully enriched.
This is the primary use case for indie developers.
**Improvements:**
  1. For enrichment, default to `qwen2.5-coder:1.5b` (same as fast ingest).
     Quality drops ~20% but latency drops from 30s to 3-5s.
  2. Add an `enrich_model` config field separate from `model` (primary).
  3. Or implement async enrichment: return `packet_quality: 0.4` immediately,
     enrich in background, store result, return from cache on next call.

#### ARCH-I02 — No request logging in brain HTTP server
**Observed:** `/tmp/brain.log` only shows startup messages. There are no
per-request logs for ingest/enrich timings, LLM latencies, or errors.
**Impact:** Debugging timeouts, slow responses, and LLM errors is blind.
**Fix:** Add a simple logging middleware that logs:
```
2026-03-03T13:10:31Z POST /v1/ingest  node=cmdStart  latency=16.2s  status=200
2026-03-03T13:11:00Z POST /v1/enrich  root=cmdStart  latency=30.1s  status=500 (timeout)
```

#### ARCH-I03 — `/v1/context-packet` requires full SnapshotInput; not ergonomic for standalone use
**Observed:** Calling `/v1/context-packet` without the correct nested
`snapshot` structure returns `entity_name: ""` and `packet_quality: 0`.
The schema requires deeply nested JSON that differs from the simpler ingest
schema. Callers need to read the code to discover the correct structure.
**Fix:** Add a simpler `GET /v1/context-packet?node_id=X&name=Y` endpoint that
wraps the POST endpoint with sensible defaults for all nested fields.

#### ARCH-I04 — Coordinate endpoint schema undiscoverable
**Observed:** Calling `/v1/coordinate` with an intuitive `{"agents":[...], "shared_entities":[...]}`
schema returns `{"error": "new_agent_id and new_scope are required"}`. The actual
schema (`new_agent_id`, `new_scope`, `conflicting_claims`) is only discoverable
by reading Go types — it's not documented in the API response or README.
**Fix:** Return a detailed error with the expected schema in the error message,
or add a `GET /v1/coordinate/schema` endpoint.

---

### PERFORMANCE NOTES (CPU / no GPU)

| Endpoint | Model | Latency | Status |
|----------|-------|---------|--------|
| `/v1/ingest` | qwen2.5-coder:1.5b | ~16s | ✅ Works |
| `/v1/enrich` | qwen2.5-coder:7b | >30s | ❌ Fails (WriteTimeout) |
| `/v1/explain-violation` | qwen2.5-coder:7b | >30s | ❌ Fails |
| `/v1/coordinate` | qwen2.5-coder:7b | >30s | ❌ Fails |
| `/v1/context-packet` (no LLM) | SQLite | <1ms | ✅ Works |
| `/v1/decision` | SQLite | <1ms | ✅ Works |
| `/v1/patterns` | SQLite | <1ms | ✅ Works |
| `/v1/sdlc` | SQLite | <1ms | ✅ Works |

**Recommendation:** On CPU-only machines, configure all LLM endpoints to use
`qwen2.5-coder:1.5b` with a 20s timeout. The 7b model is unusable without GPU.

---

### WHAT WORKS WELL ✅

- Decision log + co-occurrence pattern learning: instant, deterministic.
- Context packets (no-LLM path): instant, structured, SDLC-aware.
- SDLC phase/mode management: correct, persisted, multi-agent safe.
- Pattern hints in context-packet: correctly surfaces co-change suggestions.
- Fail-silent pattern: intelligence unavailability never crashes synapses.
- Brain.sqlite: lightweight, portable, no daemon dependency.
