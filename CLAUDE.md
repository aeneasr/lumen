# CLAUDE.md — agent-index

## Vision

**Give AI coding agents precise, local semantic code search.**

AI agents waste context window tokens reading entire files when they only need
one function. `agent-index` fixes this: it parses a Go codebase into semantic
chunks (functions, methods, types, interfaces, consts), embeds them via a local
Ollama model, stores vectors in SQLite, and exposes search over MCP. The agent
describes what it needs in natural language and gets back exact file paths and
line ranges.

Everything runs locally — no API keys, no cloud, no code leaves the machine.

## Architecture

```
.go files
    │
    ▼
┌──────────────┐     ┌──────────────┐     ┌─────────────────┐
│  Merkle Tree  │────▶│  Go AST      │────▶│  Ollama         │
│  (diff only)  │     │  Chunker     │     │  Embeddings     │
└──────────────┘     └──────────────┘     └────────┬────────┘
                                                    │
                                                    ▼
                                           ┌─────────────────┐
                                     ◀─────│  SQLite +        │
                               search      │  sqlite-vec      │
                                           └─────────────────┘
```

**Packages:**

| Package             | Responsibility                                                                                     |
| ------------------- | -------------------------------------------------------------------------------------------------- |
| `main.go`           | 3-line entrypoint calling `cmd.Execute()`                                                          |
| `cmd/`              | Cobra CLI: `root.go`, `stdio.go` (MCP server), `index.go` (CLI indexing), `search.go` (CLI search) |
| `internal/config`   | Shared config: `Config` struct, `Load()`, env helpers, `DBPathForProject`                          |
| `internal/index`    | Orchestration: Merkle diffing, embedding batching, metadata                                        |
| `internal/store`    | SQLite storage, sqlite-vec KNN search, cosine distance                                             |
| `internal/chunker`  | Go AST parsing → `Chunk` structs (function/method/type/etc.)                                       |
| `internal/embedder` | Ollama HTTP client for generating embeddings                                                       |
| `internal/merkle`   | SHA-256 Merkle tree for incremental change detection, .gitignore support                           |

## CLI

Two subcommands:

| Command                    | Description                                          |
| -------------------------- | ---------------------------------------------------- |
| `agent-index stdio`        | Start MCP server on stdin/stdout (existing behavior) |
| `agent-index index <path>` | Index a project from the CLI with progress output    |

### `agent-index index` flags

| Flag      | Short | Default                                              | Description         |
| --------- | ----- | ---------------------------------------------------- | ------------------- |
| `--model` | `-m`  | env or `ordis/jina-embeddings-v2-base-code` (Ollama) | Embedding model     |
| `--force` | `-f`  | false                                                | Force full re-index |

## MCP Tools

### `semantic_search`

| Parameter       | Type    | Required | Default | Notes                                                                                                          |
| --------------- | ------- | -------- | ------- | -------------------------------------------------------------------------------------------------------------- |
| `query`         | string  | yes      | —       | Natural language query                                                                                         |
| `path`          | string  | yes      | —       | Absolute path to project root                                                                                  |
| `limit`         | integer | no       | 20      | Max results                                                                                                    |
| `min_score`     | float   | no       | 0.5     | Minimum score threshold (-1 to 1). Results below this are excluded. Default 0.5. Use -1 to return all results. |
| `force_reindex` | boolean | no       | false   | Forces full re-index                                                                                           |

Returns plaintext with code snippets. Each result has `file:lines`, symbol name,
kind, score, and the actual source code.

**Score:** `1.0 - cosine_distance`. Range is [-1, 1] (negative = dissimilar).
Ordered descending. Default `min_score` is 0.5. Use `min_score=-1` to bypass the
threshold and return all results.

### `index_status`

| Parameter | Type   | Required |
| --------- | ------ | -------- |
| `path`    | string | yes      |

