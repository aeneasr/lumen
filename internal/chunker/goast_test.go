package chunker

import (
	"testing"
)

const testSource = `// Package example provides test fixtures.
package example

import "fmt"

// Hello prints a greeting.
func Hello(name string) {
	fmt.Println("hello", name)
}

// Greeter defines a greeting interface.
type Greeter interface {
	Greet(name string) string
}

// Server handles requests.
type Server struct {
	Port int
	Host string
}

// Start launches the server.
func (s *Server) Start() error {
	return nil
}

// MaxRetries is the max retry count.
const MaxRetries = 3

// DefaultHost is the default hostname.
var DefaultHost = "localhost"
`

func TestGoASTChunker_ChunkFunctions(t *testing.T) {
	c := NewGoAST()
	chunks, err := c.Chunk("example.go", []byte(testSource))
	if err != nil {
		t.Fatal(err)
	}

	found := findChunk(chunks, "function", "Hello")
	if found == nil {
		t.Fatal("expected to find function Hello")
	}
	if found.Content == "" {
		t.Fatal("expected non-empty content")
	}
}

func TestGoASTChunker_ChunkMethods(t *testing.T) {
	c := NewGoAST()
	chunks, err := c.Chunk("example.go", []byte(testSource))
	if err != nil {
		t.Fatal(err)
	}

	found := findChunk(chunks, "method", "Server.Start")
	if found == nil {
		t.Fatal("expected to find method Server.Start")
	}
}

func TestGoASTChunker_ChunkTypes(t *testing.T) {
	c := NewGoAST()
	chunks, err := c.Chunk("example.go", []byte(testSource))
	if err != nil {
		t.Fatal(err)
	}

	found := findChunk(chunks, "type", "Server")
	if found == nil {
		t.Fatal("expected to find type Server")
	}
}

func TestGoASTChunker_ChunkInterfaces(t *testing.T) {
	c := NewGoAST()
	chunks, err := c.Chunk("example.go", []byte(testSource))
	if err != nil {
		t.Fatal(err)
	}

	found := findChunk(chunks, "interface", "Greeter")
	if found == nil {
		t.Fatal("expected to find interface Greeter")
	}
}

func TestGoASTChunker_ChunkConstsAndVars(t *testing.T) {
	c := NewGoAST()
	chunks, err := c.Chunk("example.go", []byte(testSource))
	if err != nil {
		t.Fatal(err)
	}

	if findChunk(chunks, "const", "MaxRetries") == nil {
		t.Fatal("expected to find const MaxRetries")
	}
	if findChunk(chunks, "var", "DefaultHost") == nil {
		t.Fatal("expected to find var DefaultHost")
	}
}

func TestGoASTChunker_ChunkIncludesDocComment(t *testing.T) {
	c := NewGoAST()
	chunks, err := c.Chunk("example.go", []byte(testSource))
	if err != nil {
		t.Fatal(err)
	}

	found := findChunk(chunks, "function", "Hello")
	if found == nil {
		t.Fatal("expected function Hello")
	}
	if found.Content == "" {
		t.Fatal("expected non-empty content")
	}
	if !containsSubstring(found.Content, "Hello prints a greeting") {
		t.Fatalf("expected doc comment in content, got:\n%s", found.Content)
	}
}

func TestGoASTChunker_ChunkIDsDeterministic(t *testing.T) {
	c := NewGoAST()
	chunks1, _ := c.Chunk("example.go", []byte(testSource))
	chunks2, _ := c.Chunk("example.go", []byte(testSource))

	if len(chunks1) != len(chunks2) {
		t.Fatal("chunk counts differ")
	}
	for i := range chunks1 {
		if chunks1[i].ID != chunks2[i].ID {
			t.Fatalf("chunk %d IDs differ: %s vs %s", i, chunks1[i].ID, chunks2[i].ID)
		}
	}
}

func TestGoASTChunker_PackageDocChunk(t *testing.T) {
	c := NewGoAST()
	chunks, err := c.Chunk("example.go", []byte(testSource))
	if err != nil {
		t.Fatal(err)
	}

	found := findChunk(chunks, "package", "package example")
	if found == nil {
		t.Fatal("expected package doc chunk")
	}
}

func findChunk(chunks []Chunk, kind, symbol string) *Chunk {
	for i := range chunks {
		if chunks[i].Kind == kind && chunks[i].Symbol == symbol {
			return &chunks[i]
		}
	}
	return nil
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
