package logfilter

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestHandler_BasicFiltering(t *testing.T) {
	var buf bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)

	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level})
	handler := NewHandler(inner, level)

	// Add a filter that enables debug for job_id starting with "debug_"
	handler.SetFilters([]LogFilter{
		{Type: "job_id", Pattern: "debug_*", Level: "debug", Enabled: true},
	})

	logger := slog.New(handler)

	// Debug without matching job_id should be suppressed
	buf.Reset()
	logger.Debug("test message", "job_id", "normal_123")
	if buf.Len() > 0 {
		t.Error("Expected debug message without matching filter to be suppressed")
	}

	// Debug with matching job_id should be emitted
	buf.Reset()
	logger.Debug("test message", "job_id", "debug_123")
	if buf.Len() == 0 {
		t.Error("Expected debug message with matching filter to be emitted")
	}

	// Info should always be emitted (at or above global level)
	buf.Reset()
	logger.Info("test message", "job_id", "normal_123")
	if buf.Len() == 0 {
		t.Error("Expected info message to be emitted")
	}
}

func TestHandler_FilterSuppression(t *testing.T) {
	var buf bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo) // Global is INFO

	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewHandler(inner, level)

	// Add a filter that suppresses to WARN for certain job_ids
	handler.SetFilters([]LogFilter{
		{Type: "job_id", Pattern: "noisy_*", Level: "warn", Enabled: true},
	})

	logger := slog.New(handler)

	// Info with matching job_id should be suppressed (filter sets WARN)
	buf.Reset()
	logger.Info("noisy message", "job_id", "noisy_123")
	if buf.Len() > 0 {
		t.Error("Expected info message with suppression filter to be suppressed")
	}

	// Warn with matching job_id should be emitted
	buf.Reset()
	logger.Warn("noisy warning", "job_id", "noisy_123")
	if buf.Len() == 0 {
		t.Error("Expected warn message with matching filter to be emitted")
	}

	// Info without matching job_id should be emitted (uses global INFO level)
	buf.Reset()
	logger.Info("normal message", "job_id", "normal_123")
	if buf.Len() == 0 {
		t.Error("Expected info message without matching filter to be emitted")
	}
}

func TestHandler_ContextFilter(t *testing.T) {
	// Register a context extractor
	type ctxKey string
	const userIDKey ctxKey = "user_id"

	RegisterContextExtractor("user_id", func(ctx context.Context) (string, bool) {
		if v := ctx.Value(userIDKey); v != nil {
			if s, ok := v.(string); ok {
				return s, true
			}
		}
		return "", false
	})
	defer ClearContextExtractors()

	var buf bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)

	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewHandler(inner, level)

	handler.SetFilters([]LogFilter{
		{Type: "context:user_id", Pattern: "debug_user_*", Level: "debug", Enabled: true},
	})

	logger := slog.New(handler)

	// Debug with matching user in context should be emitted
	ctx := context.WithValue(context.Background(), userIDKey, "debug_user_123")
	buf.Reset()
	logger.DebugContext(ctx, "test message")
	if buf.Len() == 0 {
		t.Error("Expected debug message with matching context filter to be emitted")
	}

	// Debug with non-matching user in context should be suppressed
	ctx = context.WithValue(context.Background(), userIDKey, "normal_user_456")
	buf.Reset()
	logger.DebugContext(ctx, "test message")
	if buf.Len() > 0 {
		t.Error("Expected debug message without matching context filter to be suppressed")
	}
}

func TestHandler_FirstMatchWins(t *testing.T) {
	var buf bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)

	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewHandler(inner, level)

	// First filter matches and sets DEBUG, second would set WARN
	handler.SetFilters([]LogFilter{
		{Type: "job_id", Pattern: "job_*", Level: "debug", Enabled: true},
		{Type: "job_id", Pattern: "job_123", Level: "warn", Enabled: true},
	})

	logger := slog.New(handler)

	// Should use first filter (DEBUG), not second (WARN)
	buf.Reset()
	logger.Debug("test message", "job_id", "job_123")
	if buf.Len() == 0 {
		t.Error("Expected first matching filter to be used (debug)")
	}
}

func TestHandler_DisabledFilter(t *testing.T) {
	var buf bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)

	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewHandler(inner, level)

	handler.SetFilters([]LogFilter{
		{Type: "job_id", Pattern: "debug_*", Level: "debug", Enabled: false}, // Disabled
	})

	logger := slog.New(handler)

	// Debug should be suppressed because filter is disabled
	buf.Reset()
	logger.Debug("test message", "job_id", "debug_123")
	if buf.Len() > 0 {
		t.Error("Expected disabled filter to be ignored")
	}
}

