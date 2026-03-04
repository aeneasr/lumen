# Rename agent-index → Lumen Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to
> implement this plan task-by-task.

**Goal:** Rename the project from "agent-index" to "Lumen" across all files:
binary name, module path, env vars, ignore file, MCP server name, CLI command
name, data directory, and docs.

**Architecture:** A mechanical search-and-replace across Go sources, config,
scripts, and docs. The most important decisions: env vars `AGENT_INDEX_*` →
`LUMEN_*`, ignore file `.agentindexignore` → `.lumenignore`, data dir
`agent-index/` → `lumen/`, MCP server name `agent-index` → `lumen`, CLI command
`agent-index` → `lumen`, Go module `github.com/ory/agent-index` →
`github.com/ory/lumen`.

**Tech Stack:** Go, Makefile, GitHub Actions CI, bash

---

## Rename map

| Old                                    | New                      |
| -------------------------------------- | ------------------------ |
| `github.com/ory/agent-index` (module)  | `github.com/ory/lumen`   |
| `agent-index` (binary / CLI Use field) | `lumen`                  |
| `agent-index` (MCP server Name)        | `lumen`                  |
| `agent-index` (data dir segment)       | `lumen`                  |
| `AGENT_INDEX_BACKEND`                  | `LUMEN_BACKEND`          |
| `AGENT_INDEX_EMBED_MODEL`              | `LUMEN_EMBED_MODEL`      |
| `AGENT_INDEX_MAX_CHUNK_TOKENS`         | `LUMEN_MAX_CHUNK_TOKENS` |
| `AGENT_INDEX_EMBED_DIMS`               | `LUMEN_EMBED_DIMS`       |
| `.agentindexignore`                    | `.lumenignore`           |
| `agentIndexIgnore` (Go field)          | `lumenIgnore`            |
| `AgentIndexIgnore` (test func)         | `LumenIgnore`            |
| `agent-index-e2e-test` (test binary)   | `lumen-e2e-test`         |

---

### Task 1: Update Go module path

**Files:**

- Modify: `go.mod`
- Modify (all): every `*.go` file that imports `github.com/ory/agent-index`

**Step 1: Update go.mod**

```
module github.com/ory/lumen
```

(Replace the first line of go.mod.)

**Step 2: Bulk-replace all Go import paths**

```bash
find . -name '*.go' | xargs sed -i '' 's|github.com/ory/agent-index|github.com/ory/lumen|g'
```

**Step 3: Verify no old module path remains**

```bash
grep -r "ory/agent-index" --include="*.go" --include="go.mod" .
```

Expected: no output.

**Step 4: Build to confirm**

```bash
CGO_ENABLED=1 go build -o lumen .
```

Expected: compiles without errors.

**Step 5: Commit**

```bash
git add go.mod $(git diff --name-only)
git commit -m "refactor: rename Go module path to github.com/ory/lumen"
```

---

### Task 2: Rename binary, CLI command, and MCP server name

**Files:**

- Modify: `Makefile:1`
- Modify: `cmd/root.go` — `Use: "agent-index"`
- Modify: `cmd/stdio.go` — `Name: "agent-index"` (MCP server name)

**Step 1: Update Makefile**

Change line 1:

```makefile
BINARY   := lumen
```

**Step 2: Update CLI root command name**

In `cmd/root.go`, change:

```go
Use:   "lumen",
```

**Step 3: Update MCP server name**

In `cmd/stdio.go`, change:

```go
Name:    "lumen",
```

**Step 4: Verify**

```bash
CGO_ENABLED=1 go build -o lumen . && ./lumen --help
```

Expected: shows `lumen` in usage line.

**Step 5: Commit**

```bash
git add Makefile cmd/root.go cmd/stdio.go
git commit -m "refactor: rename binary, CLI command, and MCP server name to lumen"
```

---

### Task 3: Rename environment variables

**Files:**

