package store

import (
	"testing"

	"github.com/foobar/agent-index-go/internal/chunker"
)

func TestNewStore_CreatesSchema(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	var count int
	err = s.db.QueryRow("SELECT count(*) FROM files").Scan(&count)
	if err != nil {
		t.Fatalf("files table missing: %v", err)
	}
	err = s.db.QueryRow("SELECT count(*) FROM chunks").Scan(&count)
	if err != nil {
		t.Fatalf("chunks table missing: %v", err)
	}
	err = s.db.QueryRow("SELECT count(*) FROM project_meta").Scan(&count)
	if err != nil {
		t.Fatalf("project_meta table missing: %v", err)
	}
}

func TestStore_SetGetMeta(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	if err := s.SetMeta("test_key", "test_value"); err != nil {
		t.Fatal(err)
	}
	val, err := s.GetMeta("test_key")
	if err != nil {
		t.Fatal(err)
	}
	if val != "test_value" {
		t.Fatalf("expected test_value, got %s", val)
	}
}

func TestStore_UpsertAndSearchVectors(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	if err := s.UpsertFile("main.go", "abc123"); err != nil {
		t.Fatal(err)
	}

	chunks := []chunker.Chunk{
		{ID: "chunk1", FilePath: "main.go", Symbol: "Hello", Kind: "function", StartLine: 1, EndLine: 5},
		{ID: "chunk2", FilePath: "main.go", Symbol: "World", Kind: "function", StartLine: 6, EndLine: 10},
	}
	vectors := [][]float32{
		{0.1, 0.2, 0.3, 0.4},
		{0.9, 0.8, 0.7, 0.6},
	}

	if err := s.InsertChunks(chunks, vectors); err != nil {
		t.Fatal(err)
	}

	query := []float32{0.1, 0.2, 0.3, 0.4}
	results, err := s.Search(query, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Symbol != "Hello" {
		t.Fatalf("expected Hello as closest, got %s", results[0].Symbol)
	}
}

func TestStore_DeleteFileChunks(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	if err := s.UpsertFile("main.go", "abc123"); err != nil {
		t.Fatal(err)
	}
	chunks := []chunker.Chunk{
		{ID: "c1", FilePath: "main.go", Symbol: "Hello", Kind: "function", StartLine: 1, EndLine: 5},
	}
	vectors := [][]float32{{0.1, 0.2, 0.3, 0.4}}
	if err := s.InsertChunks(chunks, vectors); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteFileChunks("main.go"); err != nil {
		t.Fatal(err)
	}

	results, err := s.Search([]float32{0.1, 0.2, 0.3, 0.4}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results after delete, got %d", len(results))
	}
}

func TestStore_GetFileHashes(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	if err := s.UpsertFile("a.go", "hash_a"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertFile("b.go", "hash_b"); err != nil {
		t.Fatal(err)
	}

	hashes, err := s.GetFileHashes()
	if err != nil {
		t.Fatal(err)
	}
	if len(hashes) != 2 {
		t.Fatalf("expected 2 file hashes, got %d", len(hashes))
	}
	if hashes["a.go"] != "hash_a" {
		t.Fatalf("expected hash_a, got %s", hashes["a.go"])
	}
}

func TestStore_Stats(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	if err := s.UpsertFile("main.go", "abc123"); err != nil {
		t.Fatal(err)
	}
	chunks := []chunker.Chunk{
		{ID: "c1", FilePath: "main.go", Symbol: "Hello", Kind: "function", StartLine: 1, EndLine: 5},
		{ID: "c2", FilePath: "main.go", Symbol: "World", Kind: "function", StartLine: 6, EndLine: 10},
	}
	vectors := [][]float32{
		{0.1, 0.2, 0.3, 0.4},
		{0.5, 0.6, 0.7, 0.8},
	}
	if err := s.InsertChunks(chunks, vectors); err != nil {
		t.Fatal(err)
	}

	stats, err := s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalFiles != 1 {
		t.Fatalf("expected 1 file, got %d", stats.TotalFiles)
	}
	if stats.TotalChunks != 2 {
		t.Fatalf("expected 2 chunks, got %d", stats.TotalChunks)
	}
}

func TestStore_Pragmas(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	var mode int
	if err := s.db.QueryRow("PRAGMA synchronous").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != 1 { // 1 = NORMAL
		t.Fatalf("expected synchronous=NORMAL(1), got %d", mode)
	}
}

func TestStore_ChunkIndexesExist(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	var count int
	err = s.db.QueryRow(
		`SELECT count(*) FROM sqlite_master
		 WHERE type='index' AND name = 'idx_chunks_file_path'`,
	).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 index, got %d", count)
	}
}

func TestStore_GetMetaBatch(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	_ = s.SetMeta("key1", "val1")
	_ = s.SetMeta("key2", "val2")

	vals, err := s.GetMetaBatch([]string{"key1", "key2", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if vals["key1"] != "val1" {
		t.Fatalf("expected val1, got %s", vals["key1"])
	}
	if vals["key2"] != "val2" {
		t.Fatalf("expected val2, got %s", vals["key2"])
	}
	if _, ok := vals["missing"]; ok {
		t.Fatal("expected missing key to be absent")
	}
}
