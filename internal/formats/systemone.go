package formats

import "encoding/json"

// ═══════════════════════════════════════════════════════════════════════════════
// System One (Decisions) format
//
// The System One API sends state and typed questions to a model and returns
// structured answers. It is NOT a chat completion format — it has no messages,
// no streaming, and no text generation. Instead it classifies, scores, or
// evaluates content against typed questions.
//
// Because it doesn't map to the canonical ChatRequest/ChatResponse types,
// it is handled as a passthrough proxy (like embeddings and images).
// ═══════════════════════════════════════════════════════════════════════════════

// SystemOneRequest is the wire format for POST /systemone.
type SystemOneRequest struct {
	Model     string          `json:"model"`
	Provider  any             `json:"provider,omitempty"`
	State     any             `json:"state"`
	Questions json.RawMessage `json:"questions"`
	SessionID string          `json:"session_id,omitempty"`
	Trace     json.RawMessage `json:"trace,omitempty"`
	User      string          `json:"user,omitempty"`
}

// ParseSystemOne validates that a request body is a well-formed System One request.
// It returns the model name for upstream routing.
func ParseSystemOne(body []byte) (string, error) {
	var req SystemOneRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return "", &FormatError{Format: "systemone", Message: "invalid JSON: " + err.Error()}
	}
	if req.Model == "" {
		return "", &FormatError{Format: "systemone", Message: "model is required"}
	}
	if req.State == nil {
		return "", &FormatError{Format: "systemone", Message: "state is required"}
	}
	if len(req.Questions) == 0 {
		return "", &FormatError{Format: "systemone", Message: "questions is required"}
	}
	return req.Model, nil
}
