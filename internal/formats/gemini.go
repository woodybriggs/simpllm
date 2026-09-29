package formats

import (
	"encoding/json"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Gemini GenerateContent wire format
//
// Request:  POST /v1beta/models/{model}:generateContent
// Response: JSON or SSE stream via streamGenerateContent
// ═══════════════════════════════════════════════════════════════════════════════

// ─── Request types ──────────────────────────────────────────────────────────

type geminiRequest struct {
	Contents          []geminiContent          `json:"contents"`
	SystemInstruction *geminiContent           `json:"systemInstruction,omitempty"`
	Tools             []geminiTool             `json:"tools,omitempty"`
	GenerationConfig  *geminiGenerationConfig  `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall   `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
	InlineData       *geminiBlob           `json:"inlineData,omitempty"`
	FileData         *geminiFileData       `json:"fileData,omitempty"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response,omitempty"`
}

type geminiBlob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFileData struct {
	MimeType string `json:"mimeType"`
	FileURI  string `json:"fileUri"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDecl `json:"functionDeclarations"`
}

type geminiFunctionDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
}

// ─── Response types ─────────────────────────────────────────────────────────

type geminiResponse struct {
	Candidates    []geminiCandidate    `json:"candidates"`
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion  string               `json:"modelVersion,omitempty"`
}

type geminiCandidate struct {
	Content       *geminiContent `json:"content,omitempty"`
	FinishReason  string         `json:"finishReason,omitempty"`
	Index         int            `json:"index,omitempty"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// ─── Request parsing ────────────────────────────────────────────────────────

// ParseGemini parses a Gemini generateContent request into canonical format.
func ParseGemini(body []byte) (*ChatRequest, error) {
	var gr geminiRequest
	if err := json.Unmarshal(body, &gr); err != nil {
		return nil, err
	}

	req := &ChatRequest{
		Stream: false, // non-streaming endpoint
	}

	// System instruction.
	if gr.SystemInstruction != nil {
		for _, p := range gr.SystemInstruction.Parts {
			if p.Text != "" {
				req.System = append(req.System, TextBlock(p.Text))
			}
		}
	}

	// Contents → messages.
	for _, c := range gr.Contents {
		msg := Message{Role: c.Role}

		// Gemini uses "model" where we use "assistant".
		if msg.Role == "model" {
			msg.Role = "assistant"
		}

		for _, p := range c.Parts {
			switch {
			case p.Text != "":
				msg.Content = append(msg.Content, ContentBlock{Type: "text", Text: p.Text})
			case p.FunctionCall != nil:
				msg.Content = append(msg.Content, ContentBlock{
					Type:  "tool_use",
					Name:  p.FunctionCall.Name,
					Input: p.FunctionCall.Args,
				})
			case p.FunctionResponse != nil:
				msg.Content = append(msg.Content, ContentBlock{
					Type:  "tool_result",
					Name:  p.FunctionResponse.Name,
					Input: p.FunctionResponse.Response,
				})
			}
		}

		// Extract system messages.
		if msg.Role == "system" {
			req.System = append(req.System, msg.Content...)
			continue
		}

		req.Messages = append(req.Messages, msg)
	}

	// Generation config.
	if gr.GenerationConfig != nil {
		req.Temperature = gr.GenerationConfig.Temperature
		req.TopP = gr.GenerationConfig.TopP
		req.MaxTokens = gr.GenerationConfig.MaxOutputTokens
	}

	// Tools.
	for _, t := range gr.Tools {
		for _, fd := range t.FunctionDeclarations {
			req.Tools = append(req.Tools, Tool{
				Type: "function",
				Function: ToolFunction{
					Name:        fd.Name,
					Description: fd.Description,
					Parameters:  fd.Parameters,
				},
			})
		}
	}

	return req, nil
}

// ─── Request formatting ─────────────────────────────────────────────────────

// FormatGeminiRequest formats a canonical ChatRequest into Gemini generateContent format.
func FormatGeminiRequest(req *ChatRequest) []byte {
	gr := geminiRequest{}

	// System instruction.
	if len(req.System) > 0 {
		var parts []geminiPart
		for _, b := range req.System {
			if b.Type == "text" {
				parts = append(parts, geminiPart{Text: b.Text})
			}
		}
		if len(parts) > 0 {
			gr.SystemInstruction = &geminiContent{Parts: parts}
		}
	}

	// Messages → contents.
	for _, m := range req.Messages {
		role := m.Role
		if role == "assistant" {
			role = "model"
		}

		gc := geminiContent{Role: role}

		for _, c := range m.Content {
			switch c.Type {
			case "text":
				gc.Parts = append(gc.Parts, geminiPart{Text: c.Text})
			case "tool_use":
				gc.Parts = append(gc.Parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: c.Name,
						Args: c.Input,
					},
				})
			case "tool_result":
				gc.Parts = append(gc.Parts, geminiPart{
					FunctionResponse: &geminiFunctionResponse{
						Name:     c.Name,
						Response: c.Input,
					},
				})
			}
		}

		// Gemini requires at least one part. Send empty text if no content.
		if len(gc.Parts) == 0 {
			gc.Parts = []geminiPart{{Text: ""}}
		}

		gr.Contents = append(gr.Contents, gc)
	}

	// Generation config.
	if req.MaxTokens > 0 || req.Temperature != nil || req.TopP != nil {
		gr.GenerationConfig = &geminiGenerationConfig{
			MaxOutputTokens: req.MaxTokens,
			Temperature:     req.Temperature,
			TopP:            req.TopP,
		}
	}

	// Tools.
	for _, t := range req.Tools {
		fd := geminiFunctionDecl{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		}
		gr.Tools = append(gr.Tools, geminiTool{
			FunctionDeclarations: []geminiFunctionDecl{fd},
		})
	}

	data, _ := json.Marshal(gr)
	return data
}

