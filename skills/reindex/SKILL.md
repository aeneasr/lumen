# Lumen Reindex

Force a full re-index of the current project's codebase.

## Steps

1. Call mcp**lumen**semantic_search with:
   - path: the current working directory
   - query: "index status" (a simple query to trigger the search)
   - force_reindex: true
   - summary: true
2. Report how many files were indexed
