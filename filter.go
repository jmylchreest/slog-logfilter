package logfilter

import (
	"strings"
	"time"
)

// Source filter type prefixes.
const (
	ContextPrefix       = "context:"
	SourceFilePrefix    = "source:file"
	SourceFunctionPrefix = "source:function"
)

// LogFilter defines a log level override based on attribute matching.
type LogFilter struct {
	// Type is the attribute key to match (e.g., "job_id", "user_id", "package").
	// Special prefixes:
	//   - "context:key" for context values (e.g., "context:job_id")
	//   - "source:file" for source file path filtering
	//   - "source:function" for function name filtering
	Type string `json:"type"`

	// Pattern for matching the attribute value.
	// Supports simple glob-style patterns:
	//   - "value"    exact match
	//   - "prefix*"  prefix match
	//   - "*suffix"  suffix match
	//   - "*contains*" contains match
	Pattern string `json:"pattern"`

	// Level to apply when this filter matches.
	// Valid values: "debug", "info", "warn", "error"
	Level string `json:"level"`

	// Enabled controls whether this filter is active.
	Enabled bool `json:"enabled"`

	// ExpiresAt is an optional expiry time for temporary filters.
	// If nil or zero, the filter never expires.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// IsExpired returns true if the filter has expired.
func (f *LogFilter) IsExpired() bool {
	if f.ExpiresAt == nil || f.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(*f.ExpiresAt)
}

// IsActive returns true if the filter is enabled and not expired.
func (f *LogFilter) IsActive() bool {
	return f.Enabled && !f.IsExpired()
}

// Matches checks if the given value matches the filter pattern.
// Returns true if the pattern matches.
func (f *LogFilter) Matches(value string) bool {
	return matchPattern(f.Pattern, value)
}

// IsContextFilter returns true if this filter checks context values.
func (f *LogFilter) IsContextFilter() bool {
	return strings.HasPrefix(f.Type, ContextPrefix)
}

// ContextKey returns the context key for context filters.
// Returns empty string if not a context filter.
func (f *LogFilter) ContextKey() string {
	if !f.IsContextFilter() {
		return ""
	}
	return strings.TrimPrefix(f.Type, ContextPrefix)
}

// IsSourceFilter returns true if this filter checks source file or function.
func (f *LogFilter) IsSourceFilter() bool {
	return f.IsSourceFileFilter() || f.IsSourceFunctionFilter()
}

// IsSourceFileFilter returns true if this filter checks source file path.
func (f *LogFilter) IsSourceFileFilter() bool {
	return f.Type == SourceFilePrefix
}

// IsSourceFunctionFilter returns true if this filter checks function name.
func (f *LogFilter) IsSourceFunctionFilter() bool {
	return f.Type == SourceFunctionPrefix
}

// AttributeKey returns the attribute key for attribute filters.
// Returns the type as-is for non-context and non-source filters.
func (f *LogFilter) AttributeKey() string {
	if f.IsContextFilter() || f.IsSourceFilter() {
		return ""
	}
	return f.Type
}

// matchPattern performs fast glob-style pattern matching.
// Patterns:
//   - "value"      exact match
//   - "prefix*"    prefix match (HasPrefix)
//   - "*suffix"    suffix match (HasSuffix)
//   - "*contains*" contains match (Contains)
func matchPattern(pattern, value string) bool {
	if pattern == "" {
		return false
	}

	startsWithWildcard := strings.HasPrefix(pattern, "*")
	endsWithWildcard := strings.HasSuffix(pattern, "*")

	switch {
	case startsWithWildcard && endsWithWildcard:
		// *contains* - check if value contains the middle part
		middle := strings.TrimSuffix(strings.TrimPrefix(pattern, "*"), "*")
		if middle == "" {
			return true // Pattern is just "*" or "**", matches everything
		}
		return strings.Contains(value, middle)

	case endsWithWildcard:
		// prefix* - check if value starts with prefix
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(value, prefix)

	case startsWithWildcard:
		// *suffix - check if value ends with suffix
		suffix := strings.TrimPrefix(pattern, "*")
		return strings.HasSuffix(value, suffix)

	default:
		// Exact match
		return pattern == value
	}
}
