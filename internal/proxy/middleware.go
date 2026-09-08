package proxy

import (
	"fmt"

	"github.com/woodybriggs/simpllm/internal/formats"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Middleware
//
// Middleware runs at three points in the proxy pipeline:
//
//  1. PreTranslation   — raw request bytes, before parsing
//  2. PostCanonical    — parsed canonical ChatRequest, before upstream translation
//  3. PostTranslation  — final response bytes, before sending to client
//
// Each middleware is a function that receives the context, can read/modify it,
// and returns an error to short-circuit the chain.
//
// Multiple middleware per phase run in order. If any returns an error, the
// chain stops and the error is returned to the client.
// ═══════════════════════════════════════════════════════════════════════════════

// Phase identifies where in the pipeline middleware runs.
type Phase string

const (
	PhasePreTranslation  Phase = "pre_translation"
	PhasePostCanonical   Phase = "post_canonical"
	PhasePostTranslation Phase = "post_translation"
)

// Context carries request/response state through the middleware chain.
type Context struct {
	// ── Pre-Translation ──────────────────────────────────────────
	Body             []byte
	DownstreamFormat formats.WireFormat
	Model            string
	Stream           bool

	// ── Post-Canonical ───────────────────────────────────────────
	ChatRequest     *formats.ChatRequest
	UpstreamFormat  formats.WireFormat
	UpstreamURL     string

	// ── Post-Translation ─────────────────────────────────────────
	Response     *formats.ChatResponse
	ResponseBody []byte
	StatusCode   int

	// ── Control ──────────────────────────────────────────────────
	ShortCircuit bool
	Metadata     map[string]any
}

// Middleware is a function that processes a request at a specific pipeline phase.
type Middleware func(ctx *Context) error

// MiddlewareChain is an ordered list of middleware for a single phase.
type MiddlewareChain []Middleware

// Run executes all middleware in the chain. Stops on first error.
func (chain MiddlewareChain) Run(ctx *Context) error {
	for _, mw := range chain {
		if err := mw(ctx); err != nil {
			return fmt.Errorf("middleware error: %w", err)
		}
		if ctx.ShortCircuit {
			break
		}
	}
	return nil
}

// ═══════════════════════════════════════════════════════════════════════════════
// Stream middleware
// ═══════════════════════════════════════════════════════════════════════════════

// StreamMiddleware processes individual SSE events as they flow through.
type StreamMiddleware func(ctx *StreamContext) error

// StreamContext carries per-event state through stream middleware.
type StreamContext struct {
	Event    *formats.StreamEvent
	Metadata map[string]any
	Skip     bool
	Close    bool
}

// StreamMiddlewareChain is an ordered list of stream middleware.
type StreamMiddlewareChain []StreamMiddleware

// Run executes all middleware for a single event. Stops on first error.
func (chain StreamMiddlewareChain) Run(ctx *StreamContext) error {
	for _, mw := range chain {
		if err := mw(ctx); err != nil {
			return fmt.Errorf("stream middleware error: %w", err)
		}
		if ctx.Close {
			break
		}
	}
	return nil
}
