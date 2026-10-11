# slog-logfilter

A dynamic, filter-based logging library for Go's `log/slog`. Enables runtime log level control and attribute-based filtering without code changes.

## Features

- **Dynamic log levels** - Change global log level at runtime via `LevelVar`
- **Filter-based overrides** - Elevate or suppress logs based on attribute values
- **Context extraction** - Filter on values stored in `context.Context`
- **Source-based filtering** - Filter by file path or function name (Rust-style module filtering)
- **Simple pattern matching** - Fast glob-style patterns (prefix*, *suffix, *contains*)
- **Optional expiry** - Temporary filters with automatic expiration
- **First match wins** - Predictable filter ordering
- **Thread-safe** - Safe for concurrent use
- **Optional sanitisation** - Redact sensitive fields and matching text before output

## Installation

```bash
go get github.com/jmylchreest/slog-logfilter
```

## Quick Start

```go
package main

import (
    "log/slog"
    "github.com/jmylchreest/slog-logfilter"
)

func main() {
    // Create a filtered logger
    logger := logfilter.New(
        logfilter.WithLevel(slog.LevelInfo),
        logfilter.WithFormat("json"),
    )

    // Add filters at runtime
    logfilter.SetFilters([]logfilter.LogFilter{
        {Type: "job_id", Pattern: "debug_*", Level: "debug", Enabled: true},
    })

    // Logs with matching attributes use filter's level
    logger.Debug("processing", "job_id", "debug_123") // Emitted (filter matches)
    logger.Debug("processing", "job_id", "normal_456") // Suppressed (no match)
    logger.Info("status", "job_id", "normal_456")      // Emitted (INFO >= global)
}
```

## Filter Configuration

### LogFilter Structure

```go
type LogFilter struct {
    Type        string     `json:"type"`         // Attribute key or special prefix
    Pattern     string     `json:"pattern"`      // Glob pattern for value
    Level       string     `json:"level"`        // Minimum threshold: debug, info, warn, error
    OutputLevel string     `json:"output_level"` // Optional: transform output level
    Enabled     bool       `json:"enabled"`      // Whether filter is active
    ExpiresAt   *time.Time `json:"expires_at"`   // Optional expiry (nil = never)
}
```

### Field Defaults and Behavior

| Field | Default | Description |
|-------|---------|-------------|
| `type` | (required) | Attribute key, or special prefix (`context:`, `source:file`, `source:function`) |
| `pattern` | (required) | Glob pattern: `exact`, `prefix*`, `*suffix`, `*contains*` |
| `level` | `"info"` | Minimum threshold. Logs below this level are suppressed. |
| `output_level` | (pass-through) | If omitted/empty, preserves original log level. If set, transforms output. |
| `enabled` | `false` | Filter is only active when `true` |
| `expires_at` | (never) | If omitted/null, filter never expires |

**Important:**
- `level=""` defaults to `"info"`, which suppresses DEBUG logs. Use `level="debug"` to allow all levels.
- `output_level=""` or omitted means **pass-through** - the original log level is preserved in output.

### Filter Types

| Type | Description | Example Pattern |
|------|-------------|-----------------|
| `attribute_name` | Match log attribute value | `"job_*"` matches job_id="job_123" |
| `context:key` | Match value from context.Context | `"user_*"` matches context user_id |
| `source:file` | Match source file path (relative) | `"internal/service/*"` |
| `source:function` | Match function name | `"*Extraction*"` |

### Pattern Matching

| Pattern | Match Type | Example |
|---------|------------|---------|
| `value` | Exact | `"job_123"` matches only `"job_123"` |
| `prefix*` | Prefix | `"job_*"` matches `"job_123"`, `"job_abc"` |
| `*suffix` | Suffix | `"*_prod"` matches `"job_prod"`, `"task_prod"` |
| `*contains*` | Contains | `"*error*"` matches `"big_error_here"` |

### Example Filters

```json
[
  {"type": "job_id", "pattern": "job_abc*", "level": "debug", "enabled": true},
  {"type": "user_id", "pattern": "user_123", "level": "debug", "enabled": true},
  {"type": "context:user_id", "pattern": "debug_user_*", "level": "debug", "enabled": true},
  {"type": "source:file", "pattern": "internal/service/*", "level": "debug", "enabled": true},
  {"type": "source:function", "pattern": "*Extraction*", "level": "debug", "enabled": true},
  {"type": "endpoint", "pattern": "/api/v1/extract", "level": "debug", "enabled": true,
   "expires_at": "2024-01-15T00:00:00Z"}
]
```

