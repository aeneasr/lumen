# Lumen: semantic search for code agents

[![CI](https://github.com/ory/lumen/actions/workflows/ci.yml/badge.svg)](https://github.com/ory/lumen/actions/workflows/ci.yml)
[![Coverage Status](https://coveralls.io/repos/github/ory/lumen/badge.svg?branch=main)](https://coveralls.io/github/ory/lumen?branch=main)
[![Go Report Card](https://goreportcard.com/badge/github.com/ory/lumen)](https://goreportcard.com/report/github.com/ory/lumen)
[![Go Reference](https://pkg.go.dev/badge/github.com/ory/lumen.svg)](https://pkg.go.dev/github.com/ory/lumen)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

A 100% local semantic code search engine for AI coding agents. No API keys, no
cloud, no external database. Just open-source embedding models (Ollama or LM
Studio), SQLite + sqlite-vec, and your CPU. Works on any developer machine
because of Golang.

Lumen lights up complex code bases and makes Claude Code **2.1-2.3x faster** and
**63-81% cheaper**, with reproducible [benchmarks](docs/BENCHMARKS.md) while
**always** retaining or exceeding answer quality over the baseline.

|                              | With Ory Lumen      | Baseline (no MCP) |
| ---------------------------- | ------------------- | ----------------- |
| Task completion              | **2.1-2.3x faster** | baseline          |
| API cost                     | **63-81% cheaper**  | baseline          |
| Answer quality (blind judge) | **5/5 wins**        | 0/5 wins          |

## Install

**Prerequisites:**

1. [Ollama](https://ollama.com/) or [LM Studio](https://lmstudio.ai/download)
   installed and running
2. Pull the default embedding model:
   `ollama pull ordis/jina-embeddings-v2-base-code`
3. Have [Claude Code](https://code.claude.com/docs/en/quickstart) installed

Then:

```bash
/plugin marketplace add mksglu/claude-context-mode
/plugin install context-mode@claude-context-mode
```

The binary is downloaded automatically from the
[latest GitHub release](https://github.com/ory/lumen/releases) on first use.
Then **skills** `/lumen:doctor` (health check) and `/lumen:reindex` (forced
re-indexing) are available plus semantic search.

## Table of contents

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Supported languages](#supported-languages)
- [CLI](#cli)
- [Configuration](#configuration)
  - [Supported embedding models](#supported-embedding-models)
- [Database location](#database-location)
- [Benchmarks](#benchmarks)
- [Development](#development)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

## Supported languages

Supports **12 language families** with semantic chunking:

| Language         | Parser      | Extensions                                | Status                              |
| ---------------- | ----------- | ----------------------------------------- | ----------------------------------- |
| Go               | Native AST  | `.go`                                     | Optimized: 3.8x faster, 90% cheaper |
| Python           | tree-sitter | `.py`                                     | Tested: 1.8x faster, 72% cheaper    |
| TypeScript / TSX | tree-sitter | `.ts`, `.tsx`                             | Tested: 1.4x faster, 48% cheaper    |
| JavaScript / JSX | tree-sitter | `.js`, `.jsx`, `.mjs`                     | Supported                           |
| Rust             | tree-sitter | `.rs`                                     | Supported                           |
| Ruby             | tree-sitter | `.rb`                                     | Supported                           |
| Java             | tree-sitter | `.java`                                   | Supported                           |
| PHP              | tree-sitter | `.php`                                    | Supported                           |
| C / C++          | tree-sitter | `.c`, `.h`, `.cpp`, `.cc`, `.cxx`, `.hpp` | Supported                           |
| Markdown / MDX   | tree-sitter | `.md`, `.mdx`                             | Supported                           |
| YAML             | tree-sitter | `.yaml`, `.yml`                           | Supported                           |
| JSON             | tree-sitter | `.json`                                   | Supported                           |

Go uses the native Go AST parser for the most precise chunks. All other
languages use tree-sitter grammars.

_Note: Golang is the best-supported language. Other languages work via
tree-sitter but may benefit from improved chunking strategies and require work
to improve chunking algorithms._

## CLI

The CLI, which you can download from the
[GitHub releases page](https://github.com/ory/lumen/releases), provides
additional functionality for managing indexes and configuration.

Then, check the help:

```bash
lumen help
```

## Configuration

All configuration is via environment variables:

| Variable                 | Default           | Description                                |
| ------------------------ | ----------------- | ------------------------------------------ |
| `LUMEN_EMBED_MODEL`      | see note ¹        | Embedding model (must be in registry)      |
| `LUMEN_BACKEND`          | `ollama`          | Embedding backend (`ollama` or `lmstudio`) |
| `OLLAMA_HOST`            | `localhost:11434` | Ollama server URL                          |
| `LM_STUDIO_HOST`         | `localhost:1234`  | LM Studio server URL                       |
| `LUMEN_MAX_CHUNK_TOKENS` | `512`             | Max tokens per chunk before splitting      |

¹ `ordis/jina-embeddings-v2-base-code` (Ollama),
`nomic-ai/nomic-embed-code-GGUF` (LM Studio)

### Supported embedding models

Dimensions and context length are configured automatically per model:

| Model                                | Backend   | Dims | Context | Recommended                                                           |
| ------------------------------------ | --------- | ---- | ------- | --------------------------------------------------------------------- |
| `ordis/jina-embeddings-v2-base-code` | Ollama    | 768  | 8192    | **Best default** — lowest cost, no over-retrieval                     |
| `qwen3-embedding:8b`                 | Ollama    | 4096 | 40960   | **Best quality** — strongest dominance (7/9 wins), very slow indexing |
| `nomic-ai/nomic-embed-code-GGUF`     | LM Studio | 3584 | 8192    | **Usable** — good quality, but TypeScript over-retrieval raises costs |
| `qwen3-embedding:4b`                 | Ollama    | 2560 | 40960   | **Not recommended** — highest costs, severe TypeScript over-retrieval |
| `nomic-embed-text`                   | Ollama    | 768  | 8192    | Untested                                                              |
| `qwen3-embedding:0.6b`               | Ollama    | 1024 | 32768   | Untested                                                              |
| `all-minilm`                         | Ollama    | 384  | 512     | Untested                                                              |

Switching models creates a separate index automatically. The model name is part
of the database path hash, so different models never collide.

## Database location

Index databases are stored outside your project:

```
~/.local/share/lumen/<dir-hash>/index.db
```

Where `<hash>` is derived from the absolute project path and embedding model
name. No files are added to your repo, no `.gitignore` modifications needed.

You can safely delete the entire `lumen` directory to clear all indexes, or use
`lumen purge` to do it automatically.

## Benchmarks

Using Lumen is a clear win in speed, cost, and answer quality across both
embedding backends. The semantic search tool lets the agent find relevant code
at a fraction of the cost, significantly faster, and produces better answers
that win blind comparisons.

Key results (Ollama, jina-embeddings-v2-base-code):

| Model      | Speedup         | Cost Savings    | Quality      |
| ---------- | --------------- | --------------- | ------------ |
| Sonnet 4.6 | **2.2x faster** | **63% cheaper** | 5/5 MCP wins |
| Opus 4.6   | **2.1x faster** | **81% cheaper** | 5/5 MCP wins |

Results hold across LM Studio (nomic-embed-code) and across Go, Python, and
TypeScript in extended multi-model benchmarks.

See [docs/BENCHMARKS.md](docs/BENCHMARKS.md) for full speed/cost tables, answer
quality breakdowns, per-language results across 4 embedding models, and
reproduce instructions.

## Development

```bash
git clone https://github.com/ory/lumen.git
cd lumen
make build
claude --plugin-dir "$(pwd)"
```
