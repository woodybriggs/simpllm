package secret

import (
	"fmt"
	"strings"
)

// ResolveAllRefs resolves all $ref entries in a raw YAML config map.
//
// Resolution strategy:
//  1. Walk the map and collect all #/secrets/* references.
//  2. Fetch each secret from the keyring and inject under raw["secrets"].
//  3. Walk the map again and resolve every $ref by looking up the path in the
//     raw map itself (secrets are now just another section).
//
// Returns the total number of references resolved.
func ResolveAllRefs(raw map[string]any) (int, error) {
	// Phase 1: collect secret names referenced anywhere in the config.
	secretRefs := collectSecretRefs(raw)
	if len(secretRefs) == 0 {
		return resolveRefs(raw, raw), nil
	}

	// Phase 2: fetch all secrets from keyring, inject under raw["secrets"].
	secrets := make(map[string]any)
	for name := range secretRefs {
		value, err := Get(name)
		if err != nil {
			return 0, fmt.Errorf("resolving secret %q: %w", name, err)
		}
		secrets[name] = value
	}
	raw["secrets"] = secrets

	// Phase 3: resolve all $ref entries by map lookup.
	resolved := resolveRefs(raw, raw)
	return resolved, nil
}

// Resolve resolves a $ref path like "#/secrets/MY_KEY" to its value from the keyring.
// Used at runtime for header resolution (e.g. in headers.go).
func Resolve(ref string) (string, error) {
	if !strings.HasPrefix(ref, "#/secrets/") {
		return "", fmt.Errorf("invalid secret ref: %s", ref)
	}
	name := strings.TrimPrefix(ref, "#/secrets/")
	return Get(name)
}

func collectSecretRefs(m map[string]any) map[string]bool {
	refs := make(map[string]bool)
	collectRefsWalk(m, refs)
	return refs
}

func collectRefsWalk(m map[string]any, refs map[string]bool) {
	for _, v := range m {
		switch val := v.(type) {
		case map[string]any:
			if ref, ok := val["$ref"]; ok {
				if refStr, ok := ref.(string); ok {
					if strings.HasPrefix(refStr, "#/secrets/") {
						refs[strings.TrimPrefix(refStr, "#/secrets/")] = true
					}
				}
			}
			collectRefsWalk(val, refs)
		case []any:
			for _, item := range val {
				if sub, ok := item.(map[string]any); ok {
					collectRefsWalk(sub, refs)
				}
			}
		}
	}
}

// ── Phase 3: resolve all $ref by map lookup ───────────────────

// resolveRefs walks m and resolves all $ref entries by looking up their path
// against root (the top-level config map).
func resolveRefs(m, root map[string]any) int {
	resolved := 0
	for k, v := range m {
		switch val := v.(type) {
		case map[string]any:
			if ref, ok := val["$ref"]; ok {
				if refStr, ok := ref.(string); ok {
					path := strings.TrimPrefix(refStr, "#/")
					result, err := lookupPath(root, path)
					if err == nil {
						m[k] = result
						resolved++
						continue
					}
				}
			}
			resolved += resolveRefs(val, root)
		case []any:
			for i, item := range val {
				if sub, ok := item.(map[string]any); ok {
					if ref, ok := sub["$ref"]; ok {
						if refStr, ok := ref.(string); ok {
							path := strings.TrimPrefix(refStr, "#/")
							result, err := lookupPath(root, path)
							if err == nil {
								val[i] = result
								resolved++
								continue
							}
						}
					}
					resolved += resolveRefs(sub, root)
				}
			}
		}
	}
	return resolved
}

// lookupPath resolves a dot-separated or slash-separated path like
// "upstreams/Opencode" or "secrets/MY_KEY" against the raw config map.
func lookupPath(root map[string]any, path string) (any, error) {
	parts := strings.Split(path, "/")
	var current any = root

	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("path %q: intermediate value is not a map", path)
		}
		current, ok = m[part]
		if !ok {
			return nil, fmt.Errorf("path %q: key %q not found", path, part)
		}
	}

	return current, nil
}