## Context Filtering

Filter on values stored in context (useful for request-scoped data):

```go
// Register a context extractor
logfilter.RegisterContextExtractor("user_id", func(ctx context.Context) (string, bool) {
    if v := ctx.Value(UserIDKey); v != nil {
        if s, ok := v.(string); ok {
            return s, true
        }
    }
    return "", false
})

// Use "context:" prefix in filter type
logfilter.AddFilter(logfilter.LogFilter{
    Type:    "context:user_id",
    Pattern: "debug_user_*",
    Level:   "debug",
    Enabled: true,
})

// Logs will check context for user_id
ctx := context.WithValue(context.Background(), UserIDKey, "debug_user_123")
logger.DebugContext(ctx, "user action") // Emitted (context matches)
```

## Source-Based Filtering

Filter logs based on where they originate in your code (similar to Rust's `RUST_LOG` module filtering):

```go
// Enable debug logging for all files in the service package
logfilter.AddFilter(logfilter.LogFilter{
    Type:    "source:file",
    Pattern: "internal/service/*",
    Level:   "debug",
    Enabled: true,
})

// Enable debug logging for all Extraction-related functions
logfilter.AddFilter(logfilter.LogFilter{
    Type:    "source:function",
    Pattern: "*Extraction*",
    Level:   "debug",
    Enabled: true,
})
```

### Source Filter Details

- **`source:file`** - Matches against the source file path
- **`source:function`** - Matches against the function name (e.g., `(*ExtractionService).Extract`)

**Path formats for `source:file`:**
- **Local files** (within your project): relative path like `internal/service/extraction.go`
- **External packages**: prefixed with `@` like `@github.com/user/repo/pkg/file.go`

This allows you to filter logs from specific external dependencies:

```json
[
  {"type": "source:file", "pattern": "internal/*", "level": "debug", "enabled": true},
  {"type": "source:file", "pattern": "@github.com/jmylchreest/refyne/*", "level": "debug", "enabled": true}
]
```

### Performance

Source extraction only occurs when source-based filters are configured. If you have no `source:file` or `source:function` filters, there's zero overhead from this feature.

### Example: Debug a Specific Package

```json
[
  {"type": "source:file", "pattern": "internal/service/*", "level": "debug", "enabled": true},
  {"type": "source:file", "pattern": "internal/worker/*", "level": "debug", "enabled": true}
]
```

### Example: Debug Specific Functions

```json
[
  {"type": "source:function", "pattern": "*Handler*", "level": "debug", "enabled": true},
  {"type": "source:function", "pattern": "Process*", "level": "debug", "enabled": true}
]
```

## Optional Sanitisation

See the API comments for `SanitiserRule`, `NewSanitiser`, and `WithSanitisers`,
or browse the [executable GoDoc examples](sanitiser_example_test.go). Locally,
`go doc SanitiserRule` shows callback and matching semantics;
`go test -run Example ./...` verifies all example outputs.

Sanitisation is disabled by default. Enable the built-in rules, optionally adding
your own Go regular expressions:

```go
rules := logfilter.DefaultSanitiserRules()
rules = append(rules, logfilter.SanitiserRule{
    KeyPattern: `(?i)^customer_email$`, // Match this attribute at any group depth
    Replacement: "[PRIVATE]",
})
rules = append(rules, logfilter.SanitiserRule{
    Pattern: `\baccount-[0-9]+\b`, // Replace matching substrings in messages and strings
})
sanitiser, err := logfilter.NewSanitiser(rules...)
if err != nil {
    return err // Invalid configuration: do not start an unsanitised logger
}
logger := logfilter.New(logfilter.WithSanitiser(sanitiser))
logger.Info("processing 550e8400-e29b-41d4-a716-446655440000",
    "username", "alice", "password", "secret", "count", 3)
// msg="processing [REDACTED]" username=[REDACTED] password=[REDACTED] count=3
```

The defaults redact UUIDs, card-like numbers (13–19 digits, optionally separated
by spaces or hyphens), credential fields such as `username`, `user_id`, `password`,
`api_key`, `authorization`, and `card_number`, labelled credentials such as
`password=secret` or `username="Alice Smith"`, and Bearer tokens. Card matching
uses a callback to validate the Luhn checksum, but does not validate issuers and
may also redact numeric IDs with valid checksums. Invalid card-like text is left
unchanged; explicit card fields are always redacted. Unlabelled
usernames and passwords cannot be reliably detected; use structured fields or
application-specific patterns.

Phone numbers, email addresses and provider-specific API key formats are not
explicitly detected by the defaults. Add field rules for your own attribute names
or text patterns/callbacks for the formats your application uses.

| Rule fields | Behaviour |
|-------------|-----------|
| `KeyPattern` only | Replace the entire value, regardless of type |
| `Pattern` only | Replace matched substrings in messages and string attributes |
| Both | Replace matched substrings in string attributes with matching keys |
| `Replacement` | Literal replacement text; empty defaults to `[REDACTED]` (no `$1` expansion) |
| `Pattern` + `ReplaceFunc` | Call the function for each complete regex match |
| `ReplaceFunc` without `Pattern` | Call the function with the whole string; optionally restrict by `KeyPattern` |

Key patterns match local attribute names, not dotted group paths; use `^` and `$`
for whole-key matching. Matching is case-sensitive unless the pattern uses `(?i)`.
Whole-field rules take precedence (first match wins); text rules run sequentially.
Rules have JSON fields `key_pattern`, `pattern`, and `replacement`, and are compiled
once by `NewSanitiser`. Sanitisers are immutable and safe to share across loggers
provided any custom callbacks are safe for concurrent use.
Pass only your custom rules to omit the defaults, or `WithSanitiser(nil)` to disable.

Use multiple independently configured sanitisers on the same logger:

```go
credentials, err := logfilter.NewSanitiser(logfilter.SanitiserRule{
    KeyPattern: `(?i)^(password|api_key)$`,
    Replacement: "[SECRET]",
})
if err != nil {
    return err
}
identities, err := logfilter.NewSanitiser(logfilter.SanitiserRule{
    KeyPattern: `(?i)^(username|user_id)$`,
    Replacement: "[PERSON]",
})
if err != nil {
    return err
}
logger := logfilter.New(logfilter.WithSanitisers(credentials, identities))
logger.Info("login", "username", "alice", "password", "secret")
// username=[PERSON] password=[SECRET]
```

Sanitisers run in the supplied order, each processing the previous one's output,
including replacement text. They retain their individual rule precedence; rules
are not merged. Each logger can choose its own combination, and immutable
sanitisers can be shared across loggers. Nil and empty sanitisers are skipped.
The last `WithSanitiser` or `WithSanitisers` option replaces earlier configuration;
`WithSanitisers()` disables sanitisation. `WithSanitiser(s)` remains shorthand for
`WithSanitisers(s)`. Custom handlers also accept multiple sanitisers via
`NewSanitisingHandler(inner, credentials, identities)`.

Rules can use `ReplaceFunc` for validation or calculated replacements. With a
`Pattern`, the function receives each complete regex match. Return the match
unchanged to keep it, an empty string to remove it, or replacement text to redact
or mask it. Returned text is literal, including `$` characters.

Use a whole-field rule to replace an attribute's entire value, a regex for recognisable text,
and a callback when validation or calculated masking is required. The default
card rule combines a candidate regex with a Luhn-checking callback. Custom rules
can use the same approach for application-specific key formats or phone numbers.

```go
rule := logfilter.SanitiserRule{
    KeyPattern: `(?i)^api[_-]?key$`,
    ReplaceFunc: func(value string) string {
        if len(value) < 8 {
            return logfilter.Redaction
        }
        return "[KEY]..." + value[len(value)-4:]
    },
}
```

Without `Pattern`, the callback receives the entire string. `KeyPattern` limits
it to matching string attributes; omit that too to process all string attributes
and messages. Callbacks skip non-string values and run in text-rule order, after
whole-field redaction rules. They also apply to nested groups, resolved string
`LogValuer` values and attributes bound with `With` (at binding time).

`ReplaceFunc` and a non-empty `Replacement` are mutually exclusive. Callbacks
must be safe for concurrent calls; callback panics propagate. Functions are Go-only
and omitted from JSON: serialising and reloading rules does not preserve callbacks.
Reattach them in code before constructing the sanitiser. The default card-number
rule uses this same callback API for Luhn validation; the other defaults use regex
or whole-field replacement.

Filters match original values before sanitisation, including values supplied via
`logger.With`. Sanitisation covers messages, `With` attributes, nested slog groups,
and resolved `slog.LogValuer` values in JSON and text output. Unmatched primitive
values keep their original types. It does not scan attribute names, context values,
source metadata, or opaque `slog.Any` values such as maps, structs, slices and errors.
Redact opaque values by key, or expose their fields as slog groups/`LogValuer` values.
Custom downstream handlers can add data after sanitisation.

For a custom handler, install the sanitiser before binding any attributes to it:

```go
inner := slog.NewJSONHandler(output, nil)
safe := logfilter.NewSanitisingHandler(inner, sanitiser)
handler := logfilter.NewHandler(safe, levelVar)
logger := slog.New(handler)
```

Attributes already bound directly to `inner` cannot be sanitised by the wrapper.

## Runtime API

```go
// Change global level
logfilter.SetLevel(slog.LevelDebug)
level := logfilter.GetLevel()

// Manage filters
logfilter.SetFilters(filters)           // Replace all filters
logfilter.AddFilter(filter)             // Add single filter
logfilter.RemoveFilter("job_id", "abc*") // Remove by type+pattern
logfilter.ClearFilters()                // Remove all filters
filters := logfilter.GetFilters()       // Get current filters
```

## Filter Behavior

### Elevation (DEBUG when global is INFO)

```go
// Global level: INFO
// Filter: {type: "job_id", pattern: "debug_*", level: "debug"}

logger.Debug("msg", "job_id", "debug_123") // Emitted (filter elevates)
logger.Debug("msg", "job_id", "normal_456") // Suppressed (no filter match)
```

### Suppression (WARN when global is INFO)

```go
// Global level: INFO
// Filter: {type: "job_id", pattern: "noisy_*", level: "warn"}

logger.Info("msg", "job_id", "noisy_123")  // Suppressed (filter sets WARN)
logger.Warn("msg", "job_id", "noisy_123")  // Emitted (WARN >= filter level)
logger.Info("msg", "job_id", "normal_456") // Emitted (no filter, uses global)
```

### First Match Wins

Filters are checked in order. First matching filter determines the level:

```go
filters := []logfilter.LogFilter{
    {Type: "job_id", Pattern: "job_*", Level: "debug", Enabled: true},   // First
    {Type: "job_id", Pattern: "job_123", Level: "error", Enabled: true}, // Never used for job_123
}
// "job_123" matches first filter, uses DEBUG (not ERROR)
```

### Output Level Transformation

Use `output_level` to transform the emitted log level. This is useful when you want verbose debugging but don't want DEBUG-level noise in your log aggregator:

```go
// Global level: INFO
// Filter: {type: "job_id", pattern: "debug_*", level: "debug", output_level: "info"}

logger.Debug("detailed trace", "job_id", "debug_123")
// Result: Emitted as INFO (passes filter, level transformed)
// Output: level=INFO msg="detailed trace" job_id=debug_123
```

**Use cases:**
- Elevate debug logs to INFO so they appear in production log streams
- Make important debug info visible without lowering global log level
- Tag certain debug logs as WARN for alerting systems

```json
[
  {"type": "job_id", "pattern": "critical_*", "level": "debug", "output_level": "warn", "enabled": true},
  {"type": "source:file", "pattern": "internal/payment/*", "level": "debug", "output_level": "info", "enabled": true}
]
```

## Integration Example

Load filters from JSON config (e.g., from S3):

```go
func loadFilters(data []byte) error {
    var filters []logfilter.LogFilter
    if err := json.Unmarshal(data, &filters); err != nil {
        return err
    }
    logfilter.SetFilters(filters)
    return nil
}

// Periodic refresh
func refreshFilters(ctx context.Context, s3Client *s3.Client) {
    ticker := time.NewTicker(5 * time.Minute)
    for {
        select {
        case <-ticker.C:
            data, err := s3Client.GetObject(ctx, "config/logfilters.json")
            if err == nil {
                loadFilters(data)
            }
        case <-ctx.Done():
            return
        }
    }
}
```

## Performance

The handler is optimized for minimal overhead:

- **Early level checks**: `Enabled` uses the global level and cached lowest filter level; `Handle` still checks suppression filters
- **Simple filter patterns**: Level filters use string prefix/suffix/contains matching
- **Concurrent filter access**: Lock-free cached level reads, with an RWMutex protecting immutable filter snapshots
- **Lazy source extraction**: Source file/function only extracted when source filters are configured
- **Output level changes**: Update a record copy without rebuilding its attributes
- **Opt-in sanitisation**: Disabled sanitisers add no handler wrapper; enabled sanitisers reuse unchanged strings, groups and record attributes

Sanitiser regexes are compiled once. Matching rules and callbacks still have a
per-record cost; checking for matches before replacing strings reduces allocations
for ordinary logs but can add work to logs containing many sensitive values.
Use `go test -run '^$' -bench . -benchmem` to measure the included workloads on
your own hardware. Attributes supplied with `logger.With` are sanitised at binding
time, so their sanitisation work is not repeated for every record.

## License

MIT License - see [LICENSE](LICENSE) for details.
