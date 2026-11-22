package capture

import (
	"encoding/json"
	"math/big"
	"os"
	"sort"
	"strings"
)

// usageBreakdownKey is the outputs key carrying the normalized usage as
// compact JSON. The controller decodes it into api/v1alpha2.UsageBreakdown,
// whose JSON field names the wire types below mirror.
const usageBreakdownKey = "usage-breakdown"

// breakdownSchemaVersion mirrors api/v1alpha2.UsageBreakdownSchemaVersion.
const breakdownSchemaVersion = 1

// costDecimalPlaces bounds the decimals of a summed cost: enough for a
// fraction of a cent per million tokens, without float rounding.
const costDecimalPlaces = 12

// Coverage values, mirroring api/v1alpha2.UsageCoverage.
const (
	coverageComplete = "Complete"
	coveragePartial  = "Partial"
	coverageMissing  = "Missing"
)

// Model sources, mirroring api/v1alpha2.ModelSource.
const (
	modelReported   = "Reported"
	modelConfigured = "Configured"
)

// Notes explaining a coverage below Complete or a gap in attribution.
const (
	noteNoFinalResult      = "no-final-result"
	noteNoFinalUsage       = "no-final-usage"
	noteNoStepUsage        = "no-step-usage"
	noteModelUsageAbsent   = "model-usage-absent"
	noteUsageAfterResult   = "usage-after-final-result"
	noteFailedTurn         = "failed-turn-usage-not-reported"
	noteFailedStep         = "failed-step-usage-not-reported"
	noteSubagentNotCounted = "subagent-usage-not-reported"
	noteModelNotReported   = "model-not-reported"
	noteModelRerouted      = "model-rerouted"
	noteInconsistent       = "inconsistent-counters"
)

// envModel is the model the Task asked for, set by the Kelos entrypoints.
const envModel = "KELOS_MODEL"

type usageBreakdown struct {
	SchemaVersion     int           `json:"schemaVersion"`
	Runner            string        `json:"runner,omitempty"`
	SessionID         string        `json:"sessionID,omitempty"`
	Final             bool          `json:"final,omitempty"`
	Coverage          string        `json:"coverage"`
	Notes             []string      `json:"notes,omitempty"`
	IncludesSubagents bool          `json:"includesSubagents,omitempty"`
	Subagents         []subagentRef `json:"subagents,omitempty"`
	Models            []modelUsage  `json:"models,omitempty"`
	ReportedCostUSD   string        `json:"reportedCostUSD,omitempty"`
	CostSource        string        `json:"costSource,omitempty"`
}

type subagentRef struct {
	ID            string `json:"id"`
	Model         string `json:"model,omitempty"`
	UsageIncluded bool   `json:"usageIncluded,omitempty"`
}

type modelUsage struct {
	Model                   string           `json:"model,omitempty"`
	ModelSource             string           `json:"modelSource,omitempty"`
	CanonicalModel          string           `json:"canonicalModel,omitempty"`
	Provider                string           `json:"provider,omitempty"`
	UncachedInputTokens     *int64           `json:"uncachedInputTokens,omitempty"`
	CacheReadInputTokens    *int64           `json:"cacheReadInputTokens,omitempty"`
	CacheWriteInputTokens   *int64           `json:"cacheWriteInputTokens,omitempty"`
	CacheWrite5mInputTokens *int64           `json:"cacheWrite5mInputTokens,omitempty"`
	CacheWrite1hInputTokens *int64           `json:"cacheWrite1hInputTokens,omitempty"`
	OutputTokens            *int64           `json:"outputTokens,omitempty"`
	ReasoningOutputTokens   *int64           `json:"reasoningOutputTokens,omitempty"`
	WebSearchRequests       *int64           `json:"webSearchRequests,omitempty"`
	ReportedCostUSD         string           `json:"reportedCostUSD,omitempty"`
	CostBasis               string           `json:"costBasis,omitempty"`
	OtherCounters           map[string]int64 `json:"otherCounters,omitempty"`
}

// breakdownBuilder consumes parsed output lines and produces the run's
// normalized usage at EOF. Like usageAccumulator, it keeps O(models) state.
type breakdownBuilder interface {
	addLine(m map[string]any)
	breakdown() *usageBreakdown
}

// newBreakdownBuilder returns the builder for agentType, or nil when Kelos
// does not normalize that agent's usage.
func newBreakdownBuilder(agentType string) breakdownBuilder {
	model := os.Getenv(envModel)
	switch agentType {
	case "claude-code":
		return &claudeBreakdown{}
	case "codex":
		return newCodexBreakdown(model)
	case "opencode":
		return newOpencodeBreakdown(model)
	default:
		return nil
	}
}

// encodeBreakdown renders b as the compact one-line JSON of the outputs
// block; it never contains a newline.
func encodeBreakdown(b *usageBreakdown) (string, error) {
	data, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// notes is an ordered set of coverage notes.
type notes []string

func (n *notes) add(note string) {
	for _, existing := range *n {
		if existing == note {
			return
		}
	}
	*n = append(*n, note)
}

// coverage returns Partial when any note lowers coverage, else Complete.
// Attribution notes (model-not-reported) do not lower it: the counts are
// still all there.
func (n notes) coverage() string {
	for _, note := range n {
		if note != noteModelNotReported {
			return coveragePartial
		}
	}
	return coverageComplete
}

// configuredModel splits a KELOS_MODEL value into its model entry fields.
// withProvider splits a "provider/model" value, as OpenCode takes it.
func configuredModel(value string, withProvider bool) modelUsage {
	if value == "" {
		return modelUsage{}
	}
	m := modelUsage{Model: value, ModelSource: modelConfigured}
	if withProvider {
		if provider, model, ok := strings.Cut(value, "/"); ok && provider != "" && model != "" {
			m.Provider, m.Model = provider, model
		}
	}
	return m
}

// counter reads a non-negative integer JSON number. ok is false when the
// key is absent or not an integer, so an unknown count stays unknown.
func counter(m map[string]any, key string) (int64, bool) {
	n, ok := m[key].(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	if err != nil || i < 0 {
		return 0, false
	}
	return i, true
}

func counterPtr(m map[string]any, key string) *int64 {
	if v, ok := counter(m, key); ok {
		return &v
	}
	return nil
}

// otherCounters keeps the integer fields of m not named in known.
func otherCounters(m map[string]any, known map[string]bool) map[string]int64 {
	var out map[string]int64
	for key := range m {
		if known[key] {
			continue
		}
		v, ok := counter(m, key)
		if !ok {
			continue
		}
		if out == nil {
			out = make(map[string]int64)
		}
		out[key] = v
	}
	return out
}

func int64Ptr(v int64) *int64 { return &v }

// decimalSum adds decimal strings exactly.
type decimalSum struct {
	total big.Rat
	seen  bool
}

// add returns false when value is not a non-negative decimal.
func (d *decimalSum) add(value string) bool {
	var r big.Rat
	if _, ok := r.SetString(value); !ok || r.Sign() < 0 {
		return false
	}
	d.total.Add(&d.total, &r)
	d.seen = true
	return true
}

func (d *decimalSum) String() string {
	if !d.seen {
		return ""
	}
	s := d.total.FloatString(costDecimalPlaces)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// sortedKeys returns the keys of m in order, for deterministic output.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func mapField(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}
