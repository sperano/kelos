package logview

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNewToolCall(t *testing.T) {
	tests := []struct {
		name      string
		tool      string
		input     string
		wantName  string
		wantSum   string
		wantHunks int
		wantLines int
	}{
		{"edit is shown as update", "Edit", `{"file_path":"/workspace/repo/a.go","old_string":"x","new_string":"y"}`, "Update", "a.go", 1, 0},
		{"multi edit has a hunk per edit", "MultiEdit", `{"file_path":"/workspace/repo/a.go","edits":[{"old_string":"a","new_string":"b"},{"old_string":"c","new_string":"d"}]}`, "Update", "a.go", 2, 0},
		{"opencode edit", "edit", `{"filePath":"/workspace/repo/a.go","oldString":"a","newString":"b"}`, "Update", "a.go", 1, 0},
		{"write counts lines", "Write", `{"file_path":"/tmp/x","content":"1\n2\n"}`, "Write", "/tmp/x", 0, 2},
		{"lowercase name capitalized", "bash", `{"command":"ls"}`, "Bash", "ls", 0, 0},
		{"mcp name kept", "mcp__tracker__get", `{"id":1}`, "mcp__tracker__get", "", 0, 0},
		{"no input", "Read", ``, "Read", "", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			call := newToolCall("id", tt.tool, json.RawMessage(tt.input))
			if call.Name != tt.wantName || call.Summary != tt.wantSum {
				t.Errorf("got %q(%q), want %q(%q)", call.Name, call.Summary, tt.wantName, tt.wantSum)
			}
			if len(call.Diff) != tt.wantHunks {
				t.Errorf("hunks = %d, want %d", len(call.Diff), tt.wantHunks)
			}
			if call.WriteLines != tt.wantLines {
				t.Errorf("write lines = %d, want %d", call.WriteLines, tt.wantLines)
			}
		})
	}
}

func TestParseTodosShapes(t *testing.T) {
	var items []any
	input := `[
		{"content":"claude","status":"in_progress"},
		{"description":"gemini","status":"completed"},
		{"text":"codex done","completed":true},
		{"text":"codex open","completed":false}
	]`
	if err := json.Unmarshal([]byte(input), &items); err != nil {
		t.Fatal(err)
	}
	want := []Todo{
		{"claude", TodoInProgress},
		{"gemini", TodoCompleted},
		{"codex done", TodoCompleted},
		{"codex open", TodoPending},
	}
	if got := parseTodos(items); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
