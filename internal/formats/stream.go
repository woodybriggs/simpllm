package formats

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Streaming translation
//
// Each wire format has:
//   - A StreamParser: reads raw SSE lines → canonical StreamEvents
//   - A StreamFormatter: writes canonical StreamEvents → SSE lines
//
// The proxy uses these to translate streaming between formats:
//
//	upstream (Anthropic SSE) → StreamParser → []StreamEvent → StreamFormatter → downstream (OpenAI SSE)
//
// When source == target format, events pass through untouched.
// ═══════════════════════════════════════════════════════════════════════════════

// ─── OpenAI Chat Completions streaming ───────────────────────────────────────

// openAIStreamChunk is the raw OpenAI streaming chunk.
type openAIStreamChunk struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Model   string              `json:"model"`
	Choices []openAIStreamChoice `json:"choices"`
	Usage   *openAIUsage        `json:"usage,omitempty"`
}

// openAIStreamChoice is a single choice in a streaming chunk.
type openAIStreamChoice struct {
	Index        int           `json:"index"`
	Delta        *openAIMessage `json:"delta,omitempty"`
	FinishReason *string       `json:"finish_reason,omitempty"`
}

// ParseOpenAIStreamChunk parses an OpenAI SSE data line into a canonical StreamEvent.
func ParseOpenAIStreamChunk(data json.RawMessage) (*StreamEvent, error) {
	var chunk openAIStreamChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return nil, err
	}

	event := &StreamEvent{
		ID:    chunk.ID,
		Model: chunk.Model,
	}

	for _, c := range chunk.Choices {
		if c.Delta != nil {
			// Text content — content is json.RawMessage, could be string or array.
			if len(c.Delta.Content) > 0 && string(c.Delta.Content) != "null" {
				var text string
				if json.Unmarshal(c.Delta.Content, &text) == nil {
					event.Type = StreamEventTextDelta
					event.Text = text
				}
			}
			// Tool calls.
			for _, tc := range c.Delta.ToolCalls {
				if tc.Function.Name != "" {
					event.Type = StreamEventToolUseStart
					event.ToolID = tc.ID
					event.ToolName = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					event.Type = StreamEventToolUseDelta
					event.ToolID = tc.ID
					event.InputJSON = tc.Function.Arguments
				}
			}
			// Finish reason.
			if c.FinishReason != nil {
				event.Type = StreamEventMessageStop
				event.StopReason = mapOpenAIFinishReason(*c.FinishReason)
			}
		}
	}

	// Usage in final chunk.
	if chunk.Usage != nil {
		event.Type = StreamEventUsage
		event.Usage = &Usage{
			InputTokens:  chunk.Usage.PromptTokens,
			OutputTokens: chunk.Usage.CompletionTokens,
			TotalTokens:  chunk.Usage.TotalTokens,
		}
	}

	// If no specific type was set, it's a keepalive or unknown.
	if event.Type == "" {
		event.Type = "openai_keepalive"
	}

	return event, nil
}

