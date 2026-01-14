package logfilter

import (
	"context"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Handler is an slog.Handler that supports dynamic log levels and filter-based
// level overrides. It wraps an inner handler and checks filters before delegating.
type Handler struct {
	inner             slog.Handler
	globalLevel       *slog.LevelVar
	filters           []LogFilter
	filtersLock       sync.RWMutex
	lowestLevel       slog.Level  // Cached lowest level from active filters
	hasSourceFilters  bool        // Cached: true if any filter is source-based
	preformattedAttrs []slog.Attr // Attributes added via WithAttrs
	workDir           string      // Working directory for relative path calculation
}

// NewHandler creates a new filter-aware handler wrapping the given inner handler.
// The globalLevel is used as the default log level when no filters match.
func NewHandler(inner slog.Handler, globalLevel *slog.LevelVar) *Handler {
	wd := ""
	if cwd, err := filepath.Abs("."); err == nil {
		wd = cwd
	}
	h := &Handler{
		inner:       inner,
		globalLevel: globalLevel,
		lowestLevel: slog.LevelError + 1, // Higher than any valid level
		workDir:     wd,
	}
	return h
}

// SetFilters replaces all filters with the given list.
// Filters are applied in order; first match wins.
func (h *Handler) SetFilters(filters []LogFilter) {
	h.filtersLock.Lock()
	defer h.filtersLock.Unlock()

	h.filters = make([]LogFilter, len(filters))
	copy(h.filters, filters)
	h.updateLowestLevel()
}

// GetFilters returns a copy of the current filters.
func (h *Handler) GetFilters() []LogFilter {
	h.filtersLock.RLock()
	defer h.filtersLock.RUnlock()

	filters := make([]LogFilter, len(h.filters))
	copy(filters, h.filters)
	return filters
}

// AddFilter adds a filter to the end of the filter list.
func (h *Handler) AddFilter(filter LogFilter) {
	h.filtersLock.Lock()
	defer h.filtersLock.Unlock()

	h.filters = append(h.filters, filter)
	h.updateLowestLevel()
}

// RemoveFilter removes filters matching the given type and pattern.
func (h *Handler) RemoveFilter(filterType, pattern string) {
	h.filtersLock.Lock()
	defer h.filtersLock.Unlock()

	filtered := make([]LogFilter, 0, len(h.filters))
	for _, f := range h.filters {
		if f.Type != filterType || f.Pattern != pattern {
			filtered = append(filtered, f)
		}
	}
	h.filters = filtered
	h.updateLowestLevel()
}

// ClearFilters removes all filters.
func (h *Handler) ClearFilters() {
	h.filtersLock.Lock()
	defer h.filtersLock.Unlock()

	h.filters = nil
	h.lowestLevel = slog.LevelError + 1
	h.hasSourceFilters = false
}

// updateLowestLevel recalculates the lowest level among active filters
// and checks if any source filters are present.
// Must be called with filtersLock held.
func (h *Handler) updateLowestLevel() {
	h.lowestLevel = slog.LevelError + 1
	h.hasSourceFilters = false

	for _, f := range h.filters {
		if !f.IsActive() {
			continue
		}
		level := parseLevel(f.Level)
		if level < h.lowestLevel {
			h.lowestLevel = level
		}
		if f.IsSourceFilter() {
			h.hasSourceFilters = true
		}
	}
}

// Enabled reports whether the handler handles records at the given level.
// It returns true if either:
// - The level is >= the global level, OR
// - There are active filters that might match at this level
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	// Fast path: level is at or above global level
	if level >= h.globalLevel.Level() {
		return true
	}

	// Check if any filter could potentially enable this level
	h.filtersLock.RLock()
	lowestLevel := h.lowestLevel
	h.filtersLock.RUnlock()

	return level >= lowestLevel
}

