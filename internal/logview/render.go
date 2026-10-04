package logview

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Layout of a rendered entry, after Claude Code.
const (
	bulletMark    = "⏺ "
	thinkingMark  = "✻ "
	resultPrefix  = "  ⎿  "
	resultIndent  = "     "
	textIndent    = "  "
	nestedIndent  = "    "
	ellipsis      = "…"
	verboseHint   = " (-v to show all)"
	noOutputLabel = "(no output)"
)

// Limits applied unless the renderer is verbose.
const (
	maxSummaryRunes      = 160
	maxLineRunes         = 200
	resultPreviewLines   = 3
	thinkingPreviewLines = 3
	maxDiffLines         = 40
)

// Options configures a Renderer.
type Options struct {
	// Color enables ANSI styling.
	Color bool
	// Verbose shows full tool output, thinking, diffs and sub-agent
	// activity instead of previews.
	Verbose bool
}

// Renderer writes events in a Claude Code-like layout.
type Renderer struct {
	w       io.Writer
	styles  styles
	verbose bool
	// calls holds tool calls waiting for their result, by ID.
	calls map[string]ToolCall
	// started is set once anything was written; entries after the first
	// are separated by a blank line.
	started bool
	// inDelta is set while streamed text is being continued on one line.
	inDelta bool
	// lastCallID is the ID of the tool call whose header was written last,
	// or "" when another entry followed it.
	lastCallID string
	// inRaw is set while consecutive raw lines are written as one block.
	inRaw bool
}

// NewRenderer returns a Renderer writing to w.
func NewRenderer(w io.Writer, opts Options) *Renderer {
	return &Renderer{
		w:       w,
		styles:  newStyles(w, opts.Color),
		verbose: opts.Verbose,
		calls:   map[string]ToolCall{},
	}
}

// Finish ends a line left open by streamed text. Call it once the stream
// is exhausted.
func (r *Renderer) Finish() {
	if r.inDelta {
		fmt.Fprintln(r.w)
		r.inDelta = false
	}
}

// Render writes one event.
func (r *Renderer) Render(event Event) {
	if text, ok := event.(Text); ok && text.Delta && r.inDelta {
		fmt.Fprint(r.w, text.Text)
		return
	}
	r.Finish()
	if isNested(event) && !r.verbose {
		return
	}
	r.render(event)
}

func (r *Renderer) render(event Event) {
	switch e := event.(type) {
	case Init:
		r.entry(false, r.styles.muted.Render("model: "+e.Model))
	case Text:
		r.renderText(e)
	case Thinking:
		r.renderThinking(e)
	case ToolCall:
		r.renderToolCall(e)
	case ToolResult:
		r.renderToolResult(e)
	case FileChange:
		r.entry(false, r.bullet()+r.callTitle(fileChangeVerb(e.Kind), e.Path))
	case Todos:
		r.entry(false, r.bullet()+r.styles.tool.Render("Todos"))
		r.renderTodos(e.Items, false)
	case Error:
		r.entry(false, r.styles.failure.Render(bulletMark+"Error: ")+e.Message)
	case Result:
		r.renderResult(e)
	case Raw:
		r.renderRaw(e)
	}
}

// entry starts a new entry, separated from the previous one by a blank
// line, and writes its first line. Nested entries are indented.
func (r *Renderer) entry(nested bool, line string) {
	r.begin()
	r.line(nested, line)
}

// begin separates a new entry from the previous one.
func (r *Renderer) begin() {
	if r.started {
		fmt.Fprintln(r.w)
	}
	r.started = true
	r.lastCallID = ""
	r.inRaw = false
}

// renderRaw writes non-JSON lines muted, grouping consecutive ones.
func (r *Renderer) renderRaw(raw Raw) {
	if strings.TrimSpace(raw.Line) == "" {
		return
	}
	line := r.styles.muted.Render(r.clip(raw.Line))
	if r.inRaw {
		r.line(false, line)
		return
	}
	r.entry(false, line)
	r.inRaw = true
}

// line writes one line of the current entry.
func (r *Renderer) line(nested bool, line string) {
	if nested {
		line = nestedIndent + line
	}
	fmt.Fprintln(r.w, line)
}

