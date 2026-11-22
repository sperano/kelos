package capture

import "strings"

// codexBreakdown normalizes `codex exec --json` usage.
//
// turn.completed carries the thread's running total (TokenUsageInfo's
// total_token_usage), not the turn's own usage, so the last one is the
// run's total and nothing is summed. input_tokens includes the cached and
// cache-write input (the Responses API's input_tokens_details), and
// output_tokens includes reasoning_output_tokens. No line names the model
// or a cost, and spawned agents run in threads whose usage exec does not
// print.
type codexBreakdown struct {
	model      modelUsage
	threadID   string
	last       map[string]any
	failed     bool
	rerouted   bool
	searches   map[string]bool
	subagents  []subagentRef
	subagentID map[string]bool
}

// codexKnownUsageFields are the turn.completed usage fields mapped here.
var codexKnownUsageFields = map[string]bool{
	"input_tokens":             true,
	"cached_input_tokens":      true,
	"cache_write_input_tokens": true,
	"output_tokens":            true,
	"reasoning_output_tokens":  true,
}

// codexReroutePrefix starts the error item Codex prints for a model
// reroute ("model rerouted: <from> -> <to> (<reason>)").
const codexReroutePrefix = "model rerouted:"

func newCodexBreakdown(model string) *codexBreakdown {
	return &codexBreakdown{
		model:      configuredModel(model, false),
		searches:   make(map[string]bool),
		subagentID: make(map[string]bool),
	}
}

func (c *codexBreakdown) addLine(m map[string]any) {
	switch m["type"] {
	case "thread.started":
		if c.threadID == "" {
			c.threadID = stringField(m, "thread_id")
		}
	case "turn.completed":
		if usage := mapField(m, "usage"); usage != nil {
			c.last = usage
		}
	case "turn.failed":
		c.failed = true
	case "item.started", "item.updated", "item.completed":
		c.addItem(m["type"] == "item.completed", mapField(m, "item"))
	}
}

func (c *codexBreakdown) addItem(completed bool, item map[string]any) {
	if item == nil {
		return
	}
	switch item["type"] {
	case "web_search":
		if id := stringField(item, "id"); completed && id != "" {
			c.searches[id] = true
		}
	case "collab_tool_call":
		ids, _ := item["receiver_thread_ids"].([]any)
		for _, v := range ids {
			id, _ := v.(string)
			if id == "" || c.subagentID[id] {
				continue
			}
			c.subagentID[id] = true
			c.subagents = append(c.subagents, subagentRef{ID: id})
		}
	case "error":
		if strings.HasPrefix(stringField(item, "message"), codexReroutePrefix) {
			c.rerouted = true
		}
	}
}

func (c *codexBreakdown) breakdown() *usageBreakdown {
	b := &usageBreakdown{
		SchemaVersion: breakdownSchemaVersion,
		Runner:        "codex",
		SessionID:     c.threadID,
		Subagents:     c.subagents,
	}
	var n notes
	if c.failed {
		n.add(noteFailedTurn)
	}
	if len(c.subagents) > 0 {
		n.add(noteSubagentNotCounted)
	}
	if c.rerouted {
		n.add(noteModelRerouted)
	}
	if c.model.Model == "" {
		n.add(noteModelNotReported)
	}
	if c.last == nil {
		n.add(noteNoFinalUsage)
		b.Coverage = coverageMissing
		b.Notes = n
		return b
	}
	b.Final = true
	m := c.model
	input, hasInput := counter(c.last, "input_tokens")
	cached, _ := counter(c.last, "cached_input_tokens")
	written, _ := counter(c.last, "cache_write_input_tokens")
	if hasInput {
		uncached := input - cached - written
		if uncached < 0 {
			n.add(noteInconsistent)
			uncached = 0
		}
		m.UncachedInputTokens = int64Ptr(uncached)
	}
	m.CacheReadInputTokens = counterPtr(c.last, "cached_input_tokens")
	m.CacheWriteInputTokens = counterPtr(c.last, "cache_write_input_tokens")
	m.OutputTokens = counterPtr(c.last, "output_tokens")
	m.ReasoningOutputTokens = counterPtr(c.last, "reasoning_output_tokens")
	m.WebSearchRequests = int64Ptr(int64(len(c.searches)))
	m.OtherCounters = otherCounters(c.last, codexKnownUsageFields)
	b.Models = []modelUsage{m}
	b.Coverage = n.coverage()
	b.Notes = n
	return b
}