// FormatOpenAIStreamEvent converts a canonical StreamEvent to OpenAI SSE format.
func FormatOpenAIStreamEvent(ev *StreamEvent) string {
	var sb strings.Builder

	switch ev.Type {
	case StreamEventMessageStart:
		chunk := openAIStreamChunk{
			ID:     ev.ID,
			Object: "chat.completion.chunk",
			Model:  ev.Model,
			Choices: []openAIStreamChoice{{
				Index: 0,
				Delta: &openAIMessage{
					Role: "assistant",
				},
			}},
		}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(&sb, "data: %s\n\n", data)

	case StreamEventTextDelta:
		content, _ := json.Marshal(ev.Text)
		chunk := openAIStreamChunk{
			Object: "chat.completion.chunk",
			Choices: []openAIStreamChoice{{
				Index: 0,
				Delta: &openAIMessage{
					Content: content,
				},
			}},
		}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(&sb, "data: %s\n\n", data)

	case StreamEventToolUseStart:
		chunk := openAIStreamChunk{
			Object: "chat.completion.chunk",
			Choices: []openAIStreamChoice{{
				Index: 0,
				Delta: &openAIMessage{
					ToolCalls: []openAIToolCall{{
						ID:   ev.ToolID,
						Type: "function",
						Function: openAIToolCallFn{
							Name: ev.ToolName,
						},
					}},
				},
			}},
		}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(&sb, "data: %s\n\n", data)

	case StreamEventToolUseDelta:
		chunk := openAIStreamChunk{
			Object: "chat.completion.chunk",
			Choices: []openAIStreamChoice{{
				Index: 0,
				Delta: &openAIMessage{
					ToolCalls: []openAIToolCall{{
						ID:   ev.ToolID,
						Type: "function",
						Function: openAIToolCallFn{
							Arguments: ev.InputJSON,
						},
					}},
				},
			}},
		}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(&sb, "data: %s\n\n", data)

	case StreamEventMessageStop:
		reason := string(ev.StopReason)
		switch ev.StopReason {
		case StopReasonEndTurn:
			reason = "stop"
		case StopReasonToolUse:
			reason = "tool_calls"
		case StopReasonMaxTokens:
			reason = "length"
		}
		chunk := openAIStreamChunk{
			Object: "chat.completion.chunk",
			Choices: []openAIStreamChoice{{
				Index:        0,
				FinishReason: &reason,
			}},
		}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(&sb, "data: %s\n\n", data)

	case StreamEventUsage:
		if ev.Usage != nil {
			chunk := openAIStreamChunk{
				Object: "chat.completion.chunk",
				Usage: &openAIUsage{
					PromptTokens:     ev.Usage.InputTokens,
					CompletionTokens: ev.Usage.OutputTokens,
					TotalTokens:      ev.Usage.TotalTokens,
				},
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(&sb, "data: %s\n\n", data)
		}
	}

	return sb.String()
}

// ─── Anthropic Messages streaming ────────────────────────────────────────────

// ParseAnthropicStreamEvent parses an Anthropic SSE event into a canonical StreamEvent.
func ParseAnthropicStreamEvent(eventType string, data json.RawMessage) (*StreamEvent, error) {
	event := &StreamEvent{}

	switch eventType {
	case "message_start":
		var msg struct {
			Type  string `json:"type"`
			Message struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"message"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, err
		}
		event.Type = StreamEventMessageStart
		event.ID = msg.Message.ID
		event.Model = msg.Message.Model

	case "content_block_start":
		var block struct {
			Type          string `json:"type"`
			Index         int    `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, err
		}
		if block.ContentBlock.Type == "tool_use" {
			event.Type = StreamEventToolUseStart
			event.ToolID = block.ContentBlock.ID
			event.ToolName = block.ContentBlock.Name
		} else {
			event.Type = "content_block_start"
		}

	case "content_block_delta":
		var delta struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text,omitempty"`
				JSON string `json:"partial_json,omitempty"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(data, &delta); err != nil {
			return nil, err
		}
		switch delta.Delta.Type {
		case "text_delta":
			event.Type = StreamEventTextDelta
			event.Text = delta.Delta.Text
		case "input_json_delta":
			event.Type = StreamEventToolUseDelta
			event.InputJSON = delta.Delta.JSON
		case "thinking_delta":
			event.Type = StreamEventThinkingDelta
			event.Thinking = delta.Delta.Text
		}

	case "content_block_stop":
		event.Type = "content_block_stop"

	case "message_delta":
		var msgDelta struct {
			Type  string `json:"type"`
			Delta struct {
				StopReason   string `json:"stop_reason"`
				StopSequence string `json:"stop_sequence,omitempty"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(data, &msgDelta); err != nil {
			return nil, err
		}
		event.Type = StreamEventMessageStop
		switch msgDelta.Delta.StopReason {
		case "end_turn":
			event.StopReason = StopReasonEndTurn
		case "tool_use":
			event.StopReason = StopReasonToolUse
		case "max_tokens":
			event.StopReason = StopReasonMaxTokens
		case "stop_sequence":
			event.StopReason = StopReasonStopSequence
		}

	case "message_stop":
		event.Type = StreamEventMessageStop

	default:
		event.Type = StreamEventType(eventType)
	}

	return event, nil
}

// FormatAnthropicStreamEvent converts a canonical StreamEvent to Anthropic SSE format.
func FormatAnthropicStreamEvent(ev *StreamEvent) string {
	var sb strings.Builder

	switch ev.Type {
	case StreamEventMessageStart:
		msg := map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":          ev.ID,
				"type":        "message",
				"role":        "assistant",
				"content":     []any{},
				"model":       ev.Model,
				"stop_reason": nil,
				"usage":       map[string]any{"input_tokens": 0, "output_tokens": 0},
			},
		}
		data, _ := json.Marshal(msg)
		fmt.Fprintf(&sb, "event: message_start\ndata: %s\n\n", data)

	case StreamEventTextDelta:
		delta := map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{
				"type": "text_delta",
				"text": ev.Text,
			},
		}
		data, _ := json.Marshal(delta)
		fmt.Fprintf(&sb, "event: content_block_delta\ndata: %s\n\n", data)

	case StreamEventToolUseStart:
		start := map[string]any{
			"type":  "content_block_start",
			"index": 0,
			"content_block": map[string]any{
				"type": "tool_use",
				"id":   ev.ToolID,
				"name": ev.ToolName,
			},
		}
		data, _ := json.Marshal(start)
		fmt.Fprintf(&sb, "event: content_block_start\ndata: %s\n\n", data)

	case StreamEventToolUseDelta:
		delta := map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": ev.InputJSON,
			},
		}
		data, _ := json.Marshal(delta)
		fmt.Fprintf(&sb, "event: content_block_delta\ndata: %s\n\n", data)

	case StreamEventToolUseStop:
		stop := map[string]any{
			"type":  "content_block_stop",
			"index": 0,
		}
		data, _ := json.Marshal(stop)
		fmt.Fprintf(&sb, "event: content_block_stop\ndata: %s\n\n", data)

	case StreamEventMessageStop:
		delta := map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason": string(ev.StopReason),
			},
			"usage": map[string]any{"output_tokens": 0},
		}
		data, _ := json.Marshal(delta)
		fmt.Fprintf(&sb, "event: message_delta\ndata: %s\n\n", data)

		stop := map[string]any{"type": "message_stop"}
		stopData, _ := json.Marshal(stop)
		fmt.Fprintf(&sb, "event: message_stop\ndata: %s\n\n", stopData)
	}

	return sb.String()
}

