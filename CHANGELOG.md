# Changelog

All notable changes to synapses-intelligence are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

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
