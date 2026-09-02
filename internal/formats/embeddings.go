package formats

import "encoding/json"

// ═══════════════════════════════════════════════════════════════════════════════
// Embeddings wire format (passthrough — no translation needed)
// ═══════════════════════════════════════════════════════════════════════════════

// openAIEmbedRequest is the raw OpenAI embeddings request.
type openAIEmbedRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"`
}

// ParseEmbeddings parses an OpenAI embeddings request (passthrough).
func ParseEmbeddings(body []byte) (*ChatRequest, error) {
	var er openAIEmbedRequest
	if err := json.Unmarshal(body, &er); err != nil {
		return nil, err
	}
	return &ChatRequest{
		Model:  er.Model,
		Messages: []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "embed"}}}},
	}, nil
}
