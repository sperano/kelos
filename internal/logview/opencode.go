package logview

import "encoding/json"

// openCodeToolCallsReason is the step_finish reason of a step that ended
// to run tools; any other reason ends the run.
const openCodeToolCallsReason = "tool-calls"

// openCodeEvent is one line of opencode run --format json.
type openCodeEvent struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	Part  *openCodePart  `json:"part"`
	Error *openCodeError `json:"error"`
}

type openCodePart struct {
	Type   string          `json:"type"`
	Text   string          `json:"text"`
	Tool   string          `json:"tool"`
	CallID string          `json:"callID"`
	State  *openCodeState  `json:"state"`
	Reason string          `json:"reason"`
	Cost   float64         `json:"cost"`
	Tokens *openCodeTokens `json:"tokens"`
}

type openCodeState struct {
	Status string          `json:"status"`
	Input  json.RawMessage `json:"input"`
	Output string          `json:"output"`
	Error  string          `json:"error"`
}

// openCodeTokens reports input tokens without the cached ones.
type openCodeTokens struct {
	Input  int `json:"input"`
	Output int `json:"output"`
	Cache  struct {
		Read  int `json:"read"`
		Write int `json:"write"`
	} `json:"cache"`
}

type openCodeError struct {
	Name string `json:"name"`
	Data struct {
		Message string `json:"message"`
	} `json:"data"`
}

// openCodeParser totals the per-step usage OpenCode reports so the final
// step can show the whole run's tokens and cost.
type openCodeParser struct {
	steps int
	total Result
}

func newOpenCodeParser() *openCodeParser { return &openCodeParser{} }

// parse handles one opencode JSON line, reporting false when it is not JSON.
func (p *openCodeParser) parse(line []byte, emit func(Event)) bool {
	var event openCodeEvent
	if json.Unmarshal(line, &event) != nil {
		return false
	}
	switch event.Type {
	case "text", "reasoning":
		emitOpenCodeText(event, emit)
	case "tool_use":
		emitOpenCodeTool(event.Part, emit)
	case "step_finish":
		p.finishStep(event.Part, emit)
	case "error":
		emit(openCodeErrorEvent(event.Error))
	}
	return true
}

func emitOpenCodeText(event openCodeEvent, emit func(Event)) {
	text := event.Text
	if text == "" && event.Part != nil {
		text = event.Part.Text
	}
	switch {
	case text == "":
	case event.Type == "reasoning":
		emit(Thinking{Text: text})
	default:
		emit(Text{Text: text})
	}
}

// emitOpenCodeTool reports a tool part. OpenCode emits it once the tool
// finished, so the call and its result arrive together.
func emitOpenCodeTool(part *openCodePart, emit func(Event)) {
	if part == nil || part.State == nil {
		return
	}
	emit(newToolCall(part.CallID, part.Tool, part.State.Input))
	result := ToolResult{ID: part.CallID, Output: part.State.Output}
	if part.State.Status == "error" {
		result.IsError = true
		result.Output = part.State.Error
	}
	emit(result)
}

func (p *openCodeParser) finishStep(part *openCodePart, emit func(Event)) {
	if part == nil {
		return
	}
	p.steps++
	p.total.CostUSD += part.Cost
	if part.Tokens != nil {
		p.total.InputTokens += part.Tokens.Input + part.Tokens.Cache.Read + part.Tokens.Cache.Write
		p.total.CachedTokens += part.Tokens.Cache.Read
		p.total.OutputTokens += part.Tokens.Output
	}
	if part.Reason == openCodeToolCallsReason {
		return
	}
	result := p.total
	result.Status = ResultCompleted
	result.Turns = p.steps
	emit(result)
}

func openCodeErrorEvent(err *openCodeError) Error {
	if err == nil {
		return Error{Message: "unknown error"}
	}
	if err.Data.Message != "" {
		return Error{Message: err.Data.Message}
	}
	return Error{Message: err.Name}
}
