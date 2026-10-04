package logview

import "encoding/json"

// geminiEvent is one line of gemini --output-format stream-json.
type geminiEvent struct {
	Type       string          `json:"type"`
	Model      string          `json:"model"`
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	Delta      bool            `json:"delta"`
	ToolName   string          `json:"tool_name"`
	ToolID     string          `json:"tool_id"`
	Parameters json.RawMessage `json:"parameters"`
	Status     string          `json:"status"`
	Output     string          `json:"output"`
	Message    string          `json:"message"`
	Error      *geminiError    `json:"error"`
	Stats      *geminiStats    `json:"stats"`
}

type geminiError struct {
	Message string `json:"message"`
}

// geminiStats accepts both the camelCase fields of older Gemini CLI
// releases and the snake_case fields of newer ones.
type geminiStats struct {
	TotalInputTokens  int   `json:"totalInputTokens"`
	TotalOutputTokens int   `json:"totalOutputTokens"`
	InputTokens       int   `json:"input_tokens"`
	OutputTokens      int   `json:"output_tokens"`
	DurationMS        int64 `json:"duration_ms"`
}

const geminiStatusError = "error"

// parseGeminiLine handles one stream-json line, reporting false when it is
// not JSON.
func parseGeminiLine(line []byte, emit func(Event)) bool {
	var event geminiEvent
	if json.Unmarshal(line, &event) != nil {
		return false
	}
	switch event.Type {
	case "init":
		if event.Model != "" {
			emit(Init{Model: event.Model})
		}
	case "message":
		if event.Role == "assistant" && event.Content != "" {
			emit(Text{Text: event.Content, Delta: event.Delta})
		}
	case "tool_use":
		emit(newToolCall(event.ToolID, event.ToolName, event.Parameters))
	case "tool_result":
		emit(geminiToolResult(event))
	case "error":
		emit(Error{Message: event.Message})
	case "result":
		emit(geminiResult(event))
	}
	return true
}

func geminiToolResult(event geminiEvent) ToolResult {
	result := ToolResult{ID: event.ToolID, Output: event.Output, IsError: event.Status == geminiStatusError}
	if event.Error != nil && event.Error.Message != "" {
		result.IsError = true
		result.Output = event.Error.Message
	}
	return result
}

func geminiResult(event geminiEvent) Result {
	result := Result{Status: ResultCompleted}
	if event.Status == geminiStatusError {
		result.Status = ResultError
		if event.Error != nil {
			result.Message = event.Error.Message
		}
	}
	if stats := event.Stats; stats != nil {
		result.InputTokens = max(stats.TotalInputTokens, stats.InputTokens)
		result.OutputTokens = max(stats.TotalOutputTokens, stats.OutputTokens)
		result.DurationMS = stats.DurationMS
	}
	return result
}
