package config

import (
	"fmt"
	"net/http"

	"github.com/woodybriggs/simpllm/internal/secret"
)

// ParseHeaders converts a HeadersConfig (raw YAML map) into structured HeaderEntry values.
func ParseHeaders(raw HeadersConfig) ([]HeaderEntry, error) {
	var entries []HeaderEntry

	for name, val := range raw {
		entry := HeaderEntry{Name: name}

		switch v := val.(type) {
		case string:
			// Static: `header-name: "value"`
			entry.Value = v

		case HeadersConfig:
			// Named map type — comes from YAML struct unmarshal.
			parseHeaderConfig(v, &entry)

		case map[string]any:
			// Plain map — comes from raw YAML unmarshal.
			parseHeaderConfig(v, &entry)

		default:
			return nil, fmt.Errorf("header %q: unsupported value type %T", name, val)
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

// parseHeaderConfig extracts fields from a map into a HeaderEntry.
func parseHeaderConfig(v map[string]any, entry *HeaderEntry) {
	if f, ok := v["forward"]; ok {
		if forward, ok := f.(bool); ok {
			entry.Forward = forward
		}
	}
	if p, ok := v["prefix"]; ok {
		if prefix, ok := p.(string); ok {
			entry.Prefix = prefix
		}
	}
	if s, ok := v["suffix"]; ok {
		if suffix, ok := s.(string); ok {
			entry.Suffix = suffix
		}
	}
	if valRaw, ok := v["value"]; ok {
		switch vv := valRaw.(type) {
		case string:
			entry.Value = vv
		case map[string]any:
			if ref, ok := vv["$ref"]; ok {
				if refStr, ok := ref.(string); ok {
					entry.Secret = refStr
				}
			}
		// YAML unmarshal may produce a HeadersConfig-typed map for $ref.
		case HeadersConfig:
			if ref, ok := vv["$ref"]; ok {
				if refStr, ok := ref.(string); ok {
					entry.Secret = refStr
				}
			}
		}
	}
}

// ResolveHeaders resolves all header entries for a request.
func ResolveHeaders(entries []HeaderEntry, incoming http.Header) (map[string]string, error) {
	headers := make(map[string]string)

	for _, e := range entries {
		var value string

		switch {
		case e.Forward:
			if incoming != nil {
				value = incoming.Get(e.Name)
			}
			if value == "" {
				continue
			}

		case e.Secret != "":
			val, err := secret.Resolve(e.Secret)
			if err != nil {
				return nil, fmt.Errorf("header %q: %w", e.Name, err)
			}
			value = val

		default:
			value = e.Value
		}

		headers[e.Name] = e.Prefix + value + e.Suffix
	}

	return headers, nil
}
