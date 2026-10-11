// Package logfilter provides a dynamic, filter-based logging system for Go's slog.
//
// It supports:
//   - Dynamic log level changes at runtime via LevelVar
//   - Filter-based level overrides (elevate or suppress logs based on attributes)
//   - Context value extraction for filtering
//   - Simple glob-style pattern matching (prefix*, *suffix, *contains*)
//   - Optional filter expiry for temporary debugging
//   - Opt-in output sanitisation using regular expressions and custom callbacks
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
//	handler := logger.Handler().(*logfilter.Handler)
//	handler.SetFilters([]logfilter.LogFilter{
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
//	handler.RegisterContextExtractor("user_id", func(ctx context.Context) (string, bool) {
//	    if v := ctx.Value(UserIDKey); v != nil {
//	        return v.(string), true
//	    }
//	    return "", false
//	})
//
//	// Use context: prefix in filter type
//	handler.AddFilter(logfilter.LogFilter{
//	    Type: "context:user_id", Pattern: "user_123", Level: "debug", Enabled: true,
//	})
//
// # Independent instances
//
// [New] creates an independent logger. Applications may explicitly install it
// as the process default with slog.SetDefault. The package has no global registry.
//
// Derived handlers share levels, filters and context extractors. Runtime updates
// through any member apply to its entire logger family. Sanitisers remain
// immutable and are inherited. See [WithContextExtractors] for initial registries.
//
// Version 0.3 removes package-level configuration functions. Use the methods on
// logger.Handler().(*Handler) instead.
//
// # Sanitisation
//
// Sanitisation is disabled by default. Construct rules with [NewSanitiser] and
// enable them with [WithSanitiser] or [WithSanitisers]. [DefaultSanitiserRules]
// supplies opt-in credential, UUID, Bearer token and Luhn-validated card rules.
// [SanitiserRule.ReplaceFunc] supports custom validation and calculated replacements.
//
// Filters match original values before output sanitisation. Whole-field rules
// can replace any attribute type; text rules process messages and string values,
// including nested slog groups and resolved LogValuer values. Opaque maps,
// structs and errors require whole-field redaction or conversion to slog groups.
// See [NewSanitisingHandler] for custom handler integration and coverage limits.
package logfilter

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// Option configures the logger.
type Option func(*options)

type options struct {
	level      slog.Level
	format     string // "json" or "text"
	output     io.Writer
	source     bool
	workDir    string
	filters    []LogFilter
	sanitisers []*Sanitiser
	extractors map[string]ContextExtractor
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

// WithFilters sets the initial filters. The slice and expiry values are copied
// when the option is created; later changes to the input have no effect.
// The option can be reused to construct independent loggers.
func WithFilters(filters []LogFilter) Option {
	snapshot := make([]LogFilter, len(filters))
	for i, filter := range filters {
		snapshot[i] = copyFilter(filter)
	}
	return func(o *options) {
		o.filters = snapshot
	}
}

// WithContextExtractors sets a logger-local context extractor registry. The map
// is copied for each logger; later changes to the supplied map have no effect.
// Nil selects an empty registry. Callbacks must be safe for concurrent use.
// Every constructor starts with an empty registry unless this option is supplied.
func WithContextExtractors(extractors map[string]ContextExtractor) Option {
	snapshot := make(map[string]ContextExtractor, len(extractors))
	for key, extractor := range extractors {
		snapshot[key] = extractor
	}
	return func(o *options) { o.extractors = snapshot }
}

// WithSanitiser enables optional output sanitisation. A nil sanitiser disables it.
// Filters still match the original, unsanitised values.
func WithSanitiser(s *Sanitiser) Option {
	return WithSanitisers(s)
}

// WithSanitisers sets the output sanitisers in application order. Each sanitiser
// receives the preceding one's output. Nil and empty sanitisers are ignored.
// This replaces any earlier WithSanitiser or WithSanitisers option; no arguments
// disables sanitisation. The supplied slice is copied and can be reused safely.
func WithSanitisers(sanitisers ...*Sanitiser) Option {
	snapshot := append([]*Sanitiser(nil), sanitisers...)
	return func(o *options) { o.sanitisers = snapshot }
}

// New creates an independent logger with its own level, filters and context
// extractors. It does not change slog.Default; applications may call
// slog.SetDefault explicitly. Use logger.Handler().(*Handler) for runtime updates,
// which also reach derived loggers.
func New(opts ...Option) *slog.Logger {
	return slog.New(newConfiguredHandler(opts...))
}

func newConfiguredHandler(opts ...Option) *Handler {
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

	level := new(slog.LevelVar)
	level.Set(o.level)

	trimPrefix := detectSourcePrefix()

	handlerOpts := &slog.HandlerOptions{
		Level:     level,
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

	inner = NewSanitisingHandler(inner, o.sanitisers...)
	handler := NewHandler(inner, level)
	for key, extractor := range o.extractors {
		handler.RegisterContextExtractor(key, extractor)
	}

	// Apply initial filters if provided
	if len(o.filters) > 0 {
		handler.SetFilters(o.filters)
	}

	return handler
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
