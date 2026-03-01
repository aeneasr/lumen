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
	"slices"
	"time"

	"github.com/sethvargo/go-retry"
)

// LMStudio implements the Embedder interface using an LM Studio server
// that exposes an OpenAI-compatible /v1/embeddings endpoint.
type LMStudio struct {
	model      string
	dimensions int
	baseURL    string
	client     *http.Client
}

// NewLMStudio creates a new LMStudio embedder.
// baseURL is the LM Studio server URL (e.g. "http://localhost:1234").
func NewLMStudio(model string, dimensions int, baseURL string) (*LMStudio, error) {
	return &LMStudio{
		model:      model,
		dimensions: dimensions,
		baseURL:    baseURL,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}, nil
}

// Dimensions returns the embedding vector dimensionality.
func (l *LMStudio) Dimensions() int {
	return l.dimensions
}

// ModelName returns the model name used for embeddings.
func (l *LMStudio) ModelName() string {
	return l.model
}

// lmstudioEmbedRequest is the JSON body sent to /v1/embeddings.
type lmstudioEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// lmstudioEmbedItem is a single embedding item in the response.
type lmstudioEmbedItem struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

// lmstudioEmbedResponse is the JSON body returned from /v1/embeddings.
type lmstudioEmbedResponse struct {
	Data []lmstudioEmbedItem `json:"data"`
}

// Embed converts texts into embedding vectors, splitting into batches of 32.
func (l *LMStudio) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	var allVecs [][]float32
	for i := 0; i < len(texts); i += embedBatchSize {
		batch := texts[i:min(i+embedBatchSize, len(texts))]

		vecs, err := l.embedBatch(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("embedding batch starting at %d: %w", i, err)
		}
		allVecs = append(allVecs, vecs...)
	}

	return allVecs, nil
}

// embedBatch sends a single batch of texts to the LM Studio /v1/embeddings endpoint.
// Retries up to embedMaxRetries times on transient errors (5xx, network failures),
// respecting context cancellation between attempts.
func (l *LMStudio) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	bodyBytes, err := json.Marshal(lmstudioEmbedRequest{
		Model: l.model,
		Input: texts,
	})
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	b := retry.NewExponential(100 * time.Millisecond)

	var embedResp lmstudioEmbedResponse
	err = retry.Do(ctx, retry.WithMaxRetries(embedMaxRetries-1, b), func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.baseURL+"/v1/embeddings", bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := l.client.Do(req)
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
		return nil, fmt.Errorf("lmstudio embed: %w", err)
	}

	// Sort by index — OpenAI spec allows out-of-order responses.
	slices.SortFunc(embedResp.Data, func(a, b lmstudioEmbedItem) int {
		return a.Index - b.Index
	})

	vecs := make([][]float32, len(embedResp.Data))
	for i, item := range embedResp.Data {
		vecs[i] = item.Embedding
	}
	return vecs, nil
}
