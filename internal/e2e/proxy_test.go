package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/woodybriggs/simpllm/internal/config"
	"github.com/woodybriggs/simpllm/internal/proxy"
)

func startProxy(t *testing.T, cfgJSON string) (string, func()) {
	t.Helper()

	mockOpenAI := NewMockOpenAI()
	mockAnthropic := NewMockAnthropic()

	openAIAddr, err := mockOpenAI.Start()
	if err != nil {
		t.Fatalf("start mock openai: %v", err)
	}
	anthropicAddr, err := mockAnthropic.Start()
	if err != nil {
		t.Fatalf("start mock anthropic: %v", err)
	}

	cfgJSON = strings.ReplaceAll(cfgJSON, "{{OPENAI_ADDR}}", openAIAddr)
	cfgJSON = strings.ReplaceAll(cfgJSON, "{{ANTHROPIC_ADDR}}", anthropicAddr)

	cfg, err := config.ParseBytes([]byte(cfgJSON))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	srv := proxy.NewServer(cfg)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go http.Serve(ln, srv)

	proxyURL := fmt.Sprintf("http://%s", ln.Addr().String())

	cleanup := func() {
		mockOpenAI.Close()
		mockAnthropic.Close()
		ln.Close()
	}

	return proxyURL, cleanup
}

func doPost(t *testing.T, url, contentType string, body []byte) *http.Response {
	t.Helper()
	resp, err := http.Post(url, contentType, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func readJSON(t *testing.T, r io.Reader) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(r).Decode(&m); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	return m
}

func TestPassthrough_OpenAIChat(t *testing.T) {
	cfg := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "gpt-4o",
			"upstreams": [{"url": "http://{{OPENAI_ADDR}}/v1/chat/completions", "wire_format": "openai/chat/completions"}]
		}]
	}`
	proxyURL, cleanup := startProxy(t, cfg)
	defer cleanup()

	body := map[string]any{
		"model": "gpt-4o",
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
	}
	data, _ := json.Marshal(body)
	resp := doPost(t, proxyURL+"/v1/chat/completions", "application/json", data)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	result := readJSON(t, resp.Body)
	if result["id"] != "mock-openai-001" {
		t.Errorf("expected id mock-openai-001, got %v", result["id"])
	}
	choices := result["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "hello from the mock upstream" {
		t.Errorf("unexpected content: %v", msg["content"])
	}
}

func TestPassthrough_AnthropicMessages(t *testing.T) {
	cfg := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "claude-sonnet",
			"upstreams": [{"url": "http://{{ANTHROPIC_ADDR}}/v1/messages", "wire_format": "anthropic/messages"}]
		}]
	}`
	proxyURL, cleanup := startProxy(t, cfg)
	defer cleanup()

	body := map[string]any{
		"model":      "claude-sonnet",
		"max_tokens": 1024,
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
	}
	data, _ := json.Marshal(body)
	resp := doPost(t, proxyURL+"/v1/messages", "application/json", data)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	result := readJSON(t, resp.Body)
	if result["id"] != "mock-anthropic-001" {
		t.Errorf("expected id mock-anthropic-001, got %v", result["id"])
	}
	content := result["content"].([]any)
	block := content[0].(map[string]any)
	if block["text"] != "hello from the mock upstream" {
		t.Errorf("unexpected text: %v", block["text"])
	}
}

func TestTranslation_OpenAIToAnthropic(t *testing.T) {
	cfg := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "claude-via-openai",
			"upstreams": [{"url": "http://{{ANTHROPIC_ADDR}}/v1/messages", "wire_format": "anthropic/messages"}]
		}]
	}`
	proxyURL, cleanup := startProxy(t, cfg)
	defer cleanup()

	// Send OpenAI format, upstream is Anthropic.
	body := map[string]any{
		"model": "claude-via-openai",
		"messages": []map[string]any{
			{"role": "system", "content": "you are helpful"},
			{"role": "user", "content": "hi"},
		},
	}
	data, _ := json.Marshal(body)
	resp := doPost(t, proxyURL+"/v1/chat/completions", "application/json", data)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	result := readJSON(t, resp.Body)
	if result["object"] != "chat.completion" {
		t.Errorf("expected object chat.completion, got %v", result["object"])
	}
	choices := result["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "hello from the mock upstream" {
		t.Errorf("unexpected content: %v", msg["content"])
	}
}

func TestTranslation_AnthropicToOpenAI(t *testing.T) {
	cfg := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "gpt-via-anthropic",
			"upstreams": [{"url": "http://{{OPENAI_ADDR}}/v1/chat/completions", "wire_format": "openai/chat/completions"}]
		}]
	}`
	proxyURL, cleanup := startProxy(t, cfg)
	defer cleanup()

	// Send Anthropic format, upstream is OpenAI.
	body := map[string]any{
		"model":      "gpt-via-anthropic",
		"max_tokens": 1024,
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
	}
	data, _ := json.Marshal(body)
	resp := doPost(t, proxyURL+"/v1/messages", "application/json", data)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	result := readJSON(t, resp.Body)
	if result["type"] != "message" {
		t.Errorf("expected type message, got %v", result["type"])
	}
	content := result["content"].([]any)
	block := content[0].(map[string]any)
	if block["text"] != "hello from the mock upstream" {
		t.Errorf("unexpected text: %v", block["text"])
	}
}

