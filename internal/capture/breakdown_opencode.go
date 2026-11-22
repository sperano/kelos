package capture

// opencodeBreakdown normalizes `opencode run --format json` usage.
//
// Each step_finish part is that step's own usage (Session.getUsage), so
// steps are summed, once per part id. tokens.input excludes the cache
// reads and writes, and tokens.output excludes tokens.reasoning, which
// the contract counts inside OutputTokens. part.cost is OpenCode's own
// estimate from its price table. run.ts prints only the run's own session:
// a task tool's subagent prints no usage, and a failed or aborted step
// prints none either. No line names the model.
type opencodeBreakdown struct {
	model     modelUsage
	sessionID string
	steps     map[string]bool
	unnamed   int
	input     int64
	output    int64
	reasoning int64
	cacheRead int64
	cacheW    int64
	other     map[string]int64
	cost      decimalSum
	badCost   bool
	lastStop  string
	failed    bool
	subagents []subagentRef
	childIDs  map[string]bool
}

// opencodeKnownTokenFields are the part.tokens fields mapped here; total
// is the provider's sum of the others and would double count.
var opencodeKnownTokenFields = map[string]bool{
	"input":     true,
	"output":    true,
	"reasoning": true,
	"cache":     true,
	"total":     true,
}

// opencodeStopReason is the finish reason of a step that ended the run.
const opencodeStopReason = "stop"

func newOpencodeBreakdown(model string) *opencodeBreakdown {
	return &opencodeBreakdown{
		model:    configuredModel(model, true),
		steps:    make(map[string]bool),
		childIDs: make(map[string]bool),
	}
}

func (o *opencodeBreakdown) addLine(m map[string]any) {
	session := stringField(m, "sessionID")
	if o.sessionID == "" && session != "" {
		o.sessionID = session
	}
	if session != "" && session != o.sessionID {
		return
	}
	switch m["type"] {
	case "step_finish":
		o.addStep(mapField(m, "part"))
	case "error":
		o.failed = true
	case "tool_use":
		o.addTool(mapField(m, "part"))
	}
}

func (o *opencodeBreakdown) addStep(part map[string]any) {
	if part == nil {
		return
	}
	if id := stringField(part, "id"); id != "" {
		if o.steps[id] {
			return
		}
		o.steps[id] = true
	} else {
		o.unnamed++
	}
	o.lastStop = stringField(part, "reason")
	tokens := mapField(part, "tokens")
	if tokens != nil {
		o.addTokens(tokens)
	}
	if v, ok := part["cost"]; ok && !o.cost.add(formatNumber(v)) {
		o.badCost = true
	}
}

func (o *opencodeBreakdown) addTokens(tokens map[string]any) {
	add := func(dst *int64, src map[string]any, key string) {
		if v, ok := counter(src, key); ok {
			*dst += v
		}
	}
	add(&o.input, tokens, "input")
	add(&o.output, tokens, "output")
	add(&o.reasoning, tokens, "reasoning")
	if cache := mapField(tokens, "cache"); cache != nil {
		add(&o.cacheRead, cache, "read")
		add(&o.cacheW, cache, "write")
	}
	for key, v := range otherCounters(tokens, opencodeKnownTokenFields) {
		if o.other == nil {
			o.other = make(map[string]int64)
		}
		o.other[key] += v
	}
}

func (o *opencodeBreakdown) addTool(part map[string]any) {
	if part == nil || part["tool"] != "task" {
		return
	}
	meta := mapField(mapField(part, "state"), "metadata")
	id := stringField(meta, "sessionId")
	if id == "" || o.childIDs[id] {
		return
	}
	o.childIDs[id] = true
	ref := subagentRef{ID: id}
	if model := mapField(meta, "model"); model != nil {
		ref.Model = stringField(model, "modelID")
		if provider := stringField(model, "providerID"); provider != "" && ref.Model != "" {
			ref.Model = provider + "/" + ref.Model
		}
	}
	o.subagents = append(o.subagents, ref)
}

func (o *opencodeBreakdown) breakdown() *usageBreakdown {
	b := &usageBreakdown{
		SchemaVersion: breakdownSchemaVersion,
		Runner:        "opencode",
		SessionID:     o.sessionID,
		Subagents:     o.subagents,
	}
	var n notes
	if o.failed {
		n.add(noteFailedStep)
	}
	if len(o.subagents) > 0 {
		n.add(noteSubagentNotCounted)
	}
	if o.model.Model == "" {
		n.add(noteModelNotReported)
	}
	if len(o.steps) == 0 && o.unnamed == 0 {
		n.add(noteNoStepUsage)
		b.Coverage = coverageMissing
		b.Notes = n
		return b
	}
	if o.badCost {
		n.add(noteInconsistent)
	}
	b.Final = o.lastStop == opencodeStopReason && !o.failed
	m := o.model
	m.UncachedInputTokens = int64Ptr(o.input)
	m.CacheReadInputTokens = int64Ptr(o.cacheRead)
	m.CacheWriteInputTokens = int64Ptr(o.cacheW)
	m.OutputTokens = int64Ptr(o.output + o.reasoning)
	m.ReasoningOutputTokens = int64Ptr(o.reasoning)
	m.OtherCounters = o.other
	if !o.badCost {
		m.ReportedCostUSD = o.cost.String()
		if m.ReportedCostUSD != "" {
			b.ReportedCostUSD = m.ReportedCostUSD
			b.CostSource = "opencode"
		}
	}
	b.Models = []modelUsage{m}
	b.Coverage = n.coverage()
	b.Notes = n
	return b
}
