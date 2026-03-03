// Package logfilter provides a dynamic, filter-based logging system for Go's slog.
//
// It supports:
//   - Dynamic log level changes at runtime via LevelVar
//   - Filter-based level overrides (elevate or suppress logs based on attributes)
//   - Context value extraction for filtering
//   - Simple glob-style pattern matching (prefix*, *suffix, *contains*)
//   - Optional filter expiry for temporary debugging
//
// Basic usage:
//
//	// Create a filtered logger
//	logger := logfilter.New(
//	    logfilter.WithLevel(slog.LevelInfo),
//	    logfilter.WithFormat("json"),
//	)
//
//	// Add filters at runtime
//	logfilter.SetFilters([]logfilter.LogFilter{
//	    {Type: "job_id", Pattern: "job_abc*", Level: "debug", Enabled: true},
//	})
//
//	// Logs with matching attributes will use filter's level
//	logger.Debug("processing", "job_id", "job_abc123") // Emitted (filter matches)
//	logger.Debug("processing", "job_id", "job_xyz")    // Suppressed (no match)
//
// Context filtering:
//
//	// Register a context extractor
//	logfilter.RegisterContextExtractor("user_id", func(ctx context.Context) (string, bool) {
//	    if v := ctx.Value(UserIDKey); v != nil {
//	        return v.(string), true
//	    }
//	    return "", false
//	})
//
//	// Use context: prefix in filter type
//	logfilter.AddFilter(logfilter.LogFilter{
//	    Type: "context:user_id", Pattern: "user_123", Level: "debug", Enabled: true,
//	})
package logfilter

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
)

var (
	// defaultHandler is the global handler instance
	defaultHandler     *Handler
	defaultHandlerLock sync.RWMutex

	// defaultLevel is the global log level
	defaultLevel = new(slog.LevelVar)
)

// Option configures the logger.
type Option func(*options)

type options struct {
	level   slog.Level
	format  string // "json" or "text"
	output  io.Writer
	source  bool
	workDir string
	filters []LogFilter
}

// WithLevel sets the initial log level.
func WithLevel(level slog.Level) Option {
	return func(o *options) {
		o.level = level
	}
}

// WithFormat sets the output format ("json" or "text").
func WithFormat(format string) Option {
	return func(o *options) {
		o.format = format
	}
}

// WithOutput sets the output writer (default: os.Stdout).
func WithOutput(w io.Writer) Option {
	return func(o *options) {
		o.output = w
	}
}

// WithSource enables source file:line in log output.
func WithSource(enabled bool) Option {
	return func(o *options) {
		o.source = enabled
	}
}

// WithFilters sets the initial filters.
func WithFilters(filters []LogFilter) Option {
	return func(o *options) {
		o.filters = filters
	}
}

// New creates a new slog.Logger with filter support.
// The returned logger uses the global filter handler, so filters can be
// updated at runtime using SetFilters, AddFilter, etc.
func New(opts ...Option) *slog.Logger {
	o := &options{
		level:  slog.LevelInfo,
		format: "json",
		output: os.Stdout,
		source: true,
	}
	o.workDir, _ = os.Getwd()

	for _, opt := range opts {
		opt(o)
	}

	defaultLevel.Set(o.level)

	trimPrefix := detectSourcePrefix()

	handlerOpts := &slog.HandlerOptions{
		Level:     defaultLevel,
		AddSource: o.source,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.SourceKey {
				if src, ok := a.Value.Any().(*slog.Source); ok {
					src.File = trimSourcePath(src.File, trimPrefix, o.workDir)
				}
			}
			return a
		},
	}

	var inner slog.Handler
	if o.format == "text" {
		inner = slog.NewTextHandler(o.output, handlerOpts)
	} else {
		inner = slog.NewJSONHandler(o.output, handlerOpts)
	}

	handler := NewHandler(inner, defaultLevel)

	// Apply initial filters if provided
	if len(o.filters) > 0 {
		handler.SetFilters(o.filters)
	}

	defaultHandlerLock.Lock()
	defaultHandler = handler
	defaultHandlerLock.Unlock()

	return slog.New(handler)
}

// SetLevel changes the global log level at runtime.
func SetLevel(level slog.Level) {
	defaultLevel.Set(level)
}

