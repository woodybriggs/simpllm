package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/woodybriggs/simpllm/internal/config"
	"github.com/woodybriggs/simpllm/internal/formats"
	"github.com/woodybriggs/simpllm/internal/proxy"
	"github.com/woodybriggs/simpllm/internal/secret"
)

func startProxyWithMiddleware(t *testing.T, cfgJSON string, setup func(s *proxy.Server)) (string, func()) {
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
	if setup != nil {
		setup(srv)
	}

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

// ═══════════════════════════════════════════════════════════════════════════════
// E2E: Pre-Translation middleware
// ═══════════════════════════════════════════════════════════════════════════════

func TestMiddleware_PreTranslation_ShortCircuit(t *testing.T) {
	cfg := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "gpt-4o",
			"upstreams": [{"url": "http://{{OPENAI_ADDR}}/v1/chat/completions", "wire_format": "openai/chat/completions"}]
		}, {
			"name": "blocked-model",
			"upstreams": [{"url": "http://{{OPENAI_ADDR}}/v1/chat/completions", "wire_format": "openai/chat/completions"}]
		}]
	}`

	proxyURL, cleanup := startProxyWithMiddleware(t, cfg, func(s *proxy.Server) {
		s.UsePreTranslation(func(ctx *proxy.Context) error {
			if ctx.Model == "blocked-model" {
				ctx.ShortCircuit = true
				ctx.StatusCode = 403
				ctx.ResponseBody = []byte(`{"error": {"message": "model blocked by policy"}}`)
			}
			return nil
		})
	})
	defer cleanup()

	body := map[string]any{
		"model": "blocked-model",
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
	}
	data, _ := json.Marshal(body)
	resp := doPost(t, proxyURL+"/v1/chat/completions", "application/json", data)
	defer resp.Body.Close()

	if resp.StatusCode != 403 {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	errObj := result["error"].(map[string]any)
	if errObj["message"] != "model blocked by policy" {
		t.Errorf("unexpected error message: %v", errObj["message"])
	}
}

func TestMiddleware_PreTranslation_ModifyBody(t *testing.T) {
	cfg := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "gpt-4o",
			"upstreams": [{"url": "http://{{OPENAI_ADDR}}/v1/chat/completions", "wire_format": "openai/chat/completions"}]
		}, {
			"name": "original-model",
			"upstreams": [{"url": "http://{{OPENAI_ADDR}}/v1/chat/completions", "wire_format": "openai/chat/completions"}]
		}]
	}`

	proxyURL, cleanup := startProxyWithMiddleware(t, cfg, func(s *proxy.Server) {
		s.UsePreTranslation(func(ctx *proxy.Context) error {
			ctx.Model = "gpt-4o"
			var msg map[string]any
			json.Unmarshal(ctx.Body, &msg)
			msg["model"] = "gpt-4o"
			msg["messages"] = []map[string]any{
				{"role": "system", "content": "you are a pirate"},
				{"role": "user", "content": "hi"},
			}
			ctx.Body, _ = json.Marshal(msg)
			return nil
		})
	})
	defer cleanup()

	body := map[string]any{
		"model": "original-model",
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
}

func TestMiddleware_PreTranslation_MetadataPassthrough(t *testing.T) {
	cfg := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "gpt-4o",
			"upstreams": [{"url": "http://{{OPENAI_ADDR}}/v1/chat/completions", "wire_format": "openai/chat/completions"}]
		}]
	}`

	var sawMetadata bool

	proxyURL, cleanup := startProxyWithMiddleware(t, cfg, func(s *proxy.Server) {
		s.UsePreTranslation(func(ctx *proxy.Context) error {
			ctx.Metadata["request_id"] = "req-abc-123"
			return nil
		})
		s.UsePostCanonical(func(ctx *proxy.Context) error {
			if ctx.Metadata["request_id"] == "req-abc-123" {
				sawMetadata = true
			}
			return nil
		})
	})
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
	if !sawMetadata {
		t.Error("metadata not passed from pre-translation to post-canonical")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// E2E: Post-Canonical middleware
// ═══════════════════════════════════════════════════════════════════════════════

func TestMiddleware_PostCanonical_ShortCircuit(t *testing.T) {
	cfg := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "gpt-4o",
			"upstreams": [{"url": "http://{{OPENAI_ADDR}}/v1/chat/completions", "wire_format": "openai/chat/completions"}]
		}]
	}`

	proxyURL, cleanup := startProxyWithMiddleware(t, cfg, func(s *proxy.Server) {
		s.UsePostCanonical(func(ctx *proxy.Context) error {
			if ctx.ChatRequest != nil && ctx.ChatRequest.Model == "gpt-4o" {
				ctx.ShortCircuit = true
				ctx.StatusCode = 200
				resp := map[string]any{
					"id":      "middleware-001",
					"object":  "chat.completion",
					"model":   "gpt-4o",
					"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": "hello from middleware"}, "finish_reason": "stop"}},
				}
				ctx.ResponseBody, _ = json.Marshal(resp)
			}
			return nil
		})
	})
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

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if result["id"] != "middleware-001" {
		t.Errorf("expected middleware-001, got %v", result["id"])
	}
	choices := result["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "hello from middleware" {
		t.Errorf("unexpected content: %v", msg["content"])
	}
}