- Modify: `internal/config/config.go`
- Modify: `cmd/index.go`
- Modify: `cmd/embedder.go` (if any)
- Modify: `.github/workflows/ci.yml`
- Modify: `bench-mcp.sh`
- Modify: `e2e_test.go`
- Modify: `e2e_cli_test.go`
- Modify: `e2e_lang_test.go`

**Step 1: Bulk replace env var prefixes in all files**

```bash
find . -type f \( -name "*.go" -o -name "*.sh" -o -name "*.yml" -o -name "*.yaml" \) | \
  xargs sed -i '' \
    -e 's/AGENT_INDEX_BACKEND/LUMEN_BACKEND/g' \
    -e 's/AGENT_INDEX_EMBED_MODEL/LUMEN_EMBED_MODEL/g' \
    -e 's/AGENT_INDEX_MAX_CHUNK_TOKENS/LUMEN_MAX_CHUNK_TOKENS/g' \
    -e 's/AGENT_INDEX_EMBED_DIMS/LUMEN_EMBED_DIMS/g'
```

**Step 2: Verify**

```bash
grep -r "AGENT_INDEX_" --include="*.go" --include="*.sh" --include="*.yml" .
```

Expected: no output (or only in docs/plans which is fine).

**Step 3: Run tests**

```bash
go test ./...
```

Expected: all pass.

**Step 4: Commit**

```bash
git add -u
git commit -m "refactor: rename AGENT_INDEX_* env vars to LUMEN_*"
```

---

### Task 4: Rename .agentindexignore → .lumenignore

**Files:**

- Modify: `internal/merkle/ignore.go` — field name, file path string, comments
- Modify: `internal/merkle/ignore_test.go` — test names, file strings in test
  bodies
- Modify: `CLAUDE.md` — documentation

**Step 1: Update ignore.go field and file path**

In `internal/merkle/ignore.go`:

- Rename field `agentIndexIgnore` → `lumenIgnore` (appears in struct definition
  and all usages)
- Change string `".agentindexignore"` → `".lumenignore"` (file lookup line)
- Update comments that mention `.agentindexignore`

```bash
sed -i '' \
  -e 's/agentIndexIgnore/lumenIgnore/g' \
  -e 's/\.agentindexignore/.lumenignore/g' \
  internal/merkle/ignore.go
```

**Step 2: Update ignore_test.go**

```bash
sed -i '' \
  -e 's/AgentIndexIgnore/LumenIgnore/g' \
  -e 's/\.agentindexignore/.lumenignore/g' \
  -e 's/agentindexignore/lumenignore/g' \
  internal/merkle/ignore_test.go
```

**Step 3: Update CLAUDE.md**

In the "5-layer file filtering" bullet and any other references, change
`.agentindexignore` → `.lumenignore`.

**Step 4: Run tests**

```bash
go test ./internal/merkle/...
```

Expected: all pass.

**Step 5: Commit**

```bash
git add internal/merkle/ignore.go internal/merkle/ignore_test.go CLAUDE.md
git commit -m "refactor: rename .agentindexignore to .lumenignore"
```

---

### Task 5: Update data directory path

**Files:**

- Modify: `internal/config/config.go:81`

**Step 1: Change the data dir segment**

In `internal/config/config.go`, find:

```go
return filepath.Join(dataDir, "agent-index", hash[:16], "index.db")
```

Change to:

```go
return filepath.Join(dataDir, "lumen", hash[:16], "index.db")
```

**Step 2: Run tests**

```bash
go test ./internal/config/...
```

**Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "refactor: update data directory path from agent-index to lumen"
```

---

### Task 6: Update bench-mcp.sh

**Files:**

- Modify: `bench-mcp.sh`

**Step 1: Update binary references**

In `bench-mcp.sh`:

- Line 9: `BINARY="$REPO/agent-index"` → `BINARY="$REPO/lumen"`
- Lines 87-88: `echo "Building agent-index..."` → `echo "Building lumen..."` and
  `CGO_ENABLED=1 go build -o lumen .`
- Line 103: MCP config JSON key `"agent-index"` → `"lumen"`
- Line 127: allowed tools `mcp__agent-index__` → `mcp__lumen__`

```bash
sed -i '' \
  -e 's|go build -o agent-index|go build -o lumen|g' \
  -e 's|"$REPO/agent-index"|"$REPO/lumen"|g' \
  -e 's|Building agent-index|Building lumen|g' \
  -e 's|"agent-index":{"command":"$BINARY"|"lumen":{"command":"$BINARY"|g' \
  -e 's|mcp__agent-index__|mcp__lumen__|g' \
  bench-mcp.sh
