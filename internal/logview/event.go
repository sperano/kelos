// Package logview turns the NDJSON streams of the supported agents into a
// common event model and renders those events in a Claude Code-like style.
package logview

// Event is one renderable item of an agent's log stream.
type Event interface {
	isEvent()
}

// Init reports the model the agent started with.
type Init struct {
	Model string
}

// Text is assistant prose. Delta marks a streamed fragment that continues
// the previous Text instead of starting a new message.
type Text struct {
	Text  string
	Delta bool
	// Nested marks output of a sub-agent rather than the main agent.
	Nested bool
}

// Thinking is the agent's reasoning, when the agent emits it.
type Thinking struct {
	Text   string
	Nested bool
}

// ToolCall is the agent invoking a tool.
type ToolCall struct {
	ID      string
	Name    string
	Summary string
	// Diff holds the edit for file-editing tools whose input carries the
	// replaced and replacement text.
	Diff []Hunk
	// WriteLines is the number of lines a file-writing tool wrote.
	WriteLines int
	Todos      []Todo
	Nested     bool
}

// ToolResult is the outcome of a ToolCall, matched by ID when the agent
// provides one.
type ToolResult struct {
	ID      string
	Output  string
	IsError bool
	// Diff, when set, supersedes the diff of the matching ToolCall; Claude
	// reports it with real file line numbers.
	Diff   []Hunk
	Nested bool
}

// FileChange is a file the agent created, updated or deleted without a
// diff being available (Codex reports only paths).
type FileChange struct {
	Path string
	Kind FileChangeKind
}

// FileChangeKind is what happened to a file.
type FileChangeKind string

// File change kinds.
const (
	FileAdded   FileChangeKind = "add"
	FileUpdated FileChangeKind = "update"
	FileDeleted FileChangeKind = "delete"
)

// Todos is a todo list the agent published outside a tool call.
type Todos struct {
	Items []Todo
}

// Todo is one entry of an agent's todo list.
type Todo struct {
	Text   string
	Status TodoStatus
}

// TodoStatus is the progress of a Todo.
type TodoStatus string

// Todo statuses.
const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

// Error is an error the agent or its runtime reported.
type Error struct {
	Message string
}

// Result summarizes a finished run or turn.
type Result struct {
	Status  ResultStatus
	Detail  string
	Turns   int
	CostUSD float64
	// InputTokens counts all input tokens, CachedTokens the part of them
	// read from the prompt cache.
	InputTokens  int
	CachedTokens int
	OutputTokens int
	DurationMS   int64
	// Message is the agent's final error text, shown for failed runs.
	Message string
}

// ResultStatus is how a run ended.
type ResultStatus string

// Result statuses.
const (
	ResultCompleted  ResultStatus = "completed"
	ResultIncomplete ResultStatus = "incomplete"
	ResultError      ResultStatus = "error"
)

// Raw is a log line that is not agent JSON; it is passed through as-is.
type Raw struct {
	Line string
}

func (Init) isEvent()       {}
func (Text) isEvent()       {}
func (Thinking) isEvent()   {}
func (ToolCall) isEvent()   {}
func (ToolResult) isEvent() {}
func (FileChange) isEvent() {}
func (Todos) isEvent()      {}
func (Error) isEvent()      {}
func (Result) isEvent()     {}
func (Raw) isEvent()        {}
