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

// ModelSpec describes the known configuration for an embedding model.
type ModelSpec struct {
	Dims      int
	CtxLength int
	Backend   string // "ollama", "lmstudio", or "" for both
}

// DefaultOllamaModel is the default model when using the Ollama backend.
const DefaultOllamaModel = "ordis/jina-embeddings-v2-base-code"

// DefaultLMStudioModel is the default model when using the LM Studio backend.
const DefaultLMStudioModel = "nomic-ai/nomic-embed-code-GGUF"

// DefaultModel is an alias for DefaultOllamaModel for backward compatibility.
const DefaultModel = DefaultOllamaModel

// ModelAliases maps alternative model names to their canonical names.
// LM Studio exposes some models under different names than their repository ID.
var ModelAliases = map[string]string{
	"text-embedding-nomic-embed-code": "nomic-ai/nomic-embed-code-GGUF",
}

// KnownModels maps model names to their specifications.
var KnownModels = map[string]ModelSpec{
	"ordis/jina-embeddings-v2-base-code": {Dims: 768, CtxLength: 8192, Backend: "ollama"},
	"nomic-embed-text":                   {Dims: 768, CtxLength: 8192, Backend: "ollama"},
	"nomic-ai/nomic-embed-code-GGUF":     {Dims: 3584, CtxLength: 8192, Backend: "lmstudio"},
	"qwen3-embedding:8b":                 {Dims: 4096, CtxLength: 40960, Backend: "ollama"},
	"qwen3-embedding:4b":                 {Dims: 2560, CtxLength: 40960, Backend: "ollama"},
	"qwen3-embedding:0.6b":               {Dims: 1024, CtxLength: 32768, Backend: "ollama"},
	"all-minilm":                         {Dims: 384, CtxLength: 512, Backend: "ollama"},
}
