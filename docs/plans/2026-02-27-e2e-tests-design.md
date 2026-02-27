# E2E Tests for agent-index MCP Server

## Goal

End-to-end tests that exercise the full MCP protocol path: build the binary, launch it as a subprocess, communicate via JSON-RPC over stdin/stdout, index a real Go codebase with a real Ollama instance, and verify semantic search returns meaningful results.

No mocks, no stubs, no fallbacks.

## Architecture

- **Transport**: Go MCP SDK `CommandTransport` launches the built binary as a subprocess. Tests use `mcp.Client` to connect and call tools over real stdin/stdout JSON-RPC.
- **Build tag**: `//go:build e2e` — separate from unit tests and the existing `integration` tag.
- **Test file**: `e2e_test.go` in the root package.
- **CI model**: `all-minilm` (33MB, 384 dimensions) — fast to pull, good enough for semantic assertions.
- **Isolation**: Each test gets its own temp dir via `XDG_DATA_HOME` so databases don't collide.

## Test Fixture

`testdata/sample-project/` with ~5 Go files providing semantic variety:

- `auth.go` — authentication functions (ValidateToken, CreateSession, RevokeSession)
- `handler.go` — HTTP handlers (HandleHealth, HandleListUsers, HandleCreateUser)
- `models.go` — types and interfaces (User, Session, UserRepository)
- `database.go` — database operations (QueryUsers, InsertUser, BeginTransaction)
- `config.go` — configuration (LoadConfig, ParseDatabaseURL, GetEnvWithDefault)

## Test Cases

```
e2e_test.go
├── TestMain()                     — build binary, check Ollama health, pull model
├── TestE2E_ListTools              — connect, list tools, verify semantic_search + index_status present
├── TestE2E_IndexAndSearch         — index testdata/, search "authentication token validation", assert auth functions in top results
├── TestE2E_SearchScoreRange       — verify all scores are in (0, 1] range
├── TestE2E_SearchNegative         — search "database migration", assert auth functions are NOT in top-2 results
├── TestE2E_IncrementalUpdate      — add a file, re-search, verify new content is found
├── TestE2E_IndexStatus            — call index_status after indexing, verify file/chunk counts
├── TestE2E_ForceReindex           — call with force_reindex=true, verify reindexed=true in response
└── TestE2E_ErrorHandling          — missing path, missing query → isError=true in response
```

## Helper: startServer

Each test calls a helper that:

1. Creates a `t.TempDir()` for `XDG_DATA_HOME`
2. Builds an `exec.Cmd` pointing at the built binary
3. Sets env: `AGENT_INDEX_EMBED_MODEL`, `OLLAMA_HOST`, `XDG_DATA_HOME`
4. Wraps it in `mcp.CommandTransport`
5. Connects via `mcp.Client` and returns the session
6. Registers `t.Cleanup` to close the session

## Result Parsing

`CallTool` returns `*mcp.CallToolResult` with `Content []mcp.Content`. Our handlers serialize structured output as JSON inside a `TextContent`. Tests unmarshal that back into `SemanticSearchOutput` / `IndexStatusOutput`.

Helper function: `callToolJSON[T any](t, session, toolName, args) T`

## CI Configuration

New `e2e` job in `.github/workflows/ci.yml`:

```yaml
e2e:
  name: E2E
  runs-on: ubuntu-latest
  services:
    ollama:
      image: ollama/ollama:latest
      ports:
        - 11434:11434
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: "1.26"
        cache: true
    - name: Pull embedding model
      run: curl -s http://localhost:11434/api/pull -d '{"name":"all-minilm"}'
    - name: E2E tests
      run: CGO_ENABLED=1 go test -tags=e2e -timeout=5m -v -count=1 ./...
```

## Environment Variables

Per test subprocess:
- `AGENT_INDEX_EMBED_MODEL=all-minilm`
- `OLLAMA_HOST=http://localhost:11434` (overridable via env for local dev)
- `XDG_DATA_HOME=<t.TempDir()>`

## Not In Scope

- Mock/stub modes — real Ollama only
- Performance benchmarks — just correctness
- Multi-project concurrency tests — single project per test is sufficient
