package capture

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const usageFixtureDir = "testdata/usage"

// TestUsageBreakdownFixtures runs each runner fixture through StreamUsage
// and compares the usage-breakdown value with the hand-written expected
// breakdown next to it.
func TestUsageBreakdownFixtures(t *testing.T) {
	cases := []struct {
		runner, version, scenario, model string
	}{
		{"claude-code", "2.1.319", "multi-turn-subagent", "opus"},
		{"claude-code", "2.1.319", "repeated-result", "opus"},
		{"claude-code", "2.1.319", "interrupted", "opus"},
		{"claude-code", "2.1.319", "usage-after-result", "opus"},
		{"claude-code", "2.1.319", "unknown-model-error", ""},
		{"claude-code", "2.1.319", "auth-failure-no-api-call", "haiku"},
		{"codex", "0.167.0", "session-basic", "gpt-5.5-codex"},
		{"codex", "0.167.0", "cumulative-turns", "gpt-5.5-codex"},
		{"codex", "0.167.0", "failed-turn", "gpt-5.5-codex"},
		{"codex", "0.167.0", "interrupted", "gpt-5.5-codex"},
		{"codex", "0.167.0", "subagent-reroute", ""},
		{"opencode", "1.19.5", "multi-step", "anthropic/claude-sonnet-5-5"},
		{"opencode", "1.19.5", "subagent-abort", ""},
		{"opencode", "1.19.5", "no-usage", "anthropic/claude-sonnet-5-5"},
	}
	for _, tc := range cases {
		t.Run(tc.runner+"/"+tc.scenario, func(t *testing.T) {
			t.Setenv(envModel, tc.model)
			dir := filepath.Join(usageFixtureDir, tc.runner, tc.version)
			input := readFixture(t, filepath.Join(dir, tc.scenario+".jsonl"))
			want := decodeJSON(t, readFixture(t, filepath.Join(dir, tc.scenario+".json")))

			var out bytes.Buffer
			usage, _ := StreamUsage(tc.runner, bytes.NewReader(input), &out)
			raw, ok := usage[usageBreakdownKey]
			if !ok {
				t.Fatalf("usage has no %q key: %v", usageBreakdownKey, usage)
			}
			if strings.Contains(raw, "\n") {
				t.Fatalf("breakdown spans several lines: %q", raw)
			}
			got := decodeJSON(t, []byte(raw))
			if !reflect.DeepEqual(got, want) {
				t.Errorf("breakdown mismatch\n got: %s\nwant: %s", raw, compactJSON(t, want))
			}
		})
	}
}

func TestUsageBreakdownOnlyForNormalizedRunners(t *testing.T) {
	line := `{"type":"result","total_cost_usd":0.1,"usage":{"input_tokens":1,"output_tokens":2},"stats":{"inputTokens":1,"outputTokens":2}}` + "\n"
	for _, agent := range []string{"gemini", "cursor"} {
		var out bytes.Buffer
		usage, err := StreamUsage(agent, strings.NewReader(line), &out)
		if err != nil {
			t.Fatalf("%s: StreamUsage() error = %v", agent, err)
		}
		if _, ok := usage[usageBreakdownKey]; ok {
			t.Errorf("%s: usage has %q, want none for an agent Kelos does not normalize", agent, usageBreakdownKey)
		}
	}
}

func TestUsageBreakdownEmptyStreamIsMissing(t *testing.T) {
	want := map[string]string{
		"claude-code": `{"schemaVersion":1,"runner":"claude-code","coverage":"Missing","notes":["no-final-result"]}`,
		"codex":       `{"schemaVersion":1,"runner":"codex","coverage":"Missing","notes":["no-final-usage"]}`,
		"opencode":    `{"schemaVersion":1,"runner":"opencode","coverage":"Missing","notes":["no-step-usage"]}`,
	}
	for agent, expected := range want {
		t.Setenv(envModel, "provider/model")
		var out bytes.Buffer
		usage, _ := StreamUsage(agent, strings.NewReader(""), &out)
		if got := usage[usageBreakdownKey]; got != expected {
			t.Errorf("%s: breakdown = %s, want %s", agent, got, expected)
		}
	}
}