// ─── Response formatting ────────────────────────────────────────────────────

// FormatGemini formats a canonical ChatResponse into Gemini generateContent response format.
func FormatGemini(resp *ChatResponse) []byte {
	candidate := geminiCandidate{
		Index: 0,
		Content: &geminiContent{
			Role: "model",
		},
	}

	// Content blocks → parts.
	for _, c := range resp.Content {
		switch c.Type {
		case "text":
			candidate.Content.Parts = append(candidate.Content.Parts, geminiPart{Text: c.Text})
		case "tool_use":
			candidate.Content.Parts = append(candidate.Content.Parts, geminiPart{
				FunctionCall: &geminiFunctionCall{
					Name: c.Name,
					Args: c.Input,
				},
			})
		}
	}

	// If no parts, add empty text.
	if len(candidate.Content.Parts) == 0 {
		candidate.Content.Parts = []geminiPart{{Text: ""}}
	}

	// Map stop reason.
	switch resp.StopReason {
	case StopReasonEndTurn:
		candidate.FinishReason = "STOP"
	case StopReasonMaxTokens:
		candidate.FinishReason = "MAX_TOKENS"
	case StopReasonToolUse:
		candidate.FinishReason = "STOP" // Gemini doesn't have a tool stop reason
	default:
		candidate.FinishReason = string(resp.StopReason)
	}

	gr := geminiResponse{
		Candidates:   []geminiCandidate{candidate},
		ModelVersion: resp.Model,
	}

	if resp.Usage != nil {
		gr.UsageMetadata = &geminiUsageMetadata{
			PromptTokenCount:     resp.Usage.InputTokens,
			CandidatesTokenCount: resp.Usage.OutputTokens,
			TotalTokenCount:      resp.Usage.TotalTokens,
		}
	}

	data, _ := json.Marshal(gr)
	return data
}

// ─── Response parsing ───────────────────────────────────────────────────────

func parseGeminiResponse(body []byte) (*ChatResponse, error) {
	var gr geminiResponse
	if err := json.Unmarshal(body, &gr); err != nil {
		return nil, err
	}

	resp := &ChatResponse{
		Model: gr.ModelVersion,
	}

	if gr.UsageMetadata != nil {
		resp.Usage = &Usage{
			InputTokens:  gr.UsageMetadata.PromptTokenCount,
			OutputTokens: gr.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  gr.UsageMetadata.TotalTokenCount,
		}
	}

	for _, c := range gr.Candidates {
		if c.Content != nil {
			for _, p := range c.Content.Parts {
				switch {
				case p.Text != "":
					resp.Content = append(resp.Content, TextBlock(p.Text))
				case p.FunctionCall != nil:
					resp.Content = append(resp.Content, ContentBlock{
						Type:  "tool_use",
						Name:  p.FunctionCall.Name,
						Input: p.FunctionCall.Args,
					})
				}
			}
		}

		// Map finish reason.
		switch c.FinishReason {
		case "STOP":
			resp.StopReason = StopReasonEndTurn
		case "MAX_TOKENS":
			resp.StopReason = StopReasonMaxTokens
		case "SAFETY", "RECITATION", "BLOCKLIST", "OTHER":
			resp.StopReason = StopReason(c.FinishReason)
		}
	}

	return resp, nil
}
