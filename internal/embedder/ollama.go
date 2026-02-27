package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	ollamaBatchSize = 32
	ollamaMaxRetries = 3
)

// Ollama implements the Embedder interface using a local Ollama server.
type Ollama struct {
	model      string
	dimensions int
	baseURL    string
	client     *http.Client
}

// NewOllama creates a new Ollama embedder that calls the /api/embed endpoint.
func NewOllama(model string, dimensions int, baseURL string) (*Ollama, error) {
	return &Ollama{
		model:      model,
		dimensions: dimensions,
		baseURL:    baseURL,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}, nil
}

// Dimensions returns the embedding vector dimensionality.
func (o *Ollama) Dimensions() int {
	return o.dimensions
}

// ModelName returns the Ollama model name used for embeddings.
func (o *Ollama) ModelName() string {
	return o.model
}

// ollamaEmbedRequest is the JSON body sent to /api/embed.
type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// ollamaEmbedResponse is the JSON body returned from /api/embed.
type ollamaEmbedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed converts texts into embedding vectors, splitting into batches of 32.
func (o *Ollama) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	var allVecs [][]float32
	for i := 0; i < len(texts); i += ollamaBatchSize {
		end := i + ollamaBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[i:end]

		vecs, err := o.embedBatch(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("embedding batch %d-%d: %w", i, end, err)
		}
		allVecs = append(allVecs, vecs...)
	}

	return allVecs, nil
}

// embedBatch sends a single batch of texts to the Ollama /api/embed endpoint
// with retry logic for transient errors.
func (o *Ollama) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := ollamaEmbedRequest{
		Model: o.model,
		Input: texts,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	var lastErr error
	for attempt := range ollamaMaxRetries {
		// Re-create the request for each attempt since the body reader is consumed.
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embed", bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := o.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			backoff(attempt)
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("server error: status %d", resp.StatusCode)
			backoff(attempt)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
		}
		if readErr != nil {
			return nil, fmt.Errorf("reading response body: %w", readErr)
		}

		var embedResp ollamaEmbedResponse
		if err := json.Unmarshal(body, &embedResp); err != nil {
			return nil, fmt.Errorf("decoding response: %w", err)
		}

		return embedResp.Embeddings, nil
	}

	return nil, fmt.Errorf("ollama embed failed after %d retries: %w", ollamaMaxRetries, lastErr)
}

// backoff sleeps for an exponentially increasing duration based on the attempt number.
func backoff(attempt int) {
	d := time.Duration(1<<uint(attempt)) * 100 * time.Millisecond
	time.Sleep(d)
}
