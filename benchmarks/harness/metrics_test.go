package harness

import (
	"strings"
	"testing"
)

func TestMetricsCollector_ResultLine(t *testing.T) {
	input := `{"type":"result","total_cost_usd":0.1234,"duration_ms":5000,"duration_api_ms":4500,"num_turns":3,"usage":{"input_tokens":10000,"output_tokens":2000,"cache_read_input_tokens":500,"cache_creation_input_tokens":100}}`

	mc := NewMetricsCollector()
	if err := mc.ProcessStream(strings.NewReader(input), nil); err != nil {
		t.Fatal(err)
	}

	m := mc.Metrics()
	if m.CostUSD != 0.1234 {
		t.Errorf("CostUSD: got %f, want 0.1234", m.CostUSD)
	}
	if m.DurationMS != 5000 {
		t.Errorf("DurationMS: got %d, want 5000", m.DurationMS)
	}
	if m.DurationAPIMS != 4500 {
		t.Errorf("DurationAPIMS: got %d, want 4500", m.DurationAPIMS)
	}
	if m.NumTurns != 3 {
		t.Errorf("NumTurns: got %d, want 3", m.NumTurns)
	}
	if m.InputTokens != 10000 {
		t.Errorf("InputTokens: got %d, want 10000", m.InputTokens)
	}
	if m.OutputTokens != 2000 {
		t.Errorf("OutputTokens: got %d, want 2000", m.OutputTokens)
	}
	if m.CacheReadTokens != 500 {
		t.Errorf("CacheReadTokens: got %d, want 500", m.CacheReadTokens)
	}
}

func TestMetricsCollector_ToolCalls(t *testing.T) {
	lines := []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Grep"},{"type":"tool_use","name":"Read"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Grep"},{"type":"tool_use","name":"mcp__lumen__semantic_search"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`,
	}

	mc := NewMetricsCollector()
	if err := mc.ProcessStream(strings.NewReader(strings.Join(lines, "\n")), nil); err != nil {
		t.Fatal(err)
	}

	m := mc.Metrics()
	if m.ToolCalls["Grep"] != 2 {
		t.Errorf("Grep calls: got %d, want 2", m.ToolCalls["Grep"])
	}
	if m.ToolCalls["Read"] != 1 {
		t.Errorf("Read calls: got %d, want 1", m.ToolCalls["Read"])
	}
	if m.ToolCalls["mcp__lumen__semantic_search"] != 1 {
		t.Errorf("semantic_search calls: got %d, want 1", m.ToolCalls["mcp__lumen__semantic_search"])
	}
}

func TestMetricsCollector_EmptyStream(t *testing.T) {
	mc := NewMetricsCollector()
	if err := mc.ProcessStream(strings.NewReader(""), nil); err != nil {
		t.Fatal(err)
	}

	m := mc.Metrics()
	if m.CostUSD != 0 {
		t.Errorf("expected zero cost for empty stream")
	}
	if len(m.ToolCalls) != 0 {
		t.Errorf("expected no tool calls for empty stream")
	}
}

func TestMetricsCollector_InvalidJSON(t *testing.T) {
	input := "not json\n{\"type\":\"result\",\"total_cost_usd\":0.5}\n"

	mc := NewMetricsCollector()
	if err := mc.ProcessStream(strings.NewReader(input), nil); err != nil {
		t.Fatal(err)
	}

	m := mc.Metrics()
	if m.CostUSD != 0.5 {
		t.Errorf("CostUSD: got %f, want 0.5", m.CostUSD)
	}
}

func TestMetricsCollector_RawOutput(t *testing.T) {
	input := `{"type":"result","total_cost_usd":0.1}`
	var buf strings.Builder

	mc := NewMetricsCollector()
	if err := mc.ProcessStream(strings.NewReader(input), &buf); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), `"type":"result"`) {
		t.Errorf("raw output should contain the original line")
	}
}
