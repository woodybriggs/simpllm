package formats

import (
	"encoding/json"
)

// ═══════════════════════════════════════════════════════════════════════════════
// OpenAI Responses API wire format
//
// Request:  POST /v1/responses
// Response: JSON
// ═══════════════════════════════════════════════════════════════════════════════

// ─── Request parsing ────────────────────────────────────────────────────────

// openAIResponsesRequest is the raw OpenAI responses API request.
type openAIResponsesRequest struct {
	Model  string          `json:"model"`
	Input  json.RawMessage `json:"input"`
	Stream bool            `json:"stream,omitempty"`
	Tools  []openAITool    `json:"tools,omitempty"`
}

// ParseResponses parses an OpenAI responses API request into canonical format.
func ParseResponses(body []byte) (*ChatRequest, error) {
	var rr openAIResponsesRequest
	if err := json.Unmarshal(body, &rr); err != nil {
		return nil, err
	}

	req := &ChatRequest{
		Model:  rr.Model,
		Stream: rr.Stream,
	}

	// Input can be string or array of input items.
	var inputStr string
	if json.Unmarshal(rr.Input, &inputStr) == nil {
		req.Messages = append(req.Messages, Message{
			Role:    "user",
			Content: []ContentBlock{{Type: "text", Text: inputStr}},
		})
	} else {
		var items []struct {
			Type    string `json:"type"`
			Role    string `json:"role,omitempty"`
			Content string `json:"content,omitempty"`
		}
		if json.Unmarshal(rr.Input, &items) == nil {
			for _, item := range items {
				role := item.Role
				if role == "" {
					role = "user"
				}
				req.Messages = append(req.Messages, Message{
					Role:    role,
					Content: []ContentBlock{{Type: "text", Text: item.Content}},
				})
			}
		}
	}

	// Parse tools.
	for _, t := range rr.Tools {
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

// FormatResponsesRequest formats a canonical ChatRequest into OpenAI responses API format.
func FormatResponsesRequest(req *ChatRequest) []byte {
	// Build input from messages.
	var input []map[string]any
	for _, m := range req.Messages {
		var textParts []string
		for _, c := range m.Content {
			if c.Type == "text" {
				textParts = append(textParts, c.Text)
			}
		}
		input = append(input, map[string]any{
			"type":    "message",
			"role":    m.Role,
			"content": joinText(textParts),
		})
	}

	out := map[string]any{
		"model":  req.Model,
		"input":  input,
		"stream": req.Stream,
	}

	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type": t.Type,
				"function": map[string]any{
					"name":        t.Function.Name,
					"description": t.Function.Description,
					"parameters":  t.Function.Parameters,
				},
			})
		}
		out["tools"] = tools
	}

	data, _ := json.Marshal(out)
	return data
}

// ─── Response formatting ────────────────────────────────────────────────────

// openAIResponsesResponse is the raw OpenAI responses API response.
type openAIResponsesResponse struct {
	ID     string                `json:"id"`
	Object string                `json:"object"`
	Model  string                `json:"model"`
	Output []openAIResponseOutput `json:"output"`
	Usage  *openAIUsage          `json:"usage,omitempty"`
}

// openAIResponseOutput is a single output item in a responses API response.
type openAIResponseOutput struct {
	Type    string                  `json:"type"`
	CallID  string                  `json:"call_id,omitempty"`
	Name    string                  `json:"name,omitempty"`
	Content []openAIResponseContent `json:"content,omitempty"`
}

// openAIResponseContent is a content item in a responses API output.
type openAIResponseContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// FormatResponses formats a canonical ChatResponse into OpenAI responses API format.
func FormatResponses(resp *ChatResponse) []byte {
	var output []openAIResponseOutput

	// Build message output.
	msgOutput := openAIResponseOutput{
		Type: "message",
	}
	for _, c := range resp.Content {
		switch c.Type {
		case "text":
			msgOutput.Content = append(msgOutput.Content, openAIResponseContent{
				Type: "output_text",
				Text: c.Text,
			})
		case "tool_use":
			output = append(output, openAIResponseOutput{
				Type:   "function_call",
				CallID: c.ID,
				Name:   c.Name,
			})
		}
	}
	if len(msgOutput.Content) > 0 {
		output = append([]openAIResponseOutput{msgOutput}, output...)
	}

	rr := openAIResponsesResponse{
		ID:     resp.ID,
		Object: "response",
		Model:  resp.Model,
		Output: output,
	}

	if resp.Usage != nil {
		rr.Usage = &openAIUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}
	}

	data, _ := json.Marshal(rr)
	return data
}
