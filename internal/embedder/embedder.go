// Package embedder defines the interface for generating embedding vectors from text.
package embedder

import "context"

// Embedder converts text chunks into vector embeddings.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dimensions() int
	ModelName() string
}
