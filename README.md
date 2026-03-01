# agent-index

[![CI](https://github.com/aeneasr/agent-index-go/actions/workflows/ci.yml/badge.svg)](https://github.com/aeneasr/agent-index-go/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

A fully local semantic code search engine, exposed as an
[MCP](https://modelcontextprotocol.io/) server. It parses your codebase into
semantic chunks (functions, methods, types, interfaces, constants), embeds them
via a local Ollama model, and exposes search over MCP. Your code never leaves
your machine.

**For AI agents:** Semantic search is the fastest way to navigate a large
codebase. Instead of reading whole files, the agent describes what it needs and
gets back exact file paths and line ranges. In [benchmarks](#benchmarks) against
Prometheus/TSDB source code across 5 questions of increasing difficulty,
`semantic_search` completed tasks **2.1–2.2× faster** and **63–81% cheaper**
than the default file-read tools — and won 4 out of 5 blind quality comparisons.
Baseline (no MCP) won zero.

**For privacy:** Everything runs locally — no API keys, no code sent to external
services, no cloud dependency.

| | With `semantic_search` | Default tools (baseline) |
|---|---|---|
| Task completion | **2.1–2.2× faster** | baseline |
| API cost | **63–81% cheaper** | baseline |
| Answer quality (blind judge) | **4/5 wins** | 0/5 wins |

Supports **12 language families** with semantic chunking:

| Language         | Extensions                                | Chunking strategy                                                   |
| ---------------- | ----------------------------------------- | ------------------------------------------------------------------- |
| Go               | `.go`                                     | Native Go AST — functions, methods, types, interfaces, consts, vars |
| TypeScript / TSX | `.ts`, `.tsx`                             | tree-sitter — functions, classes, interfaces, type aliases, methods |
| JavaScript / JSX | `.js`, `.jsx`, `.mjs`                     | tree-sitter — functions, classes, methods, generators               |
| Python           | `.py`                                     | tree-sitter — function definitions, class definitions               |
| Rust             | `.rs`                                     | tree-sitter — functions, structs, enums, traits, impls, consts      |
| Ruby             | `.rb`                                     | tree-sitter — methods, singleton methods, classes, modules          |
| Java             | `.java`                                   | tree-sitter — methods, classes, interfaces, constructors, enums     |
| PHP              | `.php`                                    | tree-sitter — functions, classes, interfaces, traits, methods       |
| C / C++          | `.c`, `.h`, `.cpp`, `.cc`, `.cxx`, `.hpp` | tree-sitter — function definitions, structs, enums, classes         |
| Markdown / MDX   | `.md`, `.mdx`                             | Heading-based — each `#` / `##` / `###` section is one chunk        |
| YAML             | `.yaml`, `.yml`                           | Key-based — each top-level key and its value block is one chunk     |
| JSON             | `.json`                                   | Key-based — each top-level key and its value block is one chunk     |

## Why

AI coding agents are good at writing code but bad at navigating large codebases.
They waste context window tokens reading entire files when they only need one
function. Semantic search fixes this — the agent describes what it's looking for
in natural language and gets back precise file paths and line ranges.

Cloud-hosted vector databases solve this, but they require sending your code to
a third party. agent-index gives you the same capability with everything running
locally:

- **Local embeddings** via Ollama (no API keys, no network calls to external
  services)
- **Local storage** via SQLite + sqlite-vec (no external database)
- **Incremental indexing** via Merkle tree change detection (only re-embeds
  changed files)
- **Auto-indexing** on search (no manual reindex step)

## Install

**Prerequisites:**

1. [Ollama](https://ollama.com/) installed and running
2. [Go](https://go.dev/) 1.26+

```bash
# Pull the default embedding model
ollama pull ordis/jina-embeddings-v2-base-code

# Install the binary
CGO_ENABLED=1 go install github.com/aeneasr/agent-index@latest
```

> `CGO_ENABLED=1` is required — sqlite-vec compiles from C source.

## Setup with Claude Code

### Default: Ollama + ordis/jina-embeddings-v2-base-code

```bash
# Pull the default embedding model
ollama pull ordis/jina-embeddings-v2-base-code

# Add as an MCP server (defaults work out of the box)
claude mcp add --scope user \
  agent-index "$(go env GOPATH)/bin/agent-index" -- stdio
```

That's it. Claude Code will now have access to `semantic_search` and
`index_status` tools. On the first search against a project, it auto-indexes the
codebase.

### Alternative: LM Studio + nomic-embed-code (higher quality, code-optimized)

[LM Studio](https://lmstudio.ai/) exposes an OpenAI-compatible `/v1/embeddings`
endpoint at `http://localhost:1234` by default. `nomic-embed-code` is a
code-optimized model with 3584 dimensions.

:::warn

`nomic-ai/nomic-embed-code-GGUF` is significantly more resource intense than the
default Ollama model. Expect higher CPU usage and longer indexing times,
especially on large codebases. Consider using `agent-index index path/to/source`
to pre-index your codebase.

:::

```bash
# Download and load the model via lms CLI
lms get nomic-ai/nomic-embed-code-GGUF
lms load nomic-ai/nomic-embed-code-GGUF

# Add as MCP server using the lmstudio backend
claude mcp add --scope user \
  -eAGENT_INDEX_BACKEND=lmstudio \
  -eAGENT_INDEX_EMBED_MODEL=nomic-ai/nomic-embed-code-GGUF \
  agent-index "$(go env GOPATH)/bin/agent-index" -- stdio
```

### Switching models (Ollama)

To use a different Ollama model, set `AGENT_INDEX_EMBED_MODEL` — dims and
context are looked up automatically:

```bash
claude mcp remove --scope user agent-index
claude mcp add --scope user \
  -eAGENT_INDEX_EMBED_MODEL=nomic-embed-text \
  agent-index "$(go env GOPATH)/bin/agent-index" -- stdio
```

## CLI

The `agent-index index` command lets you pre-index a project from the terminal. This is useful for large codebases where you want indexing to happen in the background before the first MCP search.

```bash
agent-index index <project-path>
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--model` | `-m` | `$AGENT_INDEX_EMBED_MODEL` or backend default | Embedding model to use |
| `--force` | `-f` | false | Force full re-index (skip freshness check) |

**Examples:**

```bash
# Index using the default model
agent-index index ~/workspace/myproject

# Force a full re-index
agent-index index --force ~/workspace/myproject

# Use a specific model
agent-index index -m nomic-embed-text ~/workspace/myproject
```

Progress is printed to stderr. When done, the command outputs:

```
Done. Indexed 42 files, 318 chunks in 4.231s.
```

If the index is already up to date and `--force` is not set:

```
Index is already up to date.
```

> `agent-index stdio` starts the MCP server on stdin/stdout. This is invoked automatically by Claude Code — you don't need to run it manually.

## MCP Tools

### `semantic_search`

Search indexed code using natural language. Auto-indexes if the index is stale.

| Parameter       | Type    | Required | Description                                                                   |
| --------------- | ------- | -------- | ----------------------------------------------------------------------------- |
| `query`         | string  | yes      | Natural language search query                                                 |
| `path`          | string  | yes      | Absolute path to the project root                                             |
| `limit`         | integer | no       | Max results (default: 50)                                                     |
| `min_score`     | float   | no       | Minimum score threshold (-1 to 1). Default 0.5. Use -1 to return all results. |
| `force_reindex` | boolean | no       | Force full re-index before searching                                          |

Returns file paths, symbol names, line ranges, and similarity scores (0–1).

### `index_status`

Check indexing status without triggering a reindex.

| Parameter | Type   | Required | Description                       |
| --------- | ------ | -------- | --------------------------------- |
| `path`    | string | yes      | Absolute path to the project root |

## Configuration

All configuration is via environment variables:

| Variable                       | Default                                                                                      | Description                                |
| ------------------------------ | -------------------------------------------------------------------------------------------- | ------------------------------------------ |
| `AGENT_INDEX_EMBED_MODEL`      | `ordis/jina-embeddings-v2-base-code` (Ollama) / `nomic-ai/nomic-embed-code-GGUF` (LM Studio) | Embedding model (must be in registry)      |
| `AGENT_INDEX_BACKEND`          | `ollama`                                                                                     | Embedding backend (`ollama` or `lmstudio`) |
| `OLLAMA_HOST`                  | `http://localhost:11434`                                                                     | Ollama server URL                          |
| `LM_STUDIO_HOST`               | `http://localhost:1234`                                                                      | LM Studio server URL                       |
| `AGENT_INDEX_MAX_CHUNK_TOKENS` | `512`                                                                                        | Max tokens per chunk before splitting      |

### Supported embedding models

Dimensions and context length are configured automatically per model:

| Model                                | Backend   | Dims | Context | Size   | Notes                           |
| ------------------------------------ | --------- | ---- | ------- | ------ |---------------------------------|
| `ordis/jina-embeddings-v2-base-code` | Ollama    | 768  | 8192    | ~323MB | Default. Code-optimized, Fast   |
| `nomic-embed-text`                   | Ollama    | 768  | 8192    | ~274MB | Fast, good general quality      |
| `nomic-ai/nomic-embed-code-GGUF`     | LM Studio | 3584 | 8192    | ~274MB | Code-optimized, high-dim, slow  |
| `qwen3-embedding:8b`                 | Ollama    | 4096 | 40960   | ~4.7GB | Highest quality, extremely slow |
| `qwen3-embedding:4b`                 | Ollama    | 2560 | 40960   | ~2.6GB | High quality                    |
| `qwen3-embedding:0.6b`               | Ollama    | 1024 | 32768   | ~522MB | Lightweight                     |
| `all-minilm`                         | Ollama    | 384  | 512     | ~33MB  | Tiny, CI usem fast              |

Switching models creates a separate index automatically — the model name is part
of the database path hash, so different models never collide.

## Supported Languages

| Language         | Parser          | Status            |
| ---------------- | --------------- |-------------------|
| Go               | Native `go/ast` | Thoroughly tested |
| TypeScript / TSX | tree-sitter     | Supported         |
| JavaScript / JSX | tree-sitter     | Supported         |
| Python           | tree-sitter     | Supported         |
| Rust             | tree-sitter     | Supported         |
| Ruby             | tree-sitter     | Supported         |
| Java             | tree-sitter     | Supported         |
| C                | tree-sitter     | Supported         |
| C++              | tree-sitter     | Supported         |

Go uses the native Go AST parser, which produces the most precise chunks and has
comprehensive test coverage. All other languages use tree-sitter grammars — they
work but have less test coverage and may miss some language-specific constructs.

## How It Works

1. **Change detection**: SHA-256 Merkle tree identifies added/modified/removed
   files. If nothing changed, search hits the existing index directly.
2. **AST chunking**: Changed files are parsed into semantic chunks. Go files use
   the native `go/ast` parser; other languages use tree-sitter grammars. Each
   function, method, type, interface, and const/var declaration becomes a chunk,
   including its doc comment.
3. **Embedding**: Chunks are batched (32 at a time) and sent to Ollama for
   embedding.
4. **Storage**: Vectors and metadata go into SQLite via sqlite-vec with cosine
   distance. Database lives in `$XDG_DATA_HOME/agent-index/` — your project
   directory stays clean.
5. **Search**: Query is embedded with the same model, KNN search returns the
   closest matches.

## Storage

Index databases are stored outside your project:

```
~/.local/share/agent-index/<hash>/index.db
```

Where `<hash>` is derived from the absolute project path and embedding model
name. No files are added to your repo, no `.gitignore` modifications needed.

You can safely delete the entire `agent-index` directory to clear all indexes, or
delete specific subdirectories to clear indexes for specific projects/models.

## It's A Game Changer: Benchmarks

`bench-mcp.sh` runs 5 questions of increasing difficulty against
[Prometheus/TSDB Go fixtures](testdata/fixtures/go), across 2 models (Sonnet
4.6, Opus 4.6) and 3 scenarios:

- **baseline** — default tools only (grep, file reads), no MCP
- **mcp-only** — `semantic_search` only, no file reads
- **mcp-full** — all tools + `semantic_search`

Answers are ranked blind by an LLM judge (Opus 4.6).

### Speed & cost

Totals across all 5 questions × 2 models:

| Model      | Scenario | Total Time               | Total Cost              |
| ---------- | -------- | ------------------------ | ----------------------- |
| Sonnet 4.6 | baseline | 496.8s                   | $5.97                   |
| Sonnet 4.6 | mcp-only | 228.9s (**2.2× faster**) | $2.20 (**63% cheaper**) |
| Opus 4.6   | baseline | 478.0s                   | $9.66                   |
| Opus 4.6   | mcp-only | 229.9s (**2.1× faster**) | $1.79 (**81% cheaper**) |

### Answer quality

Baseline never wins. `mcp-only` wins all medium/hard/very-hard questions at a
fraction of the cost.

| Question        | Difficulty | Winner          | Judge summary                                                                                                                           |
| --------------- | ---------- | --------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| label-matcher   | easy       | opus / mcp-full | Correct, complete; full type definitions and constructor source with accurate line references                                           |
| histogram       | medium     | opus / mcp-only | Good coverage of both bucket systems (classic + native), hot/cold swap, and iteration; 7–20× cheaper than baseline                      |
| tsdb-compaction | hard       | opus / mcp-only | Uniquely covers all three trigger paths, compactor initialization, and planning strategies; 5–6× cheaper than baseline                  |
| promql-engine   | very-hard  | opus / mcp-only | Thorough coverage of all four topics (engine, functions, AST, rules) with accurate file:line references; half the cost of opus/baseline |
| scrape-pipeline | very-hard  | opus / mcp-only | Best Registry coverage; unique dual data-flow summary for scraping and exposition paths                                                 |

`mcp-only` wins 4/5, `mcp-full` wins 1/5, `baseline` wins 0/5.

### Reproduce

Requires Ollama, the `claude` CLI, `jq`, and `bc`.

```bash
./bench-mcp.sh                                          # all questions, all models
./bench-mcp.sh --model sonnet                           # filter by model
./bench-mcp.sh --question tsdb-compaction               # filter by question
./bench-mcp.sh --model opus --question label-matcher    # combine
```

Results land in `bench-results/<timestamp>/`. The script runs an LLM judge at
the end to rank answers.

## Building from source

```bash
CGO_ENABLED=1 go build -o agent-index .
```

## Formatting

JSON and Markdown files are formatted with [Prettier](https://prettier.io/):

```bash
npx prettier --write "**/*.{json,md}"
```
