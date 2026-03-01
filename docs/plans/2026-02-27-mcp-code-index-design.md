# MCP Code Index Server — Design Document

**Date:** 2026-02-27 **Status:** Approved

## Overview

A Go MCP server that provides semantic code search for code agents like Claude
Code. It indexes Go source files by parsing them into semantically meaningful
chunks (functions, methods, types), embedding them via a local Ollama model, and
storing the vectors in SQLite with sqlite-vec. A Merkle tree detects file
changes so re-indexing only processes modified files.

## Architecture

Monolithic Go binary with internal packages. Runs over stdio as an MCP server.

```
agent-index/
├── main.go                  # MCP server entry, stdio transport
├── internal/
│   ├── chunker/             # Chunker interface + go/ast implementation
│   │   ├── chunker.go       # Interface definition
│   │   └── goast.go         # Go AST implementation
│   ├── embedder/            # Embedder interface + Ollama implementation
│   │   ├── embedder.go      # Interface definition
│   │   └── ollama.go        # Ollama client implementation
│   ├── store/               # SQLite + sqlite-vec storage
│   │   └── store.go
│   ├── merkle/              # Merkle tree change detection
│   │   └── merkle.go
│   └── index/               # Orchestrator: ties all subsystems together
│       └── index.go
├── go.mod
└── go.sum
```

## Key Dependencies

| Dependency                               | Purpose                                                  |
| ---------------------------------------- | -------------------------------------------------------- |
| `github.com/modelcontextprotocol/go-sdk` | Official MCP Go SDK (stdio transport, tool registration) |
| `github.com/mattn/go-sqlite3`            | SQLite driver (CGO)                                      |
| `github.com/asg017/sqlite-vec`           | sqlite-vec extension for vector search                   |
| `github.com/ollama/ollama/api`           | Ollama Go client for embeddings                          |
| `go/ast`, `go/parser`, `go/token`        | Go stdlib for AST-based code chunking                    |

## Design Decisions

| Decision                     | Choice                                                      | Rationale                                                                                                      |
| ---------------------------- | ----------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| Go chunker                   | `go/ast` stdlib with interface for tree-sitter later        | Zero deps, perfect Go parsing, no CGO for the parser. Interface allows adding tree-sitter for other languages. |
| SQLite driver                | `mattn/go-sqlite3` + CGO                                    | Native C performance, sqlite-vec compiles from source                                                          |
| Index storage                | XDG data dir (`~/.local/share/agent-index/<project-hash>/`) | Keeps project dirs clean, no .gitignore needed                                                                 |
| Index trigger                | Auto-index on first search if stale/empty                   | Seamless UX, Merkle tree makes it fast                                                                         |
| Embedding provider           | Interface with Ollama as first impl                         | Extensible to OpenAI/Voyage later                                                                              |
| Search output                | File paths + line ranges only                               | Claude uses its own Read tool to fetch content                                                                 |
| Explicit index_codebase tool | Dropped                                                     | Auto-indexing on search is sufficient; force_reindex param covers the force case                               |

## Core Types

```go
type Chunk struct {
    ID        string   // deterministic: hash(filePath + symbolName + startLine)
    FilePath  string   // relative to project root
    Language  string   // "go"
    Symbol    string   // "FuncName", "TypeName.MethodName", "package"
    Kind      string   // "function", "method", "type", "interface", "const", "var", "package"
    StartLine int
    EndLine   int
    Content   string   // raw source text (used for embedding, not stored long-term)
}

type SearchResult struct {
    FilePath  string  `json:"file_path"`
    Symbol    string  `json:"symbol"`
    Kind      string  `json:"kind"`
    StartLine int     `json:"start_line"`
    EndLine   int     `json:"end_line"`
    Score     float32 `json:"score"`
}

type IndexStatus struct {
    ProjectPath    string `json:"project_path"`
    TotalFiles     int    `json:"total_files"`
    IndexedFiles   int    `json:"indexed_files"`
    TotalChunks    int    `json:"total_chunks"`
    StaleFiles     int    `json:"stale_files"`
    LastIndexedAt  string `json:"last_indexed_at"`
    EmbeddingModel string `json:"embedding_model"`
    OllamaStatus   string `json:"ollama_status"`
}
```

## SQLite Schema

```sql
CREATE TABLE files (
    path         TEXT PRIMARY KEY,
    content_hash TEXT NOT NULL,
    indexed_at   INTEGER NOT NULL
);

CREATE TABLE merkle_nodes (
    path TEXT PRIMARY KEY,
    hash TEXT NOT NULL
);

CREATE TABLE project_meta (
    key   TEXT PRIMARY KEY,
    value TEXT
);

CREATE TABLE chunks (
    id         TEXT PRIMARY KEY,
    file_path  TEXT NOT NULL,
    symbol     TEXT NOT NULL,
    kind       TEXT NOT NULL,
    start_line INTEGER NOT NULL,
    end_line   INTEGER NOT NULL,
    FOREIGN KEY (file_path) REFERENCES files(path) ON DELETE CASCADE
);

CREATE VIRTUAL TABLE vec_chunks USING vec0(
    id TEXT PRIMARY KEY,
    embedding float[1024]
);
```

