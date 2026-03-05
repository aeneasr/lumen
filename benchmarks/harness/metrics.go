package harness

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// RunMetrics holds all metrics collected from a single benchmark run.
type RunMetrics struct {
	CostUSD           float64        `json:"cost_usd"`
	DurationMS        int64          `json:"duration_ms"`
	DurationAPIMS     int64          `json:"duration_api_ms"`
	InputTokens       int64          `json:"input_tokens"`
	OutputTokens      int64          `json:"output_tokens"`
	CacheReadTokens   int64          `json:"cache_read_tokens"`
	CacheCreateTokens int64          `json:"cache_create_tokens"`
	NumTurns          int            `json:"num_turns"`
	ToolCalls         map[string]int `json:"tool_calls"`
}

// streamEvent represents a single line from claude --output-format stream-json.
type streamEvent struct {
	Type    string          `json:"type"`
	Subtype string          `json:"subtype,omitempty"`
	Message json.RawMessage `json:"message,omitempty"`

	// Result fields (type=result)
	TotalCostUSD  float64      `json:"total_cost_usd,omitempty"`
	DurationMS    int64        `json:"duration_ms,omitempty"`
	DurationAPIMS int64        `json:"duration_api_ms,omitempty"`
	NumTurns      int          `json:"num_turns,omitempty"`
	Result        string       `json:"result,omitempty"`
	IsError       bool         `json:"is_error,omitempty"`
	Usage         *streamUsage `json:"usage,omitempty"`
}

type streamUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

// messageContent is used to extract tool_use blocks from assistant messages.
type messageContent struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

// MetricsCollector accumulates metrics from stream-json events.
type MetricsCollector struct {
	metrics RunMetrics
}

// NewMetricsCollector creates a new collector.
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		metrics: RunMetrics{
			ToolCalls: make(map[string]int),
		},
	}
}

// ProcessStream reads stream-json lines from r and collects metrics.
// It also writes every line to rawOut for archival.
func (mc *MetricsCollector) ProcessStream(r io.Reader, rawOut io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB max line

	for scanner.Scan() {
		line := scanner.Text()

		if rawOut != nil {
			_, _ = io.WriteString(rawOut, line+"\n")
		}

		mc.processLine(line)
	}

	return scanner.Err()
}

func (mc *MetricsCollector) processLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}

	var evt streamEvent
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		return
	}

	switch evt.Type {
	case "result":
		mc.metrics.CostUSD = evt.TotalCostUSD
		mc.metrics.DurationMS = evt.DurationMS
		mc.metrics.DurationAPIMS = evt.DurationAPIMS
		mc.metrics.NumTurns = evt.NumTurns
		if evt.Usage != nil {
			mc.metrics.InputTokens = evt.Usage.InputTokens
			mc.metrics.OutputTokens = evt.Usage.OutputTokens
			mc.metrics.CacheReadTokens = evt.Usage.CacheReadInputTokens
			mc.metrics.CacheCreateTokens = evt.Usage.CacheCreationInputTokens
		}

	case "assistant":
		mc.extractToolCalls(evt.Message)
	}
}

func (mc *MetricsCollector) extractToolCalls(raw json.RawMessage) {
	if raw == nil {
		return
	}

	var msg messageContent
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	for _, block := range msg.Content {
		if block.Type == "tool_use" && block.Name != "" {
			mc.metrics.ToolCalls[block.Name]++
		}
	}
}

// Metrics returns the collected metrics.
func (mc *MetricsCollector) Metrics() RunMetrics {
	return mc.metrics
}
