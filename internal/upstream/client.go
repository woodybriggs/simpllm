package upstream

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/woodybriggs/simpllm/internal/config"
	"github.com/woodybriggs/simpllm/internal/formats"
)

// Client handles communication with upstream AI providers.
type Client struct {
	http *http.Client
}

// NewClient creates a new upstream client.
func NewClient() *Client {
	return &Client{
		http: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

// Request represents an upstream request with all necessary context.
type Request struct {
	Model          string
	Body           []byte
	WireFormat     formats.WireFormat
	Stream         bool
	Upstream       config.UpstreamRef
	UpstreamURL    string
	Headers        []config.HeaderEntry
	IncomingHeader http.Header // headers from the original client request
	UpstreamFmt    formats.WireFormat
}

// Response represents an upstream response.
type Response struct {
	StatusCode int
	Body       []byte
	Headers    http.Header
}

// Do sends a request to the upstream and returns the response.
func (c *Client) Do(req *Request) (*Response, error) {
	httpReq, err := http.NewRequest("POST", req.UpstreamURL, bytes.NewReader(req.Body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Set format-specific headers.
	httpReq.Header.Set("Content-Type", "application/json")
	if req.UpstreamFmt == formats.WireAnthropicMessages {
		httpReq.Header.Set("anthropic-version", "2023-06-01")
	}

	// Apply headers.
	if err := ApplyHeaders(req.Headers, req.IncomingHeader, httpReq); err != nil {
		return nil, fmt.Errorf("applying headers: %w", err)
	}

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("upstream request failed: %w", err)
	}

	body, err := io.ReadAll(httpResp.Body)
	httpResp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("reading upstream response: %w", err)
	}

	return &Response{
		StatusCode: httpResp.StatusCode,
		Body:       body,
		Headers:    httpResp.Header,
	}, nil
}

// DoStreamRaw sends a streaming request and returns the raw response body.
func (c *Client) DoStreamRaw(req *Request) (io.ReadCloser, int, error) {
	httpReq, err := http.NewRequest("POST", req.UpstreamURL, bytes.NewReader(req.Body))
	if err != nil {
		return nil, 0, fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if req.UpstreamFmt == formats.WireAnthropicMessages {
		httpReq.Header.Set("anthropic-version", "2023-06-01")
	}

	if err := ApplyHeaders(req.Headers, req.IncomingHeader, httpReq); err != nil {
		return nil, 0, fmt.Errorf("applying headers: %w", err)
	}

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("upstream stream request failed: %w", err)
	}

	if httpResp.StatusCode != 200 {
		body, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		return nil, httpResp.StatusCode, &UpstreamError{
			StatusCode: httpResp.StatusCode,
			Body:       body,
		}
	}

	return httpResp.Body, httpResp.StatusCode, nil
}

// DoStream sends a streaming request to the upstream and returns a stream reader.
func (c *Client) DoStream(req *Request) (*StreamReader, error) {
	httpReq, err := http.NewRequest("POST", req.UpstreamURL, bytes.NewReader(req.Body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if req.UpstreamFmt == formats.WireAnthropicMessages {
		httpReq.Header.Set("anthropic-version", "2023-06-01")
	}

	if err := ApplyHeaders(req.Headers, req.IncomingHeader, httpReq); err != nil {
		return nil, fmt.Errorf("applying headers: %w", err)
	}

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("upstream stream request failed: %w", err)
	}

	if httpResp.StatusCode != 200 {
		body, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		return nil, &UpstreamError{
			StatusCode: httpResp.StatusCode,
			Body:       body,
		}
	}

	return NewStreamReader(httpResp.Body, req.UpstreamFmt), nil
}

// StreamReader reads SSE events from a streaming upstream response.
type StreamReader struct {
	scanner *bufio.Scanner
	format  formats.WireFormat
	closed  bool
}

// NewStreamReader creates a new stream reader.
func NewStreamReader(r io.Reader, format formats.WireFormat) *StreamReader {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &StreamReader{
		scanner: scanner,
		format:  format,
	}
}

// StreamEvent represents a single SSE event from a streaming upstream.
type StreamEvent struct {
	Event string
	Data  json.RawMessage
}

// Next reads the next SSE event. Returns nil when the stream ends.
func (s *StreamReader) Next() (*StreamEvent, error) {
	if s.closed {
		return nil, io.EOF
	}

	event := &StreamEvent{}

	for s.scanner.Scan() {
		line := s.scanner.Text()

		if strings.HasPrefix(line, ":") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			event.Event = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				return nil, io.EOF
			}
			event.Data = json.RawMessage(data)
			return event, nil
		} else if line == "" {
			if event.Event != "" || event.Data != nil {
				return event, nil
			}
		}
	}

	if err := s.scanner.Err(); err != nil {
		return nil, err
	}

	return nil, io.EOF
}

// Close marks the reader as closed.
func (s *StreamReader) Close() {
	s.closed = true
}

// UpstreamError represents an error response from an upstream.
type UpstreamError struct {
	StatusCode int
	Body       []byte
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("upstream error %d: %s", e.StatusCode, string(e.Body))
}

// DetectFormat attempts to detect the wire format from a request's path and content.
func DetectFormat(path string, body []byte) formats.WireFormat {
	switch {
	case strings.Contains(path, "/messages"):
		return formats.WireAnthropicMessages
	case strings.Contains(path, "/responses"):
		return formats.WireOpenAIResponses
	case strings.Contains(path, "/systemone"):
		return formats.WireSystemOne
	case strings.Contains(path, "/chat/completions"):
		return formats.WireOpenAIChatCompletions
	case strings.Contains(path, "/generateContent"), strings.Contains(path, ":generateContent"), strings.Contains(path, ":streamGenerateContent"):
		return formats.WireGeminiGenerateContent
	case strings.Contains(path, "/embeddings"):
		return formats.WireOpenAIChatCompletions
	case strings.Contains(path, "/images/generations"):
		return formats.WireOpenAIChatCompletions
	default:
		return detectFromBody(body)
	}
}

func detectFromBody(body []byte) formats.WireFormat {
	var probe struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
		System    any `json:"system"`
		Input     any `json:"input"`
		Questions any `json:"questions"`
		State     any `json:"state"`
		MaxTok    int `json:"max_tokens"`
	}

	if json.Unmarshal(body, &probe) != nil {
		return formats.WireOpenAIChatCompletions
	}

	// System One has "questions" and "state" but no "messages".
	if probe.Questions != nil && probe.State != nil && len(probe.Messages) == 0 {
		return formats.WireSystemOne
	}

	if probe.System != nil && probe.MaxTok > 0 && len(probe.Messages) > 0 {
		return formats.WireAnthropicMessages
	}

	if probe.Input != nil && len(probe.Messages) == 0 {
		return formats.WireOpenAIResponses
	}

	return formats.WireOpenAIChatCompletions
}