// ─── Stream translators ──────────────────────────────────────────────────────

// StreamTranslator reads SSE events from an upstream, translates them to the
// downstream's wire format, and writes them to the output.
type StreamTranslator struct {
	reader  io.Reader
	writer  io.Writer
	flusher interface{ Flush() }
	format  WireFormat
}

// NewStreamTranslator creates a new stream translator.
func NewStreamTranslator(reader io.Reader, writer io.Writer, flusher interface{ Flush() }, format WireFormat) *StreamTranslator {
	return &StreamTranslator{
		reader:  reader,
		writer:  writer,
		flusher: flusher,
		format:  format,
	}
}

// Run reads from the upstream, parses to canonical, formats to downstream format, and writes.
func (st *StreamTranslator) Run() error {
	scanner := NewSSEScanner(st.reader)

	for {
		eventType, data, err := scanner.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Parse to canonical based on upstream format.
		var canonical *StreamEvent
		switch st.format {
		case WireOpenAIChatCompletions:
			canonical, err = ParseOpenAIStreamChunk(data)
		case WireAnthropicMessages:
			canonical, err = ParseAnthropicStreamEvent(eventType, data)
		default:
			fmt.Fprintf(st.writer, "event: %s\ndata: %s\n\n", eventType, data)
			if st.flusher != nil {
				st.flusher.Flush()
			}
			continue
		}

		if err != nil {
			continue
		}

		if canonical == nil || canonical.Type == "" || canonical.Type == "openai_keepalive" ||
			canonical.Type == "content_block_start" || canonical.Type == "content_block_stop" {
			continue
		}

		// Format canonical → downstream format.
		var output string
		switch st.format {
		case WireOpenAIChatCompletions:
			output = FormatOpenAIStreamEvent(canonical)
		case WireAnthropicMessages:
			output = FormatAnthropicStreamEvent(canonical)
		default:
			continue
		}

		if output != "" {
			fmt.Fprint(st.writer, output)
			if st.flusher != nil {
				st.flusher.Flush()
			}
		}
	}

	fmt.Fprintf(st.writer, "data: [DONE]\n\n")
	if st.flusher != nil {
		st.flusher.Flush()
	}

	return nil
}

// ─── SSE Scanner ─────────────────────────────────────────────────────────────

// SSEScanner reads SSE events from a reader, yielding (eventType, data) pairs.
type SSEScanner struct {
	scanner *bufio.Scanner
	event   string
	data    json.RawMessage
}

// NewSSEScanner creates a new SSE scanner.
func NewSSEScanner(r io.Reader) *SSEScanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &SSEScanner{scanner: scanner}
}

// Next returns the next SSE event. Returns io.EOF when the stream ends.
func (s *SSEScanner) Next() (eventType string, data json.RawMessage, err error) {
	for s.scanner.Scan() {
		line := s.scanner.Text()

		if strings.HasPrefix(line, ":") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			s.event = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			raw := strings.TrimPrefix(line, "data: ")
			if raw == "[DONE]" {
				return "", nil, io.EOF
			}
			s.data = json.RawMessage(raw)
		} else if line == "" {
			if s.event != "" || s.data != nil {
				eventType = s.event
				data = s.data
				s.event = ""
				s.data = nil
				return eventType, data, nil
			}
		}
	}

	if err := s.scanner.Err(); err != nil {
		return "", nil, err
	}

	return "", nil, io.EOF
}
