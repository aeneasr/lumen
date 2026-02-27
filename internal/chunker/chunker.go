// Package chunker splits source files into semantically meaningful code chunks.
package chunker

// Chunk represents a semantically meaningful piece of source code.
type Chunk struct {
	ID        string // deterministic: sha256(filePath + symbol + startLine)[:16]
	FilePath  string // relative to project root
	Symbol    string // "FuncName", "TypeName.MethodName"
	Kind      string // "function", "method", "type", "interface", "const", "var", "package"
	StartLine int
	EndLine   int
	Content   string // raw source text, used for embedding
}

// Chunker splits source files into semantically meaningful chunks.
type Chunker interface {
	Chunk(filePath string, content []byte) ([]Chunk, error)
}
