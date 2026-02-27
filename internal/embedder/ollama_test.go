package embedder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type mockOllamaResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Model      string      `json:"model"`
}

func TestOllamaEmbedder_Embed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		resp := mockOllamaResponse{
			Model: "nomic-embed-text",
			Embeddings: [][]float32{
				{0.1, 0.2, 0.3, 0.4},
				{0.5, 0.6, 0.7, 0.8},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	e, err := NewOllama("nomic-embed-text", 4, server.URL)
	if err != nil {
		t.Fatal(err)
	}

	vecs, err := e.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vecs))
	}
	if len(vecs[0]) != 4 {
		t.Fatalf("expected 4 dimensions, got %d", len(vecs[0]))
	}
}

func TestOllamaEmbedder_Dimensions(t *testing.T) {
	e, _ := NewOllama("nomic-embed-text", 1024, "http://localhost:11434")
	if e.Dimensions() != 1024 {
		t.Fatalf("expected 1024, got %d", e.Dimensions())
	}
}

func TestOllamaEmbedder_ModelName(t *testing.T) {
	e, _ := NewOllama("nomic-embed-text", 1024, "http://localhost:11434")
	if e.ModelName() != "nomic-embed-text" {
		t.Fatalf("expected nomic-embed-text, got %s", e.ModelName())
	}
}

func TestOllamaEmbedder_Batching(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		input := req["input"].([]any)

		embeddings := make([][]float32, len(input))
		for i := range input {
			embeddings[i] = []float32{0.1, 0.2, 0.3, 0.4}
		}
		resp := mockOllamaResponse{Model: "test", Embeddings: embeddings}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	e, _ := NewOllama("test", 4, server.URL)
	texts := make([]string, 50)
	for i := range texts {
		texts[i] = "text"
	}

	vecs, err := e.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 50 {
		t.Fatalf("expected 50 vectors, got %d", len(vecs))
	}
	if callCount != 2 {
		t.Fatalf("expected 2 batch calls (32+18), got %d", callCount)
	}
}

func TestOllamaEmbedder_ErrorHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	e, _ := NewOllama("test", 4, server.URL)
	_, err := e.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestOllama_Embed_ContextCancelledStopsRetry(t *testing.T) {
	// Server always returns 500 to force retry attempts.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	emb, _ := NewOllama("test", 4, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before any request

	start := time.Now()
	_, err := emb.Embed(ctx, []string{"hello"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	// With context-aware backoff, should return almost immediately.
	// The old time.Sleep ignores context — this test catches the regression.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected fast failure on pre-cancelled context, took %v", elapsed)
	}
}
