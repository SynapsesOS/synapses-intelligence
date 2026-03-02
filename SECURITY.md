# Security Policy

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

Use [GitHub Security Advisories](https://github.com/SynapsesOS/synapses-intelligence/security/advisories/new)
to report vulnerabilities privately. You can expect a response within 72 hours.

Please include:
- Description of the vulnerability and its potential impact
- Steps to reproduce
- Affected versions
- Any suggested mitigations (if known)

## Security model

synapses-intelligence is a **local-only service** — no data leaves your machine.

- Binds to `localhost:11435` by default — not exposed to the network
- All LLM inference runs via a local [Ollama](https://ollama.com) instance
- No API keys, no cloud endpoints, no telemetry
- SQLite database (`brain.sqlite`) stored at `~/.synapses/` — local filesystem only
- Config file (`brain.json`) stored at `~/.synapses/brain.json` — never committed to git

## Known limitations

- The HTTP server has no authentication by default. If you expose `brain serve` beyond
  localhost (not recommended), add a reverse proxy with auth.
- `brain.json` stores Ollama connection details in plaintext — keep it outside your repo.
