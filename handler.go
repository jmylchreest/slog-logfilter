package logfilter

import (
	"context"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

// Handler is an slog.Handler with runtime level and filter controls. Handlers
// derived with WithAttrs or WithGroup share level, filters and context extractors
// with their parent. Updates through any member reach the entire logger family.
// Independently constructed handlers have separate configurations.
type Handler struct {
	*handlerState
	inner             slog.Handler
	preformattedAttrs []slog.Attr
	workDir           string
}

// handlerState is shared by a root handler and all derived handlers.
// Filter slices are immutable once published; their replacement is locked.
type handlerState struct {
	level            *slog.LevelVar
	filters          []LogFilter
	filtersLock      sync.RWMutex
	lowestLevel      atomic.Int64
	hasSourceFilters bool
	extractors       map[string]ContextExtractor
	extractorsLock   sync.RWMutex
}

// NewHandler creates a new filter-aware handler wrapping the given inner handler.
// The level is used as the default log level when no filters match.
// The supplied LevelVar must be non-nil and is not registered globally. The
// handler starts with an empty context extractor registry. Reusing a LevelVar
// across independently constructed handlers shares only that level.
func NewHandler(inner slog.Handler, level *slog.LevelVar) *Handler {
	wd := ""
	if cwd, err := filepath.Abs("."); err == nil {
		wd = cwd
	}
	h := &Handler{
		inner:        inner,
		handlerState: &handlerState{level: level},
		workDir:      wd,
	}
	h.lowestLevel.Store(int64(slog.LevelError + 1)) // Higher than any valid level
	return h
}

// SetLevel changes this handler's default level and that of its derived handlers.
// Independently constructed loggers are unaffected unless they explicitly use
// the same LevelVar via NewHandler.
func (h *Handler) SetLevel(level slog.Level) { h.level.Set(level) }

// GetLevel returns this handler's current default level.
func (h *Handler) GetLevel() slog.Level { return h.level.Level() }

// SetFilters replaces all filters with the given list.
// Filters are applied in order; first match wins.
// The new filters apply to all existing derived handlers.
func (h *Handler) SetFilters(filters []LogFilter) {
	h.filtersLock.Lock()
	defer h.filtersLock.Unlock()

	h.filters = make([]LogFilter, len(filters))
	for i, filter := range filters {
		h.filters[i] = copyFilter(filter)
	}
	h.updateLowestLevel()
}

// GetFilters returns a copy of the current filters.
func (h *Handler) GetFilters() []LogFilter {
	h.filtersLock.RLock()
	defer h.filtersLock.RUnlock()

	filters := make([]LogFilter, len(h.filters))
	for i, filter := range h.filters {
		filters[i] = copyFilter(filter)
	}
	return filters
}

// AddFilter adds a filter to the end of the filter list.
func (h *Handler) AddFilter(filter LogFilter) {
	h.filtersLock.Lock()
	defer h.filtersLock.Unlock()

	// Published filter slices are read after releasing filtersLock, and may also
	// belong to derived handlers. Never re-prepare their cached fields in place.
	filters := make([]LogFilter, len(h.filters)+1)
	copy(filters, h.filters)
	filters[len(h.filters)] = copyFilter(filter)
	h.filters = filters
	h.updateLowestLevel()
}

// copyFilter owns the expiry value so input/output config cannot mutate a live
// logger family indirectly through ExpiresAt pointers.
func copyFilter(filter LogFilter) LogFilter {
	if filter.ExpiresAt != nil {
		expiry := *filter.ExpiresAt
		filter.ExpiresAt = &expiry
	}
	return filter
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
	h.lowestLevel.Store(int64(slog.LevelError + 1))
	h.hasSourceFilters = false
}

// updateLowestLevel recalculates the lowest level among active filters
// and checks if any source filters are present.
// Must be called with filtersLock held.
func (h *Handler) updateLowestLevel() {
	lowest := slog.LevelError + 1
	h.hasSourceFilters = false

	for i := range h.filters {
		h.filters[i].prepare()
		f := &h.filters[i]
		if !f.IsActive() {
			continue
		}
		if f.parsedLevel < lowest {
			lowest = f.parsedLevel
		}
		if f.kind == filterKindSourceFile || f.kind == filterKindSourceFunction {
			h.hasSourceFilters = true
		}
	}
	h.lowestLevel.Store(int64(lowest))
}

// Enabled reports whether the handler handles records at the given level.
// It returns true if either:
// - The level is >= the handler default, OR
// - There are active filters that might match at this level
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	// Fast path: level is at or above the handler default
	if level >= h.level.Level() {
		return true
	}

	// Check if any filter could potentially enable this level.
	// lowestLevel is updated atomically, no lock needed on the hot path.
	lowestLevel := slog.Level(h.lowestLevel.Load())

	return level >= lowestLevel
}