// GetLevel returns the current global log level.
func GetLevel() slog.Level {
	return defaultLevel.Level()
}

// SetFilters replaces all filters on the global handler.
// Filters are applied in order; first match wins.
func SetFilters(filters []LogFilter) {
	defaultHandlerLock.RLock()
	h := defaultHandler
	defaultHandlerLock.RUnlock()

	if h != nil {
		h.SetFilters(filters)
	}
}

// GetFilters returns a copy of the current filters.
func GetFilters() []LogFilter {
	defaultHandlerLock.RLock()
	h := defaultHandler
	defaultHandlerLock.RUnlock()

	if h != nil {
		return h.GetFilters()
	}
	return nil
}

// AddFilter adds a filter to the global handler.
func AddFilter(filter LogFilter) {
	defaultHandlerLock.RLock()
	h := defaultHandler
	defaultHandlerLock.RUnlock()

	if h != nil {
		h.AddFilter(filter)
	}
}

// RemoveFilter removes filters matching the given type and pattern.
func RemoveFilter(filterType, pattern string) {
	defaultHandlerLock.RLock()
	h := defaultHandler
	defaultHandlerLock.RUnlock()

	if h != nil {
		h.RemoveFilter(filterType, pattern)
	}
}

// ClearFilters removes all filters from the global handler.
func ClearFilters() {
	defaultHandlerLock.RLock()
	h := defaultHandler
	defaultHandlerLock.RUnlock()

	if h != nil {
		h.ClearFilters()
	}
}

// GetHandler returns the global filter handler.
// This can be used to wrap with additional handlers or for testing.
func GetHandler() *Handler {
	defaultHandlerLock.RLock()
	defer defaultHandlerLock.RUnlock()
	return defaultHandler
}

// SetDefault creates a new logger with the given options and sets it as
// the default slog logger.
func SetDefault(opts ...Option) *slog.Logger {
	logger := New(opts...)
	slog.SetDefault(logger)
	return logger
}

// detectSourcePrefix discovers the filesystem prefix that should be stripped
// from source paths to produce clean, module-relative paths.
//
// It uses runtime/debug.ReadBuildInfo to find the main module path
// (e.g. "github.com/user/repo"), then looks for that path component in
// the working directory to determine the root. For example, if the module
// path is "github.com/user/repo" and the binary was built from
// "/home/user/src/github.com/user/repo", the prefix returned is
// "/home/user/src/github.com/user/repo/".
//
// When running from an installed binary (CWD unrelated to source), it
// returns just the module path suffix so it can be matched anywhere in
// the source path.
func detectSourcePrefix() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok || bi.Main.Path == "" {
		return ""
	}

	// The main module path looks like "github.com/user/repo" or
	// "github.com/user/repo/cmd/foo". We want the root module, which
	// is the shortest path that appears in source file paths.
	modPath := bi.Main.Path

	// Try to locate the module root in the current working directory.
	// This handles the "built from source" case where CWD contains
	// the module path as a directory component.
	if cwd, err := os.Getwd(); err == nil {
		if idx := strings.Index(cwd, modPath); idx >= 0 {
			return cwd[:idx+len(modPath)] + string(filepath.Separator)
		}
	}

	// Fallback: return the module path so we can search for it in
	// arbitrary source file paths (handles installed binaries where
	// CWD is unrelated to the source tree).
	return modPath
}

// trimSourcePath produces a clean, short source path for log output.
//
// Strategy (in order):
//  1. If prefix is set and found in filePath, strip everything up to and
//     including the prefix → "pkg/foo/bar.go"
//  2. If workDir makes a clean relative path (no ".." prefix), use it
//  3. Fall back to just the base filename
func trimSourcePath(filePath, prefix, workDir string) string {
	// Strategy 1: strip the detected module prefix
	if prefix != "" {
		if idx := strings.Index(filePath, prefix); idx >= 0 {
			trimmed := filePath[idx+len(prefix):]
			// Ensure no leading separator
			trimmed = strings.TrimLeft(trimmed, "/"+string(filepath.Separator))
			if trimmed != "" {
				return trimmed
			}
		}
	}

	// Strategy 2: try workDir-relative path
	if workDir != "" {
		if rel, err := filepath.Rel(workDir, filePath); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}

	// Strategy 3: basename only
	return filepath.Base(filePath)
}
