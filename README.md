# agent-index

A fully local semantic code search engine, exposed as an [MCP](https://modelcontextprotocol.io/) server. Think of it as a self-hosted alternative to cloud-based code vector databases — but everything runs on your machine, embeddings included.

It parses your Go codebase into semantic chunks (functions, methods, types, interfaces, constants) using the Go AST, embeds them via a local Ollama model, stores vectors in SQLite with [sqlite-vec](https://github.com/asg017/sqlite-vec), and exposes semantic search over MCP. Your code never leaves your machine.

Currently supports **Go** — more languages may follow if there's interest.

## Why

AI coding agents are good at writing code but bad at navigating large codebases. They waste context window tokens reading entire files when they only need one function. Semantic search fixes this — the agent describes what it's looking for in natural language and gets back precise file paths and line ranges.

Cloud-hosted vector databases solve this, but they require sending your code to a third party. agent-index-go gives you the same capability with everything running locally:

- **Local embeddings** via Ollama (no API keys, no network calls to external services)
- **Local storage** via SQLite + sqlite-vec (no external database)
- **Incremental indexing** via Merkle tree change detection (only re-embeds changed files)
- **Auto-indexing** on search (no manual reindex step)

## Install

**Prerequisites:**

1. [Ollama](https://ollama.com/) installed and running
2. [Go](https://go.dev/) 1.26+

```bash
# Pull an embedding model
ollama pull mxbai-embed-large

# Install the binary
CGO_ENABLED=1 go install github.com/foobar/agent-index-go@latest
```

> `CGO_ENABLED=1` is required — sqlite-vec compiles from C source.

## Setup with Claude Code

```bash
# Pull the embedding model
ollama pull mxbai-embed-large

# Add as an MCP server
claude mcp add --scope user \
  -e AGENT_INDEX_EMBED_MODEL=mxbai-embed-large \
  -e OLLAMA_HOST=http://localhost:11434 \
  agent-index "$HOME/go/bin/agent-index-go"
```

That's it. Claude Code will now have access to `semantic_search` and `index_status` tools. On the first search against a project, it auto-indexes the codebase.

## MCP Tools

### `semantic_search`

Search indexed code using natural language. Auto-indexes if the index is stale.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `query` | string | yes | Natural language search query |
| `path` | string | yes | Absolute path to the project root |
| `limit` | integer | no | Max results (default: 10) |
| `kind` | string | no | Filter: `function`, `method`, `type`, `interface`, `const`, `var` |
| `force_reindex` | boolean | no | Force full re-index before searching |

Returns file paths, symbol names, line ranges, and similarity scores (0–1).

### `index_status`

Check indexing status without triggering a reindex.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `path` | string | yes | Absolute path to the project root |

## Configuration

All configuration is via environment variables:

| Variable | Default | Description |
|---|---|---|
| `AGENT_INDEX_EMBED_MODEL` | `nomic-embed-text` | Ollama embedding model name |
| `OLLAMA_HOST` | `http://localhost:11434` | Ollama server URL |

### Choosing an embedding model

Any Ollama embedding model works. Some options:

| Model | Dimensions | Notes |
|---|---|---|
| `mxbai-embed-large` | 1024 | Good balance of quality and speed |
| `nomic-embed-text` | 768 | Lightweight, fast |
| `snowflake-arctic-embed2` | 1024 | High quality |

Switching models creates a separate index automatically — the model name is part of the database path hash, so different models never collide.

## How It Works

```
  .go files
      │
      ▼
┌─────────────┐     ┌──────────────┐     ┌─────────────────┐
│  Merkle Tree │────▶│  Go AST      │────▶│  Ollama         │
│  (diff only) │     │  Chunker     │     │  Embeddings     │
└─────────────┘     └──────────────┘     └────────┬────────┘
                                                   │
                                                   ▼
                                          ┌─────────────────┐
                                    ◀─────│  SQLite +        │
                              search      │  sqlite-vec      │
                                          └─────────────────┘
```

1. **Change detection**: SHA-256 Merkle tree identifies added/modified/removed files. If nothing changed, search hits the existing index directly.
2. **AST chunking**: Changed files are parsed with `go/ast`. Each function, method, type, interface, and const/var declaration becomes a chunk, including its doc comment.
3. **Embedding**: Chunks are batched (32 at a time) and sent to Ollama for embedding.
4. **Storage**: Vectors and metadata go into SQLite via sqlite-vec with cosine distance. Database lives in `$XDG_DATA_HOME/agent-index/` — your project directory stays clean.
5. **Search**: Query is embedded with the same model, KNN search returns the closest matches.

## Storage

Index databases are stored outside your project:

```
~/.local/share/agent-index/<hash>/index.db
```

Where `<hash>` is derived from the absolute project path and embedding model name. No files are added to your repo, no `.gitignore` modifications needed.

## Building from source

```bash
CGO_ENABLED=1 go build -o agent-index-go .
```

## License

MIT