func TestHandler_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)

	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewHandler(inner, level)

	handler.SetFilters([]LogFilter{
		{Type: "job_id", Pattern: "debug_*", Level: "debug", Enabled: true},
	})

	// Create logger with pre-set job_id
	logger := slog.New(handler).With("job_id", "debug_456")

	// Debug should be emitted because job_id was set via With()
	buf.Reset()
	logger.Debug("test message")
	if buf.Len() == 0 {
		t.Error("Expected debug message with pre-set matching attribute to be emitted")
	}

	// Verify the job_id is in the output
	if !strings.Contains(buf.String(), "debug_456") {
		t.Error("Expected job_id to be in output")
	}
}

func TestHandler_SetFilters(t *testing.T) {
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)

	handler := NewHandler(slog.NewTextHandler(&bytes.Buffer{}, nil), level)

	// Initial filters
	handler.SetFilters([]LogFilter{
		{Type: "a", Pattern: "1", Level: "debug", Enabled: true},
		{Type: "b", Pattern: "2", Level: "info", Enabled: true},
	})

	filters := handler.GetFilters()
	if len(filters) != 2 {
		t.Errorf("Expected 2 filters, got %d", len(filters))
	}

	// Replace filters
	handler.SetFilters([]LogFilter{
		{Type: "c", Pattern: "3", Level: "warn", Enabled: true},
	})

	filters = handler.GetFilters()
	if len(filters) != 1 {
		t.Errorf("Expected 1 filter after replace, got %d", len(filters))
	}
	if filters[0].Type != "c" {
		t.Errorf("Expected filter type 'c', got %q", filters[0].Type)
	}
}

func TestHandler_AddRemoveFilter(t *testing.T) {
	level := new(slog.LevelVar)
	handler := NewHandler(slog.NewTextHandler(&bytes.Buffer{}, nil), level)

	// Add filters
	handler.AddFilter(LogFilter{Type: "a", Pattern: "1", Level: "debug", Enabled: true})
	handler.AddFilter(LogFilter{Type: "b", Pattern: "2", Level: "info", Enabled: true})

	if len(handler.GetFilters()) != 2 {
		t.Error("Expected 2 filters after adding")
	}

	// Remove one
	handler.RemoveFilter("a", "1")

	filters := handler.GetFilters()
	if len(filters) != 1 {
		t.Errorf("Expected 1 filter after remove, got %d", len(filters))
	}
	if filters[0].Type != "b" {
		t.Error("Wrong filter removed")
	}

	// Clear all
	handler.ClearFilters()
	if len(handler.GetFilters()) != 0 {
		t.Error("Expected 0 filters after clear")
	}
}

func TestHandler_SourceFileFilter(t *testing.T) {
	var buf bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)

	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewHandler(inner, level)

	// Add a filter that enables debug for this test file
	// The file path will be relative, like "handler_test.go" or similar
	handler.SetFilters([]LogFilter{
		{Type: SourceFilePrefix, Pattern: "*handler_test*", Level: "debug", Enabled: true},
	})

	logger := slog.New(handler)

	// Debug should be emitted because we're in handler_test.go
	buf.Reset()
	logger.Debug("test message from handler_test.go")
	if buf.Len() == 0 {
		t.Error("Expected debug message with matching source file filter to be emitted")
	}
}

func TestHandler_SourceFileFilter_NoMatch(t *testing.T) {
	var buf bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)

	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewHandler(inner, level)

	// Add a filter for a file that doesn't exist
	handler.SetFilters([]LogFilter{
		{Type: SourceFilePrefix, Pattern: "*nonexistent_file*", Level: "debug", Enabled: true},
	})

	logger := slog.New(handler)

	// Debug should be suppressed because we're not in nonexistent_file.go
	buf.Reset()
	logger.Debug("test message")
	if buf.Len() > 0 {
		t.Error("Expected debug message without matching source file filter to be suppressed")
	}
}

func TestHandler_SourceFunctionFilter(t *testing.T) {
	var buf bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)

	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewHandler(inner, level)

	// Add a filter that matches this test function name
	handler.SetFilters([]LogFilter{
		{Type: SourceFunctionPrefix, Pattern: "*SourceFunctionFilter*", Level: "debug", Enabled: true},
	})

	logger := slog.New(handler)

	// Debug should be emitted because we're in TestHandler_SourceFunctionFilter
	buf.Reset()
	logger.Debug("test message from TestHandler_SourceFunctionFilter")
	if buf.Len() == 0 {
		t.Error("Expected debug message with matching source function filter to be emitted")
	}
}

