package store

import (
	"database/sql"
	"fmt"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"

	"github.com/foobar/agent-index-go/internal/chunker"
)

func init() {
	sqlite_vec.Auto()
}

// SearchResult represents a single result from a vector search.
type SearchResult struct {
	FilePath  string
	Symbol    string
	Kind      string
	StartLine int
	EndLine   int
	Distance  float64
}

// StoreStats holds aggregate statistics about the store contents.
type StoreStats struct {
	TotalFiles  int
	TotalChunks int
}

// Store manages SQLite + sqlite-vec storage for code chunks and their
// embedding vectors.
type Store struct {
	db         *sql.DB
	dimensions int
}

// New opens (or creates) a SQLite database at dsn, enables WAL mode and
// foreign keys, and creates the schema tables if they do not exist.
// dimensions specifies the size of the embedding vectors.
func New(dsn string, dimensions int) (*Store, error) {
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Enable WAL mode and foreign keys.
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("exec %q: %w", p, err)
		}
	}

	if err := createSchema(db, dimensions); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	return &Store{db: db, dimensions: dimensions}, nil
}

func createSchema(db *sql.DB, dimensions int) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS files (
			path TEXT PRIMARY KEY,
			hash TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS project_meta (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS chunks (
			id         TEXT PRIMARY KEY,
			file_path  TEXT NOT NULL REFERENCES files(path),
			symbol     TEXT NOT NULL,
			kind       TEXT NOT NULL,
			start_line INTEGER NOT NULL,
			end_line   INTEGER NOT NULL
		)`,
		fmt.Sprintf(
			`CREATE VIRTUAL TABLE IF NOT EXISTS vec_chunks USING vec0(
				id TEXT PRIMARY KEY,
				embedding float[%d] distance_metric=cosine
			)`, dimensions),
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("exec %q: %w", s, err)
		}
	}
	return nil
}

// SetMeta upserts a key-value pair in the project_meta table.
func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO project_meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

// GetMeta retrieves a value from the project_meta table by key.
func (s *Store) GetMeta(key string) (string, error) {
	var val string
	err := s.db.QueryRow("SELECT value FROM project_meta WHERE key = ?", key).Scan(&val)
	if err != nil {
		return "", err
	}
	return val, nil
}

// UpsertFile inserts or updates a file path and its content hash.
func (s *Store) UpsertFile(path, hash string) error {
	_, err := s.db.Exec(
		`INSERT INTO files (path, hash) VALUES (?, ?)
		 ON CONFLICT(path) DO UPDATE SET hash = excluded.hash`,
		path, hash,
	)
	return err
}

// InsertChunks inserts a batch of chunks and their corresponding embedding
// vectors into the chunks and vec_chunks tables within a single transaction.
func (s *Store) InsertChunks(chunks []chunker.Chunk, vectors [][]float32) error {
	if len(chunks) != len(vectors) {
		return fmt.Errorf("chunks and vectors length mismatch: %d vs %d", len(chunks), len(vectors))
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	chunkStmt, err := tx.Prepare(
		`INSERT OR REPLACE INTO chunks (id, file_path, symbol, kind, start_line, end_line)
		 VALUES (?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("prepare chunk insert: %w", err)
	}
	defer chunkStmt.Close()

	vecStmt, err := tx.Prepare(
		`INSERT OR REPLACE INTO vec_chunks (id, embedding) VALUES (?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("prepare vec insert: %w", err)
	}
	defer vecStmt.Close()

	for i, c := range chunks {
		if _, err := chunkStmt.Exec(c.ID, c.FilePath, c.Symbol, c.Kind, c.StartLine, c.EndLine); err != nil {
			return fmt.Errorf("insert chunk %s: %w", c.ID, err)
		}
		blob, err := sqlite_vec.SerializeFloat32(vectors[i])
		if err != nil {
			return fmt.Errorf("serialize vector %d: %w", i, err)
		}
		if _, err := vecStmt.Exec(c.ID, blob); err != nil {
			return fmt.Errorf("insert vec %s: %w", c.ID, err)
		}
	}

	return tx.Commit()
}

// DeleteFileChunks removes all chunks (and their vectors) associated with the
// given file path, then removes the file record itself.
func (s *Store) DeleteFileChunks(filePath string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Delete vec_chunks entries for chunks belonging to this file.
	_, err = tx.Exec(
		`DELETE FROM vec_chunks WHERE id IN (SELECT id FROM chunks WHERE file_path = ?)`,
		filePath,
	)
	if err != nil {
		return fmt.Errorf("delete vec_chunks: %w", err)
	}

	// Delete chunks.
	_, err = tx.Exec(`DELETE FROM chunks WHERE file_path = ?`, filePath)
	if err != nil {
		return fmt.Errorf("delete chunks: %w", err)
	}

	// Delete file record.
	_, err = tx.Exec(`DELETE FROM files WHERE path = ?`, filePath)
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}

	return tx.Commit()
}

// Search performs a KNN vector search and returns the closest chunks.
// If kindFilter is non-empty, results are filtered to only that kind.
// The approach: query vec_chunks for KNN matches (over-fetching when kind
// filter is applied), join to chunks, filter by kind, and limit.
func (s *Store) Search(queryVec []float32, limit int, kindFilter string) ([]SearchResult, error) {
	blob, err := sqlite_vec.SerializeFloat32(queryVec)
	if err != nil {
		return nil, fmt.Errorf("serialize query: %w", err)
	}

	// When filtering by kind, over-fetch from vec_chunks since the virtual
	// table cannot filter by kind itself.
	k := limit
	if kindFilter != "" {
		k = limit * 3
		if k < 10 {
			k = 10
		}
	}

	query := `
		SELECT c.file_path, c.symbol, c.kind, c.start_line, c.end_line, v.distance
		FROM vec_chunks v
		JOIN chunks c ON v.id = c.id
		WHERE v.embedding MATCH ?
		AND v.k = ?
		ORDER BY v.distance
	`
	args := []any{blob, k}

	if kindFilter != "" {
		// Wrap the vec query and filter by kind.
		query = fmt.Sprintf(`
			SELECT file_path, symbol, kind, start_line, end_line, distance
			FROM (%s) sub
			WHERE kind = ?
			LIMIT ?
		`, query)
		args = append(args, kindFilter, limit)
	} else {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.FilePath, &r.Symbol, &r.Kind, &r.StartLine, &r.EndLine, &r.Distance); err != nil {
			return nil, fmt.Errorf("scan result: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return results, nil
}

// GetFileHashes returns a map of file path to content hash for all tracked files.
func (s *Store) GetFileHashes() (map[string]string, error) {
	rows, err := s.db.Query("SELECT path, hash FROM files")
	if err != nil {
		return nil, fmt.Errorf("query files: %w", err)
	}
	defer rows.Close()

	hashes := make(map[string]string)
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			return nil, fmt.Errorf("scan file: %w", err)
		}
		hashes[path] = hash
	}
	return hashes, rows.Err()
}

// Stats returns aggregate statistics about the store contents.
func (s *Store) Stats() (StoreStats, error) {
	var stats StoreStats
	if err := s.db.QueryRow("SELECT count(*) FROM files").Scan(&stats.TotalFiles); err != nil {
		return stats, fmt.Errorf("count files: %w", err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM chunks").Scan(&stats.TotalChunks); err != nil {
		return stats, fmt.Errorf("count chunks: %w", err)
	}
	return stats, nil
}

// DeleteAll removes all data from all tables. Useful when the embedding model
// changes and all vectors must be recomputed.
func (s *Store) DeleteAll() error {
	stmts := []string{
		"DELETE FROM vec_chunks",
		"DELETE FROM chunks",
		"DELETE FROM files",
		"DELETE FROM project_meta",
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt, err)
		}
	}
	return nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}
