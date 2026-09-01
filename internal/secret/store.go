package secret

import (
	"fmt"
	"sync"

	"github.com/zalando/go-keyring"
)

const serviceName = "simpllm"

var (
	cache   = make(map[string]string)
	cacheMu sync.RWMutex
)

// Set stores a secret in the system keyring and in-memory cache.
func Set(name, value string) error {
	cacheMu.Lock()
	cache[name] = value
	cacheMu.Unlock()

	return keyring.Set(serviceName, name, value)
}

// Get retrieves a secret from cache or the system keyring.
func Get(name string) (string, error) {
	cacheMu.RLock()
	if v, ok := cache[name]; ok {
		cacheMu.RUnlock()
		return v, nil
	}
	cacheMu.RUnlock()

	value, err := keyring.Get(serviceName, name)
	if err != nil {
		return "", fmt.Errorf("secret %q not found: %w", name, err)
	}

	cacheMu.Lock()
	cache[name] = value
	cacheMu.Unlock()

	return value, nil
}

// Delete removes a secret from the system keyring and cache.
func Delete(name string) error {
	cacheMu.Lock()
	delete(cache, name)
	cacheMu.Unlock()

	return keyring.Delete(serviceName, name)
}