Returns: `total_files`, `total_chunks`, `last_indexed_at` (RFC3339).

## Configuration

| Variable                       | Default                                                                                      | Description                                        |
| ------------------------------ | -------------------------------------------------------------------------------------------- | -------------------------------------------------- |
| `AGENT_INDEX_BACKEND`          | `ollama`                                                                                     | Embedding backend: `ollama` or `lmstudio`          |
| `AGENT_INDEX_EMBED_MODEL`      | `ordis/jina-embeddings-v2-base-code` (Ollama) / `nomic-ai/nomic-embed-code-GGUF` (LM Studio) | Embedding model (must be in known models registry) |
| `AGENT_INDEX_MAX_CHUNK_TOKENS` | `512`                                                                                        | Max estimated tokens per chunk before splitting    |
| `OLLAMA_HOST`                  | `http://localhost:11434`                                                                     | Ollama server URL                                  |
| `LM_STUDIO_HOST`               | `http://localhost:1234`                                                                      | LM Studio server URL                               |

Switching models creates a separate index automatically — the DB path is
SHA-256(projectPath + modelName).

### Known models

Dimensions and context length are looked up automatically from
`internal/embedder/models.go`:

| Model                                | Backend                 | Dims | Context | Size   |
| ------------------------------------ | ----------------------- | ---- | ------- | ------ |
| `ordis/jina-embeddings-v2-base-code` | Ollama (**default**)    | 768  | 8192    | ~323MB |
| `nomic-embed-text`                   | Ollama                  | 768  | 8192    | ~274MB |
| `nomic-ai/nomic-embed-code-GGUF`     | LM Studio (**default**) | 3584 | 8192    | ~274MB |
| `qwen3-embedding:8b`                 | Ollama                  | 4096 | 40960   | ~4.7GB |
| `qwen3-embedding:4b`                 | Ollama                  | 2560 | 40960   | ~2.6GB |
| `qwen3-embedding:0.6b`               | Ollama                  | 1024 | 32768   | ~522MB |
| `all-minilm`                         | Ollama                  | 384  | 512     | ~33MB  |

## Key Implementation Details

### Chunk kinds

`function`, `method`, `type`, `interface`, `const`, `var` — imports and package
declarations are skipped (package chunks pollute search results).

### File filtering

Five layers, applied in order during tree walks:

1. **`SkipDirs`** — hardcoded set of ~30 directory basenames always skipped
   (`.git`, `vendor`, `node_modules`, `__pycache__`, `target`, `.venv`, `dist`,
   IDE dirs, etc.). Cheapest check (map lookup).
2. **`.gitignore`** — root + nested, hierarchical matching via
   `sabhiram/go-gitignore`. Supports `*` globs, `**`, directory patterns
   (`build/`), negation (`!important.gen.go`), and comments. Nested
   `.gitignore` files in subdirectories are discovered lazily during the walk
   and cached.
3. **`.agentindexignore`** — same syntax as `.gitignore`, root + nested. Allows
   excluding files from indexing without modifying `.gitignore`. Loaded at every
   directory level alongside `.gitignore`.
4. **`.gitattributes`** — patterns marked with `linguist-generated` or
   `linguist-generated=true` are treated as ignore rules. Root + nested,
   hierarchical matching. `linguist-generated=false` is explicitly not matched.
5. **Extension filter** — only files with extensions matching the chunker's
   supported languages are indexed.

`MakeSkip(rootDir, exts)` creates an `IgnoreTree` that lazily discovers and
caches ignore rules as `filepath.WalkDir` traverses directories. The `SkipFunc`
signature is unchanged — callers need zero modifications. For path `a/b/c/foo.go`,
matchers are checked at each ancestor level (`""`, `"a"`, `"a/b"`, `"a/b/c"`),
each receiving the path relative to its directory.

### Incremental indexing

