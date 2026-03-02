# CLAUDE.md — lumen

## Go Standards

- **Version**: Go 1.26+
- **Build**: `CGO_ENABLED=1 go build -o lumen .` (sqlite-vec requires CGO)
- **Format**: `gofmt` (enforced in CI)
- **Lint**: `golangci-lint run` (zero issues, see `.golangci.yml`)
- **Vet**: `go vet ./...` (external dependency warnings OK)

## Code Quality Rules

### Testing
- **Unit + integration**: `go test ./...`
- **E2E tests**: `go test -tags e2e ./...` (requires Ollama/LM Studio running)
- **All tests must pass before commit**
- Coverage tracked but not enforced

### Linting & Errors
- **golangci-lint**: Must pass with zero issues before any PR
- **Error handling**: Explicit blank assignment `_ = err` when intentionally ignoring errors
- **Defer cleanup**: Always defer resource cleanup (defer Close() on database/file handles)
- **Panics**: Only during package initialization, never in business logic
- **No "not found" confusion**: Distinguish between "resource not found" and actual database errors

### Git Conventional Commits
- **Format**: Follow [Conventional Commits](https://www.conventionalcommits.org/) specification
- **Types**: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `chore`, `ci`
- **Scope**: Optional, package or component name (e.g., `fix(chunker): ...`, `feat(store): ...`)
- **Breaking changes**: Add `!` after type/scope (e.g., `feat!: ...`) or `BREAKING CHANGE:` footer
- **Examples**:
  - `fix: handle nil pointers in search results`
  - `feat(store): add batch upsert for chunks`
  - `docs: update README with new API examples`
  - `refactor: simplify merkle tree comparison`

### Idiomatic Go Patterns
- **Interface satisfaction**: Implicit, verified by compilation (no "implements" comments)
- **Error as value**: Return error as final argument, check immediately
- **Context passing**: Thread context through all async operations
- **defer for cleanup**: Prefer defer over manual cleanup
- **Table-driven tests**: Use for multiple test cases
- **Unexported helpers**: Package-local utilities as unexported functions
- **No generic error strings**: Use proper error types/wrapping

## Core Technologies

| Tech          | Purpose                                 | Notes                           |
| ------------- | --------------------------------------- | ------------------------------- |
| SQLite        | Vector storage + schema persistence     | Uses sqlite-vec for KNN search  |
| MCP (Model Context Protocol) | Agent integration               | stdio transport                 |
| Ollama/LM Studio | Embeddings generation              | Local models, configurable      |
| Go AST        | Code parsing into semantic chunks       | Functions, types, methods, etc. |
| Cobra         | CLI framework                           | Subcommands: index, stdio       |

## Commands Reference

See `Makefile` for all commands:

```bash
make build        # Build binary (CGO_ENABLED=1)
make test         # Run unit + integration tests
make e2e          # Run E2E tests (requires Ollama/LM Studio)
make lint         # Run golangci-lint
make vet          # Run go vet
make format       # Format code & markdown
make tidy         # Update go.mod
make clean        # Remove binary
make install      # Install binary
```

## Project Structure

```
.
├── main.go              # 3-line entrypoint
├── cmd/
│   ├── root.go         # Cobra root command
│   ├── stdio.go        # MCP server
│   └── index.go        # CLI indexing
├── internal/
│   ├── config/         # Config loading & paths
│   ├── index/          # Orchestration (Merkle + embedding + chunking)
│   ├── store/          # SQLite + sqlite-vec operations
│   ├── chunker/        # Go AST parsing → chunks
│   ├── embedder/       # Ollama/LM Studio HTTP client
│   └── merkle/         # Change detection (SHA-256 tree)
└── testdata/           # Fixtures for E2E tests
```

## Key Design Decisions

- **Merkle tree for diffs**: Avoid re-indexing unchanged code
- **Model name in DB path**: Different models → separate indexes (SHA-256 hash of path + model name)
- **5-layer file filtering**: SkipDirs → .gitignore → .lumenignore → .gitattributes → extension
- **Chunk splitting at line boundaries**: Oversized chunks split at `LUMEN_MAX_CHUNK_TOKENS` (512 default)
- **32-batch embedding**: Balance memory vs. API round-trips
- **Cosine distance KNN**: Normalized for semantic similarity