Project metadata keys: `root_hash`, `last_indexed_at`, `embedding_model`,
`embedding_dimensions`, `project_path`.

## Merkle Tree Change Detection

1. **File hashing:** SHA-256 of each source file's content (leaf nodes).
2. **Directory hashing:** SHA-256 of sorted child hashes concatenated (internal
   nodes).
3. **Staleness check:** Compare current root hash against stored root hash.
   - Match: index is fresh, skip indexing.
   - Mismatch: walk tree, find changed directories, identify changed files at
     leaf level.
4. **Incremental update:** Only re-chunk and re-embed files with changed hashes.
   Delete old chunks for changed files first.

## Chunker Interface

```go
type Chunker interface {
    Supports(language string) bool
    Chunk(filePath string, content []byte) ([]Chunk, error)
}
```

**Go AST implementation** extracts:

- Functions (`*ast.FuncDecl` without receiver) → kind "function"
- Methods (`*ast.FuncDecl` with receiver) → kind "method", symbol
  "ReceiverType.MethodName"
- Structs (`*ast.TypeSpec` with struct type) → kind "type"
- Interfaces (`*ast.TypeSpec` with interface type) → kind "interface"
- Constants (`*ast.GenDecl` with `token.CONST`) → kind "const"
- Variables (`*ast.GenDecl` with `token.VAR`) → kind "var"
- Package doc comment → kind "package"

Each chunk includes its doc comment. Content extracted via byte offset slicing
from the original source.

Files included: `*.go`, excluding `vendor/`, `testdata/`, `.git/`, and
`.gitignore`'d paths.

## Embedder Interface

```go
type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
    Dimensions() int
    ModelName() string
}
```

**Ollama implementation:**

- Uses `github.com/ollama/ollama/api` with `/api/embed` batch endpoint.
- Default model: `nomic-embed-text` (1024 dimensions), configurable via
  `AGENT_INDEX_EMBED_MODEL`.
- Ollama host via `OLLAMA_HOST` (defaults to `localhost:11434`).
- Batch size: 32 texts per request.
- Retry with backoff on transient errors.
- Model auto-pull if not available locally.
- Model change between runs invalidates entire index (vectors aren't comparable
  across models).

## MCP Tools

Two tools exposed via the official MCP Go SDK:

### `semantic_search`

Search indexed codebase using natural language. Auto-indexes if stale.

```go
type SemanticSearchInput struct {
    Query        string `json:"query" jsonschema:"Natural language search query"`
    Path         string `json:"path" jsonschema:"Absolute path to the project root"`
    Limit        int    `json:"limit,omitempty" jsonschema:"Max results to return (default 10)"`
    Kind         string `json:"kind,omitempty" jsonschema:"Filter by chunk kind: function, method, type, interface, const, var"`
    ForceReindex bool   `json:"force_reindex,omitempty" jsonschema:"Force full re-index before searching"`
}

type SemanticSearchOutput struct {
    Results      []SearchResult `json:"results"`
    Reindexed    bool           `json:"reindexed"`
    IndexedFiles int            `json:"indexed_files,omitempty"`
}
```

### `index_status`

Check indexing status of a project.

```go
type IndexStatusInput struct {
    Path string `json:"path" jsonschema:"Absolute path to the project root"`
}

type IndexStatusOutput = IndexStatus
```

## Orchestration Flow

### Indexing (triggered by search or force_reindex)

1. Resolve DB path:
   `~/.local/share/agent-index/<sha256(abs_path)[:16]>/index.db`
2. Open/create SQLite DB, ensure schema
3. Check `project_meta.embedding_model` vs current model → mismatch wipes index
4. Walk filesystem, compute file SHA-256 hashes (skip .git, vendor, testdata,
   .gitignore'd)
5. Build Merkle tree from file hashes
6. Compare against stored root hash
7. For changed files: delete old chunks + embeddings, re-chunk via go/ast
8. Batch embed new chunks via Ollama (32 texts per batch)
9. In a single transaction: batch insert file hashes, chunks, embeddings, update
   merkle nodes + root hash
10. Return stats

### Searching

1. Resolve DB path, open DB
2. Check staleness → auto-index if needed
3. Embed query text → query vector
4. SQL vector search via sqlite-vec with optional kind filter
5. Return file paths + line ranges + scores

## Error Handling

- Ollama not running → "Ollama is not running at {host}. Start it with
  `ollama serve`."
- Model not found → attempt auto-pull, then "Model {name} not available. Run
  `ollama pull {name}`."
- DB corruption → delete index, suggest force_reindex
- File read errors → skip file, continue indexing

## Testing Strategy

| Package             | Test Approach                                                  |
| ------------------- | -------------------------------------------------------------- |
| `internal/chunker`  | Parse known `.go` files, assert chunk boundaries/symbols/kinds |
| `internal/embedder` | Mock HTTP server, test batching/retry/errors                   |
| `internal/store`    | In-memory SQLite, test CRUD + vector search with known vectors |
| `internal/merkle`   | Build tree, mutate files, verify diff detection                |
| `internal/index`    | Mock all interfaces, test orchestration flows end-to-end       |

Integration test (build tag `integration`): real `.go` files + real Ollama if
available, skipped otherwise.
