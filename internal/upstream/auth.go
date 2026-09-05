package upstream

import (
	"net/http"

	"github.com/woodybriggs/simpllm/internal/config"
)

// ApplyHeaders sets all configured headers on an HTTP request.
func ApplyHeaders(entries []config.HeaderEntry, incoming http.Header, out *http.Request) error {
	resolved, err := config.ResolveHeaders(entries, incoming)
	if err != nil {
		return err
	}
	for k, v := range resolved {
		out.Header.Set(k, v)
	}
	return nil
}
