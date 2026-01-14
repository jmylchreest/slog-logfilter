package logfilter

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestNew_DefaultOptions(t *testing.T) {
	logger := New()
	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}

	// Default level should be INFO
	if GetLevel() != slog.LevelInfo {
		t.Errorf("Expected default level INFO, got %v", GetLevel())
	}
}

func TestNew_WithOptions(t *testing.T) {
	var buf bytes.Buffer
	logger := New(
		WithLevel(slog.LevelDebug),
		WithFormat("text"),
		WithOutput(&buf),
		WithSource(false),
	)

	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}

	if GetLevel() != slog.LevelDebug {
		t.Errorf("Expected level DEBUG, got %v", GetLevel())
	}

	// Log something and verify output
	logger.Info("test message")
	if !strings.Contains(buf.String(), "test message") {
		t.Error("Expected log output to contain message")
	}
}

func TestNew_WithFilters(t *testing.T) {
	var buf bytes.Buffer
	filters := []LogFilter{
		{Type: "job_id", Pattern: "test_*", Level: "debug", Enabled: true},
	}

	_ = New(
		WithLevel(slog.LevelInfo),
		WithFormat("text"),
		WithOutput(&buf),
		WithFilters(filters),
	)

	// Verify filters were set
	gotFilters := GetFilters()
	if len(gotFilters) != 1 {
		t.Errorf("Expected 1 filter, got %d", len(gotFilters))
	}
}

func TestSetLevel(t *testing.T) {
	_ = New(WithLevel(slog.LevelInfo))

	SetLevel(slog.LevelDebug)
	if GetLevel() != slog.LevelDebug {
		t.Errorf("Expected DEBUG after SetLevel, got %v", GetLevel())
	}

	SetLevel(slog.LevelWarn)
	if GetLevel() != slog.LevelWarn {
		t.Errorf("Expected WARN after SetLevel, got %v", GetLevel())
	}
}

func TestSetFilters_Global(t *testing.T) {
	_ = New()

	filters := []LogFilter{
		{Type: "a", Pattern: "1", Level: "debug", Enabled: true},
		{Type: "b", Pattern: "2", Level: "info", Enabled: true},
	}

	SetFilters(filters)

	got := GetFilters()
	if len(got) != 2 {
		t.Errorf("Expected 2 filters, got %d", len(got))
	}

	// Clear
	ClearFilters()
	if len(GetFilters()) != 0 {
		t.Error("Expected 0 filters after clear")
	}
}

func TestAddRemoveFilter_Global(t *testing.T) {
	_ = New()
	ClearFilters()

	AddFilter(LogFilter{Type: "x", Pattern: "y", Level: "debug", Enabled: true})
	if len(GetFilters()) != 1 {
		t.Error("Expected 1 filter after add")
	}

	RemoveFilter("x", "y")
	if len(GetFilters()) != 0 {
		t.Error("Expected 0 filters after remove")
	}
}

func TestGetHandler(t *testing.T) {
	_ = New()

	handler := GetHandler()
	if handler == nil {
		t.Error("Expected non-nil handler")
	}
}

func TestSetDefault(t *testing.T) {
	var buf bytes.Buffer
	logger := SetDefault(
		WithLevel(slog.LevelInfo),
		WithFormat("text"),
		WithOutput(&buf),
	)

	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}

	// slog.Default() should now use our logger
	slog.Info("test via default")
	if !strings.Contains(buf.String(), "test via default") {
		t.Error("Expected slog.Default to use our handler")
	}
}

func TestIntegration_FilterElevation(t *testing.T) {
	var buf bytes.Buffer

	logger := New(
		WithLevel(slog.LevelInfo),
		WithFormat("text"),
		WithOutput(&buf),
		WithFilters([]LogFilter{
			{Type: "job_id", Pattern: "debug_*", Level: "debug", Enabled: true},
		}),
	)

	// Debug without matching attribute - suppressed
	buf.Reset()
	logger.Debug("msg1", "job_id", "normal_123")
	if buf.Len() > 0 {
		t.Error("Expected debug without match to be suppressed")
	}

	// Debug with matching attribute - emitted
	buf.Reset()
	logger.Debug("msg2", "job_id", "debug_abc")
	if buf.Len() == 0 {
		t.Error("Expected debug with match to be emitted")
	}
	if !strings.Contains(buf.String(), "msg2") {
		t.Error("Expected message content in output")
	}
}

func TestIntegration_FilterSuppression(t *testing.T) {
	var buf bytes.Buffer

	logger := New(
		WithLevel(slog.LevelInfo),
		WithFormat("text"),
		WithOutput(&buf),
		WithFilters([]LogFilter{
			{Type: "job_id", Pattern: "noisy_*", Level: "error", Enabled: true},
		}),
	)

	// Info with matching attribute - suppressed (filter sets ERROR)
	buf.Reset()
	logger.Info("msg1", "job_id", "noisy_123")
	if buf.Len() > 0 {
		t.Error("Expected info with suppression filter to be suppressed")
	}

	// Error with matching attribute - emitted
	buf.Reset()
	logger.Error("msg2", "job_id", "noisy_123")
	if buf.Len() == 0 {
		t.Error("Expected error with match to be emitted")
	}

	// Info without matching attribute - emitted (global level)
	buf.Reset()
	logger.Info("msg3", "job_id", "normal_456")
	if buf.Len() == 0 {
		t.Error("Expected info without match to be emitted")
	}
}

func TestIntegration_FilterExpiry(t *testing.T) {
	var buf bytes.Buffer

	past := time.Now().Add(-1 * time.Hour)
	future := time.Now().Add(1 * time.Hour)

	logger := New(
		WithLevel(slog.LevelInfo),
		WithFormat("text"),
		WithOutput(&buf),
		WithFilters([]LogFilter{
			{Type: "job_id", Pattern: "expired_*", Level: "debug", Enabled: true, ExpiresAt: &past},
			{Type: "job_id", Pattern: "active_*", Level: "debug", Enabled: true, ExpiresAt: &future},
		}),
	)

	// Expired filter should not match
	buf.Reset()
	logger.Debug("msg1", "job_id", "expired_123")
	if buf.Len() > 0 {
		t.Error("Expected expired filter to not match")
	}

	// Active filter should match
	buf.Reset()
	logger.Debug("msg2", "job_id", "active_456")
	if buf.Len() == 0 {
		t.Error("Expected active filter to match")
	}
}