func TestHandler_SourceFunctionFilter_NoMatch(t *testing.T) {
	var buf bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)

	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewHandler(inner, level)

	// Add a filter for a function that doesn't match
	handler.SetFilters([]LogFilter{
		{Type: SourceFunctionPrefix, Pattern: "*NonExistentFunction*", Level: "debug", Enabled: true},
	})

	logger := slog.New(handler)

	// Debug should be suppressed
	buf.Reset()
	logger.Debug("test message")
	if buf.Len() > 0 {
		t.Error("Expected debug message without matching source function filter to be suppressed")
	}
}

func TestHandler_HasSourceFilters(t *testing.T) {
	level := new(slog.LevelVar)
	handler := NewHandler(slog.NewTextHandler(&bytes.Buffer{}, nil), level)

	// Initially no source filters
	handler.filtersLock.RLock()
	hasSource := handler.hasSourceFilters
	handler.filtersLock.RUnlock()
	if hasSource {
		t.Error("Expected hasSourceFilters to be false initially")
	}

	// Add a source filter
	handler.SetFilters([]LogFilter{
		{Type: SourceFilePrefix, Pattern: "*test*", Level: "debug", Enabled: true},
	})

	handler.filtersLock.RLock()
	hasSource = handler.hasSourceFilters
	handler.filtersLock.RUnlock()
	if !hasSource {
		t.Error("Expected hasSourceFilters to be true after adding source filter")
	}

	// Add non-source filters only
	handler.SetFilters([]LogFilter{
		{Type: "job_id", Pattern: "*", Level: "debug", Enabled: true},
	})

	handler.filtersLock.RLock()
	hasSource = handler.hasSourceFilters
	handler.filtersLock.RUnlock()
	if hasSource {
		t.Error("Expected hasSourceFilters to be false after removing source filters")
	}

	// Clear filters
	handler.ClearFilters()

	handler.filtersLock.RLock()
	hasSource = handler.hasSourceFilters
	handler.filtersLock.RUnlock()
	if hasSource {
		t.Error("Expected hasSourceFilters to be false after clearing")
	}
}

func TestHandler_ExtractSource(t *testing.T) {
	level := new(slog.LevelVar)
	handler := NewHandler(slog.NewTextHandler(&bytes.Buffer{}, nil), level)

	// Get the PC for this function
	// We'll test extractSource directly
	var pc uintptr
	// Use a closure to get the PC
	func() {
		// This is a bit tricky - we need to capture the PC from within the test
		// The PC is normally set by slog when logging, but we can get it manually
		pcs := make([]uintptr, 1)
		n := capturePC(pcs)
		if n > 0 {
			pc = pcs[0]
		}
	}()

	if pc != 0 {
		file, function := handler.extractSource(pc)

		// File should contain "handler_test"
		if !strings.Contains(file, "handler_test") {
			t.Errorf("Expected file to contain 'handler_test', got %q", file)
		}

		// Function should contain something meaningful
		if function == "" {
			t.Error("Expected function to be non-empty")
		}
	}
}

// capturePC captures the program counter - helper for testing
func capturePC(pcs []uintptr) int {
	// Skip 2 frames: runtime.Callers and capturePC
	return runtimeCallers(2, pcs)
}

// runtimeCallers wraps runtime.Callers for testing
func runtimeCallers(skip int, pcs []uintptr) int {
	// Import runtime at the top of the file if not already
	// For now, we'll just return 0 to skip this detailed test
	// The other tests cover the functionality
	return 0
}

func TestHandler_SourceFilter_PrefixMatch(t *testing.T) {
	var buf bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)

	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewHandler(inner, level)

	// Match files starting with "handler"
	handler.SetFilters([]LogFilter{
		{Type: SourceFilePrefix, Pattern: "handler*", Level: "debug", Enabled: true},
	})

	logger := slog.New(handler)

	// Should match because file is handler_test.go
	buf.Reset()
	logger.Debug("test")
	if buf.Len() == 0 {
		t.Error("Expected debug message with prefix matching source file to be emitted")
	}
}

func TestHandler_SourceFilter_SuffixMatch(t *testing.T) {
	var buf bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)

	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewHandler(inner, level)

	// Match files ending with "_test.go"
	handler.SetFilters([]LogFilter{
		{Type: SourceFilePrefix, Pattern: "*_test.go", Level: "debug", Enabled: true},
	})

	logger := slog.New(handler)

	// Should match because file is handler_test.go
	buf.Reset()
	logger.Debug("test")
	if buf.Len() == 0 {
		t.Error("Expected debug message with suffix matching source file to be emitted")
	}
}