func TestMiddleware_PostCanonical_InjectFields(t *testing.T) {
	cfg := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "gpt-4o",
			"upstreams": [{"url": "http://{{OPENAI_ADDR}}/v1/chat/completions", "wire_format": "openai/chat/completions"}]
		}]
	}`

	var sawInjected bool

	proxyURL, cleanup := startProxyWithMiddleware(t, cfg, func(s *proxy.Server) {
		s.UsePostCanonical(func(ctx *proxy.Context) error {
			if ctx.ChatRequest != nil {
				ctx.ChatRequest.Messages = append(
					[]formats.Message{{Role: "system", Content: []formats.ContentBlock{formats.TextBlock("you are helpful")}}},
					ctx.ChatRequest.Messages...,
				)
				sawInjected = true
			}
			return nil
		})
	})
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
	if !sawInjected {
		t.Error("post-canonical middleware did not see ChatRequest")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// E2E: Post-Translation middleware
// ═══════════════════════════════════════════════════════════════════════════════

func TestMiddleware_PostTranslation_ModifyResponse(t *testing.T) {
	cfg := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "gpt-4o",
			"upstreams": [{"url": "http://{{OPENAI_ADDR}}/v1/chat/completions", "wire_format": "openai/chat/completions"}]
		}]
	}`

	proxyURL, cleanup := startProxyWithMiddleware(t, cfg, func(s *proxy.Server) {
		s.UsePostTranslation(func(ctx *proxy.Context) error {
			var inner map[string]any
			json.Unmarshal(ctx.ResponseBody, &inner)
			envelope := map[string]any{
				"proxy_version": "1.0",
				"upstream":      inner,
			}
			ctx.ResponseBody, _ = json.Marshal(envelope)
			return nil
		})
	})
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

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if result["proxy_version"] != "1.0" {
		t.Errorf("expected proxy_version 1.0, got %v", result["proxy_version"])
	}
	if result["upstream"] == nil {
		t.Error("expected upstream field")
	}
}

func TestMiddleware_PostTranslation_ShortCircuit(t *testing.T) {
	cfg := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "gpt-4o",
			"upstreams": [{"url": "http://{{OPENAI_ADDR}}/v1/chat/completions", "wire_format": "openai/chat/completions"}]
		}]
	}`

	proxyURL, cleanup := startProxyWithMiddleware(t, cfg, func(s *proxy.Server) {
		s.UsePostTranslation(func(ctx *proxy.Context) error {
			ctx.ShortCircuit = true
			ctx.StatusCode = 200
			ctx.ResponseBody = []byte(`{"custom": true}`)
			return nil
		})
	})
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

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if result["custom"] != true {
		t.Errorf("expected custom=true, got %v", result)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// E2E: Stream middleware
// ═══════════════════════════════════════════════════════════════════════════════

func TestStreamMiddleware_SkipTextDeltas(t *testing.T) {
	cfg := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "gpt-4o",
			"upstreams": [{"url": "http://{{OPENAI_ADDR}}/v1/chat/completions", "wire_format": "openai/chat/completions"}]
		}]
	}`

	proxyURL, cleanup := startProxyWithMiddleware(t, cfg, func(s *proxy.Server) {
		s.UseStreamEvent(func(ctx *proxy.StreamContext) error {
			if ctx.Event.Type == formats.StreamEventTextDelta {
				ctx.Skip = true
			}
			return nil
		})
	})
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

	chunks := readSSE(t, resp.Body)
	for _, raw := range chunks {
		var chunk map[string]any
		json.Unmarshal([]byte(raw), &chunk)
		if choices, ok := chunk["choices"].([]any); ok {
			for _, c := range choices {
				choice := c.(map[string]any)
				if delta, ok := choice["delta"].(map[string]any); ok {
					if content, ok := delta["content"].(string); ok && content != "" {
						t.Errorf("expected text deltas to be skipped, but got content: %s", content)
					}
				}
			}
		}
	}
}

