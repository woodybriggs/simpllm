package formats

import (
	"encoding/json"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Anthropic Messages wire format
//
// Request:  POST /v1/messages
// Response: JSON or SSE stream
// ═══════════════════════════════════════════════════════════════════════════════

// ─── Request parsing ────────────────────────────────────────────────────────

// anthropicRequest is the raw Anthropic messages request.
type anthropicRequest struct {
	Model     string              `json:"model"`
	MaxTokens int                 `json:"max_tokens"`
	System    json.RawMessage     `json:"system,omitempty"`
	Messages  []anthropicMessage  `json:"messages"`
	Stream    bool                `json:"stream,omitempty"`
	Tools     []anthropicTool     `json:"tools,omitempty"`
}

// anthropicMessage is an Anthropic message.
type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// anthropicTool is an Anthropic tool definition.
type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

// ParseAnthropic parses an Anthropic messages request into canonical format.
func ParseAnthropic(body []byte) (*ChatRequest, error) {
	var ar anthropicRequest
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, err
	}

	req := &ChatRequest{
		Model:     ar.Model,
		MaxTokens: ar.MaxTokens,
		Stream:    ar.Stream,
	}

	// Parse system prompt.
	if len(ar.System) > 0 {
		// System can be string or array of content blocks.
		var sysStr string
		if json.Unmarshal(ar.System, &sysStr) == nil {
			req.System = append(req.System, ContentBlock{Type: "text", Text: sysStr})
		} else {
			var blocks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(ar.System, &blocks) == nil {
				for _, b := range blocks {
					req.System = append(req.System, ContentBlock{Type: b.Type, Text: b.Text})
				}
			}
		}
	}

	// Parse messages.
	for _, m := range ar.Messages {
		msg := Message{Role: m.Role}

		// Content can be string or array.
		var contentStr string
		if json.Unmarshal(m.Content, &contentStr) == nil {
			if contentStr != "" {
				msg.Content = append(msg.Content, ContentBlock{Type: "text", Text: contentStr})
			}
		} else {
			var blocks []struct {
				Type  string         `json:"type"`
				Text  string         `json:"text,omitempty"`
				ID    string         `json:"id,omitempty"`
				Name  string         `json:"name,omitempty"`
				Input map[string]any `json:"input,omitempty"`
			}
			if json.Unmarshal(m.Content, &blocks) == nil {
				for _, b := range blocks {
					switch b.Type {
					case "text":
						msg.Content = append(msg.Content, ContentBlock{Type: "text", Text: b.Text})
					case "tool_use":
						msg.Content = append(msg.Content, ContentBlock{
							Type:  "tool_use",
							ID:    b.ID,
							Name:  b.Name,
							Input: b.Input,
						})
					case "tool_result":
						msg.Content = append(msg.Content, ContentBlock{
							Type: "tool_result",
							ID:   b.ID,
							Text: b.Text,
						})
					}
				}
			}
		}

		req.Messages = append(req.Messages, msg)
	}

	// Parse tools.
	for _, t := range ar.Tools {
		req.Tools = append(req.Tools, Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	return req, nil
}

// ─── Request formatting ─────────────────────────────────────────────────────

// FormatAnthropicRequest formats a canonical ChatRequest into Anthropic messages format.
func FormatAnthropicRequest(req *ChatRequest) []byte {
	out := anthropicRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Stream:    req.Stream,
	}

	// Format system prompt.
	if len(req.System) > 0 {
		var parts []string
		for _, b := range req.System {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		if len(parts) == 1 {
			data, _ := json.Marshal(parts[0])
			out.System = data
		} else if len(parts) > 1 {
			content := joinText(parts)
			data, _ := json.Marshal(content)
			out.System = data
		}
	}

	// Format messages.
	for _, m := range req.Messages {
		am := anthropicMessage{Role: m.Role}

		var blocks []map[string]any
		for _, c := range m.Content {
			switch c.Type {
			case "text":
				blocks = append(blocks, map[string]any{
					"type": "text",
					"text": c.Text,
				})
			case "tool_use":
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    c.ID,
					"name":  c.Name,
					"input": c.Input,
				})
			case "tool_result":
				blocks = append(blocks, map[string]any{
					"type":      "tool_result",
					"tool_use_id": c.ID,
					"content":   c.Text,
				})
			}
		}

		if len(blocks) == 1 {
			data, _ := json.Marshal(blocks[0])
			am.Content = data
		} else if len(blocks) > 1 {
			data, _ := json.Marshal(blocks)
			am.Content = data
		} else {
			am.Content = json.RawMessage(`""`)
		}

		out.Messages = append(out.Messages, am)
	}

	// Format tools.
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, anthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}

	data, _ := json.Marshal(out)
	return data
}

// ─── Response formatting ────────────────────────────────────────────────────

// anthropicResponse is the raw Anthropic messages response.
type anthropicResponse struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Role       string             `json:"role"`
	Content    []anthropicContent `json:"content"`
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
	Usage      *anthropicUsage    `json:"usage,omitempty"`
}

// anthropicContent is a content block in an Anthropic response.
type anthropicContent struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`
}

// anthropicUsage is the usage block in an Anthropic response.
type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// FormatAnthropic formats a canonical ChatResponse into Anthropic messages format.
func FormatAnthropic(resp *ChatResponse) []byte {
	var content []anthropicContent
	for _, c := range resp.Content {
		switch c.Type {
		case "text":
			content = append(content, anthropicContent{Type: "text", Text: c.Text})
		case "tool_use":
			content = append(content, anthropicContent{
				Type:  "tool_use",
				ID:    c.ID,
				Name:  c.Name,
				Input: c.Input,
			})
		}
	}

	ar := anthropicResponse{
		ID:      resp.ID,
		Type:    "message",
		Role:    "assistant",
		Content: content,
		Model:   resp.Model,
	}

	// Map stop reason.
	switch resp.StopReason {
	case StopReasonEndTurn:
		ar.StopReason = "end_turn"
	case StopReasonToolUse:
		ar.StopReason = "tool_use"
	case StopReasonMaxTokens:
		ar.StopReason = "max_tokens"
	case StopReasonStopSequence:
		ar.StopReason = "stop_sequence"
	default:
		ar.StopReason = string(resp.StopReason)
	}

	if resp.Usage != nil {
		ar.Usage = &anthropicUsage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		}
	}

	data, _ := json.Marshal(ar)
	return data
}
