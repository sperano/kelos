package logview

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// codexShellPrefix is how codex exec wraps every command it runs.
	codexShellPrefix = "/bin/bash -lc "
	// doubleQuoteEscapes are the characters a backslash escapes inside a
	// double-quoted shell string.
	doubleQuoteEscapes = "$`\"\\\n"
	// codexCollabWait is the collaboration tool that only waits for
	// sub-agents; it is too frequent to be worth showing.
	codexCollabWait = "wait"
)

// codexEvent is one line of codex exec --json.
type codexEvent struct {
	Type    string      `json:"type"`
	Item    *codexItem  `json:"item"`
	Usage   *codexUsage `json:"usage"`
	Message string      `json:"message"`
	Error   *codexError `json:"error"`
}

type codexItem struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Text             string          `json:"text"`
	Command          string          `json:"command"`
	AggregatedOutput string          `json:"aggregated_output"`
	ExitCode         *int            `json:"exit_code"`
	Status           string          `json:"status"`
	Server           string          `json:"server"`
	Tool             string          `json:"tool"`
	Arguments        json.RawMessage `json:"arguments"`
	Result           *codexMCPResult `json:"result"`
	Error            *codexError     `json:"error"`
	Changes          []codexChange   `json:"changes"`
	Query            string          `json:"query"`
	Prompt           string          `json:"prompt"`
	Items            []any           `json:"items"`
}

type codexMCPResult struct {
	Content json.RawMessage `json:"content"`
}

type codexError struct {
	Message string `json:"message"`
}

type codexChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type codexUsage struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
}

// parseCodexLine handles one codex exec --json line, reporting false when
// it is not JSON.
func parseCodexLine(line []byte, emit func(Event)) bool {
	var event codexEvent
	if json.Unmarshal(line, &event) != nil {
		return false
	}
	switch event.Type {
	case "item.started":
		emitCodexItemStarted(event.Item, emit)
	case "item.updated":
		if event.Item != nil && event.Item.Type == "todo_list" {
			emit(Todos{Items: parseTodos(event.Item.Items)})
		}
	case "item.completed":
		emitCodexItemCompleted(event.Item, emit)
	case "turn.completed":
		emit(codexTurnResult(event.Usage))
	case "turn.failed":
		result := Result{Status: ResultError}
		if event.Error != nil {
			result.Message = event.Error.Message
		}
		emit(result)
	case "error":
		emit(Error{Message: event.Message})
	}
	return true
}

func emitCodexItemStarted(item *codexItem, emit func(Event)) {
	if item == nil {
		return
	}
	switch item.Type {
	case "command_execution":
		emit(ToolCall{ID: item.ID, Name: "Bash", Summary: relativePath(unwrapCodexCommand(item.Command))})
	case "mcp_tool_call":
		emit(ToolCall{ID: item.ID, Name: item.Server + "." + item.Tool, Summary: compactJSON(item.Arguments)})
	case "web_search":
		emit(ToolCall{ID: item.ID, Name: "WebSearch", Summary: item.Query})
	case "collab_tool_call":
		if item.Tool != codexCollabWait {
			emit(ToolCall{ID: item.ID, Name: "Agent", Summary: strings.TrimSpace(item.Tool + " " + item.Prompt)})
		}
	case "todo_list":
		emit(Todos{Items: parseTodos(item.Items)})
	}
}

func emitCodexItemCompleted(item *codexItem, emit func(Event)) {
	if item == nil {
		return
	}
	switch item.Type {
	case "agent_message":
		if item.Text != "" {
			emit(Text{Text: item.Text})
		}
	case "reasoning":
		if item.Text != "" {
			emit(Thinking{Text: item.Text})
		}
	case "command_execution":
		emit(codexCommandResult(item))
	case "mcp_tool_call":
		emit(codexMCPToolResult(item))
	case "file_change":
		for _, change := range item.Changes {
			emit(FileChange{Path: relativePath(change.Path), Kind: FileChangeKind(change.Kind)})
		}
	case "todo_list":
		emit(Todos{Items: parseTodos(item.Items)})
	}
}

func codexCommandResult(item *codexItem) ToolResult {
	result := ToolResult{ID: item.ID, Output: item.AggregatedOutput, IsError: item.Status == "failed"}
	if item.ExitCode != nil && *item.ExitCode != 0 {
		result.IsError = true
		result.Output = strings.TrimRight(fmt.Sprintf("exit code %d\n%s", *item.ExitCode, item.AggregatedOutput), "\n")
	}
	return result
}

func codexMCPToolResult(item *codexItem) ToolResult {
	result := ToolResult{ID: item.ID}
	if item.Result != nil {
		result.Output = contentText(item.Result.Content)
	}
	if item.Error != nil {
		result.IsError = true
		result.Output = item.Error.Message
	}
	return result
}

func codexTurnResult(usage *codexUsage) Result {
	result := Result{Status: ResultCompleted}
	if usage != nil {
		result.InputTokens = usage.InputTokens
		result.CachedTokens = usage.CachedInputTokens
		result.OutputTokens = usage.OutputTokens
	}
	return result
}

// unwrapCodexCommand removes codex's "/bin/bash -lc '<script>'" wrapper so
// the script is shown as the agent wrote it.
func unwrapCodexCommand(command string) string {
	script, ok := strings.CutPrefix(command, codexShellPrefix)
	if !ok {
		return command
	}
	if word, ok := shellWord(script); ok {
		return word
	}
	return command
}

// shellWord unquotes s when it is exactly one POSIX shell word built from
// quoted and unquoted parts. It reports false for anything else.
func shellWord(s string) (string, bool) {
	var word strings.Builder
	var quote rune
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			// Inside double quotes a backslash only escapes these.
			if quote == '"' && !strings.ContainsRune(doubleQuoteEscapes, r) {
				word.WriteRune('\\')
			}
			word.WriteRune(r)
			escaped = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '\\' && quote != '\'':
			escaped = true
		case quote == '"':
			if r == '"' {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\n':
			return "", false
		default:
			word.WriteRune(r)
		}
	}
	return word.String(), quote == 0 && !escaped
}

// compactJSON renders raw on one line, or returns "" when it is empty.
func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	compact, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	return string(compact)
}
