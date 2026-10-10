package logview

import "strings"

// Control character ranges that must not reach the terminal: agent and
// tool output is untrusted, and escape sequences in it could retitle the
// window, move the cursor or overwrite earlier lines.
const (
	c0End  = 0x1f
	del    = 0x7f
	c1Low  = 0x80
	c1High = 0x9f
)

// clean removes control characters from text, keeping newlines and tabs.
// A carriage return ending a line is dropped with the rest.
func clean(text string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r <= c0End, r == del, r >= c1Low && r <= c1High:
			return -1
		default:
			return r
		}
	}, text)
}

func cleanHunks(hunks []Hunk) []Hunk {
	cleaned := make([]Hunk, len(hunks))
	for i, hunk := range hunks {
		cleaned[i] = Hunk{OldStart: hunk.OldStart, NewStart: hunk.NewStart, Lines: make([]DiffLine, len(hunk.Lines))}
		for j, line := range hunk.Lines {
			cleaned[i].Lines[j] = DiffLine{Op: line.Op, Text: clean(line.Text)}
		}
	}
	return cleaned
}

func cleanTodos(todos []Todo) []Todo {
	cleaned := make([]Todo, len(todos))
	for i, todo := range todos {
		cleaned[i] = Todo{Text: clean(todo.Text), Status: todo.Status}
	}
	return cleaned
}

// sanitize returns event with every displayed string cleaned.
func sanitize(event Event) Event {
	switch e := event.(type) {
	case Init:
		e.Model = clean(e.Model)
		return e
	case Text:
		e.Text = clean(e.Text)
		return e
	case Thinking:
		e.Text = clean(e.Text)
		return e
	case ToolCall:
		e.Name, e.Summary = clean(e.Name), clean(e.Summary)
		e.Diff, e.Todos = cleanHunks(e.Diff), cleanTodos(e.Todos)
		return e
	case ToolResult:
		e.Output, e.Diff = clean(e.Output), cleanHunks(e.Diff)
		return e
	case FileChange:
		e.Path = clean(e.Path)
		return e
	case Todos:
		e.Items = cleanTodos(e.Items)
		return e
	case Error:
		e.Message = clean(e.Message)
		return e
	case Result:
		e.Detail, e.Message = clean(e.Detail), clean(e.Message)
		return e
	case Raw:
		e.Line = clean(e.Line)
		return e
	}
	return event
}
