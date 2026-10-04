package logview

import (
	"bufio"
	"encoding/json"
	"io"
)

const (
	scanInitialBuffer = 64 * 1024
	scanMaxLine       = 10 * 1024 * 1024
)

// Agent types whose output has a dedicated parser. Any other type is
// parsed as Claude Code stream-json, the default agent.
const (
	AgentCodex    = "codex"
	AgentGemini   = "gemini"
	AgentOpenCode = "opencode"
)

// Parse reads agentType's NDJSON from r and calls emit for each event.
func Parse(agentType string, r io.Reader, emit func(Event)) error {
	var parser func([]byte, func(Event)) bool
	switch agentType {
	case AgentCodex:
		parser = parseCodexLine
	case AgentGemini:
		parser = parseGeminiLine
	case AgentOpenCode:
		parser = newOpenCodeParser().parse
	default:
		parser = parseClaudeLine
	}
	return scanLines(r, func(line []byte) {
		if !parser(line, emit) {
			emit(Raw{Line: string(line)})
		}
	})
}

// scanLines calls handle for every non-empty line of r.
func scanLines(r io.Reader, handle func([]byte)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, scanInitialBuffer), scanMaxLine)
	for scanner.Scan() {
		if line := scanner.Bytes(); len(line) > 0 {
			handle(line)
		}
	}
	return scanner.Err()
}

// contentText flattens a tool result content that is either a string or
// a list of {"type":"text","text":...} blocks.
func contentText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var joined string
	for _, block := range blocks {
		if block.Type != "text" {
			continue
		}
		if joined != "" {
			joined += "\n"
		}
		joined += block.Text
	}
	return joined
}
