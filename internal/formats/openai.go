package formats

import (
	"encoding/json"
)

// ═══════════════════════════════════════════════════════════════════════════════
// OpenAI Chat Completions wire format
//
// Request:  POST /v1/chat/completions
// Response: JSON or SSE stream
// ═══════════════════════════════════════════════════════════════════════════════

// ─── Request parsing ────────────────────────────────────────────────────────

// openAIChatRequest is the raw OpenAI chat completions request.
type openAIChatRequest struct {
	Model       string            `json:"model"`
	Messages    []openAIMessage   `json:"messages"`
	MaxTokens   int               `json:"max_tokens,omitempty"`
	Temperature *float64          `json:"temperature,omitempty"`
	TopP        *float64          `json:"top_p,omitempty"`
	Stream      bool              `json:"stream,omitempty"`
	Tools       []openAITool      `json:"tools,omitempty"`
}

// openAIMessage is an OpenAI message (used in both request and response).
type openAIMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// openAITool is an OpenAI tool definition.
type openAITool struct {
	Type     string         `json:"type"`
	Function openAIToolFunc `json:"function"`
}

// openAIToolFunc is the function part of an OpenAI tool.
type openAIToolFunc struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// openAIToolCall is a tool call in an OpenAI response.
type openAIToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function openAIToolCallFn `json:"function"`
}

// openAIToolCallFn is the function part of a tool call.
type openAIToolCallFn struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ParseOpenAIChat parses an OpenAI chat completions request into canonical format.
func ParseOpenAIChat(body []byte) (*ChatRequest, error) {
	var ocr openAIChatRequest
	if err := json.Unmarshal(body, &ocr); err != nil {
		return nil, err
	}

	req := &ChatRequest{
		Model:       ocr.Model,
		MaxTokens:   ocr.MaxTokens,
		Temperature: ocr.Temperature,
		TopP:        ocr.TopP,
		Stream:      ocr.Stream,
	}

	// Parse messages.
	for _, m := range ocr.Messages {
		msg := Message{Role: m.Role}

		// Content can be string or array of content blocks.
		var content string
		if json.Unmarshal(m.Content, &content) == nil {
			if content != "" {
				msg.Content = append(msg.Content, ContentBlock{Type: "text", Text: content})
			}
		} else {
			var blocks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(m.Content, &blocks) == nil {
				for _, b := range blocks {
					msg.Content = append(msg.Content, ContentBlock{Type: b.Type, Text: b.Text})
				}
			}
		}

		// Extract system messages.
		if m.Role == "system" {
			req.System = append(req.System, msg.Content...)
			continue
		}

		// Extract tool calls.
		for _, tc := range m.ToolCalls {
			var input map[string]any
			json.Unmarshal([]byte(tc.Function.Arguments), &input)
			msg.Content = append(msg.Content, ContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: input,
			})
		}

		req.Messages = append(req.Messages, msg)
	}

	// Parse tools.
	for _, t := range ocr.Tools {
		req.Tools = append(req.Tools, Tool{
			Type: t.Type,
			Function: ToolFunction{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			},
		})
	}

	return req, nil
}

// ─── Request formatting ─────────────────────────────────────────────────────

// FormatOpenAIChatRequest formats a canonical ChatRequest into OpenAI chat completions format.
func FormatOpenAIChatRequest(req *ChatRequest) []byte {
	out := openAIChatRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
	}

	// Add system message.
	if len(req.System) > 0 {
		sysContent := formatContentBlocks(req.System)
		data, _ := json.Marshal(sysContent)
		out.Messages = append(out.Messages, openAIMessage{Role: "system", Content: data})
	}

	// Add messages.
	for _, m := range req.Messages {
		om := openAIMessage{Role: m.Role}

		// Separate text content from tool calls.
		var textParts []string
		for _, c := range m.Content {
			switch c.Type {
			case "text":
				textParts = append(textParts, c.Text)
			case "tool_use":
				args, _ := json.Marshal(c.Input)
				om.ToolCalls = append(om.ToolCalls, openAIToolCall{
					ID:   c.ID,
					Type: "function",
					Function: openAIToolCallFn{
						Name:      c.Name,
						Arguments: string(args),
					},
				})
			case "tool_result":
				om.ToolCallID = c.ID
				om.Role = "tool"
				textParts = append(textParts, c.Text)
			}
		}

		if len(textParts) > 0 {
			content := joinText(textParts)
			data, _ := json.Marshal(content)
			om.Content = data
		} else {
			om.Content = json.RawMessage(`""`)
		}

		out.Messages = append(out.Messages, om)
	}

	// Add tools.
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, openAITool{
			Type: t.Type,
			Function: openAIToolFunc{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			},
		})
	}

	data, _ := json.Marshal(out)
	return data
}

// ─── Response formatting ────────────────────────────────────────────────────

// openAIChatResponse is the raw OpenAI chat completions response.
type openAIChatResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage,omitempty"`
}

// openAIChoice is a single choice in an OpenAI response.
type openAIChoice struct {
	Index        int            `json:"index"`
	Message      *openAIMessage `json:"message,omitempty"`
	FinishReason *string        `json:"finish_reason,omitempty"`
}

// openAIUsage is the usage block in an OpenAI response.
type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// FormatOpenAIChat formats a canonical ChatResponse into OpenAI chat completions format.
func FormatOpenAIChat(resp *ChatResponse) []byte {
	choice := openAIChoice{
		Index: 0,
		Message: &openAIMessage{
			Role: "assistant",
		},
	}

	// Build content and tool calls.
	var textParts []string
	for _, c := range resp.Content {
		switch c.Type {
		case "text":
			textParts = append(textParts, c.Text)
		case "tool_use":
			args, _ := json.Marshal(c.Input)
			choice.Message.ToolCalls = append(choice.Message.ToolCalls, openAIToolCall{
				ID:   c.ID,
				Type: "function",
				Function: openAIToolCallFn{
					Name:      c.Name,
					Arguments: string(args),
				},
			})
		}
	}

	if len(textParts) > 0 {
		content := joinText(textParts)
		data, _ := json.Marshal(content)
		choice.Message.Content = data
	} else {
		choice.Message.Content = json.RawMessage(`""`)
	}

	// Map stop reason.
	if resp.StopReason != "" {
		reason := string(resp.StopReason)
		switch resp.StopReason {
		case StopReasonEndTurn:
			reason = "stop"
		case StopReasonToolUse:
			reason = "tool_calls"
		case StopReasonMaxTokens:
			reason = "length"
		}
		choice.FinishReason = &reason
	}

	ocr := openAIChatResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Model:   resp.Model,
		Choices: []openAIChoice{choice},
	}

	if resp.Usage != nil {
		ocr.Usage = &openAIUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}
	}

	data, _ := json.Marshal(ocr)
	return data
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func formatContentBlocks(blocks []ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return joinText(parts)
}

func joinText(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += "\n" + p
	}
	return result
}
