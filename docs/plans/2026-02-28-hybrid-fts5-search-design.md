# Hybrid FTS5 + Vector Search Design

## Vision

Add SQLite FTS5 full-text search alongside the existing vector index and combine
results using Reciprocal Rank Fusion (RRF). Every search automatically benefits
from both semantic similarity (vector) and exact keyword matching (FTS5) — no
new parameters, no new tools.

## Problem

Pure vector search misses exact identifier matches when a semantically similar
but differently-named symbol scores higher. Example: searching `"ValidateToken"`
may return `AuthenticateUser` first because the embedding model finds it
semantically closer. FTS5 catches the literal match and pushes the exact result
to the top via RRF.

## Decisions

| Decision       | Choice                        | Rationale                                                                          |
| -------------- | ----------------------------- | ---------------------------------------------------------------------------------- |
| Scope          | All files (code + config)     | Code benefits from exact symbol lookup; uniform treatment; simpler implementation  |
| Activation     | Always hybrid, no flag        | FTS5 degrades gracefully to zero when no matches; strictly better than pure vector |
| Score fusion   | Reciprocal Rank Fusion (k=60) | Rank-based; scale-independent; no BM25/cosine incompatibility                      |
| Merge location | Single SQL CTE                | One DB round-trip; RRF computed in SQL; no Go-side merge maps                      |
| Build tag      | `fts5` (mattn/go-sqlite3)     | Required to enable FTS5 module in CGo SQLite build                                 |

## Architecture

### Schema

```sql
CREATE VIRTUAL TABLE IF NOT EXISTS fts_chunks USING fts5(
    id UNINDEXED,
    content,
    content='',
    contentless_delete=1
)
```

- **Contentless** (`content=''`): raw text not stored in FTS5; already on disk.
  Halves storage overhead.
- **`contentless_delete=1`**: enables explicit row deletion (required; no
  triggers on contentless tables).
- **`id UNINDEXED`**: chunk ID stored but not tokenized; used as join key only.
- Tokenizer: FTS5 default `unicode61` — splits on non-alphanumeric.
  `ValidateToken` stays one token.

### Insert Path

`InsertChunks` adds `fts_chunks` to the existing transaction alongside `chunks`
and `vec_chunks`:

```go
ftsStmt.Exec(c.ID, c.Content)  // inside same tx as chunk + vec inserts
```

`Chunk.Content` already carries raw source text — no new data flow needed.

### Delete Path

`DeleteFileChunks` deletes in order: `fts_chunks` → `vec_chunks` → `chunks` →
`files`.

`fts_chunks` must be deleted before `chunks` since it needs the IDs (or we
collect IDs first).

### Query Path — RRF CTE

Single SQL query combining both result sets:

```sql
WITH vector_results AS (
    SELECT id, ROW_NUMBER() OVER (ORDER BY distance) AS rank
    FROM vec_chunks
    WHERE embedding MATCH ? AND k = ?
),
fts_results AS (
    SELECT id, ROW_NUMBER() OVER (ORDER BY bm25(fts_chunks)) AS rank
    FROM fts_chunks
    WHERE fts_chunks MATCH ?
    LIMIT ?
),
all_ids AS (
    SELECT id FROM vector_results
    UNION
    SELECT id FROM fts_results
),
rrf AS (
    SELECT
        a.id,
        COALESCE(1.0 / (60.0 + vr.rank), 0.0) +
        COALESCE(1.0 / (60.0 + fr.rank), 0.0) AS rrf_score
    FROM all_ids a
    LEFT JOIN vector_results vr ON a.id = vr.id
    LEFT JOIN fts_results fr ON a.id = fr.id
)
SELECT r.id, c.file_path, c.symbol, c.kind, c.start_line, c.end_line, r.rrf_score
FROM rrf r
JOIN chunks c ON r.id = c.id
ORDER BY r.rrf_score DESC
LIMIT ?
```

**FTS query string**: user query tokens joined with `OR` to avoid strict AND
semantics:

```go
ftsQuery = strings.Join(strings.Fields(query), " OR ")
```

**`min_score` filter**: applied post-query in Go on the RRF score (not in SQL),
since RRF scores are not comparable to cosine distance. The `min_score`
parameter semantics shift to filtering on RRF score; document clearly.

**Verified**: vec0 and FTS5 MATCH clauses both work inside CTEs in SQLite. See
`TestHybridCTE_VecAndFTS5InCTE`.

## Build Tag Change

FTS5 requires the `fts5` build tag for `mattn/go-sqlite3`:

```bash
# Before
CGO_ENABLED=1 go build -o agent-index .
CGO_ENABLED=1 go test ./...

# After
CGO_ENABLED=1 go build -tags=fts5 -o agent-index .
CGO_ENABLED=1 go test -tags=fts5 ./...
```

Makefile and CI updated accordingly. `golangci-lint` gets `--build-tags=fts5`.

## Testing

### Unit tests (`internal/store/`, `-tags=fts5`)

| Test                                     | What it checks                                             |
| ---------------------------------------- | ---------------------------------------------------------- |
| `TestNewStore_CreatesSchema` (extend)    | `fts_chunks` table exists after schema creation            |
| `TestHybridCTE_VecAndFTS5InCTE` (exists) | CTE UNION RRF query runs; exact match ranks first          |
| `TestStore_HybridSearch_FTSOnly`         | FTS match with orthogonal query vec; result still returned |
| `TestStore_HybridSearch_VectorOnly`      | FTS gibberish query; pure vector results unaffected        |
| `TestStore_FTS_DeleteOnFileDelete`       | Delete file → `fts_chunks` rows gone                       |

### E2E tests (`-tags=fts5,e2e`, requires Ollama)

| Test                                   | What it checks                                           |
| -------------------------------------- | -------------------------------------------------------- |
| `TestE2E_HybridSearch_ExactIdentifier` | Search `"ValidateToken"` → `ValidateToken` is top result |

Existing E2E tests pass unchanged — hybrid is transparent to callers.

## Files Changed

| File                                | Change                                                                          |
| ----------------------------------- | ------------------------------------------------------------------------------- |
| `internal/store/store.go`           | Add `fts_chunks` to schema; extend `InsertChunks`, `DeleteFileChunks`, `Search` |
| `internal/store/store_test.go`      | Extend schema test; add hybrid unit tests                                       |
| `internal/store/hybrid_cte_test.go` | CTE verification test (already written)                                         |
| `Makefile`                          | Add `-tags=fts5` everywhere                                                     |
| `.github/workflows/ci.yml`          | Add `-tags=fts5` to test, vet, lint steps                                       |
| `CLAUDE.md`                         | Update build command; document min_score semantics change                       |
