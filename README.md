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
    Type      string     `json:"type"`       // Attribute key or special prefix
    Pattern   string     `json:"pattern"`    // Glob pattern for value
    Level     string     `json:"level"`      // Level: debug, info, warn, error
    Enabled   bool       `json:"enabled"`    // Whether filter is active
    ExpiresAt *time.Time `json:"expires_at"` // Optional expiry (nil = never)
}
```

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

- **`source:file`** - Matches against the relative file path (e.g., `internal/service/extraction.go`)
- **`source:function`** - Matches against the function name (e.g., `(*ExtractionService).Extract`)

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

- **Fast path**: If log level >= global level, no filter checking needed
- **Cached lowest level**: Quick check if any filter could match
- **Simple patterns**: No regex, just string prefix/suffix/contains
- **Lock-free reads**: RWMutex for concurrent filter access
- **Lazy source extraction**: Source file/function only extracted when source filters are configured

## License

MIT License - see [LICENSE](LICENSE) for details.