// Handle processes a log record, applying filters to determine the effective level.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	effectiveLevel := h.globalLevel.Level()

	// Collect attributes from record and preformatted attrs
	attrs := make(map[string]string)

	// Add preformatted attributes first
	for _, a := range h.preformattedAttrs {
		attrs[a.Key] = attrValueToString(a.Value)
	}

	// Add record attributes (may override preformatted)
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = attrValueToString(a.Value)
		return true
	})

	// Check filters (first match wins)
	h.filtersLock.RLock()
	filters := h.filters
	hasSourceFilters := h.hasSourceFilters
	h.filtersLock.RUnlock()

	// Extract source info only if we have source filters (performance optimization)
	var sourceFile, sourceFunction string
	if hasSourceFilters && r.PC != 0 {
		sourceFile, sourceFunction = h.extractSource(r.PC)
	}

	for _, f := range filters {
		if !f.IsActive() {
			continue
		}

		var value string
		var found bool

		switch {
		case f.IsSourceFileFilter():
			// Match against source file path
			value = sourceFile
			found = sourceFile != ""
		case f.IsSourceFunctionFilter():
			// Match against function name
			value = sourceFunction
			found = sourceFunction != ""
		case f.IsContextFilter():
			// Extract from context
			value, found = extractFromContext(ctx, f.ContextKey())
		default:
			// Check record attributes
			value, found = attrs[f.AttributeKey()]
		}

		if found && f.Matches(value) {
			effectiveLevel = parseLevel(f.Level)
			break // First match wins
		}
	}

	// Check if record should be emitted
	if r.Level < effectiveLevel {
		return nil // Suppress
	}

	return h.inner.Handle(ctx, r)
}

// extractSource extracts the source file and function name from a program counter.
// The file path is made relative to the working directory when possible.
func (h *Handler) extractSource(pc uintptr) (file, function string) {
	frames := runtime.CallersFrames([]uintptr{pc})
	frame, _ := frames.Next()

	if frame.File != "" {
		// Try to make the path relative to working directory
		if h.workDir != "" {
			if rel, err := filepath.Rel(h.workDir, frame.File); err == nil {
				file = rel
			} else {
				file = filepath.Base(frame.File)
			}
		} else {
			file = frame.File
		}
	}

	if frame.Function != "" {
		// Extract just the function name (after last dot, but handle receiver types)
		// e.g., "github.com/pkg/service.(*Service).Method" -> "(*Service).Method"
		function = frame.Function
		if lastSlash := strings.LastIndex(function, "/"); lastSlash >= 0 {
			// Find the package.Function part after the last slash
			afterSlash := function[lastSlash+1:]
			if dotIdx := strings.Index(afterSlash, "."); dotIdx >= 0 {
				function = afterSlash[dotIdx+1:]
			}
		} else if dotIdx := strings.Index(function, "."); dotIdx >= 0 {
			function = function[dotIdx+1:]
		}
	}

	return file, function
}

// WithAttrs returns a new Handler with the given attributes added.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandler := &Handler{
		inner:             h.inner.WithAttrs(attrs),
		globalLevel:       h.globalLevel,
		filters:           h.filters,
		lowestLevel:       h.lowestLevel,
		hasSourceFilters:  h.hasSourceFilters,
		preformattedAttrs: append(h.preformattedAttrs, attrs...),
		workDir:           h.workDir,
	}
	return newHandler
}

// WithGroup returns a new Handler with the given group name.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{
		inner:             h.inner.WithGroup(name),
		globalLevel:       h.globalLevel,
		filters:           h.filters,
		lowestLevel:       h.lowestLevel,
		hasSourceFilters:  h.hasSourceFilters,
		preformattedAttrs: h.preformattedAttrs,
		workDir:           h.workDir,
	}
}

// parseLevel converts a level string to slog.Level.
func parseLevel(level string) slog.Level {
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

// attrValueToString converts an slog.Value to a string for pattern matching.
func attrValueToString(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindInt64:
		return v.String()
	case slog.KindUint64:
		return v.String()
	case slog.KindFloat64:
		return v.String()
	case slog.KindBool:
		return v.String()
	case slog.KindTime:
		return v.Time().String()
	case slog.KindDuration:
		return v.Duration().String()
	default:
		return v.String()
	}
}
