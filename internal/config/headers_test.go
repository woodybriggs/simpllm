package config

import (
	"net/http"
	"testing"

	"github.com/woodybriggs/simpllm/internal/secret"
)

func TestParseHeaders_Static(t *testing.T) {
	raw := HeadersConfig{
		"x-api-version": "2024-01-01",
		"x-custom":      "hello",
	}

	entries, err := ParseHeaders(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	for _, e := range entries {
		if e.Value == "" {
			t.Errorf("header %q: expected non-empty value", e.Name)
		}
		if e.Secret != "" {
			t.Errorf("header %q: expected no secret", e.Name)
		}
		if e.Forward {
			t.Errorf("header %q: expected no forward", e.Name)
		}
	}
}

func TestParseHeaders_Constructed(t *testing.T) {
	raw := HeadersConfig{
		"authorization": map[string]any{
			"value":  "my-api-key",
			"prefix": "Bearer ",
			"suffix": "",
		},
	}

	entries, err := ParseHeaders(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	e := entries[0]
	if e.Name != "authorization" {
		t.Errorf("expected name authorization, got %s", e.Name)
	}
	if e.Value != "my-api-key" {
		t.Errorf("expected value my-api-key, got %s", e.Value)
	}
	if e.Prefix != "Bearer " {
		t.Errorf("expected prefix 'Bearer ', got %q", e.Prefix)
	}
}

func TestParseHeaders_ConstructedSecret(t *testing.T) {
	raw := HeadersConfig{
		"authorization": map[string]any{
			"value": map[string]any{
				"$ref": "#/secrets/MY_KEY",
			},
			"prefix": "Bearer ",
		},
	}

	entries, err := ParseHeaders(raw)
	if err != nil {
		t.Fatal(err)
	}

	e := entries[0]
	if e.Secret != "#/secrets/MY_KEY" {
		t.Errorf("expected secret #/secrets/MY_KEY, got %s", e.Secret)
	}
	if e.Prefix != "Bearer " {
		t.Errorf("expected prefix 'Bearer ', got %s", e.Prefix)
	}
}

func TestParseHeaders_Forward(t *testing.T) {
	raw := HeadersConfig{
		"x-session": map[string]any{
			"forward": true,
		},
	}

	entries, err := ParseHeaders(raw)
	if err != nil {
		t.Fatal(err)
	}

	e := entries[0]
	if !e.Forward {
		t.Error("expected forward=true")
	}
}

func TestParseHeaders_Mixed(t *testing.T) {
	raw := HeadersConfig{
		"x-static":      "fixed-value",
		"x-forwarded":   map[string]any{"forward": true},
		"authorization": map[string]any{"value": "key-123", "prefix": "Bearer "},
	}

	entries, err := ParseHeaders(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	types := map[string]string{}
	for _, e := range entries {
		switch {
		case e.Forward:
			types[e.Name] = "forward"
		case e.Secret != "":
			types[e.Name] = "secret"
		default:
			types[e.Name] = "static"
		}
	}
	if types["x-static"] != "static" {
		t.Errorf("x-static: expected static, got %s", types["x-static"])
	}
	if types["x-forwarded"] != "forward" {
		t.Errorf("x-forwarded: expected forward, got %s", types["x-forwarded"])
	}
	if types["authorization"] != "static" {
		t.Errorf("authorization: expected static, got %s", types["authorization"])
	}
}

func TestResolveHeaders_Static(t *testing.T) {
	entries := []HeaderEntry{
		{Name: "x-api-version", Value: "2024-01-01"},
		{Name: "x-custom", Value: "hello"},
	}

	headers, err := ResolveHeaders(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if headers["x-api-version"] != "2024-01-01" {
		t.Errorf("expected 2024-01-01, got %s", headers["x-api-version"])
	}
	if headers["x-custom"] != "hello" {
		t.Errorf("expected hello, got %s", headers["x-custom"])
	}
}

func TestResolveHeaders_Forward(t *testing.T) {
	entries := []HeaderEntry{
		{Name: "x-session", Forward: true},
	}

	incoming := http.Header{}
	incoming.Set("X-Session", "abc-123")

	headers, err := ResolveHeaders(entries, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if headers["x-session"] != "abc-123" {
		t.Errorf("expected abc-123, got %s", headers["x-session"])
	}
}

func TestResolveHeaders_ForwardMissing(t *testing.T) {
	entries := []HeaderEntry{
		{Name: "x-missing", Forward: true},
	}

	// No incoming header — should be skipped.
	headers, err := ResolveHeaders(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := headers["x-missing"]; ok {
		t.Error("expected x-missing to be skipped")
	}
}

func TestResolveHeaders_PrefixSuffix(t *testing.T) {
	entries := []HeaderEntry{
		{Name: "authorization", Value: "my-key", Prefix: "Bearer ", Suffix: ""},
		{Name: "x-tagged", Value: "data", Prefix: "[", Suffix: "]"},
	}

	headers, err := ResolveHeaders(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if headers["authorization"] != "Bearer my-key" {
		t.Errorf("expected 'Bearer my-key', got %q", headers["authorization"])
	}
	if headers["x-tagged"] != "[data]" {
		t.Errorf("expected '[data]', got %q", headers["x-tagged"])
	}
}

func TestResolveHeaders_Secret(t *testing.T) {
	// Seed the cache directly to avoid needing a real keyring.
	secret.Set("TEST_UNIT_KEY", "unit-test-value-42")

	entries := []HeaderEntry{
		{Name: "authorization", Secret: "#/secrets/TEST_UNIT_KEY", Prefix: "Bearer "},
	}

	headers, err := ResolveHeaders(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if headers["authorization"] != "Bearer unit-test-value-42" {
		t.Errorf("expected 'Bearer unit-test-value-42', got %q", headers["authorization"])
	}
}

func TestResolveHeaders_ForwardPrecedence(t *testing.T) {
	// Forward + prefix
	entries := []HeaderEntry{
		{Name: "x-session", Forward: true, Prefix: "session-"},
	}

	incoming := http.Header{}
	incoming.Set("X-Session", "abc-123")

	headers, err := ResolveHeaders(entries, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if headers["x-session"] != "session-abc-123" {
		t.Errorf("expected 'session-abc-123', got %q", headers["x-session"])
	}
}

func TestParseHeaders_UnsupportedType(t *testing.T) {
	raw := HeadersConfig{
		"x-bad": 12345,
	}

	_, err := ParseHeaders(raw)
	if err == nil {
		t.Error("expected error for unsupported type")
	}
}
