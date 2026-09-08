package proxy

import (
	"errors"
	"testing"

	"github.com/woodybriggs/simpllm/internal/formats"
)

func TestMiddlewareChain_RunEmpty(t *testing.T) {
	chain := MiddlewareChain{}
	ctx := &Context{Metadata: make(map[string]any)}
	if err := chain.Run(ctx); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestMiddlewareChain_RunOrder(t *testing.T) {
	var order []string

	mw1 := func(ctx *Context) error {
		order = append(order, "first")
		return nil
	}
	mw2 := func(ctx *Context) error {
		order = append(order, "second")
		return nil
	}
	mw3 := func(ctx *Context) error {
		order = append(order, "third")
		return nil
	}

	chain := MiddlewareChain{mw1, mw2, mw3}
	ctx := &Context{Metadata: make(map[string]any)}

	if err := chain.Run(ctx); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(order))
	}
	if order[0] != "first" || order[1] != "second" || order[2] != "third" {
		t.Errorf("unexpected order: %v", order)
	}
}

func TestMiddlewareChain_ErrorStopsChain(t *testing.T) {
	var order []string
	stopErr := errors.New("stop here")

	mw1 := func(ctx *Context) error {
		order = append(order, "first")
		return nil
	}
	mw2 := func(ctx *Context) error {
		order = append(order, "second")
		return stopErr
	}
	mw3 := func(ctx *Context) error {
		order = append(order, "third")
		return nil
	}

	chain := MiddlewareChain{mw1, mw2, mw3}
	ctx := &Context{Metadata: make(map[string]any)}

	err := chain.Run(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	if len(order) != 2 {
		t.Fatalf("expected 2 calls before stop, got %d", len(order))
	}
	if order[0] != "first" || order[1] != "second" {
		t.Errorf("unexpected order: %v", order)
	}
}

func TestMiddlewareChain_ShortCircuit(t *testing.T) {
	var order []string

	mw1 := func(ctx *Context) error {
		order = append(order, "first")
		return nil
	}
	mw2 := func(ctx *Context) error {
		order = append(order, "shortcircuit")
		ctx.ShortCircuit = true
		ctx.StatusCode = 418
		ctx.ResponseBody = []byte(`{"error": "teapot"}`)
		return nil
	}
	mw3 := func(ctx *Context) error {
		order = append(order, "third")
		return nil
	}

	chain := MiddlewareChain{mw1, mw2, mw3}
	ctx := &Context{Metadata: make(map[string]any)}

	if err := chain.Run(ctx); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(order) != 2 {
		t.Fatalf("expected 2 calls (third skipped), got %d", len(order))
	}
	if ctx.StatusCode != 418 {
		t.Errorf("expected status 418, got %d", ctx.StatusCode)
	}
	if string(ctx.ResponseBody) != `{"error": "teapot"}` {
		t.Errorf("unexpected body: %s", ctx.ResponseBody)
	}
}

func TestMiddlewareChain_MetadataPassthrough(t *testing.T) {
	mw1 := func(ctx *Context) error {
		ctx.Metadata["key"] = "value-from-mw1"
		return nil
	}
	mw2 := func(ctx *Context) error {
		if ctx.Metadata["key"] != "value-from-mw1" {
			t.Errorf("expected metadata from mw1, got %v", ctx.Metadata["key"])
		}
		ctx.Metadata["key2"] = "value-from-mw2"
		return nil
	}

	chain := MiddlewareChain{mw1, mw2}
	ctx := &Context{Metadata: make(map[string]any)}

	if err := chain.Run(ctx); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if ctx.Metadata["key"] != "value-from-mw1" {
		t.Error("metadata from mw1 not preserved")
	}
	if ctx.Metadata["key2"] != "value-from-mw2" {
		t.Error("metadata from mw2 not preserved")
	}
}

func TestMiddlewareChain_CanModifyBody(t *testing.T) {
	mw := func(ctx *Context) error {
		ctx.Model = "rewritten-model"
		return nil
	}

	chain := MiddlewareChain{mw}
	ctx := &Context{
		Model:    "original-model",
		Metadata: make(map[string]any),
	}

	if err := chain.Run(ctx); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if ctx.Model != "rewritten-model" {
		t.Errorf("expected rewritten-model, got %s", ctx.Model)
	}
}

func TestMiddlewareChain_CanInjectChatRequest(t *testing.T) {
	mw := func(ctx *Context) error {
		ctx.ChatRequest = &formats.ChatRequest{
			Model: "injected",
		}
		return nil
	}

	chain := MiddlewareChain{mw}
	ctx := &Context{Metadata: make(map[string]any)}

	if err := chain.Run(ctx); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if ctx.ChatRequest == nil || ctx.ChatRequest.Model != "injected" {
		t.Error("expected ChatRequest to be injected")
	}
}

// ─── Stream middleware tests ─────────────────────────────────────────────────

func TestStreamMiddlewareChain_RunEmpty(t *testing.T) {
	chain := StreamMiddlewareChain{}
	ctx := &StreamContext{Metadata: make(map[string]any)}
	if err := chain.Run(ctx); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestStreamMiddlewareChain_SkipEvent(t *testing.T) {
	var called bool

	mw := func(ctx *StreamContext) error {
		called = true
		if ctx.Event != nil && ctx.Event.Type == formats.StreamEventTextDelta {
			ctx.Skip = true
		}
		return nil
	}

	chain := StreamMiddlewareChain{mw}
	ctx := &StreamContext{
		Event:    &formats.StreamEvent{Type: formats.StreamEventTextDelta, Text: "hello"},
		Metadata: make(map[string]any),
	}

	if err := chain.Run(ctx); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !called {
		t.Error("middleware not called")
	}
	if !ctx.Skip {
		t.Error("expected Skip=true")
	}
}

func TestStreamMiddlewareChain_CloseStream(t *testing.T) {
	mw := func(ctx *StreamContext) error {
		ctx.Close = true
		return nil
	}

	chain := StreamMiddlewareChain{mw}
	ctx := &StreamContext{
		Event:    &formats.StreamEvent{Type: formats.StreamEventMessageStop},
		Metadata: make(map[string]any),
	}

	if err := chain.Run(ctx); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ctx.Close {
		t.Error("expected Close=true")
	}
}

func TestStreamMiddlewareChain_ErrorStopsChain(t *testing.T) {
	var order []string
	stopErr := errors.New("stream stop")

	mw1 := func(ctx *StreamContext) error {
		order = append(order, "first")
		return nil
	}
	mw2 := func(ctx *StreamContext) error {
		order = append(order, "second")
		return stopErr
	}
	mw3 := func(ctx *StreamContext) error {
		order = append(order, "third")
		return nil
	}

	chain := StreamMiddlewareChain{mw1, mw2, mw3}
	ctx := &StreamContext{
		Event:    &formats.StreamEvent{Type: formats.StreamEventTextDelta},
		Metadata: make(map[string]any),
	}

	err := chain.Run(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	if len(order) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(order))
	}
}

func TestStreamMiddlewareChain_MetadataPassthrough(t *testing.T) {
	mw1 := func(ctx *StreamContext) error {
		ctx.Metadata["stream-key"] = "from-mw1"
		return nil
	}
	mw2 := func(ctx *StreamContext) error {
		if ctx.Metadata["stream-key"] != "from-mw1" {
			t.Errorf("expected metadata from mw1, got %v", ctx.Metadata["stream-key"])
		}
		return nil
	}

	chain := StreamMiddlewareChain{mw1, mw2}
	ctx := &StreamContext{
		Event:    &formats.StreamEvent{Type: formats.StreamEventTextDelta},
		Metadata: make(map[string]any),
	}

	if err := chain.Run(ctx); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}
