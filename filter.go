package logfilter

import (
	"log/slog"
	"strings"
	"time"
)

// Source filter type prefixes.
const (
	ContextPrefix        = "context:"
	SourceFilePrefix     = "source:file"
	SourceFunctionPrefix = "source:function"
)

// filterKind classifies a filter's type for fast dispatch in the hot path.
type filterKind int

const (
	filterKindAttribute      filterKind = iota // Match against record/preformatted attributes
	filterKindSourceFile                       // Match against source file path
	filterKindSourceFunction                   // Match against function name
	filterKindContext                          // Match against context value
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

	// Level is the minimum threshold for logs matching this filter.
	// Logs below this level are suppressed, logs at or above pass through.
	// Valid values: "debug", "info", "warn", "error"
	Level string `json:"level"`

	// OutputLevel optionally transforms the log level in the output.
	// If set, matching logs are emitted at this level instead of their original level.
	// This is useful for elevating debug logs to info so they appear in normal log streams.
	// If empty, the original log level is preserved.
	// Valid values: "", "debug", "info", "warn", "error"
	OutputLevel string `json:"output_level,omitempty"`

	// Enabled controls whether this filter is active.
	Enabled bool `json:"enabled"`

	// ExpiresAt is an optional expiry time for temporary filters.
	// If nil or zero, the filter never expires.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	// Cached fields — set by prepare(), not serialized.
	kind              filterKind `json:"-"` // Pre-classified filter kind
	parsedLevel       slog.Level `json:"-"` // Cached ParseLevel(Level)
	parsedOutputLevel slog.Level `json:"-"` // Cached ParseLevel(OutputLevel)
	contextKey        string     `json:"-"` // Cached context key (trimmed prefix)
	attributeKey      string     `json:"-"` // Cached attribute key
}

// prepare pre-computes cached fields from the JSON-serializable fields.
// Must be called after constructing or deserializing a LogFilter before use
// in the hot path. Handler.SetFilters and Handler.AddFilter call this automatically.
func (f *LogFilter) prepare() {
	// Classify the filter kind
	switch {
	case f.Type == SourceFilePrefix:
		f.kind = filterKindSourceFile
	case f.Type == SourceFunctionPrefix:
		f.kind = filterKindSourceFunction
	case strings.HasPrefix(f.Type, ContextPrefix):
		f.kind = filterKindContext
		f.contextKey = strings.TrimPrefix(f.Type, ContextPrefix)
	default:
		f.kind = filterKindAttribute
		f.attributeKey = f.Type
	}

	// Cache parsed levels
	f.parsedLevel = ParseLevel(f.Level)
	if f.OutputLevel != "" {
		f.parsedOutputLevel = ParseLevel(f.OutputLevel)
	}
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

// HasOutputLevel returns true if this filter transforms the output level.
func (f *LogFilter) HasOutputLevel() bool {
	return f.OutputLevel != ""
}

// GetOutputLevel returns the parsed output level, or the original level if not set.
func (f *LogFilter) GetOutputLevel(originalLevel slog.Level) slog.Level {
	if f.OutputLevel == "" {
		return originalLevel
	}
	return ParseLevel(f.OutputLevel)
}

// cachedParsedLevel returns the pre-computed parsed level.
// Only valid after prepare() has been called.
func (f *LogFilter) cachedParsedLevel() slog.Level {
	return f.parsedLevel
}

// cachedOutputLevel returns the pre-computed output level, or the original level if not set.
// Only valid after prepare() has been called.
func (f *LogFilter) cachedOutputLevel(originalLevel slog.Level) slog.Level {
	if f.OutputLevel == "" {
		return originalLevel
	}
	return f.parsedOutputLevel
}

// ParseLevel converts a level string to slog.Level.
func ParseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
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