// Handle processes a log record, applying filters to determine the effective level.
// If a matching filter has OutputLevel set, the record's level is transformed before emission.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	effectiveLevel := h.level.Level()
	var matchedFilter *LogFilter

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

	// Attribute map is built lazily — only when an attribute filter is encountered.
	var attrs map[string]string

	for i := range filters {
		f := &filters[i]
		if !f.IsActive() {
			continue
		}

		var value string
		var found bool

		switch f.kind {
		case filterKindSourceFile:
			// Match against source file path
			value = sourceFile
			found = sourceFile != ""
		case filterKindSourceFunction:
			// Match against function name
			value = sourceFunction
			found = sourceFunction != ""
		case filterKindContext:
			// Extract from context
			value, found = h.extractFromContext(ctx, f.contextKey)
		default:
			// Build the attribute map on first need
			if attrs == nil {
				attrs = make(map[string]string, len(h.preformattedAttrs)+r.NumAttrs())
				for _, a := range h.preformattedAttrs {
					attrs[a.Key] = attrValueToString(a.Value)
				}
				r.Attrs(func(a slog.Attr) bool {
					attrs[a.Key] = attrValueToString(a.Value)
					return true
				})
			}
			// Check record attributes
			value, found = attrs[f.attributeKey]
		}

		if found && f.Matches(value) {
			effectiveLevel = f.parsedLevel
			matchedFilter = f
			break // First match wins
		}
	}

	// Check if record should be emitted
	if r.Level < effectiveLevel {
		return nil // Suppress
	}

	// Transform log level if filter specifies an output level
	if matchedFilter != nil && matchedFilter.HasOutputLevel() {
		// r is passed by value: changing its level does not modify the caller's
		// record or its attribute storage, so no attribute copy is needed.
		r.Level = matchedFilter.cachedOutputLevel(r.Level)
	}

	return h.inner.Handle(ctx, r)
}

// extractSource extracts the source file and function name from a program counter.
// For local files (within working directory), returns relative paths.
// For external packages, returns the module path (e.g., "@github.com/pkg/module/file.go").
func (h *Handler) extractSource(pc uintptr) (file, function string) {
	frames := runtime.CallersFrames([]uintptr{pc})
	frame, _ := frames.Next()

	if frame.File != "" {
		file = h.formatSourcePath(frame.File, frame.Function)
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

// formatSourcePath formats the source file path for display.
// Local files (within working directory) get relative paths.
// External packages get module paths prefixed with "@".
func (h *Handler) formatSourcePath(filePath, functionName string) string {
	// Try to make the path relative to working directory
	if h.workDir != "" {
		if rel, err := filepath.Rel(h.workDir, filePath); err == nil {
			// Check if it's within the project (doesn't start with ..)
			if !strings.HasPrefix(rel, "..") {
				return rel
			}
		}
	}

	// External package - extract module path from function name
	// Function name looks like: "github.com/user/repo/pkg.(*Type).Method"
	if functionName != "" {
		// Find the last slash to get the package path
		if lastSlash := strings.LastIndex(functionName, "/"); lastSlash >= 0 {
			// Get everything up to and including the package name
			afterSlash := functionName[lastSlash+1:]
			if dotIdx := strings.Index(afterSlash, "."); dotIdx >= 0 {
				// Module path is everything before the type/function
				modulePath := functionName[:lastSlash+1+dotIdx]
				// Add the filename
				fileName := filepath.Base(filePath)
				return "@" + modulePath + "/" + fileName
			}
		}
	}

	// Fallback to just the filename
	return filepath.Base(filePath)
}

// WithAttrs returns a derived handler sharing its parent's runtime configuration.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// Copy preformattedAttrs to avoid aliasing the parent's backing array.
	merged := make([]slog.Attr, len(h.preformattedAttrs), len(h.preformattedAttrs)+len(attrs))
	copy(merged, h.preformattedAttrs)
	merged = append(merged, attrs...)
	return &Handler{
		inner:             h.inner.WithAttrs(attrs),
		handlerState:      h.handlerState,
		preformattedAttrs: merged,
		workDir:           h.workDir,
	}
}

// WithGroup returns a derived handler sharing its parent's runtime configuration.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{
		inner:             h.inner.WithGroup(name),
		handlerState:      h.handlerState,
		preformattedAttrs: h.preformattedAttrs,
		workDir:           h.workDir,
	}
}

// attrValueToString converts an slog.Value to a string for pattern matching.
func attrValueToString(v slog.Value) string {
	switch v.Kind() {
	case slog.KindTime:
		return v.Time().String()
	case slog.KindDuration:
		return v.Duration().String()
	default:
		return v.String()
	}
}
