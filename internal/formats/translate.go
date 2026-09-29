package formats

import "encoding/json"

// ═══════════════════════════════════════════════════════════════════════════════
// Translation orchestration
//
// This file ties the parsers and formatters together. The flow is:
//
//	1. Parse incoming request → canonical ChatRequest/EmbedRequest/etc.
//	2. Format canonical → target wire format bytes
//	3. Send to upstream
//	4. Parse upstream response → canonical ChatResponse/etc.
//	5. Format canonical → client's wire format
//
// The key insight: translation is just parse(A) → format(B). No N×N translators.
// ═══════════════════════════════════════════════════════════════════════════════

// WireFormat identifies a supported wire format.
type WireFormat string

const (
	WireOpenAIChatCompletions WireFormat = "openai/chat/completions"
	WireAnthropicMessages     WireFormat = "anthropic/messages"
	WireOpenAIResponses       WireFormat = "openai/responses"
	WireGeminiGenerateContent WireFormat = "gemini/generateContent"
)

// TranslateRequest converts an incoming request body from sourceFormat to targetFormat.
func TranslateRequest(body []byte, sourceFormat, targetFormat WireFormat) ([]byte, error) {
	if sourceFormat == targetFormat {
		return body, nil
	}

	// Step 1: Parse source → canonical.
	chatReq, err := ParseToCanonical(body, sourceFormat)
	if err != nil {
		return nil, err
	}

	// Step 2: Format canonical → target.
	return FormatFromCanonical(chatReq, targetFormat)
}

// TranslateResponse converts an upstream response from upstreamFormat to clientFormat.
func TranslateResponse(body []byte, upstreamFormat, clientFormat WireFormat) ([]byte, error) {
	if upstreamFormat == clientFormat {
		return body, nil
	}

	// Step 1: Parse upstream → canonical.
	chatResp, err := ParseResponseToCanonical(body, upstreamFormat)
	if err != nil {
		return nil, err
	}

	// Step 2: Format canonical → client format.
	return FormatResponseFromCanonical(chatResp, clientFormat)
}

// ─── Request path ────────────────────────────────────────────────────────────

// ParseToCanonical parses a request body into a canonical ChatRequest.
func ParseToCanonical(body []byte, format WireFormat) (*ChatRequest, error) {
	switch format {
	case WireOpenAIChatCompletions:
		return ParseOpenAIChat(body)
	case WireAnthropicMessages:
		return ParseAnthropic(body)
	case WireOpenAIResponses:
		return ParseResponses(body)
	case WireGeminiGenerateContent:
		return ParseGemini(body)
	default:
		return nil, &FormatError{Format: string(format), Message: "unsupported wire format"}
	}
}

// FormatFromCanonical formats a canonical ChatRequest into the target wire format.
func FormatFromCanonical(req *ChatRequest, format WireFormat) ([]byte, error) {
	switch format {
	case WireOpenAIChatCompletions:
		return FormatOpenAIChatRequest(req), nil
	case WireAnthropicMessages:
		return FormatAnthropicRequest(req), nil
	case WireOpenAIResponses:
		return FormatResponsesRequest(req), nil
	case WireGeminiGenerateContent:
		return FormatGeminiRequest(req), nil
	default:
		return nil, &FormatError{Format: string(format), Message: "unsupported wire format"}
	}
}

// ─── Response path ───────────────────────────────────────────────────────────

// ParseResponseToCanonical parses an upstream response into a canonical ChatResponse.
func ParseResponseToCanonical(body []byte, format WireFormat) (*ChatResponse, error) {
	switch format {
	case WireOpenAIChatCompletions:
		return parseOpenAIResponse(body)
	case WireAnthropicMessages:
		return parseAnthropicResponse(body)
	case WireOpenAIResponses:
		return parseResponsesResponse(body)
	case WireGeminiGenerateContent:
		return parseGeminiResponse(body)
	default:
		return nil, &FormatError{Format: string(format), Message: "unsupported wire format"}
	}
}

// FormatResponseFromCanonical formats a canonical ChatResponse into the target wire format.
func FormatResponseFromCanonical(resp *ChatResponse, format WireFormat) ([]byte, error) {
	switch format {
	case WireOpenAIChatCompletions:
		return FormatOpenAIChat(resp), nil
	case WireAnthropicMessages:
		return FormatAnthropic(resp), nil
	case WireOpenAIResponses:
		return FormatResponses(resp), nil
	case WireGeminiGenerateContent:
		return FormatGemini(resp), nil
	default:
		return nil, &FormatError{Format: string(format), Message: "unsupported wire format"}
	}
}

// ─── Response parsing helpers ────────────────────────────────────────────────
// These parse raw upstream responses into our canonical ChatResponse.