func TestError_UnknownModel(t *testing.T) {
	cfg := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "gpt-4o",
			"upstreams": [{"url": "http://{{OPENAI_ADDR}}/v1/chat/completions", "wire_format": "openai/chat/completions"}]
		}]
	}`
	proxyURL, cleanup := startProxy(t, cfg)
	defer cleanup()

	body := map[string]any{
		"model": "nonexistent-model",
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
	}
	data, _ := json.Marshal(body)
	resp := doPost(t, proxyURL+"/v1/chat/completions", "application/json", data)
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestModelsEndpoint(t *testing.T) {
	cfg := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [
			{"name": "gpt-4o", "upstreams": [{"url": "http://{{OPENAI_ADDR}}/v1/chat/completions", "wire_format": "openai/chat/completions"}]},
			{"name": "claude-sonnet", "upstreams": [{"url": "http://{{ANTHROPIC_ADDR}}/v1/messages", "wire_format": "anthropic/messages"}]}
		]
	}`
	proxyURL, cleanup := startProxy(t, cfg)
	defer cleanup()

	resp, err := http.Get(proxyURL + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	result := readJSON(t, resp.Body)
	data := result["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("expected 2 models, got %d", len(data))
	}
}

// ─── Streaming tests ────────────────────────────────────────────────────────

func readSSE(t *testing.T, body io.Reader) []string {
	t.Helper()
	var lines []string
	dec := json.NewDecoder(body)
	scanner := newLineScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}
			lines = append(lines, data)
		}
	}
	_ = dec
	return lines
}

type lineScanner struct {
	scanner *bufio.Scanner
}

func newLineScanner(r io.Reader) *lineScanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &lineScanner{scanner: s}
}

func (s *lineScanner) Scan() bool   { return s.scanner.Scan() }
func (s *lineScanner) Text() string { return s.scanner.Text() }

func TestStream_OpenAIPassthrough(t *testing.T) {
	cfg := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "gpt-4o",
			"upstreams": [{"url": "http://{{OPENAI_ADDR}}/v1/chat/completions", "wire_format": "openai/chat/completions"}]
		}]
	}`
	proxyURL, cleanup := startProxy(t, cfg)
	defer cleanup()

	body := map[string]any{
		"model":  "gpt-4o",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
	}
	data, _ := json.Marshal(body)
	resp := doPost(t, proxyURL+"/v1/chat/completions", "application/json", data)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %s", ct)
	}

	chunks := readSSE(t, resp.Body)
	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks, got %d", len(chunks))
	}

	for i, raw := range chunks {
		var chunk map[string]any
		if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
			t.Fatalf("chunk %d: invalid JSON: %v", i, err)
		}
	}
}

func TestStream_AnthropicPassthrough(t *testing.T) {
	cfg := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "claude-sonnet",
			"upstreams": [{"url": "http://{{ANTHROPIC_ADDR}}/v1/messages", "wire_format": "anthropic/messages"}]
		}]
	}`
	proxyURL, cleanup := startProxy(t, cfg)
	defer cleanup()

	body := map[string]any{
		"model":      "claude-sonnet",
		"max_tokens": 1024,
		"stream":     true,
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
	}
	data, _ := json.Marshal(body)
	resp := doPost(t, proxyURL+"/v1/messages", "application/json", data)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %s", ct)
	}

	chunks := readSSE(t, resp.Body)
	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks, got %d", len(chunks))
	}
}
