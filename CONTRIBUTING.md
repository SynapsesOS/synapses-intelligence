# Contributing to synapses-intelligence

Thank you for your interest in contributing!

## Prerequisites

- Go 1.22+
- [Ollama](https://ollama.com) (for running integration tests)
- A local model pulled: `ollama pull qwen2.5-coder:1.5b`

## Development setup

```bash
git clone https://github.com/Divish1032/synapses-intelligence.git
cd synapses-intelligence
go mod tidy
go build ./...
go test ./...
```

Build and run locally:
```bash
make build        # outputs bin/brain
brain serve       # starts on :11435
```

## Project structure

```
cmd/brain/              CLI entry point (serve, setup, config, status)
config/                 Config loader + DefaultConfigPath()
internal/
  contextbuilder/       Builds Context Packets from graph + LLM
  enricher/             Enriches node observations with LLM summaries
  guardian/             Enforces architectural rules; suggests fixes
  ingestor/             Ingests structural snapshots from synapses
  llm/                  Ollama HTTP client + LLMClient interface
  orchestrator/         Coordinates multi-agent context requests
  sdlc/                 SDLC phase management
  store/                SQLite persistence (brain.sqlite)
pkg/brain/              Public Brain interface + NullBrain stub
server/                 HTTP server exposing /v1/* endpoints
```

## Key invariants

- **No API keys, no cloud.** All LLM calls go to local Ollama.
- **NullBrain safety.** `pkg/brain/null.go` must compile and return safe zero values for all methods — used when brain is not configured.
- **LLMClient interface.** All LLM calls go through `internal/llm.LLMClient`. Use `MockClient` in tests — never call Ollama in unit tests.
- **Context Packets are capped at ~800 tokens.** The enricher must respect `MaxTokens` in the packet request.

## Adding a new brain capability

1. Add the method to the `Brain` interface in `pkg/brain/brain.go`
2. Implement it on `impl` in the same file
3. Add a no-op stub to `pkg/brain/null.go`
4. Add the HTTP endpoint in `server/server.go`
5. Write tests using `MockClient`

## Code style

- `gofmt` + `goimports` (run `make fmt` or your editor)
- Errors are lowercase: `fmt.Errorf("failed to build packet: %w", err)`
- No global state outside `cmd/`
- All exported types and functions must have doc comments

## Reporting issues

Open an issue at https://github.com/Divish1032/synapses-intelligence/issues.
For security vulnerabilities, see [SECURITY.md](SECURITY.md).