`EnsureFresh` builds the Merkle tree once, compares root hash to stored hash. If
stale, delegates to `indexWithTree` (internal method). `Index` also delegates to
`indexWithTree`. Neither builds the tree twice.

### Vector search

- `vec_chunks` virtual table uses `distance_metric=cosine`
- KNN query: `WHERE embedding MATCH ? AND k = ? ORDER BY distance LIMIT ?`
- Distance ascending → score descending after `1.0 - distance` conversion
- No kind filter at query time (removed): callers see kind in results but cannot
  pre-filter

### Database path

```go
sha256(projectPath + modelName) → ~/.local/share/agent-index/<hash>/index.db
```

### Chunk splitting

Oversized chunks (estimated tokens > `AGENT_INDEX_MAX_CHUNK_TOKENS`) are split
at line boundaries before embedding. Token count is estimated as
`len(content) / 4`. Sub-chunks get `[1/N]` symbol suffixes and adjusted line
ranges. No overlap between sub-chunks. A single line exceeding the limit passes
through unsplit.

### Embedding batching

Chunks are batched 32 at a time before sending to Ollama. Context length
(`num_ctx`) is set automatically from the model registry.

### IndexerCache

One `*index.Indexer` per project path; lazy init with shared embedder. Lives for
the process lifetime.

## Testing

### Test types

| Command                   | What it runs                |
| ------------------------- | --------------------------- |
| `go test ./...`           | Unit + integration tests    |
| `go test -tags e2e ./...` | E2E tests (requires Ollama) |

### E2E test approach

- Build tag `//go:build e2e`
- `TestMain` builds the binary; each test launches it as a subprocess via MCP
  SDK `CommandTransport`
- Communicates over real stdin/stdout JSON-RPC (no mocks)
- Each test gets an isolated temp dir via `XDG_DATA_HOME`
- Fixture: `testdata/sample-project/` — 5 Go files, ~21 chunks (7 functions, 3
  types, 1 interface + package chunks)
- CI uses `all-minilm` model (33 MB, 384 dims) via Ollama service container

### Key test invariants

- Result scores must be in `(0, 1]` range
- Results must be ordered descending by score
- Second search on unchanged project: `Reindexed=false`
- `index_status` after indexing: `TotalFiles=5`, `TotalChunks>15`,
  `LastIndexedAt` valid RFC3339 within 60s

## Build

```bash
CGO_ENABLED=1 go build -o agent-index .
```

`CGO_ENABLED=1` is required — sqlite-vec compiles from C source.

## Key Dependencies

| Dep                                        | Purpose                          |
| ------------------------------------------ | -------------------------------- |
| `github.com/spf13/cobra`                   | CLI framework (subcommands)      |
| `github.com/modelcontextprotocol/go-sdk`   | MCP server/client                |
| `github.com/asg017/sqlite-vec-go-bindings` | sqlite-vec CGo bindings          |
| `github.com/mattn/go-sqlite3`              | SQLite CGo driver                |
| `github.com/sethvargo/go-retry`            | Context-aware retry with backoff |
| `github.com/sabhiram/go-gitignore`         | .gitignore pattern matching      |

## Decisions Made

- **Kind filter removed from `Store.Search`**: Was doing over-fetch (3× limit)
  then post-filter. Removed entirely; kind is still in results. Simplifies
  query, avoids over-fetch complexity.
- **`Status()` is DB-only**: No filesystem walk; reads persisted metadata. Fast
  but can diverge if metadata updates fail.
- **`stale_files` removed from `index_status` output**: Was expensive and
  misleading.
- **Model name in DB path hash**: Switching models creates a fresh index
  automatically, no collision.
- **`indexWithTree` internal method**: Eliminated the double Merkle tree build
  between `EnsureFresh` and `Index`.
- **`SkipDirs` is a shared map**: `DefaultSkip`, `MakeExtSkip`, and `MakeSkip`
  all use `SkipDirs` for directory filtering. Single source of truth.
