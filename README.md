![Ory Lumen: Semantic code search for AI agents](.github/lumen-banner.png)

[![CI](https://github.com/ory/lumen/actions/workflows/ci.yml/badge.svg)](https://github.com/ory/lumen/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/ory/lumen)](https://goreportcard.com/report/github.com/ory/lumen)
[![Go Reference](https://pkg.go.dev/badge/github.com/ory/lumen.svg)](https://pkg.go.dev/github.com/ory/lumen)
[![Coverage Status](https://coveralls.io/repos/github/ory/lumen/badge.svg?branch=main)](https://coveralls.io/github/ory/lumen?branch=main)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

Claude reads entire files to find what it needs. Lumen gives it a map.

Lumen is a 100% local semantic code search engine for AI coding agents. No API
keys, no cloud, no external database, just open-source embedding models
([Ollama](https://ollama.com/) or [LM Studio](https://lmstudio.ai/)), SQLite,
and your CPU. A single static binary, no runtime required.

The payoff is measurable: **2.1–2.3× faster** task completion and **63–81%
cheaper** API costs, with reproducible [benchmarks](docs/BENCHMARKS.md) and
answer quality that wins every blind comparison.

| | With lumen | Baseline (no MCP) |
| ---------------------------- | --------------------------- | --------------------------- |
| Task completion | **2.1-2.3x faster** | baseline |
| API cost | **63-81% cheaper** | baseline |
| Answer quality (blind judge) | **5/5 wins** | 0/5 wins |

## Demo

<img src="docs/demo/demo.gif" alt="Lumen demo" width="600"/>

_Claude Code asking about the [Prometheus](https://github.com/prometheus/prometheus)
codebase. Lumen's `semantic_search` finds the relevant code without reading entire files._

## Quick Start

**Prerequisites:**

1. [Ollama](https://ollama.com/) installed and running, then pull the default
   embedding model:
   ```bash
   ollama pull ordis/jina-embeddings-v2-base-code
   ```
2. [Claude Code](https://code.claude.com/docs/en/quickstart) installed

**Install:**

```bash
/plugin marketplace add ory/lumen
/plugin install lumen@lumen
```

That's it. On first session start, Lumen:

1. Downloads the binary automatically from the
   [latest GitHub release](https://github.com/ory/lumen/releases)
2. Indexes your project in the background using Merkle tree change detection
3. Registers a `semantic_search` MCP tool that Claude uses automatically

Two skills are also available: `/lumen:doctor` (health check) and
`/lumen:reindex` (forced re-indexing).

## What You Get

- **Semantic vector search** — Claude finds relevant functions, types, and
  modules by meaning, not keyword matching
- **Auto-indexing** — indexes on session start, only re-processes changed files
  via Merkle tree diffing
- **Incremental updates** — re-indexes only what changed; large codebases
  re-index in seconds after the first run
- **12 language families** — Go, Python, TypeScript, JavaScript, Rust, Ruby,
  Java, PHP, C/C++, Markdown, YAML, JSON
- **Zero cloud** — embeddings stay on your machine; no data leaves your network
- **Ollama and LM Studio** — works with either local embedding backend

## How It Works

Lumen sits between your codebase and Claude as an MCP server. When a session
starts, it walks your project and builds a **Merkle tree** over file hashes:
only changed files get re-chunked and re-embedded. Each file is split into
semantic chunks (functions, types, methods) using Go's native AST or
tree-sitter grammars for other languages. Chunks are embedded and stored in
**SQLite + sqlite-vec** using cosine-distance KNN for retrieval.

```
Files → semantic chunks → vector embeddings → SQLite/sqlite-vec → KNN search
```

When Claude needs to understand code, it calls `semantic_search` instead of
reading entire files. The index is stored outside your repo
(`~/.local/share/lumen/<hash>/index.db`), keyed by project path and model name
— different models never share an index.

## Benchmarks

Key results (Ollama, `jina-embeddings-v2-base-code`, Golang fixture):

| Model | Speedup | Cost Savings | Quality |
| ---------- | --------------- | --------------- | ------------ |
| Sonnet 4.6 | **2.2x faster** | **63% cheaper** | 5/5 MCP wins |
| Opus 4.6 | **2.1x faster** | **81% cheaper** | 5/5 MCP wins |

Results hold across LM Studio (nomic-embed-code) and across Go, Python, and
TypeScript in extended multi-model benchmarks.

See [docs/BENCHMARKS.md](docs/BENCHMARKS.md) for full speed/cost tables, answer
quality breakdowns, per-language results across 4 embedding models, and
reproduce instructions.

## Supported Languages

Supports **12 language families** with semantic chunking:

| Language | Parser | Extensions | Status |
| ---------------- | ----------- | ----------------------------------------- |-------------------------------------|
| Go | Native AST | `.go` | Optimized: 3.8x faster, 90% cheaper |
| Python | tree-sitter | `.py` | Tested: 1.8x faster, 72% cheaper |
| TypeScript / TSX | tree-sitter | `.ts`, `.tsx` | Tested: 1.4x faster, 48% cheaper |
| JavaScript / JSX | tree-sitter | `.js`, `.jsx`, `.mjs` | Supported |
| Rust | tree-sitter | `.rs` | Supported |
| Ruby | tree-sitter | `.rb` | Supported |
| Java | tree-sitter | `.java` | Supported |
| PHP | tree-sitter | `.php` | Supported |
| C / C++ | tree-sitter | `.c`, `.h`, `.cpp`, `.cc`, `.cxx`, `.hpp` | Supported |
| Markdown / MDX | tree-sitter | `.md`, `.mdx` | Supported |
| YAML | tree-sitter | `.yaml`, `.yml` | Supported |
| JSON | tree-sitter | `.json` | Supported |

Go uses the native Go AST parser for the most precise chunks. All other
languages use tree-sitter grammars.

_Note: Golang is the best-supported language. Other languages work via
tree-sitter but may benefit from improved chunking strategies._

## Configuration

All configuration is via environment variables:

| Variable | Default | Description |
| ------------------------- | -------------------- | ------------------------------------------ |
| `LUMEN_EMBED_MODEL` | see note ¹ | Embedding model (must be in registry) |
| `LUMEN_BACKEND` | `ollama` | Embedding backend (`ollama` or `lmstudio`) |
| `OLLAMA_HOST` | `localhost:11434` | Ollama server URL |
| `LM_STUDIO_HOST` | `localhost:1234` | LM Studio server URL |
| `LUMEN_MAX_CHUNK_TOKENS` | `512` | Max tokens per chunk before splitting |

¹ `ordis/jina-embeddings-v2-base-code` (Ollama), `nomic-ai/nomic-embed-code-GGUF` (LM Studio)

### Supported Embedding Models

Dimensions and context length are configured automatically per model:

| Model | Backend | Dims | Context | Recommended |
| ------------------------------------ | --------- | ---- | ------- |-----------------------------------------------------------------------|
| `ordis/jina-embeddings-v2-base-code` | Ollama | 768 | 8192 | **Best default** — lowest cost, no over-retrieval |
| `qwen3-embedding:8b` | Ollama | 4096 | 40960 | **Best quality** — strongest dominance (7/9 wins), very slow indexing |
| `nomic-ai/nomic-embed-code-GGUF` | LM Studio | 3584 | 8192 | **Usable** — good quality, but TypeScript over-retrieval raises costs |
| `qwen3-embedding:4b` | Ollama | 2560 | 40960 | **Not recommended** — highest costs, severe TypeScript over-retrieval |
| `nomic-embed-text` | Ollama | 768 | 8192 | Untested |
| `qwen3-embedding:0.6b` | Ollama | 1024 | 32768 | Untested |
| `all-minilm` | Ollama | 384 | 512 | Untested |

Switching models creates a separate index automatically. The model name is part
of the database path hash, so different models never collide.

## Database Location

Index databases are stored outside your project:

```
~/.local/share/lumen/<hash>/index.db
```

Where `<hash>` is derived from the absolute project path and embedding model
name. No files are added to your repo, no `.gitignore` modifications needed.

You can safely delete the entire `lumen` directory to clear all indexes,
or use `lumen purge` to do it automatically.

## CLI Reference

Download the binary from the
[GitHub releases page](https://github.com/ory/lumen/releases) or let the plugin
install it automatically.

```bash
lumen help
```

## Troubleshooting

**Ollama not running / "connection refused"**

Start Ollama and verify the model is pulled:

```bash
ollama serve
ollama pull ordis/jina-embeddings-v2-base-code
```

Run `/lumen:doctor` inside Claude Code to confirm connectivity.

**Stale index after large refactor**

Run `/lumen:reindex` inside Claude Code to force a full re-index, or:

```bash
lumen purge && lumen index .
```

**Switching embedding models**

Set `LUMEN_EMBED_MODEL` to a model from the supported table above. Each model
gets its own database; the old index is not deleted automatically.

**Slow first indexing**

The first run embeds every file. Subsequent runs only process changed files
(typically a few seconds). For large projects (100k+ lines), first indexing can
take several minutes — this is a one-time cost.

## Development

```bash
git clone https://github.com/ory/lumen.git
cd lumen

# Build (CGO required for sqlite-vec)
make build

# Run tests
make test

# Run linter
make lint

# Load as a Claude Code plugin from source
make plugin-dev
```

See [CLAUDE.md](CLAUDE.md) for architecture details, design decisions, and
contribution guidelines.
