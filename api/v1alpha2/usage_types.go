package v1alpha2

import (
	"k8s.io/apimachinery/pkg/types"
)

// UsageBreakdownSchemaVersion is the version of the normalized usage
// contract that kelos-capture emits and the controller stores.
const UsageBreakdownSchemaVersion int32 = 1

// UsageCoverage states how much of an attempt's usage a breakdown holds.
// +kubebuilder:validation:Enum=Complete;Partial;Missing
type UsageCoverage string

const (
	// UsageCoverageComplete means the agent reported all the usage it
	// spent, as far as its output format can tell.
	UsageCoverageComplete UsageCoverage = "Complete"
	// UsageCoveragePartial means some usage is known to be absent
	// (subagents the agent does not report, failed steps); Notes says what.
	UsageCoveragePartial UsageCoverage = "Partial"
	// UsageCoverageMissing means no usage was reported at all. Counters
	// are absent, never zero.
	UsageCoverageMissing UsageCoverage = "Missing"
)

// ModelSource states where a model name comes from.
// +kubebuilder:validation:Enum=Reported;Configured
type ModelSource string

const (
	// ModelSourceReported means the agent's output named the model.
	ModelSourceReported ModelSource = "Reported"
	// ModelSourceConfigured means the output names no model and the name
	// is the one the Task asked for; the agent may have used another.
	ModelSourceConfigured ModelSource = "Configured"
)

// UsageAttempt is the usage of one agent Pod of a Task.
type UsageAttempt struct {
	// PodName is the agent Pod whose output was read.
	// +kubebuilder:validation:MinLength=1
	PodName string `json:"podName"`

	// PodUID identifies the attempt across Pods that reuse a name.
	// +optional
	PodUID types.UID `json:"podUID,omitempty"`

	// Breakdown is the attempt's normalized usage. When the Pod printed
	// none or its log could not be read, the controller records a
	// breakdown with coverage Missing and a note saying why.
	Breakdown UsageBreakdown `json:"breakdown"`
}

// UsageBreakdown is the normalized usage one agent run reported.
type UsageBreakdown struct {
	// SchemaVersion is the version of this contract.
	// +kubebuilder:validation:Minimum=1
	SchemaVersion int32 `json:"schemaVersion"`

	// Runner is the agent type whose output was parsed.
	// +optional
	Runner string `json:"runner,omitempty"`

	// SessionID is the agent's own session or thread id.
	// +optional
	SessionID string `json:"sessionID,omitempty"`

	// Final is true when the agent printed its terminal usage summary.
	// +optional
	Final bool `json:"final,omitempty"`

	// Coverage states how much of the attempt's usage this holds.
	Coverage UsageCoverage `json:"coverage"`

	// Notes are short machine-readable reasons for the coverage, such as
	// no-final-result or subagent-usage-not-reported.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=64
	Notes []string `json:"notes,omitempty"`

	// IncludesSubagents is true when the model totals already include the
	// usage of the subagents the run started.
	// +optional
	IncludesSubagents bool `json:"includesSubagents,omitempty"`

	// Subagents are the child sessions the run started, when the output
	// names them.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=64
	Subagents []SubagentUsageRef `json:"subagents,omitempty"`

	// Models holds one entry per model the run used.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=32
	Models []ModelUsage `json:"models,omitempty"`

	// ReportedCostUSD is the run's cost in USD as the agent itself reported
	// it (CostSource), never a Kelos estimate: a plain non-negative decimal
	// string, exact as printed.
	// +optional
	// +kubebuilder:validation:Pattern=`^(0|[1-9][0-9]*)(\.[0-9]+)?$`
	// +kubebuilder:validation:MaxLength=40
	ReportedCostUSD string `json:"reportedCostUSD,omitempty"`

	// CostSource names what computed ReportedCostUSD (the agent type).
	// +optional
	CostSource string `json:"costSource,omitempty"`
}

