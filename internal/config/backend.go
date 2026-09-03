package config

// UpstreamRef references a named upstream or defines an inline one.
type UpstreamRef struct {
	Name       string         `yaml:"name,omitempty"`        // reference a named upstream
	URL        string         `yaml:"url,omitempty"`         // inline: upstream URL
	Headers    HeadersConfig  `yaml:"headers,omitempty"`     // headers to send with every request
	WireFormat string         `yaml:"wire_format,omitempty"` // openai/chat/completions | anthropic/messages | openai/responses
	Weight     int            `yaml:"weight,omitempty"`      // load balancing weight (default 1)
}

// HeadersConfig is a map of header name → value config.
//
// Each entry is one of:
//
//   - string            → static constant header
//   - object with "value" → constructed: prefix + value + suffix (value can be a $ref)
//   - object with "forward": true → passthrough from client request
//
// Examples:
//
//	headers:
//	  x-api-version: "2024-01-01"                       # static
//	  authorization:                                     # constructed
//	    value:
//	      $ref: "#/secrets/MY_API_KEY"
//	    prefix: "Bearer "
//	  x-custom:
//    value: "my-value"                                  # constructed, literal
//    prefix: "pre-"
//    suffix: "-post"
//	  x-opencode-session:
//	    forward: true                                    # passthrough from client
type HeadersConfig map[string]any

// HeaderEntry is the resolved form of a single header config entry.
type HeaderEntry struct {
	Name    string // header name (the map key)
	Value   string // literal value (if set directly)
	Secret  string // keyring $ref path (if set, resolved at startup)
	Prefix  string // prepended to resolved value
	Suffix  string // appended to resolved value
	Forward bool   // if true, copied from the incoming client request
}
