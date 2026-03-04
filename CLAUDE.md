# CLAUDE.md — lumen

Lumen is a code search and indexing tool designed for integration with the
Claude Code plugin system. It provides fast, semantic search capabilities over
codebases by leveraging vector embeddings and a Merkle tree structure to
efficiently detect changes and minimize re-indexing.

This repository is structured as a claude plugin available via the claude
marketplace.

## Go Standards

- **Version**: Go 1.26+
- **Build**: `CGO_ENABLED=1 go build -o bin/lumen-<os>-<arch> .` (sqlite-vec
  requires CGO)
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
- **Error handling**: Explicit blank assignment `_ = err` when intentionally
  ignoring errors
- **Defer cleanup**: Always defer resource cleanup (defer Close() on
  database/file handles)
- **Panics**: Only during package initialization, never in business logic
- **No "not found" confusion**: Distinguish between "resource not found" and
  actual database errors

### Git Conventional Commits

- **Format**: Follow
  [Conventional Commits](https://www.conventionalcommits.org/) specification
- **Types**: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`,
  `chore`, `ci`
- **Scope**: Optional, package or component name (e.g., `fix(chunker): ...`,
  `feat(store): ...`)
- **Breaking changes**: Add `!` after type/scope (e.g., `feat!: ...`) or
  `BREAKING CHANGE:` footer
- **Examples**:
  - `fix: handle nil pointers in search results`
  - `feat(store): add batch upsert for chunks`
  - `docs: update README with new API examples`
  - `refactor: simplify merkle tree comparison`

### Idiomatic Go Patterns

- **Interface satisfaction**: Implicit, verified by compilation (no "implements"
  comments)
- **Error as value**: Return error as final argument, check immediately
- **Context passing**: Thread context through all async operations
- **defer for cleanup**: Prefer defer over manual cleanup
- **Table-driven tests**: Use for multiple test cases
- **Unexported helpers**: Package-local utilities as unexported functions
- **No generic error strings**: Use proper error types/wrapping

## Core Technologies

| Tech                         | Purpose                             | Notes                           |
| ---------------------------- | ----------------------------------- | ------------------------------- |
| SQLite                       | Vector storage + schema persistence | Uses sqlite-vec for KNN search  |
| MCP (Model Context Protocol) | Agent integration                   | stdio transport                 |
| Ollama/LM Studio             | Embeddings generation               | Local models, configurable      |
| Go AST                       | Code parsing into semantic chunks   | Functions, types, methods, etc. |
| Cobra                        | CLI framework                       | Subcommands: index, stdio       |

## Commands Reference

See `Makefile` for all commands:

```bash
make build        # Build binary to bin/ (CGO_ENABLED=1)
make test         # Run unit + integration tests
make e2e          # Run E2E tests (requires Ollama/LM Studio)
make lint         # Run golangci-lint
make vet          # Run go vet
make format       # Format code & markdown
make tidy         # Update go.mod
make clean        # Remove bin/ and dist/
make plugin-dev   # Build + print plugin-dir usage
```

## Plugin Development

```bash
make build
claude --plugin-dir .
```

This loads lumen as a Claude Code plugin directly from the repo. The plugin
system handles MCP registration, hooks, and skills declaratively via:

- `.claude-plugin/plugin.json` — plugin manifest
- `.mcp.json` — MCP server config
- `hooks/hooks.json` — SessionStart + PreToolUse hooks
- `skills/` — `/lumen:doctor` and `/lumen:reindex` skills

## Environment Variables

| Variable                 | Default           | Description                                |
| ------------------------ | ----------------- | ------------------------------------------ |
| `LUMEN_BACKEND`          | `ollama`          | Embedding backend (`ollama` or `lmstudio`) |
| `LUMEN_EMBED_MODEL`      | see note ¹        | Embedding model (must be in registry)      |
| `OLLAMA_HOST`            | `localhost:11434` | Ollama server URL                          |
| `LM_STUDIO_HOST`         | `localhost:1234`  | LM Studio server URL                       |
| `LUMEN_MAX_CHUNK_TOKENS` | `512`             | Max tokens per chunk before splitting      |

¹ `ordis/jina-embeddings-v2-base-code` (Ollama),
`nomic-ai/nomic-embed-code-GGUF` (LM Studio)

## Project Structure

```
.
├── main.go              # 3-line entrypoint
├── .claude-plugin/      # Plugin manifest
├── .mcp.json            # MCP server config
├── hooks/               # Hook declarations
├── skills/              # Skill definitions
├── scripts/             # Platform wrappers (run.sh, run.bat)
├── cmd/
│   ├── root.go         # Cobra root command
│   ├── stdio.go        # MCP server
│   ├── hook.go         # Hook handlers
│   ├── purge.go        # Index data cleanup
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
- **Model name in DB path**: Different models → separate indexes (SHA-256 hash
  of path + model name)
- **5-layer file filtering**: SkipDirs → .gitignore → .lumenignore →
  .gitattributes → extension
- **Chunk splitting at line boundaries**: Oversized chunks split at
  `LUMEN_MAX_CHUNK_TOKENS` (512 default)
- **32-batch embedding**: Balance memory vs. API round-trips
- **Cosine distance KNN**: Normalized for semantic similarity
- **Plugin system**: Declarative hooks/MCP/skills via `.claude-plugin/`,
  replacing manual install/uninstall

## Claude Integration Notes

When planning any work related to claude code plugin, marketplace, hooks,
ensuring tool use, and other areas around the claude integration you MUST base
your thinking on the following AUTHORATIVE reference docs:

- Marketplace Plugin:
  https://code.claude.com/docs/en/plugin-marketplaces#marketplace-schema
- Plugin Reference: https://code.claude.com/docs/en/plugins-reference