func TestStreamMiddleware_CloseEarly(t *testing.T) {
	cfg := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "gpt-4o",
			"upstreams": [{"url": "http://{{OPENAI_ADDR}}/v1/chat/completions", "wire_format": "openai/chat/completions"}]
		}]
	}`

	proxyURL, cleanup := startProxyWithMiddleware(t, cfg, func(s *proxy.Server) {
		s.UseStreamEvent(func(ctx *proxy.StreamContext) error {
			ctx.Close = true
			return nil
		})
	})
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

	chunks := readSSE(t, resp.Body)
	if len(chunks) > 3 {
		t.Errorf("expected <= 3 chunks after early close, got %d", len(chunks))
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// E2E: Headers
// ═══════════════════════════════════════════════════════════════════════════════

func TestHeaders_StaticConstant(t *testing.T) {
	staticMock := newMock(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Custom") != "static-value" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error": "missing X-Custom header"}`))
			return
		}
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "ok", "choices": [{"index": 0, "message": {"role": "assistant", "content": "hi"}}]}`))
	})
	addr, _ := staticMock.Start()
	defer staticMock.Close()

	cfgJSON := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "test-model",
			"upstreams": [{
				"url": "http://` + addr + `/v1/chat/completions",
				"wire_format": "openai/chat/completions",
				"headers": {
					"x-custom": "static-value"
				}
			}]
		}]
	}`

	cfg, _ := config.ParseBytes([]byte(cfgJSON))
	srv := proxy.NewServer(cfg)

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go http.Serve(ln, srv)
	defer ln.Close()

	proxyURL := fmt.Sprintf("http://%s", ln.Addr().String())

	body := map[string]any{
		"model": "test-model",
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
}

func TestHeaders_ForwardFromClient(t *testing.T) {
	var capturedSession string

	sessionMock := newMock(func(w http.ResponseWriter, r *http.Request) {
		capturedSession = r.Header.Get("X-Session-ID")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "ok", "choices": [{"index": 0, "message": {"role": "assistant", "content": "hi"}}]}`))
	})
	addr, _ := sessionMock.Start()
	defer sessionMock.Close()

	cfgJSON := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "test-model",
			"upstreams": [{
				"url": "http://` + addr + `/v1/chat/completions",
				"wire_format": "openai/chat/completions",
				"headers": {
					"x-session-id": {"forward": true}
				}
			}]
		}]
	}`

	cfg, _ := config.ParseBytes([]byte(cfgJSON))
	srv := proxy.NewServer(cfg)

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go http.Serve(ln, srv)
	defer ln.Close()

	proxyURL := fmt.Sprintf("http://%s", ln.Addr().String())

	body := map[string]any{
		"model": "test-model",
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
	}
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", proxyURL+"/v1/chat/completions", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Session-ID", "sess-abc-123")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	t.Logf("status: %d, body: %s", resp.StatusCode, string(respBody))

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if capturedSession != "sess-abc-123" {
		t.Errorf("expected session sess-abc-123, got %s", capturedSession)
	}
}

func TestHeaders_ConstructedWithPrefix(t *testing.T) {
	var capturedAuth string

	authMock := newMock(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "ok", "choices": [{"index": 0, "message": {"role": "assistant", "content": "hi"}}]}`))
	})
	addr, _ := authMock.Start()
	defer authMock.Close()

	secret.Set("E2E_TEST_KEY", "test-key-12345")

	cfgJSON := `{
		"listen": {"http": "127.0.0.1:0"},
		"models": [{
			"name": "test-model",
			"upstreams": [{
				"url": "http://` + addr + `/v1/chat/completions",
				"wire_format": "openai/chat/completions",
				"headers": {
					"authorization": {
						"value": {"$ref": "#/secrets/E2E_TEST_KEY"},
						"prefix": "Bearer "
					}
				}
			}]
		}]
	}`

	cfg, _ := config.ParseBytes([]byte(cfgJSON))
	srv := proxy.NewServer(cfg)

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go http.Serve(ln, srv)
	defer ln.Close()

	proxyURL := fmt.Sprintf("http://%s", ln.Addr().String())

	body := map[string]any{
		"model": "test-model",
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
	if capturedAuth != "Bearer test-key-12345" {
		t.Errorf("expected 'Bearer test-key-12345', got %q", capturedAuth)
	}
}