// SubagentUsageRef identifies a child session of a run.
type SubagentUsageRef struct {
	// ID is the child session id as the agent printed it.
	// +kubebuilder:validation:MinLength=1
	ID string `json:"id"`

	// Model is the child's model, when printed.
	// +optional
	Model string `json:"model,omitempty"`

	// UsageIncluded is true when the child's usage is inside Models.
	// +optional
	UsageIncluded bool `json:"usageIncluded,omitempty"`
}

// ModelUsage is the usage of one model within a run. Token counters are
// disjoint except ReasoningOutputTokens, which OutputTokens includes, and
// the CacheWrite5m/1h split, which CacheWriteInputTokens includes. An
// absent counter is unknown, not zero.
type ModelUsage struct {
	// Model is the model id as reported, or as configured (ModelSource).
	// Empty when neither is known.
	// +optional
	Model string `json:"model,omitempty"`

	// ModelSource states where Model comes from.
	// +optional
	ModelSource ModelSource `json:"modelSource,omitempty"`

	// CanonicalModel is the agent's canonical id for Model, when reported.
	// +optional
	CanonicalModel string `json:"canonicalModel,omitempty"`

	// Provider is the model provider, when reported or configured.
	// +optional
	Provider string `json:"provider,omitempty"`

	// UncachedInputTokens are input tokens neither read from nor written
	// to a prompt cache.
	// +optional
	// +kubebuilder:validation:Minimum=0
	UncachedInputTokens *int64 `json:"uncachedInputTokens,omitempty"`

	// CacheReadInputTokens are input tokens read from a prompt cache.
	// +optional
	// +kubebuilder:validation:Minimum=0
	CacheReadInputTokens *int64 `json:"cacheReadInputTokens,omitempty"`

	// CacheWriteInputTokens are input tokens written to a prompt cache.
	// +optional
	// +kubebuilder:validation:Minimum=0
	CacheWriteInputTokens *int64 `json:"cacheWriteInputTokens,omitempty"`

	// CacheWrite5mInputTokens is the part of CacheWriteInputTokens written
	// with a five-minute lifetime, when reported.
	// +optional
	// +kubebuilder:validation:Minimum=0
	CacheWrite5mInputTokens *int64 `json:"cacheWrite5mInputTokens,omitempty"`

	// CacheWrite1hInputTokens is the part of CacheWriteInputTokens written
	// with a one-hour lifetime, when reported.
	// +optional
	// +kubebuilder:validation:Minimum=0
	CacheWrite1hInputTokens *int64 `json:"cacheWrite1hInputTokens,omitempty"`

	// OutputTokens are all output tokens, reasoning included.
	// +optional
	// +kubebuilder:validation:Minimum=0
	OutputTokens *int64 `json:"outputTokens,omitempty"`

	// ReasoningOutputTokens is the reasoning part of OutputTokens.
	// +optional
	// +kubebuilder:validation:Minimum=0
	ReasoningOutputTokens *int64 `json:"reasoningOutputTokens,omitempty"`

	// WebSearchRequests is the number of server-side web searches.
	// +optional
	// +kubebuilder:validation:Minimum=0
	WebSearchRequests *int64 `json:"webSearchRequests,omitempty"`

	// ReportedCostUSD is this model's cost in USD as the agent reported
	// it, a plain non-negative decimal string.
	// +optional
	// +kubebuilder:validation:Pattern=`^(0|[1-9][0-9]*)(\.[0-9]+)?$`
	// +kubebuilder:validation:MaxLength=40
	ReportedCostUSD string `json:"reportedCostUSD,omitempty"`

	// CostBasis is the agent's label for how it priced ReportedCostUSD,
	// when it gives one.
	// +optional
	CostBasis string `json:"costBasis,omitempty"`

	// OtherCounters keeps numeric usage counters the agent reported that
	// this contract does not name yet, under the agent's own key.
	// +optional
	// +kubebuilder:validation:MaxProperties=32
	OtherCounters map[string]int64 `json:"otherCounters,omitempty"`
}
