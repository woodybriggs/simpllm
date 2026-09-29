package config

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/woodybriggs/simpllm/internal/secret"
	"gopkg.in/yaml.v3"
)

// Config is the top-level proxy configuration.
type Config struct {
	Listen ListenConfig          `yaml:"listen"`
	Models []ModelConfig         `yaml:"models"`
	Auth   map[string]AuthConfig `yaml:"auth,omitempty"`
}

// ListenConfig defines where the proxy listens.
type ListenConfig struct {
	HTTP string `yaml:"http"` // e.g. ":8080"
	Unix string `yaml:"unix"` // e.g. "/tmp/simpllm.sock"
}

// ModelConfig maps a virtual model name to one or more upstreams.
type ModelConfig struct {
	Name      string            `yaml:"name"`               // virtual model name (what agents send)
	Upstreams []UpstreamRef     `yaml:"upstreams"`          // one or more upstreams
	Rewrite   map[string]string `yaml:"rewrite,omitempty"`  // model name rewrite per upstream
}

// AuthConfig defines optional auth for incoming requests.
type AuthConfig struct {
	Type  string `yaml:"type"`  // "bearer"
	Token string `yaml:"token"` // expected token (supports $ref)
}

// ═══════════════════════════════════════════════════════════════════════════════
// Config loading
// ═══════════════════════════════════════════════════════════════════════════════

// Load finds and merges config files using the default resolution order:
//
//  1. ~/.config/simpllm/config.yaml  (global)
//  2. ./.simpllm/config.yaml         (local, merged on top)
//
// If an explicit path is given (via -config), it is loaded alone without merging.
func Load(explicitPath string) (*Config, error) {
	if explicitPath != "" {
		return Parse(explicitPath)
	}

	global := homeConfigPath()
	local := localConfigPath()

	globalCfg, globalErr := loadIfExists(global)
	localCfg, localErr := loadIfExists(local)

	if globalErr != nil && localErr != nil {
		// Neither exists — fall back to defaults.
		return defaultConfig(), nil
	}
	if globalErr != nil {
		// Only local exists.
		return localCfg, nil
	}
	if localErr != nil {
		// Only global exists.
		return globalCfg, nil
	}

	// Both exist — merge local on top of global.
	globalCfg.Merge(localCfg)

	log.Printf("config: merged %s + %s", global, local)
	return globalCfg, nil
}

// Parse loads a config from a single YAML file, resolves secret refs, and validates.
func Parse(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	cfg, err := parseConfigBytes(data)
	if err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validating config %s: %w", path, err)
	}

	return cfg, nil
}

// ParseBytes loads a config from raw YAML bytes, resolving secret refs (for testing).
func ParseBytes(data []byte) (*Config, error) {
	cfg, err := parseConfigBytes(data)
	if err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// parseConfigBytes is the shared parsing path: resolve $ref → unmarshal.
func parseConfigBytes(data []byte) (*Config, error) {
	// Step 1: Unmarshal into raw map to resolve $ref entries.
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("pre-parse: %w", err)
	}

	// Step 2: Resolve all $ref entries. Secrets are fetched from keyring
	// and injected into the map, then all refs are resolved by map lookup.
	resolved, err := secret.ResolveAllRefs(raw)
	if err != nil {
		return nil, fmt.Errorf("resolving refs: %w", err)
	}
	if resolved > 0 {
		log.Printf("config: resolved %d ref(s)", resolved)
	}

	// Step 3: Re-marshal and unmarshal into Config struct.
	resolvedData, err := yaml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("re-marshal: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(resolvedData, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	return &cfg, nil
}

// loadIfExists tries to load a config from a path. Returns error if file doesn't exist.
func loadIfExists(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseConfigBytes(data)
}

// ═══════════════════════════════════════════════════════════════════════════════
// Config manipulation
// ═══════════════════════════════════════════════════════════════════════════════

// Merge applies the non-empty fields of other on top of c.
func (c *Config) Merge(other *Config) {
	if other.Listen.HTTP != "" {
		c.Listen.HTTP = other.Listen.HTTP
	}
	if other.Listen.Unix != "" {
		c.Listen.Unix = other.Listen.Unix
	}
	if len(other.Models) > 0 {
		c.Models = other.Models
	}
	if len(other.Auth) > 0 {
		if c.Auth == nil {
			c.Auth = make(map[string]AuthConfig)
		}
		for k, v := range other.Auth {
			c.Auth[k] = v
		}
	}
}

func (c *Config) validate() error {
	if len(c.Models) == 0 {
		return fmt.Errorf("at least one model must be defined")
	}

	if c.Listen.HTTP == "" && c.Listen.Unix == "" {
		c.Listen.HTTP = ":8080"
	}

	for i, m := range c.Models {
		if m.Name == "" {
			return fmt.Errorf("model[%d]: name is required", i)
		}
		if len(m.Upstreams) == 0 {
			return fmt.Errorf("model[%q]: at least one upstream is required", m.Name)
		}
		for j, u := range m.Upstreams {
			if u.Name == "" && u.URL == "" {
				return fmt.Errorf("model[%q].upstream[%d]: name or url is required", m.Name, j)
			}
			if u.URL != "" && u.WireFormat == "" {
				return fmt.Errorf("model[%q].upstream[%d]: wire_format is required for inline upstreams", m.Name, j)
			}
			if u.Weight == 0 {
				c.Models[i].Upstreams[j].Weight = 1
			}
		}
	}

	return nil
}

// ═══════════════════════════════════════════════════════════════════════════════
// Helpers
// ═══════════════════════════════════════════════════════════════════════════════

func defaultConfig() *Config {
	return &Config{
		Listen: ListenConfig{
			HTTP: ":8080",
		},
	}
}

// ConfigPaths returns the resolved config file paths for the given explicit path.
// If explicitPath is set, returns just that path. Otherwise returns the global
// and local config paths.
func ConfigPaths(explicitPath string) []string {
	if explicitPath != "" {
		return []string{explicitPath}
	}
	return []string{homeConfigPath(), localConfigPath()}
}

func homeConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home + "/.config/simpllm/config.yaml"
}

func localConfigPath() string {
	return ".simpllm/config.yaml"
}

func (c *Config) HasAuthProvider(name string) bool {
	_, ok := c.Auth[name]
	return ok
}

func (c *Config) ModelNames() []string {
	names := make([]string, len(c.Models))
	for i, m := range c.Models {
		names[i] = m.Name
	}
	return names
}

func (c *Config) FindModel(name string) *ModelConfig {
	for i := range c.Models {
		if c.Models[i].Name == name {
			return &c.Models[i]
		}
	}
	lower := strings.ToLower(name)
	for i := range c.Models {
		if strings.ToLower(c.Models[i].Name) == lower {
			return &c.Models[i]
		}
	}
	return nil
}
