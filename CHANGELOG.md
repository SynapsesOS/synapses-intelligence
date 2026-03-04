# Changelog

All notable changes to synapses-intelligence are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

---

## [0.6.1] — 2026-03-04

### Fixed

- **BUG-I01: Thinking mode prefix sent to non-Qwen3 models**: `OllamaClient.Generate()` was prepending `/think\n\n` or `/no_think\n\n` to ALL models regardless of model family. `qwen2.5-coder` and other non-Qwen3 models do not understand these prefixes, causing them to produce garbled or empty output. Fixed in `ollama.go`: thinking prefixes are now only applied when `strings.HasPrefix(model, "qwen3")`. Non-Qwen3 models receive a clean prompt with no prefix. (`internal/llm/ollama.go`)

- **BUG-I01 (brain.go): `WithThinking(true)` hardcoded for enrich/orchestrate**: `brain.go New()` was calling `.WithThinking(true)` unconditionally for Tier 2 and Tier 3 clients. Added `supportsThinking(model string) bool` helper that checks the model prefix and passes the result to `WithThinking()`. (`pkg/brain/brain.go`)

- **BUG-I02: Tilde (`~/`) not expanded in `db_path`**: When `brain.json` contained `"db_path": "~/.synapses/brain.sqlite"`, the path was used literally, causing `brain.sqlite` to be written to a `~/` directory relative to the current working directory. Fixed in `applyDefaults()` by expanding `~/` to `os.UserHomeDir()`. (`config/config.go`)

### Added

- **GPU accelerator detection in `brain setup`**: `cmdSetup` now calls `detectAccelerator()` — a pure Go (no CGo) function that checks `runtime.GOOS == "darwin"` (Metal), `exec.LookPath("nvidia-smi")` (CUDA), and `exec.LookPath("rocm-smi")` (ROCm). GPU machines use the Qwen3.5 family; CPU-only machines use `qwen2.5-coder` (proven fast: 10-20s vs 60s+ for Qwen3.5 on CPU). (`cmd/brain/main.go`)

- **Tiered model assignment in `brain setup`**: `tierModelsForAccelerator(accel)` returns hardware-appropriate models for all 4 tiers. `brain setup` now writes `model_ingest`, `model_guardian`, `model_enrich`, and `model_orchestrate` as separate keys instead of assigning the same model to all tiers. (`cmd/brain/main.go`)

---

## [0.6.0] — 2026-03-03

### Added

- **Architectural Decision Records (ADRs)**: New `adrs` table in brain.sqlite. New HTTP endpoints: `POST /v1/adr` (create/update), `GET /v1/adr` (list all), `GET /v1/adr/{id}` (get one). ADRs store the "why" behind architectural decisions as permanent cold memory. Surfaced via synapses `get_adrs` MCP tool and in `get_context(format="compact")` for matching files. (`server/server.go`, `internal/store/store.go`)

- **Domain-aware enricher**: The enricher now detects the domain of each entity from its file path (`internal/parser/` → parser domain, `internal/mcp/` → MCP domain, etc.) and applies specialized prompt prefixes. Parser domain prompts emphasize language-specific quirks; MCP domain prompts emphasize fail-silent semantics and latency constraints. (`internal/enricher/enricher.go`)

- **Doc fallback for `packet_quality`**: When brain.sqlite has no summary for a node, the graph's own AST doc comment is used as `root_summary`. This gives `packet_quality ≥ 0.4` immediately on a fresh install without any LLM call. (`internal/contextbuilder/builder.go`)

- **`RootFile` wired to enricher**: The `root_file` field from the context packet request is now passed to the enricher, enabling domain detection and per-file architectural insights. (`internal/contextbuilder/builder.go`, `internal/enricher/enricher.go`)

- **`brain benchmark` command**: Standalone latency benchmark for all installed Ollama models. Prints a table with model name, latency, and pass/fail against the configured timeout. (`cmd/brain/main.go`)

- **Graph warnings in context packets**: `GraphWarnings` in context packets now include deterministic topology-based warnings (blast radius, missing tests) computed from graph structure without any LLM call.

---

## [0.5.1] — 2026-03-03

### Fixed

- **WriteTimeout hardcoded at 30s**: HTTP server `WriteTimeout` was hardcoded to 30 seconds, causing all three LLM endpoints (`/v1/ingest`, `/v1/enrich`, `/v1/explain-violation`) to always return 503 on CPU-only machines where inference takes 15-60s. Changed to `2 × TimeoutMS`. (`server/server.go`)

- **Context packet doc fallback**: `BuildContextPacket` now falls back to the root node's graph doc comment when brain.sqlite has no summary, giving `packet_quality ≥ 0.4` on cold-brain deployments.

---

## [0.3.1] — 2026-03-02

### Fixed