func TestDecimalSumIsExact(t *testing.T) {
	var d decimalSum
	for _, v := range []string{"0.1", "0.2", "1.2e-05"} {
		if !d.add(v) {
			t.Fatalf("add(%q) = false", v)
		}
	}
	if got, want := d.String(), "0.300012"; got != want {
		t.Errorf("sum = %s, want %s", got, want)
	}
	if d.add("-1") {
		t.Error("add(-1) = true, want a negative cost refused")
	}
	if d.add("abc") {
		t.Error("add(abc) = true, want a non-number refused")
	}
	var empty decimalSum
	if got := empty.String(); got != "" {
		t.Errorf("empty sum = %q, want empty (unknown, not zero)", got)
	}
}

func TestClaudeExponentCostBecomesPlainDecimal(t *testing.T) {
	line := `{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"s","total_cost_usd":1.2e-7,"modelUsage":{"claude-haiku-5-5":{"inputTokens":1,"outputTokens":0,"costUSD":1.2e-7}}}` + "\n"
	var out bytes.Buffer
	usage, _ := StreamUsage("claude-code", strings.NewReader(line), &out)
	want := `{"schemaVersion":1,"runner":"claude-code","sessionID":"s","final":true,"coverage":"Complete","includesSubagents":true,"models":[{"model":"claude-haiku-5-5","modelSource":"Reported","uncachedInputTokens":1,"outputTokens":0,"reportedCostUSD":"0.00000012"}],"reportedCostUSD":"0.00000012","costSource":"claude-code"}`
	if got := usage[usageBreakdownKey]; got != want {
		t.Errorf("breakdown = %s, want %s", got, want)
	}
}

func TestOpencodeNegativeCostIsInconsistent(t *testing.T) {
	t.Setenv(envModel, "openai/gpt-5.5")
	line := `{"type":"step_finish","sessionID":"s","part":{"id":"p1","sessionID":"s","reason":"stop","cost":-0.5,"tokens":{"input":1,"output":2,"reasoning":0,"cache":{"read":0,"write":0}}}}` + "\n"
	var out bytes.Buffer
	usage, _ := StreamUsage("opencode", strings.NewReader(line), &out)
	want := `{"schemaVersion":1,"runner":"opencode","sessionID":"s","final":true,"coverage":"Partial","notes":["inconsistent-counters"],"models":[{"model":"gpt-5.5","modelSource":"Configured","provider":"openai","uncachedInputTokens":1,"cacheReadInputTokens":0,"cacheWriteInputTokens":0,"outputTokens":2,"reasoningOutputTokens":0}]}`
	if got := usage[usageBreakdownKey]; got != want {
		t.Errorf("breakdown = %s, want %s", got, want)
	}
}

func TestCodexCachedAboveInputIsInconsistent(t *testing.T) {
	t.Setenv(envModel, "gpt-5.5")
	line := `{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":30,"output_tokens":1,"reasoning_output_tokens":0}}` + "\n"
	var out bytes.Buffer
	usage, _ := StreamUsage("codex", strings.NewReader(line), &out)
	want := `{"schemaVersion":1,"runner":"codex","final":true,"coverage":"Partial","notes":["inconsistent-counters"],"models":[{"model":"gpt-5.5","modelSource":"Configured","uncachedInputTokens":0,"cacheReadInputTokens":30,"outputTokens":1,"reasoningOutputTokens":0,"webSearchRequests":0}]}`
	if got := usage[usageBreakdownKey]; got != want {
		t.Errorf("breakdown = %s, want %s", got, want)
	}
}

func TestClaudeResultWithoutModelUsageIsMissing(t *testing.T) {
	line := `{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"s","total_cost_usd":0.5,"usage":{"input_tokens":1,"output_tokens":2}}` + "\n"
	var out bytes.Buffer
	usage, _ := StreamUsage("claude-code", strings.NewReader(line), &out)
	want := `{"schemaVersion":1,"runner":"claude-code","sessionID":"s","final":true,"coverage":"Missing","notes":["model-usage-absent"],"includesSubagents":true,"reportedCostUSD":"0.5","costSource":"claude-code"}`
	if got := usage[usageBreakdownKey]; got != want {
		t.Errorf("breakdown = %s, want %s", got, want)
	}
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func decodeJSON(t *testing.T, data []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	return v
}

func compactJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(data)
}
