package logview

import (
	"encoding/json"
	"strings"
	"unicode"
)

// workspaceRoot is where agent pods check out the repository; paths under
// it are shown relative to it.
const workspaceRoot = "/workspace/repo/"

// summaryKeys are the tool input fields that best describe a call, most
// specific first. Agents spell the same field differently.
var summaryKeys = []string{
	"file_path", "filePath", "absolute_path", "notebook_path", "path",
	"command", "cmd", "pattern", "query", "url", "description", "prompt",
}

// displayNames maps agent tool names to the names Claude Code displays.
var displayNames = map[string]string{
	"Edit":                "Update",
	"MultiEdit":           "Update",
	"edit":                "Update",
	"replace":             "Update",
	"EditFile":            "Update",
	"write":               "Write",
	"write_file":          "Write",
	"WriteFile":           "Write",
	"read_file":           "Read",
	"ReadFile":            "Read",
	"run_shell_command":   "Bash",
	"Shell":               "Bash",
	"search_file_content": "Grep",
	"google_web_search":   "WebSearch",
	"web_fetch":           "WebFetch",
}

// newToolCall describes a tool invocation from its name and JSON input.
func newToolCall(id, name string, input json.RawMessage) ToolCall {
	call := ToolCall{ID: id, Name: displayName(name)}
	var fields map[string]any
	if len(input) == 0 || json.Unmarshal(input, &fields) != nil {
		return call
	}
	call.Summary = inputSummary(fields)
	call.Diff = editHunks(fields)
	if content, ok := fields["content"].(string); ok && call.Name == "Write" {
		call.WriteLines = len(splitLines(content))
	}
	call.Todos = parseTodos(fields["todos"])
	return call
}

// displayName maps a tool name to its Claude Code spelling and capitalizes
// plain lowercase names such as OpenCode's "bash".
func displayName(name string) string {
	if mapped, ok := displayNames[name]; ok {
		return mapped
	}
	for _, r := range name {
		if !unicode.IsLower(r) {
			return name
		}
	}
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func inputSummary(fields map[string]any) string {
	for _, key := range summaryKeys {
		if value, ok := fields[key].(string); ok && value != "" {
			return relativePath(value)
		}
	}
	return ""
}

// editHunks returns the diff of an edit-style tool input, which carries
// the replaced text either directly or as a list of edits (MultiEdit).
func editHunks(fields map[string]any) []Hunk {
	if hunk, ok := replacement(fields); ok {
		return []Hunk{hunk}
	}
	edits, _ := fields["edits"].([]any)
	var hunks []Hunk
	for _, edit := range edits {
		if editFields, ok := edit.(map[string]any); ok {
			if hunk, ok := replacement(editFields); ok {
				hunks = append(hunks, hunk)
			}
		}
	}
	return hunks
}

func replacement(fields map[string]any) (Hunk, bool) {
	for _, keys := range [][2]string{{"old_string", "new_string"}, {"oldString", "newString"}} {
		oldText, oldOK := fields[keys[0]].(string)
		newText, newOK := fields[keys[1]].(string)
		if oldOK && newOK {
			return replacementHunk(oldText, newText), true
		}
	}
	return Hunk{}, false
}

// parseTodos reads a todo list in any of the agents' shapes: Claude and
// OpenCode use content/status, Gemini description/status, Codex
// text/completed.
func parseTodos(raw any) []Todo {
	items, _ := raw.([]any)
	var todos []Todo
	for _, item := range items {
		fields, ok := item.(map[string]any)
		if !ok {
			continue
		}
		todo := Todo{Status: TodoPending}
		for _, key := range []string{"content", "description", "text"} {
			if text, ok := fields[key].(string); ok && text != "" {
				todo.Text = text
				break
			}
		}
		if status, ok := fields["status"].(string); ok && status != "" {
			todo.Status = TodoStatus(status)
		}
		if done, ok := fields["completed"].(bool); ok && done {
			todo.Status = TodoCompleted
		}
		todos = append(todos, todo)
	}
	return todos
}

// relativePath strips the workspace checkout prefix from paths.
func relativePath(value string) string {
	return strings.ReplaceAll(value, workspaceRoot, "")
}
