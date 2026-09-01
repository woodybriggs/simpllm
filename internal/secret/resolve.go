package secret

import (
	"fmt"
	"strings"
)

// ResolveConfigMap walks a parsed YAML config map and resolves any $ref secret references in-place.
// Returns the number of references resolved.
func ResolveConfigMap(raw map[string]any) (int, error) {
	return resolveMap(raw)
}

func resolveMap(m map[string]any) (int, error) {
	resolved := 0
	for k, v := range m {
		switch val := v.(type) {
		case map[string]any:
			// Check for $ref at this level.
			if ref, ok := val["$ref"]; ok {
				if refStr, ok := ref.(string); ok {
					if strings.HasPrefix(refStr, "#/secrets/") {
						name := strings.TrimPrefix(refStr, "#/secrets/")
						value, err := Get(name)
						if err != nil {
							return resolved, fmt.Errorf("resolving %s: %w", refStr, err)
						}
						m[k] = value
						resolved++
						continue
					}
				}
			}
			// Recurse into nested map.
			n, err := resolveMap(val)
			resolved += n
			if err != nil {
				return resolved, err
			}
		case []any:
			for _, item := range val {
				if sub, ok := item.(map[string]any); ok {
					n, err := resolveMap(sub)
					resolved += n
					if err != nil {
						return resolved, err
					}
				}
			}
		}
	}
	return resolved, nil
}

// Resolve resolves a $ref path like "#/secrets/MY_KEY" to its value.
func Resolve(ref string) (string, error) {
	if !strings.HasPrefix(ref, "#/secrets/") {
		return "", fmt.Errorf("invalid secret ref: %s", ref)
	}
	name := strings.TrimPrefix(ref, "#/secrets/")
	return Get(name)
}
