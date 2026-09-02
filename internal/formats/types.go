package formats

// These types are the single source of truth inside simpllm. All wire formats
// are parsed into these canonical types, translated between formats, and then
// formatted back out.

// ChatRequest is the canonical request format.
type ChatRequest struct {
	Model       string
	Messages    []Message
	System      []ContentBlock
	MaxTokens   int
	Temperature *float64
	TopP        *float64
	Stream      bool
	Tools       []Tool
}

// ChatResponse is the canonical response format.
type ChatResponse struct {
	ID         string
	Model      string
	Content    []ContentBlock
	StopReason StopReason
	Usage      *Usage
}

// Message represents a single message in a conversation.
type Message struct {
	Role    string
	Content []ContentBlock
}

// ContentBlock represents a piece of content (text, tool_use, etc.).
type ContentBlock struct {
	Type  string
	Text  string
	ID    string
	Name  string
	Input map[string]any
}

// Usage tracks token counts.
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// Tool describes a tool the model can call.
type Tool struct {
	Type     string
	Function ToolFunction
}

// ToolFunction describes a callable function.
type ToolFunction struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// StopReason indicates why the model stopped generating.
type StopReason string

const (
	StopReasonEndTurn      StopReason = "end_turn"
	StopReasonToolUse      StopReason = "tool_use"
	StopReasonMaxTokens    StopReason = "max_tokens"
	StopReasonStopSequence StopReason = "stop_sequence"
)

// ─── Stream event types ─────────────────────────────────────────────────────

// StreamEventType identifies the kind of stream event.
type StreamEventType string

const (
	StreamEventMessageStart  StreamEventType = "message_start"
	StreamEventTextDelta     StreamEventType = "text_delta"
	StreamEventToolUseStart  StreamEventType = "tool_use_start"
	StreamEventToolUseDelta  StreamEventType = "tool_use_delta"
	StreamEventToolUseStop   StreamEventType = "tool_use_stop"
	StreamEventMessageStop   StreamEventType = "message_stop"
	StreamEventUsage         StreamEventType = "usage"
	StreamEventThinkingDelta StreamEventType = "thinking_delta"
)

// StreamEvent is the canonical representation of a streaming SSE event.
type StreamEvent struct {
	Type       StreamEventType
	ID         string
	Model      string
	Text       string
	Thinking   string
	StopReason StopReason
	Usage      *Usage
	ToolID     string
	ToolName   string
	InputJSON  string
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// TextBlock creates a text content block.
func TextBlock(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}