- **OllamaClient stop tokens caused silent empty enricher responses**: `Generate()`
  had stop tokens `["\n\n", "```"]`. The enricher prompt causes `qwen2.5-coder:1.5b`
  to begin output with ` ```json`, which immediately triggers the `"```"` stop token
  and returns an empty string before any content is produced. All LLM insight
  generation silently failed — `context-packet` returned `packet_quality ≤ 0.4` with
  no insight regardless of SDLC phase. Removed both stop tokens: `ExtractJSON()`
  already handles markdown-wrapped JSON correctly. Increased `NumPredict` 150 → 250
  to accommodate the enricher's longer output. (`internal/llm/ollama.go`)

- **Ingestor plain-text fallback**: `parseSummary` returned an error when
  `qwen2.5-coder:1.5b` returned plain text instead of a JSON object (common with
  small models under varied prompts). Added fallback path: if JSON parsing fails,
  the raw response text is trimmed and used as the summary (capped at 300 chars).
  (`internal/ingestor/ingestor.go`)

- **Enricher plain-text fallback**: Same issue in `parseInsight` — model plain-text
  responses caused the enricher to return an error, which was silently swallowed by
  the context builder. Added identical fallback (capped at 400 chars).
  (`internal/enricher/enricher.go`)

- **Default `TimeoutMS` too short for CPU-only Ollama**: Default was 3000ms
  (3 seconds). `qwen2.5-coder:1.5b` inference on a CPU takes 2–5+ seconds depending
  on system load, causing intermittent `context deadline exceeded` errors on ingest
  and enrich calls. Increased default from 3000 to 30000 (30 seconds).
  (`config/config.go`)

---

## [0.3.0] — 2026-02-27

### Added
- **Insight cache** (`insight_cache` table in brain.sqlite): LLM-generated insights
  are now cached per `(node_id, phase)` pair with a 6-hour TTL. Subsequent calls to
  `BuildContextPacket` or `Enrich` return the cached result instantly without hitting
  Ollama — the slow path only runs on cache miss.
- **Cache invalidation on re-ingest**: `UpsertSummary` now automatically deletes all
  `insight_cache` rows for the re-ingested node. Code changes always produce fresh insights.
- **`PacketQuality float64`** field on `ContextPacket` (0.0–1.0 heuristic):
  - `0.0` — no summaries ingested yet (empty packet)
  - `0.4` — root summary present, no dependencies, no insight
  - `0.5` — root + dependency summaries, no LLM insight
  - `1.0` — root summary + dep summaries + LLM insight (fully enriched)
  Agents can read this field to decide whether to request a follow-up enrichment pass.
- **`LLMUsed bool`** field on `EnrichResponse` and `ContextPacket`: `true` means a live
  Ollama call was made; `false` means all data came from brain.sqlite (sub-millisecond).
- **`internal/llm/util.go`**: Shared `ExtractJSON()` and `Truncate()` utilities extracted
  from four internal packages that previously duplicated the code.
- **`INTELLIGENCE.md`**: Comprehensive machine-readable reference for LLM agents and
  developers. Covers all 9 capabilities, 3 Synapses injection points, integration
  challenges, and v0.4.0 improvement proposals.
- **New CLI subcommands**:
  - `brain sdlc` — show current SDLC phase and quality mode
  - `brain sdlc phase <phase>` — set phase (planning/development/testing/review/deployment)
  - `brain sdlc mode <mode>` — set mode (quick/standard/enterprise)
  - `brain decisions [entity]` — list recent agent decision log entries
  - `brain patterns` — list learned co-occurrence patterns by confidence
- **New HTTP endpoints**:
  - `POST /v1/context-packet` — assemble a phase-aware Context Packet
  - `GET /v1/sdlc` — get current SDLC config
  - `PUT /v1/sdlc/phase` — update SDLC phase
  - `PUT /v1/sdlc/mode` — update quality mode
  - `POST /v1/decision` — log an agent decision (feeds learning loop)
  - `GET /v1/patterns` — list learned co-occurrence patterns

### Fixed
- `Reset()` now clears all 6 tables including `insight_cache` (previously omitted).
- Store pruning functions (`pruneOldData`) now log errors to stderr instead of silently
  discarding them. Violation cache is also pruned (7-day TTL).
- JSON unmarshal errors in `GetSummaryWithTags` and `GetRecentDecisions` are now
  logged to stderr.

---

## [0.2.0] — 2026-02-27

### Added
- **Context Packet** (`BuildContextPacket`): Phase-aware structured semantic document
  replacing raw graph nodes in LLM prompts. Delivers ~90% token savings vs raw nodes.
  Capped at ~800 tokens. 7 sections gated by phase matrix.
- **SDLC phase system**: 5 phases — `planning`, `development`, `testing`, `review`,
  `deployment`. Each phase controls which Context Packet sections are assembled.
  Stored per-project in `brain.sqlite` (`sdlc_config` table).
- **Quality modes**: `quick`, `standard`, `enterprise`. Drive the `QualityGate`
  checklist injected into every Context Packet.
- **`internal/sdlc/profiles.go`**: Static phase→section matrix (`SectionsForPhase`),
  quality gate profiles (`GateForMode`), and phase guidance strings (`PhaseGuidance`).
  Pure data package — no external imports.
- **`internal/sdlc/manager.go`**: `Manager` for getting/setting phase and mode in
  brain.sqlite. `ResolvePhase` and `ResolveMode` apply per-request overrides on top
  of stored project config.
- **`internal/contextbuilder/builder.go`**: Two-step `Builder.Build()` pipeline:
  1. Fast path (<5ms): SQLite lookups for root summary, dependency summaries,
     constraints with fix hints, team status, pattern hints, phase guidance.
  2. Optional LLM path (+2–3s): calls enricher for insight + concerns (only when
     `EnableLLM=true` and phase has `LLMInsight` section enabled).
- **`internal/contextbuilder/learner.go`**: `Learner.RecordDecision()` — pure Go
  co-occurrence counter, no LLM. Writes symmetric pairs (A→B and B→A) to
  `context_patterns` table. Confidence = `co_count / total_count`.
- **Co-occurrence learning tables** (`context_patterns`, `decision_log`): patterns
  are pruned after 14 days if co_count < 2; decision log pruned after 30 days.
- **New public types** in `pkg/brain/types.go`: `ContextPacket`, `SDLCPhase`,
  `QualityMode`, `ContextPacketRequest`, `DecisionRequest`, `ConstraintItem`,
  `AgentStatus`, `QualityGate`, `PatternHint`, `SDLCConfig`.
- **New `Brain` interface methods**: `BuildContextPacket`, `LogDecision`,
  `SetSDLCPhase`, `SetQualityMode`, `GetSDLCConfig`, `GetPatterns`.
- **New config fields** in `config.BrainConfig`: `ContextBuilder`, `LearningEnabled`,
  `DefaultPhase`, `DefaultMode`.
- **`NullBrain`** updated: all new methods return zero-value safe responses.

### Changed
- **Ingestor prompt upgraded** to produce `{"summary": "...", "tags": ["tag1", "tag2"]}`.
  Tags stored in `semantic_summaries.tags` column (JSON). Enables domain-based pattern
  matching. Existing rows default to `[]`.
- **`semantic_summaries` schema** gains `tags TEXT NOT NULL DEFAULT '[]'`.
- **`brain.IngestResponse`** gains `Tags []string`.
- **`brain.EnrichResponse`** gains `Summaries map[string]string` (bulk node ID→summary,
  loaded from SQLite, no LLM).

---

## [0.1.0] — 2026-02-27

### Added
- **`Brain` interface** (`pkg/brain`): public contract for all Brain capabilities.
  `NullBrain` is the fail-silent zero-value implementation.
- **Semantic Ingestor** (`internal/ingestor`): generates a 1-sentence summary for a
  code entity via Ollama. Stored in `semantic_summaries` table. Called on file-save.
- **Context Enricher** (`internal/enricher`): generates a 2-sentence architectural
  insight + concerns for an entity and its callee/caller neighbourhood. Called during
  `get_context`.
- **Rule Guardian** (`internal/guardian`): generates a plain-English explanation and
  fix suggestion for an architectural rule violation. Cached in `violation_cache` by
  `(rule_id, source_file)`. Called during `get_violations`.
- **Task Orchestrator** (`internal/orchestrator`): suggests work distribution when two
  agents conflict on the same scope. Falls back to a deterministic response without
  LLM if conflicts are simple.
- **LLM client layer** (`internal/llm`): `LLMClient` interface, Ollama HTTP client
  (`POST /api/generate`), and `MockLLMClient` for tests.
- **SQLite store** (`internal/store`): `semantic_summaries` and `violation_cache`
  tables. Pure-Go SQLite (`modernc.org/sqlite`), no CGO.
- **HTTP sidecar server** (`server`): REST API on `localhost:11435`. Endpoints:
  `GET /v1/health`, `GET /v1/summary/{nodeId}`, `POST /v1/ingest`,
  `POST /v1/enrich`, `POST /v1/explain-violation`, `POST /v1/coordinate`.
- **CLI binary** (`cmd/brain`): `brain serve|status|ingest|summaries|reset|version`.
- **`config.BrainConfig`**: JSON config file support with `BRAIN_CONFIG` env override.
  All features individually toggleable (`ingest`, `enrich`, `guardian`, `orchestrate`).
- Default model: `qwen2.5-coder:1.5b` (~900MB, works on 4GB RAM).

---

## [0.0.1] — 2026-02-27

Initial project scaffolding for synapses-intelligence.

- Go module `github.com/synapses/synapses-intelligence`
- Repository structure: `cmd/`, `config/`, `internal/`, `pkg/brain/`, `server/`
- `go.mod` with `modernc.org/sqlite` as the only non-stdlib dependency
- `Makefile` with `build`, `test`, `lint`, `tidy`, `serve`, `status`, `pull-model` targets
