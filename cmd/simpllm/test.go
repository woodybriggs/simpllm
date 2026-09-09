package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func testCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: simpllm test <endpoint> [model]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "examples:")
		fmt.Fprintln(os.Stderr, "  simpllm test chat                       # send to /v1/chat/completions with default model")
		fmt.Fprintln(os.Stderr, "  simpllm test chat gpt-4o                # specify model")
		fmt.Fprintln(os.Stderr, "  simpllm test messages claude-sonnet     # send to /v1/messages (Anthropic format)")
		fmt.Fprintln(os.Stderr, "  simpllm test chat gpt-4o --stream       # enable streaming")
		fmt.Fprintln(os.Stderr, "  simpllm test chat gpt-4o --raw '{...}'  # send custom JSON body")
		os.Exit(1)
	}

	endpoint := args[0]
	model := "gpt-4o"
	stream := false
	raw := ""
	baseURL := os.Getenv("SIMPLLM_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}

	// Parse remaining args.
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--stream":
			stream = true
		case "--raw":
			if i+1 < len(args) {
				raw = args[i+1]
				i++
			}
		case "--url":
			if i+1 < len(args) {
				baseURL = args[i+1]
				i++
			}
		default:
			model = args[i]
		}
	}

	var path string
	var contentType string
	var body []byte

	switch endpoint {
	case "chat", "completions":
		path = "/v1/chat/completions"
		contentType = "application/json"
		if raw != "" {
			body = []byte(raw)
		} else {
			body = chatBody(model, stream)
		}

	case "messages":
		path = "/v1/messages"
		contentType = "application/json"
		if raw != "" {
			body = []byte(raw)
		} else {
			body = anthropicBody(model, stream)
		}

	case "embeddings":
		path = "/v1/embeddings"
		contentType = "application/json"
		if raw != "" {
			body = []byte(raw)
		} else {
			body = embedBody(model)
		}

	case "models":
		path = "/v1/models"
		contentType = ""

	default:
		fmt.Fprintf(os.Stderr, "unknown endpoint: %s\n", endpoint)
		fmt.Fprintln(os.Stderr, "valid endpoints: chat, messages, embeddings, models")
		os.Exit(1)
	}

	url := strings.TrimRight(baseURL, "/") + path

	fmt.Fprintf(os.Stderr, "POST %s\n", url)
	fmt.Fprintf(os.Stderr, "model: %s  stream: %v\n\n", model, stream)

	if stream {
		doStream(url, contentType, body)
	} else {
		doRequest(url, contentType, body)
	}
}

func doRequest(url, contentType string, body []byte) {
	resp, err := http.Post(url, contentType, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	fmt.Fprintf(os.Stderr, "status: %d\n\n", resp.StatusCode)

	raw, _ := io.ReadAll(resp.Body)
	var pretty bytes.Buffer
	json.Indent(&pretty, raw, "", "  ")
	fmt.Println(pretty.String())
}

func doStream(url, contentType string, body []byte) {
	resp, err := http.Post(url, contentType, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	fmt.Fprintf(os.Stderr, "status: %d  content-type: %s\n\n", resp.StatusCode, resp.Header.Get("Content-Type"))

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			fmt.Printf("\033[90m%s\033[0m\n", line)
		} else if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				fmt.Println("\033[90mdata: [DONE]\033[0m")
				break
			}
			// Pretty-print the JSON.
			var pretty bytes.Buffer
			if json.Indent(&pretty, []byte(data), "", "  ") == nil {
				fmt.Println(pretty.String())
			} else {
				fmt.Println(data)
			}
			fmt.Println()
		}
	}
}

// --- Request bodies ---

func chatBody(model string, stream bool) []byte {
	body := map[string]any{
		"model":  model,
		"stream": stream,
		"messages": []map[string]string{
			{"role": "user", "content": "Say hello in one sentence."},
		},
	}
	data, _ := json.Marshal(body)
	return data
}

func anthropicBody(model string, stream bool) []byte {
	body := map[string]any{
		"model":      model,
		"max_tokens": 256,
		"stream":     stream,
		"messages": []map[string]string{
			{"role": "user", "content": "Say hello in one sentence."},
		},
	}
	data, _ := json.Marshal(body)
	return data
}

func embedBody(model string) []byte {
	body := map[string]any{
		"model": model,
		"input": "hello world",
	}
	data, _ := json.Marshal(body)
	return data
}
