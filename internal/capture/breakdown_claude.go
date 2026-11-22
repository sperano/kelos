package capture

// claudeBreakdown normalizes Claude Code stream-json usage.
//
// Claude Code prints a "result" line at the end of each turn; a run that
// waits for background work can print several (result_index 0, 1, ...).
// Each result's modelUsage is the session's cumulative per-model total,
// main loop, subagents, compaction and other internal calls included, and
// total_cost_usd is cumulative the same way. So only the last result is
// read and nothing is summed. The result's "usage" field covers the main
// loop only, and assistant lines repeat one message's usage on every
// content block; neither is used here.
type claudeBreakdown struct {
	sessionID string
	last      map[string]any
	// activityAfterLast is set when an assistant line follows the last
	// result: usage the run spent after its last cumulative total.
	activityAfterLast bool
}

// claudeKnownModelFields are the modelUsage fields mapped to the contract,
// or deliberately dropped (context and output limits are not usage).
var claudeKnownModelFields = map[string]bool{
	"inputTokens":                         true,
	"outputTokens":                        true,
	"cacheReadInputTokens":                true,
	"cacheCreationInputTokens":            true,
	"cacheCreationEphemeral5mInputTokens": true,
	"cacheCreationEphemeral1hInputTokens": true,
	"thinkingTokens":                      true,
	"webSearchRequests":                   true,
	"costUSD":                             true,
	"contextWindow":                       true,
	"maxOutputTokens":                     true,
}

func (c *claudeBreakdown) addLine(m map[string]any) {
	if id := stringField(m, "session_id"); id != "" && c.sessionID == "" {
		c.sessionID = id
	}
	switch m["type"] {
	case "result":
		c.last = m
		c.activityAfterLast = false
	case "assistant":
		if c.last != nil {
			c.activityAfterLast = true
		}
	}
}

func (c *claudeBreakdown) breakdown() *usageBreakdown {
	b := &usageBreakdown{
		SchemaVersion: breakdownSchemaVersion,
		Runner:        "claude-code",
		SessionID:     c.sessionID,
	}
	if c.last == nil {
		b.Coverage = coverageMissing
		b.Notes = []string{noteNoFinalResult}
		return b
	}
	b.Final = true
	b.IncludesSubagents = true
	if v, ok := c.last["total_cost_usd"]; ok {
		if cost, ok := decimalString(formatNumber(v)); ok {
			b.ReportedCostUSD = cost
			b.CostSource = "claude-code"
		}
	}
	var n notes
	if c.activityAfterLast {
		n.add(noteUsageAfterResult)
	}
	usage, ok := c.last["modelUsage"].(map[string]any)
	if !ok {
		n.add(noteModelUsageAbsent)
		b.Coverage = coverageMissing
		b.Notes = n
		return b
	}
	for _, model := range sortedKeys(usage) {
		fields, ok := usage[model].(map[string]any)
		if !ok {
			n.add(noteInconsistent)
			continue
		}
		b.Models = append(b.Models, claudeModelUsage(model, fields))
	}
	b.Coverage = n.coverage()
	b.Notes = n
	return b
}

func claudeModelUsage(model string, f map[string]any) modelUsage {
	m := modelUsage{
		Model:                   model,
		ModelSource:             modelReported,
		CanonicalModel:          stringField(f, "canonicalModel"),
		Provider:                stringField(f, "provider"),
		UncachedInputTokens:     counterPtr(f, "inputTokens"),
		CacheReadInputTokens:    counterPtr(f, "cacheReadInputTokens"),
		CacheWriteInputTokens:   counterPtr(f, "cacheCreationInputTokens"),
		CacheWrite5mInputTokens: counterPtr(f, "cacheCreationEphemeral5mInputTokens"),
		CacheWrite1hInputTokens: counterPtr(f, "cacheCreationEphemeral1hInputTokens"),
		OutputTokens:            counterPtr(f, "outputTokens"),
		ReasoningOutputTokens:   counterPtr(f, "thinkingTokens"),
		WebSearchRequests:       counterPtr(f, "webSearchRequests"),
		CostBasis:               stringField(f, "costBasis"),
		OtherCounters:           otherCounters(f, claudeKnownModelFields),
	}
	if v, ok := f["costUSD"]; ok {
		if cost, ok := decimalString(formatNumber(v)); ok {
			m.ReportedCostUSD = cost
		}
	}
	return m
}

// decimalString renders a JSON number as a plain decimal (no exponent),
// rounded to costDecimalPlaces; ok is false for a negative or non-numeric value.
func decimalString(s string) (string, bool) {
	var d decimalSum
	if !d.add(s) {
		return "", false
	}
	return d.String(), true
}
