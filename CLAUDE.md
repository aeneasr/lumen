# CLAUDE.md — agent-index-go

## Vision

**Give AI coding agents precise, local semantic code search.**

AI agents waste context window tokens reading entire files when they only need one function. `agent-index-go` fixes this: it parses a Go codebase into semantic chunks (functions, methods, types, interfaces, consts), embeds them via a local Ollama model, stores vectors in SQLite, and exposes search over MCP. The agent describes what it needs in natural language and gets back exact file paths and line ranges.

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

| Package | Responsibility |
|---|---|
| `main.go` | MCP server, two tools: `semantic_search` and `index_status` |
| `internal/index` | Orchestration: Merkle diffing, embedding batching, metadata |
| `internal/store` | SQLite storage, sqlite-vec KNN search, cosine distance |
| `internal/chunker` | Go AST parsing → `Chunk` structs (function/method/type/etc.) |
| `internal/embedder` | Ollama HTTP client for generating embeddings |
| `internal/merkle` | SHA-256 Merkle tree for incremental change detection, .gitignore support |

## MCP Tools

### `semantic_search`

| Parameter | Type | Required | Default | Notes |
|---|---|---|---|---|
| `query` | string | yes | — | Natural language query |
| `path` | string | yes | — | Absolute path to project root |
| `limit` | integer | no | 10 | Max results |
| `force_reindex` | boolean | no | false | Forces full re-index |

Returns: `SearchResultItem[]` with `file_path`, `symbol`, `kind`, `start_line`, `end_line`, `score`.

**Score:** `1.0 - cosine_distance`. Ordered descending (highest similarity first).

### `index_status`

| Parameter | Type | Required |
|---|---|---|
| `path` | string | yes |

Returns: `total_files`, `total_chunks`, `last_indexed_at` (RFC3339).

## Configuration

| Variable | Default | Description |
|---|---|---|
| `AGENT_INDEX_EMBED_MODEL` | `nomic-embed-text` | Ollama embedding model |
| `AGENT_INDEX_EMBED_DIMS` | model-dependent | Override embedding dimensions |
| `OLLAMA_HOST` | `http://localhost:11434` | Ollama server URL |

Switching models creates a separate index automatically — the DB path is SHA-256(projectPath + modelName).

## Key Implementation Details

### Chunk kinds

`package`, `function`, `method`, `type`, `interface`, `const`, `var` — imports are explicitly skipped.

### File filtering

Three layers, applied in order during tree walks:

1. **`SkipDirs`** — hardcoded set of ~30 directory basenames always skipped (`.git`, `vendor`, `node_modules`, `__pycache__`, `target`, `.venv`, `dist`, IDE dirs, etc.). Cheapest check (map lookup).
2. **`.gitignore`** — root `.gitignore` is read via `sabhiram/go-gitignore` if present. Supports `*` globs, `**`, directory patterns (`build/`), negation (`!important.gen.go`), and comments. Nested `.gitignore` files are not yet supported.
3. **Extension filter** — only files with extensions matching the chunker's supported languages are indexed.

`MakeSkip(rootDir, exts)` composes all three layers into a single `SkipFunc`. If no `.gitignore` exists, the gitignore layer is silently skipped.

### Incremental indexing

`EnsureFresh` builds the Merkle tree once, compares root hash to stored hash. If stale, delegates to `indexWithTree` (internal method). `Index` also delegates to `indexWithTree`. Neither builds the tree twice.

### Vector search

- `vec_chunks` virtual table uses `distance_metric=cosine`
- KNN query: `WHERE embedding MATCH ? AND k = ? ORDER BY distance LIMIT ?`
- Distance ascending → score descending after `1.0 - distance` conversion
- No kind filter at query time (removed): callers see kind in results but cannot pre-filter

### Database path

```go
sha256(projectPath + modelName) → ~/.local/share/agent-index/<hash>/index.db
```

### Embedding batching

Chunks are batched 32 at a time before sending to Ollama.

### IndexerCache

One `*index.Indexer` per project path; lazy init with shared embedder. Lives for the process lifetime.

## Testing

### Test types

| Command | What it runs |
|---|---|
| `go test ./...` | Unit + integration tests |
| `go test -tags e2e ./...` | E2E tests (requires Ollama) |

### E2E test approach

- Build tag `//go:build e2e`
- `TestMain` builds the binary; each test launches it as a subprocess via MCP SDK `CommandTransport`
- Communicates over real stdin/stdout JSON-RPC (no mocks)
- Each test gets an isolated temp dir via `XDG_DATA_HOME`
- Fixture: `testdata/sample-project/` — 5 Go files, ~21 chunks (7 functions, 3 types, 1 interface + package chunks)
- CI uses `all-minilm` model (33 MB, 384 dims) via Ollama service container

### Key test invariants

- Result scores must be in `(0, 1]` range
- Results must be ordered descending by score
- Second search on unchanged project: `Reindexed=false`
- `index_status` after indexing: `TotalFiles=5`, `TotalChunks>15`, `LastIndexedAt` valid RFC3339 within 60s

## Build

```bash
CGO_ENABLED=1 go build -o agent-index-go .
```

`CGO_ENABLED=1` is required — sqlite-vec compiles from C source.

## Key Dependencies

| Dep | Purpose |
|---|---|
| `github.com/modelcontextprotocol/go-sdk` | MCP server/client |
| `github.com/asg017/sqlite-vec-go-bindings` | sqlite-vec CGo bindings |
| `github.com/mattn/go-sqlite3` | SQLite CGo driver |
| `github.com/sethvargo/go-retry` | Context-aware retry with backoff |
| `github.com/sabhiram/go-gitignore` | .gitignore pattern matching |

## Decisions Made

- **Kind filter removed from `Store.Search`**: Was doing over-fetch (3× limit) then post-filter. Removed entirely; kind is still in results. Simplifies query, avoids over-fetch complexity.
- **`Status()` is DB-only**: No filesystem walk; reads persisted metadata. Fast but can diverge if metadata updates fail.
- **`stale_files` removed from `index_status` output**: Was expensive and misleading.
- **Model name in DB path hash**: Switching models creates a fresh index automatically, no collision.
- **`indexWithTree` internal method**: Eliminated the double Merkle tree build between `EnsureFresh` and `Index`.
- **Root `.gitignore` only**: Covers the vast majority of projects. Nested `.gitignore` support can be added later if needed.
- **`SkipDirs` is a shared map**: `DefaultSkip`, `MakeExtSkip`, and `MakeSkip` all use `SkipDirs` for directory filtering. Single source of truth.
