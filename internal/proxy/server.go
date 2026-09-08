package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/woodybriggs/simpllm/internal/upstream"
	"github.com/woodybriggs/simpllm/internal/config"
	"github.com/woodybriggs/simpllm/internal/formats"
)

// Server is the main proxy server.
type Server struct {
	cfg    *config.Config
	client *upstream.Client
	mux    *http.ServeMux

	preTranslation  MiddlewareChain
	postCanonical   MiddlewareChain
	postTranslation MiddlewareChain
	streamEvent     StreamMiddlewareChain
}

// NewServer creates a new proxy server.
func NewServer(cfg *config.Config) *Server {
	s := &Server{
		cfg:    cfg,
		client: upstream.NewClient(),
		mux:    http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) UsePreTranslation(mw ...Middleware)  { s.preTranslation = append(s.preTranslation, mw...) }
func (s *Server) UsePostCanonical(mw ...Middleware)    { s.postCanonical = append(s.postCanonical, mw...) }
func (s *Server) UsePostTranslation(mw ...Middleware)  { s.postTranslation = append(s.postTranslation, mw...) }
func (s *Server) UseStreamEvent(mw ...StreamMiddleware) { s.streamEvent = append(s.streamEvent, mw...) }

func (s *Server) routes() {
	s.mux.HandleFunc("/v1/chat/completions", s.handleChat)
	s.mux.HandleFunc("/v1/embeddings", s.handleEmbed)
	s.mux.HandleFunc("/v1/images/generations", s.handleImage)
	s.mux.HandleFunc("/v1/responses", s.handleResponses)
	s.mux.HandleFunc("/v1/messages", s.handleMessages)
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/v1/models", s.handleModels)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.error(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	var probe struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		s.error(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	s.proxy(w, r, body, probe.Model, formats.WireOpenAIChatCompletions, probe.Stream)
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.error(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	var probe struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		s.error(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	s.proxy(w, r, body, probe.Model, formats.WireAnthropicMessages, probe.Stream)
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.error(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	var probe struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		s.error(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	s.proxy(w, r, body, probe.Model, formats.WireOpenAIResponses, probe.Stream)
}

func (s *Server) handleEmbed(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.error(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		s.error(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	s.proxyPassthrough(w, r, body, probe.Model, formats.WireOpenAIChatCompletions)
}

func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.error(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		s.error(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	s.proxyPassthrough(w, r, body, probe.Model, formats.WireOpenAIChatCompletions)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	models := make([]map[string]string, 0, len(s.cfg.Models))
	for _, m := range s.cfg.Models {
		models = append(models, map[string]string{
			"id": m.Name, "object": "model", "owned_by": "proxy",
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": models})
}

func (s *Server) proxy(w http.ResponseWriter, r *http.Request, body []byte, model string, downstreamFormat formats.WireFormat, stream bool) {
	m := s.cfg.FindModel(model)
	if m == nil {
		s.error(w, http.StatusNotFound, fmt.Sprintf("model %q not found", model))
		return
	}

	uref := m.Upstreams[0]
	uurl, headers, ufmt, err := s.resolveUpstream(uref, m)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}

	ctx := &Context{
		Body:             body,
		DownstreamFormat: downstreamFormat,
		Model:            model,
		Stream:           stream,
		UpstreamFormat:   ufmt,
		UpstreamURL:      uurl,
		Metadata:         make(map[string]any),
	}
	if err := s.preTranslation.Run(ctx); err != nil {
		s.error(w, http.StatusBadRequest, err.Error())
		return
	}
	if ctx.ShortCircuit {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(ctx.StatusCode)
		w.Write(ctx.ResponseBody)
		return
	}
	body = ctx.Body

	chatReq, err := formats.ParseToCanonical(body, downstreamFormat)
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Sprintf("parse error: %v", err))
		return
	}

	if m.Rewrite != nil {
		if newModel, ok := m.Rewrite[uurl]; ok {
			chatReq.Model = newModel
		}
	}

	ctx.ChatRequest = chatReq
	if err := s.postCanonical.Run(ctx); err != nil {
		s.error(w, http.StatusBadRequest, err.Error())
		return
	}
	if ctx.ShortCircuit {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(ctx.StatusCode)
		w.Write(ctx.ResponseBody)
		return
	}

	reqBody, err := formats.FormatFromCanonical(chatReq, ufmt)
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Sprintf("format error: %v", err))
		return
	}

	ureq := &upstream.Request{
		Model:          model,
		Body:           reqBody,
		WireFormat:     downstreamFormat,
		Stream:         stream,
		Upstream:       uref,
		UpstreamURL:    uurl,
		Headers:        headers,
		IncomingHeader: r.Header,
		UpstreamFmt:    ufmt,
	}

	if stream {
		s.proxyStream(w, r, ureq, ufmt, downstreamFormat, ctx)
		return
	}

	resp, err := s.client.Do(ureq)
	if err != nil {
		s.error(w, http.StatusBadGateway, fmt.Sprintf("upstream error: %v", err))
		return
	}

	respBody, err := formats.TranslateResponse(resp.Body, ufmt, downstreamFormat)
	if err != nil {
		s.error(w, http.StatusBadGateway, fmt.Sprintf("response translation error: %v", err))
		return
	}

	ctx.Response = nil
	ctx.ResponseBody = respBody
	ctx.StatusCode = resp.StatusCode
	if err := s.postTranslation.Run(ctx); err != nil {
		s.error(w, http.StatusBadRequest, err.Error())
		return
	}
	if ctx.ShortCircuit {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(ctx.StatusCode)
		w.Write(ctx.ResponseBody)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ctx.StatusCode)
	w.Write(ctx.ResponseBody)
}

func (s *Server) proxyStream(w http.ResponseWriter, r *http.Request, ureq *upstream.Request, upstreamFmt, downstreamFmt formats.WireFormat, ctx *Context) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.error(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	body, statusCode, err := s.client.DoStreamRaw(ureq)
	if err != nil {
		if ue, ok := err.(*upstream.UpstreamError); ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(ue.StatusCode)
			w.Write(ue.Body)
			return
		}
		s.error(w, http.StatusBadGateway, fmt.Sprintf("upstream stream error: %v", err))
		return
	}
	defer body.Close()

	w.WriteHeader(statusCode)

	if len(s.streamEvent) > 0 || upstreamFmt != downstreamFmt {
		s.translateStream(body, w, flusher, upstreamFmt, downstreamFmt, ctx)
	} else {
		s.passthroughStream(body, w, flusher)
	}
}

func (s *Server) translateStream(body io.Reader, w http.ResponseWriter, flusher http.Flusher, upstreamFmt, downstreamFmt formats.WireFormat, reqCtx *Context) {
	scanner := formats.NewSSEScanner(body)
	meta := reqCtx.Metadata
	if meta == nil {
		meta = make(map[string]any)
	}

	for {
		eventType, data, err := scanner.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("stream read error: %v", err)
			break
		}

		var event *formats.StreamEvent
		switch upstreamFmt {
		case formats.WireOpenAIChatCompletions:
			event, err = formats.ParseOpenAIStreamChunk(data)
		case formats.WireAnthropicMessages:
			event, err = formats.ParseAnthropicStreamEvent(eventType, data)
		default:
			if eventType != "" {
				fmt.Fprintf(w, "event: %s\n", eventType)
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			continue
		}
		if err != nil || event == nil || event.Type == "" {
			continue
		}
		if event.Type == "openai_keepalive" || event.Type == "content_block_start" || event.Type == "content_block_stop" || event.Type == "ping" {
			continue
		}

		sctx := &StreamContext{Event: event, Metadata: meta}
		if err := s.streamEvent.Run(sctx); err != nil {
			log.Printf("stream middleware error: %v", err)
			break
		}
		if sctx.Skip {
			continue
		}

		var output string
		switch downstreamFmt {
		case formats.WireOpenAIChatCompletions:
			output = formats.FormatOpenAIStreamEvent(event)
		case formats.WireAnthropicMessages:
			output = formats.FormatAnthropicStreamEvent(event)
		default:
			continue
		}

		if output != "" {
			fmt.Fprint(w, output)
			flusher.Flush()
		}

		if sctx.Close {
			break
		}
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func (s *Server) passthroughStream(body io.Reader, w http.ResponseWriter, flusher http.Flusher) {
	scanner := formats.NewSSEScanner(body)
	for {
		eventType, data, err := scanner.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("stream read error: %v", err)
			break
		}
		if eventType != "" {
			fmt.Fprintf(w, "event: %s\n", eventType)
		}
		fmt.Fprintf(w, "data: %s\n\n", string(data))
		flusher.Flush()
	}
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func (s *Server) proxyPassthrough(w http.ResponseWriter, r *http.Request, body []byte, model string, format formats.WireFormat) {
	m := s.cfg.FindModel(model)
	if m == nil {
		s.error(w, http.StatusNotFound, fmt.Sprintf("model %q not found", model))
		return
	}

	uref := m.Upstreams[0]
	uurl, headers, _, err := s.resolveUpstream(uref, m)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err.Error())
		return
	}

	ureq := &upstream.Request{
		Model:          model,
		Body:           body,
		WireFormat:     format,
		Upstream:       uref,
		UpstreamURL:    uurl,
		Headers:        headers,
		IncomingHeader: r.Header,
		UpstreamFmt:    format,
	}

	resp, err := s.client.Do(ureq)
	if err != nil {
		s.error(w, http.StatusBadGateway, fmt.Sprintf("upstream error: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(resp.Body)
}

func (s *Server) resolveUpstream(ref config.UpstreamRef, model *config.ModelConfig) (url string, headers []config.HeaderEntry, format formats.WireFormat, err error) {
	if ref.URL != "" {
		url = ref.URL
		headers, err = config.ParseHeaders(ref.Headers)
		if err != nil {
			return "", nil, "", fmt.Errorf("parsing headers: %w", err)
		}
		format = formats.WireFormat(ref.WireFormat)
		return url, headers, format, nil
	}
	return "", nil, "", fmt.Errorf("upstream %q: url is required", ref.Name)
}

func (s *Server) error(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "proxy_error",
			"code":    code,
		},
	})
}
