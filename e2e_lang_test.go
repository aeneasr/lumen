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

//go:build e2e

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bradleyjkemp/cupaloy/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var snapshotter = cupaloy.New(
	cupaloy.EnvVariableName("UPDATE_SNAPSHOTS"),
	cupaloy.SnapshotSubdirectory("testdata/snapshots"),
)

func fixturesPath(lang string) string {
	p, _ := filepath.Abs(filepath.Join("testdata", "fixtures", lang))
	return p
}

// startLangServer starts the MCP server with a reduced max chunk token size
// suitable for all-minilm's 512-token context window.
func startLangServer(t *testing.T) *mcp.ClientSession {
	t.Helper()
	dataHome := t.TempDir()
	ollamaHost := "http://localhost:11434"
	if h := os.Getenv("OLLAMA_HOST"); h != "" {
		ollamaHost = h
	}

	cmd := exec.Command(serverBinary, "stdio")
	cmd.Env = []string{
		"OLLAMA_HOST=" + ollamaHost,
		"LUMEN_EMBED_MODEL=all-minilm",
		"LUMEN_MAX_CHUNK_TOKENS=200",
		"XDG_DATA_HOME=" + dataHome,
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
	}

	transport := &mcp.CommandTransport{Command: cmd}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "e2e-lang-test-client",
		Version: "0.1.0",
	}, nil)

	ctx := context.Background()
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("failed to connect to server: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func langSearch(t *testing.T, session *mcp.ClientSession, lang, query string) string {
	t.Helper()
	dir := fixturesPath(lang)
	out := callSearch(t, session, map[string]any{
		"query":     query,
		"path":      dir,
		"limit":     30,
		"min_score": -1.0,
	})
	var b strings.Builder
	fmt.Fprintf(&b, "results: %d\n", len(out.Results))
	for _, r := range out.Results {
		fmt.Fprintf(&b, "%s:%d-%d  %s (%s)\n", r.FilePath, r.StartLine, r.EndLine, r.Symbol, r.Kind)
	}
	return b.String()
}

func runLangTest(t *testing.T, lang string, queries []string) {
	t.Helper()
	session := startLangServer(t)
	for _, q := range queries {
		q := q
		t.Run(strings.ReplaceAll(q, " ", "_"), func(t *testing.T) {
			result := langSearch(t, session, lang, q)
			snapshotter.SnapshotT(t, result)
		})
	}
}

func TestLang_Go(t *testing.T) {
	runLangTest(t, "go", []string{
		"HTTP request handler",
		"authentication token validation",
		"database connection pool",
		"time series storage and retrieval",
		"error handling and logging",
		"configuration loading from file",
	})
}

func TestLang_Java(t *testing.T) {
	runLangTest(t, "java", []string{
		"pet owner repository database",
		"REST controller request mapping",
		"JPA entity model fields",
		"Spring service dependency injection",
		"form input validation",
	})
}

func TestLang_PHP(t *testing.T) {
	runLangTest(t, "php", []string{
		"HTTP request handling middleware",
		"database query builder",
		"authentication guard session",
		"collection helper methods",
		"model relationships",
	})
}

func TestLang_JavaScript(t *testing.T) {
	runLangTest(t, "js", []string{
		"HTTP router middleware",
		"request response object",
		"event emitter listener",
		"error handling exception",
		"module exports function",
	})
}

func TestLang_TypeScript(t *testing.T) {
	runLangTest(t, "ts", []string{
		"event listener registration",
		"async operation with cancellation",
		"URI path manipulation",
		"lifecycle disposable resource",
		"platform detection operating system",
	})
}

func TestLang_Ruby(t *testing.T) {
	runLangTest(t, "ruby", []string{
		"route matching URL",
		"controller action rendering",
		"database record query scope",
		"authentication callback filter",
		"module include concern",
	})
}

func TestLang_Python(t *testing.T) {
	runLangTest(t, "python", []string{
		"HTTP route handler decorator",
		"database model query filter",
		"authentication login view",
		"request context middleware",
		"exception error handling",
	})
}

func TestLang_Rust(t *testing.T) {
	runLangTest(t, "rust", []string{
		"async runtime executor spawn",
		"file search pattern match",
		"TCP network listener accept",
		"mutex lock concurrent access",
		"error result propagation",
	})
}

func TestLang_YAML(t *testing.T) {
	runLangTest(t, "yaml", []string{
		"Kubernetes deployment replicas",
		"CI pipeline build steps",
		"service port configuration",
		"environment variables secrets",
	})
}

func TestLang_Markdown(t *testing.T) {
	runLangTest(t, "md", []string{
		"installation setup guide",
		"ownership borrowing memory",
		"error handling panic recover",
		"async concurrent threads",
	})
}

func TestLang_JSON(t *testing.T) {
	runLangTest(t, "json", []string{
		"build scripts commands",
		"TypeScript compiler options",
		"package dependencies versions",
		"API paths endpoints",
	})
}
