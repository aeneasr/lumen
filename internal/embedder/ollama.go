// Copyright 2026 Aeneas Rekkas
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sethvargo/go-retry"
)

// Ollama implements the Embedder interface using a local Ollama server.
type Ollama struct {
	model         string
	dimensions    int
	contextLength int
	baseURL       string
	client        *http.Client
}

// NewOllama creates a new Ollama embedder that calls the /api/embed endpoint.
// contextLength sets num_ctx in Ollama requests; 0 means use Ollama's default.
func NewOllama(model string, dimensions int, contextLength int, baseURL string) (*Ollama, error) {
	return &Ollama{
		model:         model,
		dimensions:    dimensions,
		contextLength: contextLength,
		baseURL:       baseURL,
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
	Model   string         `json:"model"`
	Input   []string       `json:"input"`
	Options map[string]any `json:"options,omitempty"`
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
	for i := 0; i < len(texts); i += embedBatchSize {
		batch := texts[i:min(i+embedBatchSize, len(texts))]

		vecs, err := o.embedBatch(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("embedding batch starting at %d: %w", i, err)
		}
		allVecs = append(allVecs, vecs...)
	}

	return allVecs, nil
}

// embedBatch sends a single batch of texts to the Ollama /api/embed endpoint.
// Retries up to embedMaxRetries times on transient errors (5xx, network failures),
// respecting context cancellation between attempts.
func (o *Ollama) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := ollamaEmbedRequest{
		Model: o.model,
		Input: texts,
	}
	if o.contextLength > 0 {
		reqBody.Options = map[string]any{"num_ctx": o.contextLength}
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	b := retry.NewExponential(100 * time.Millisecond)

	var embedResp ollamaEmbedResponse
	err = retry.Do(ctx, retry.WithMaxRetries(embedMaxRetries-1, b), func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embed", bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := o.client.Do(req)
		if err != nil {
			return retry.RetryableError(fmt.Errorf("request failed: %w", err))
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode >= 500 {
			return retry.RetryableError(fmt.Errorf("server error: status %d", resp.StatusCode))
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
		}
		if readErr != nil {
			return fmt.Errorf("reading response body: %w", readErr)
		}

		return json.Unmarshal(body, &embedResp)
	})
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}

	return embedResp.Embeddings, nil
}