func (r *Renderer) bullet() string {
	return r.styles.bullet.Render(bulletMark)
}

func (r *Renderer) renderText(text Text) {
	lines := splitLines(text.Text)
	if len(lines) == 0 {
		return
	}
	if text.Delta {
		r.begin()
		prefix := ""
		if text.Nested {
			prefix = nestedIndent
		}
		fmt.Fprint(r.w, prefix+r.bullet()+text.Text)
		r.inDelta = true
		return
	}
	r.entry(text.Nested, r.bullet()+lines[0])
	for _, line := range lines[1:] {
		r.line(text.Nested, textIndent+line)
	}
}

func (r *Renderer) renderThinking(thinking Thinking) {
	lines := splitLines(thinking.Text)
	if len(lines) == 0 {
		return
	}
	shown, hidden := r.preview(lines, thinkingPreviewLines)
	r.entry(thinking.Nested, r.styles.thinking.Render(thinkingMark+shown[0]))
	for _, line := range shown[1:] {
		r.line(thinking.Nested, r.styles.thinking.Render(textIndent+line))
	}
	if hidden > 0 {
		r.line(thinking.Nested, r.styles.muted.Render(textIndent+moreLines(hidden)))
	}
}

func (r *Renderer) renderToolCall(call ToolCall) {
	r.entry(call.Nested, r.bullet()+r.callTitle(call.Name, call.Summary))
	if len(call.Todos) > 0 {
		r.renderTodos(call.Todos, call.Nested)
	}
	if call.ID == "" {
		return
	}
	r.calls[call.ID] = call
	r.lastCallID = call.ID
}

// resultHeader repeats a call's header when other entries were written
// since it, as happens with parallel tool calls, so the result is not
// shown under the wrong call.
func (r *Renderer) resultHeader(result ToolResult, call ToolCall, known bool) {
	if !known || result.ID == r.lastCallID {
		return
	}
	r.entry(result.Nested, r.styles.muted.Render(bulletMark+call.Name+r.summarySuffix(call.Summary)))
	r.lastCallID = result.ID
}

// callTitle renders "Name(summary)" with the summary on one line.
func (r *Renderer) callTitle(name, summary string) string {
	return r.styles.tool.Render(name) + r.summarySuffix(summary)
}

func (r *Renderer) summarySuffix(summary string) string {
	if summary == "" {
		return ""
	}
	return "(" + r.oneLine(summary) + ")"
}

func (r *Renderer) oneLine(summary string) string {
	lines := splitLines(summary)
	if len(lines) == 0 {
		return ""
	}
	line := lines[0]
	if len(lines) > 1 {
		line += " " + ellipsis
	}
	if !r.verbose && utf8.RuneCountInString(line) > maxSummaryRunes {
		line = string([]rune(line)[:maxSummaryRunes]) + ellipsis
	}
	return line
}

// clip shortens one line of output unless the renderer is verbose.
func (r *Renderer) clip(line string) string {
	if r.verbose || utf8.RuneCountInString(line) <= maxLineRunes {
		return line
	}
	return string([]rune(line)[:maxLineRunes]) + ellipsis
}

// preview returns the lines to show and how many were left out.
func (r *Renderer) preview(lines []string, limit int) ([]string, int) {
	if r.verbose || len(lines) <= limit {
		return lines, 0
	}
	return lines[:limit], len(lines) - limit
}

func moreLines(hidden int) string {
	return fmt.Sprintf("%s +%d lines%s", ellipsis, hidden, verboseHint)
}

func isNested(event Event) bool {
	switch e := event.(type) {
	case Text:
		return e.Nested
	case Thinking:
		return e.Nested
	case ToolCall:
		return e.Nested
	case ToolResult:
		return e.Nested
	}
	return false
}

// fileChangeVerb names a file change the way Claude Code names its tools.
func fileChangeVerb(kind FileChangeKind) string {
	switch kind {
	case FileAdded:
		return "Create"
	case FileDeleted:
		return "Delete"
	default:
		return "Update"
	}
}
