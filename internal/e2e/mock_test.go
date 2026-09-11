package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// MockUpstream is a test HTTP server that simulates an upstream AI provider.
type MockUpstream struct {
	Addr   string
	Server *http.Server

	mu      sync.Mutex
	Request *CapturedRequest
}

type CapturedRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

func newMock(handler http.HandlerFunc) *MockUpstream {
	return &MockUpstream{
		Server: &http.Server{
			Addr:              ":0",
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

func (m *MockUpstream) capture(r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Request != nil {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		m.Request.Method = r.Method
		m.Request.Path = r.URL.Path
		m.Request.Header = r.Header.Clone()
		m.Request.Body = body
	}
}

func (m *MockUpstream) Start() (string, error) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return "", err
	}
	m.Addr = ln.Addr().String()
	go m.Server.Serve(ln)
	return m.Addr, nil
}

func (m *MockUpstream) Close() {
	m.Server.Close()
}

func (m *MockUpstream) SetRequestCapture(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if enabled {
		m.Request = &CapturedRequest{}
	} else {
		m.Request = nil
	}
}

func (m *MockUpstream) GetCapturedRequest() *CapturedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Request
}

// ═══════════════════════════════════════════════════════════════════════════════
// OpenAI mock
// ═══════════════════════════════════════════════════════════════════════════════

func NewMockOpenAI() *MockUpstream {
	m := &MockUpstream{}
	m.Server = &http.Server{
		Addr: ":0",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m.capture(r)

			body, _ := io.ReadAll(r.Body)
			var req struct {
				Model  string `json:"model"`
				Stream bool   `json:"stream"`
			}
			json.Unmarshal(body, &req)

			if req.Stream {
				writeOpenAIStream(w, req.Model, "hello from the mock upstream")
				return
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"id":      "mock-openai-001",
				"object":  "chat.completion",
				"created": time.Now().Unix(),
				"model":   req.Model,
				"choices": []map[string]any{{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "hello from the mock upstream",
					},
					"finish_reason": "stop",
				}},
				"usage": map[string]any{
					"prompt_tokens":     10,
					"completion_tokens": 5,
					"total_tokens":      15,
				},
			})
		}),
	}
	return m
}

// ═══════════════════════════════════════════════════════════════════════════════
// Anthropic mock
// ═══════════════════════════════════════════════════════════════════════════════

func NewMockAnthropic() *MockUpstream {
	m := &MockUpstream{}
	m.Server = &http.Server{
		Addr: ":0",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m.capture(r)

			body, _ := io.ReadAll(r.Body)
			var req struct {
				Model  string `json:"model"`
				Stream bool   `json:"stream"`
			}
			json.Unmarshal(body, &req)

			if req.Stream {
				writeAnthropicStream(w, req.Model, "hello from the mock upstream")
				return
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"id":   "mock-anthropic-001",
				"type": "message",
				"role": "assistant",
				"content": []map[string]any{{
					"type": "text",
					"text": "hello from the mock upstream",
				}},
				"model":       req.Model,
				"stop_reason": "end_turn",
				"usage": map[string]any{
					"input_tokens":  10,
					"output_tokens": 5,
				},
			})
		}),
	}
	return m
}

// ═══════════════════════════════════════════════════════════════════════════════
// SSE streaming helpers
// ═══════════════════════════════════════════════════════════════════════════════

func writeOpenAIStream(w http.ResponseWriter, model, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher := w.(http.Flusher)

	chunk := map[string]any{
		"id":      "mock-stream-001",
		"object":  "chat.completion.chunk",
		"model":   model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant"}}},
	}
	data, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	chunk["choices"] = []map[string]any{{"index": 0, "delta": map[string]any{"content": text}}}
	data, _ = json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	reason := "stop"
	chunk["choices"] = []map[string]any{{"index": 0, "finish_reason": &reason}}
	data, _ = json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func writeAnthropicStream(w http.ResponseWriter, model, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher := w.(http.Flusher)

	send := func(event string, v any) {
		data, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
	}

	send("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": "mock-stream-anth-001", "type": "message", "role": "assistant",
			"content": []any{}, "model": model,
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})

	send("content_block_start", map[string]any{
		"type": "content_block_start", "index": 0,
		"content_block": map[string]any{"type": "text", "text": ""},
	})

	send("content_block_delta", map[string]any{
		"type": "content_block_delta", "index": 0,
		"delta": map[string]any{"type": "text_delta", "text": text},
	})

	send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})

	send("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn"},
		"usage": map[string]any{"output_tokens": 5},
	})

	send("message_stop", map[string]any{"type": "message_stop"})
}