```

**Step 2: Verify the file looks correct**

```bash
grep -n "agent-index\|agent_index" bench-mcp.sh
```

Expected: no output.

**Step 3: Commit**

```bash
git add bench-mcp.sh
git commit -m "refactor: update bench-mcp.sh for lumen rename"
```

---

### Task 7: Update e2e test binary name

**Files:**

- Modify: `e2e_test.go`

**Step 1: Update test binary path**

In `e2e_test.go`, change:

```go
bin := filepath.Join(os.TempDir(), "lumen-e2e-test")
```

**Step 2: Run e2e build check (no Ollama needed)**

```bash
go build -tags e2e ./... 2>&1
```

Expected: compiles.

**Step 3: Commit**

```bash
git add e2e_test.go
git commit -m "refactor: update e2e test binary name to lumen-e2e-test"
```

---

### Task 8: Update README.md

**Files:**

- Modify: `README.md`

**Step 1: Replace all agent-index references**

```bash
sed -i '' \
  -e 's/agent-index/lumen/g' \
  -e 's/agent_index/lumen/g' \
  -e 's/AGENT_INDEX/LUMEN/g' \
  README.md
```

**Step 2: Update project title / heading** (manual review)

Open README.md and verify the title and intro text reads "Lumen" and "lumen"
appropriately. The tagline should be: **Lumen — semantic search for code
agents**.

**Step 3: Commit**

```bash
git add README.md
git commit -m "docs: update README for Lumen rename"
```

---

### Task 9: Update remaining comments and package docs in Go files

**Files:**

- Modify: `main.go` — package comment
- Modify: `cmd/root.go` — package comment
- Modify: `internal/config/config.go` — package comment, Config struct comment
- Modify: `internal/chunker/structured.go` — comment about
  `AGENT_INDEX_MAX_CHUNK_TOKENS`

**Step 1: Bulk replace in comments**

```bash
find . -name '*.go' | xargs sed -i '' \
  -e 's/agent-index entry point/lumen entry point/g' \
  -e 's/the agent-index CLI/the lumen CLI/g' \
  -e 's/agent-index CLI/lumen CLI/g' \
  -e 's/agent-index process/lumen process/g'
```

**Step 2: Verify build and tests still pass**

```bash
CGO_ENABLED=1 go build -o lumen . && go test ./...
```

Expected: build succeeds, all tests pass.

**Step 3: Commit**

```bash
git add -u
git commit -m "refactor: update Go comments and package docs for lumen rename"
```

---

### Task 10: Final verification

**Step 1: Check for any remaining old references**

```bash
grep -r "agent-index\|agent_index\|AGENT_INDEX\|agentindex\|agentIndexIgnore" \
  --include="*.go" --include="*.mod" --include="*.sh" --include="*.yml" \
  --include="Makefile" --include="README.md" --include="CLAUDE.md" \
  . | grep -v docs/plans | grep -v bench-results
```

Expected: no output.

**Step 2: Full build + test**

```bash
CGO_ENABLED=1 make build && make test
```

Expected: binary `lumen` created, all tests pass.

**Step 3: Verify binary name**

```bash
./lumen --help
```

Expected: usage shows `lumen` not `agent-index`.

**Step 4: Verify MCP server name**

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}' | ./lumen stdio 2>/dev/null | head -5
```

Expected: response JSON contains `"name":"lumen"`.

**Step 5: Final commit if any stragglers**

```bash
git add -u && git commit -m "chore: lumen rename cleanup"
```
