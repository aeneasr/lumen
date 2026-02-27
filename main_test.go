package main

import (
	"context"
	"sync"
	"testing"
)

func TestIndexerCache_ConcurrentReads(t *testing.T) {
	ic := &indexerCache{embedder: &stubEmbedder{}}

	const goroutines = 20
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Path doesn't exist on disk — getOrCreate will error, that's fine.
			// We're testing there's no data race on the cache map/mutex.
			ic.getOrCreate("/nonexistent/path/for/race/test")
		}()
	}
	wg.Wait()
}

// stubEmbedder satisfies embedder.Embedder for tests.
type stubEmbedder struct{}

func (s *stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i := range vecs {
		vecs[i] = []float32{0.1, 0.2, 0.3, 0.4}
	}
	return vecs, nil
}
func (s *stubEmbedder) Dimensions() int   { return 4 }
func (s *stubEmbedder) ModelName() string { return "stub" }