func parseOpenAIResponse(body []byte) (*ChatResponse, error) {
	var ocr openAIChatResponse
	if err := json.Unmarshal(body, &ocr); err != nil {
		return nil, err
	}

	resp := &ChatResponse{
		ID:    ocr.ID,
		Model: ocr.Model,
	}

	if ocr.Usage != nil {
		resp.Usage = &Usage{
			InputTokens:  ocr.Usage.PromptTokens,
			OutputTokens: ocr.Usage.CompletionTokens,
			TotalTokens:  ocr.Usage.TotalTokens,
		}
	}

	// Convert choices to content blocks.
	for _, c := range ocr.Choices {
		if c.Message != nil {
			resp.Content = append(resp.Content, parseOpenAIContent(c.Message.Content)...)

			// Extract tool calls.
			for _, tc := range c.Message.ToolCalls {
				var input map[string]any
				json.Unmarshal([]byte(tc.Function.Arguments), &input)
				resp.Content = append(resp.Content, ContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: input,
				})
			}
		}
		// Map finish reason.
		if c.FinishReason != nil {
			resp.StopReason = mapOpenAIFinishReason(*c.FinishReason)
		}
	}

	return resp, nil
}

func mapOpenAIFinishReason(reason string) StopReason {
	switch reason {
	case "stop":
		return StopReasonEndTurn
	case "tool_calls":
		return StopReasonToolUse
	case "length":
		return StopReasonMaxTokens
	default:
		return StopReason(reason)
	}
}

func parseOpenAIContent(raw json.RawMessage) []ContentBlock {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if text != "" {
			return []ContentBlock{{Type: "text", Text: text}}
		}
		return nil
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var result []ContentBlock
		for _, b := range blocks {
			result = append(result, ContentBlock{Type: b.Type, Text: b.Text})
		}
		return result
	}

	return nil
}

func parseAnthropicResponse(body []byte) (*ChatResponse, error) {
	var ar anthropicResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, err
	}

	resp := &ChatResponse{
		ID:    ar.ID,
		Model: ar.Model,
	}

	if ar.Usage != nil {
		resp.Usage = &Usage{
			InputTokens:  ar.Usage.InputTokens,
			OutputTokens: ar.Usage.OutputTokens,
			TotalTokens:  ar.Usage.InputTokens + ar.Usage.OutputTokens,
		}
	}

	// Convert content blocks.
	for _, block := range ar.Content {
		switch block.Type {
		case "text":
			resp.Content = append(resp.Content, TextBlock(block.Text))
		case "tool_use":
			var input map[string]any
			if block.Input != nil {
				inputBytes, _ := json.Marshal(block.Input)
				json.Unmarshal(inputBytes, &input)
			}
			resp.Content = append(resp.Content, ContentBlock{
				Type:  "tool_use",
				ID:    block.ID,
				Name:  block.Name,
				Input: input,
			})
		}
	}

	// Map stop reason.
	switch ar.StopReason {
	case "end_turn":
		resp.StopReason = StopReasonEndTurn
	case "tool_use":
		resp.StopReason = StopReasonToolUse
	case "max_tokens":
		resp.StopReason = StopReasonMaxTokens
	case "stop_sequence":
		resp.StopReason = StopReasonStopSequence
	default:
		resp.StopReason = StopReason(ar.StopReason)
	}

	return resp, nil
}

func parseResponsesResponse(body []byte) (*ChatResponse, error) {
	var rr openAIResponsesResponse
	if err := json.Unmarshal(body, &rr); err != nil {
		return nil, err
	}

	resp := &ChatResponse{
		ID:    rr.ID,
		Model: rr.Model,
	}

	if rr.Usage != nil {
		resp.Usage = &Usage{
			InputTokens:  rr.Usage.PromptTokens,
			OutputTokens: rr.Usage.CompletionTokens,
			TotalTokens:  rr.Usage.TotalTokens,
		}
	}

	for _, out := range rr.Output {
		switch out.Type {
		case "message":
			for _, c := range out.Content {
				if c.Type == "output_text" {
					resp.Content = append(resp.Content, TextBlock(c.Text))
				}
			}
		case "function_call":
			resp.Content = append(resp.Content, ContentBlock{
				Type: "tool_use",
				ID:   out.CallID,
				Name: out.Name,
			})
		}
	}

	if len(resp.Content) > 0 {
		hasToolUse := false
		for _, b := range resp.Content {
			if b.Type == "tool_use" {
				hasToolUse = true
				break
			}
		}
		if hasToolUse {
			resp.StopReason = StopReasonToolUse
		} else {
			resp.StopReason = StopReasonEndTurn
		}
	}

	return resp, nil
}

// ─── Error type ──────────────────────────────────────────────────────────────

// FormatError represents a wire format error.
type FormatError struct {
	Format  string
	Message string
}

func (e *FormatError) Error() string {
	return "format error [" + e.Format + "]: " + e.Message
}
